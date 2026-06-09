package signals_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase R / FR-8 §8.4: stress proofs. Most valuable under `go test -race`, which
// turns any unsynchronized access into a hard failure. Skipped under -short.

// I5: 1000 goroutines concurrently add/emit/remove on a sync signal — exercises the
// atomic-load read path against copy-on-write writes with no goroutine fan-out. Each
// goroutine owns a unique key and balances every add with a remove, so the signal
// must be empty at the end.
func TestStress_LockFreeCore(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in -short mode")
	}
	sig := signals.NewSync[int]()
	ctx := context.Background()

	const goroutines = 1000
	const iters = 500

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("g%d", id)
			for j := 0; j < iters; j++ {
				sig.AddListener(noop, key)
				sig.Emit(ctx, id)
				_ = sig.Len()
				_ = sig.HasKey(key)
				_ = sig.Keys()
				sig.RemoveListener(key)
			}
		}(i)
	}
	wg.Wait()

	if !sig.IsEmpty() {
		t.Fatalf("expected empty after balanced add/remove, Len=%d", sig.Len())
	}
}

// I12: concurrent async emissions must drain back to baseline (no goroutine leak).
// Polls with a deadline rather than sleeping a fixed amount, to stay non-flaky.
func TestStress_AsyncNoGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in -short mode")
	}
	base := runtime.NumGoroutine()
	sig := signals.New[int]()
	ctx := context.Background()

	const goroutines = 200
	const iters = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("g%d", id)
			for j := 0; j < iters; j++ {
				sig.AddListener(noop, key)
				sig.EmitAndWait(ctx, id) // wait so each emission's handler goroutines complete
				sig.RemoveListener(key)
			}
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(3 * time.Second)
	after := runtime.NumGoroutine()
	for after > base+20 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > base+20 {
		t.Fatalf("possible goroutine leak: baseline=%d, after=%d", base, after)
	}
}

// I8/I12 under adversarial load: many concurrent emitters on a BOUNDED async signal,
// with concurrent add/remove churn, while an observer continuously samples introspection.
// Under -race this proves the bounded dispatcher + error model have no race, deadlock, or
// goroutine leak, and that the concurrency bound is never exceeded across overlapping emits.
func TestStress_BoundedAsyncUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in -short mode")
	}
	base := runtime.NumGoroutine()

	const limit = 8
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: limit})
	sig.OnError(func(context.Context, error) {}) // exercise the error sink under load

	var inFlight, peak int32
	// A permanent error-returning listener that also tracks live concurrency.
	sig.AddListenerWithErr(func(context.Context, int) error {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		atomic.AddInt32(&inFlight, -1)
		return nil
	}, "tracker")

	const emitters = 100
	const iters = 50
	var wg sync.WaitGroup
	for i := 0; i < emitters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("g%d", id)
			for j := 0; j < iters; j++ {
				sig.AddListener(noop, key)
				if id%2 == 0 {
					sig.EmitAndWait(context.Background(), id)
				} else {
					sig.Emit(context.Background(), id)
				}
				_ = sig.Len()
				sig.RemoveListener(key)
			}
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got > limit {
		t.Fatalf("peak concurrency %d exceeded the bound %d", got, limit)
	}

	deadline := time.Now().Add(3 * time.Second)
	after := runtime.NumGoroutine()
	for after > base+20 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > base+20 {
		t.Fatalf("possible goroutine leak under bounded load: baseline=%d, after=%d", base, after)
	}
}
