package signals_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
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
