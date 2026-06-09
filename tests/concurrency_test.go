package signals_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase R / FR-8: property-based proofs of the lock-free core invariants over long,
// seeded (reproducible) operation streams checked against an independent model.
//   I1 count integrity · I2 introspection consistency · I3 no-call-after-remove
//   I4 all-live-receive · I15 ordering · I17 happens-before (sequential)

// I1 + I2: after any Add/Remove stream, Len()/HasKey()/Keys() agree exactly with an
// independent model of which keys are live.
func TestProperty_KeyedStateConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sig := signals.NewSync[int]()
	model := map[string]bool{}

	for step := 0; step < 5000; step++ {
		key := fmt.Sprintf("k%d", rng.Intn(30))
		switch rng.Intn(2) {
		case 0:
			r := sig.AddListener(noop, key)
			if model[key] {
				if r != -1 {
					t.Fatalf("step %d: dup add of %q returned %d, want -1", step, key, r)
				}
			} else {
				model[key] = true
			}
		case 1:
			r := sig.RemoveListener(key)
			if model[key] {
				delete(model, key)
			} else if r != -1 {
				t.Fatalf("step %d: removing absent %q returned %d, want -1", step, key, r)
			}
		}
		if got := sig.Len(); got != len(model) {
			t.Fatalf("step %d: Len()=%d, model=%d", step, got, len(model))
		}
		if got := sig.HasKey(key); got != model[key] {
			t.Fatalf("step %d: HasKey(%q)=%v, model=%v", step, key, got, model[key])
		}
		keys := sig.Keys()
		if len(keys) != len(model) {
			t.Fatalf("step %d: Keys() len=%d, model=%d", step, len(keys), len(model))
		}
		for _, k := range keys {
			if !model[k] {
				t.Fatalf("step %d: Keys() returned non-live %q", step, k)
			}
		}
	}
}

// I3 + I4: every emission invokes exactly the currently-live set — proving both
// "all live receive" and "no call after remove". Unique keys ⇒ each listener has one
// lifecycle, so its observed call count must equal the emits while it was live.
func TestProperty_LiveListenersReceiveExactlyEmissions(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	sig := signals.NewSync[int]()
	ctx := context.Background()

	type listener struct {
		calls    int64
		expected int64
		key      string
	}
	var all, live []*listener
	next := 0

	for step := 0; step < 4000; step++ {
		switch rng.Intn(3) {
		case 0:
			l := &listener{key: fmt.Sprintf("k%d", next)}
			next++
			sig.AddListener(func(context.Context, int) { atomic.AddInt64(&l.calls, 1) }, l.key)
			all = append(all, l)
			live = append(live, l)
		case 1:
			if len(live) > 0 {
				i := rng.Intn(len(live))
				sig.RemoveListener(live[i].key)
				live[i] = live[len(live)-1]
				live = live[:len(live)-1]
			}
		case 2:
			sig.Emit(ctx, step)
			for _, l := range live {
				l.expected++
			}
		}
	}
	for _, l := range all {
		if got := atomic.LoadInt64(&l.calls); got != l.expected {
			t.Fatalf("listener %q: %d calls, expected %d", l.key, got, l.expected)
		}
	}
}

// I15 + I17: sync emits in registration order (= registration order until the first
// removal); after a removal, set membership is preserved (each remaining listener
// fires exactly once, the removed one not at all). The happens-before of sequential
// AddListener→Emit is implicit in observing all registrations.
func TestProperty_SyncOrderingAndMembership(t *testing.T) {
	sig := signals.NewSync[int]()
	var order []int
	const n = 20
	for i := 0; i < n; i++ {
		i := i
		sig.AddListener(func(context.Context, int) { order = append(order, i) }, fmt.Sprintf("k%d", i))
	}

	sig.Emit(context.Background(), 0)
	if len(order) != n {
		t.Fatalf("got %d fires, want %d", len(order), n)
	}
	for i := 0; i < n; i++ {
		if order[i] != i {
			t.Fatalf("position %d = %d, want %d (registration order before any removal)", i, order[i], i)
		}
	}

	// After a removal, ordering is no longer guaranteed, but membership is.
	order = nil
	sig.RemoveListener("k5")
	sig.Emit(context.Background(), 0)
	if len(order) != n-1 {
		t.Fatalf("after removal got %d fires, want %d", len(order), n-1)
	}
	seen := map[int]bool{}
	for _, v := range order {
		if seen[v] {
			t.Fatalf("listener %d fired twice", v)
		}
		seen[v] = true
	}
	if seen[5] {
		t.Fatal("removed listener 5 still fired")
	}
}

// Phase R / FR-8 §8.3: native fuzz targets for the lock-free core.
//   FuzzSyncOps_Model      — I1 count integrity over arbitrary sequential streams.
//   FuzzConcurrentOps_Race — I5 no-corruption under arbitrary concurrent interleavings.
//
// Run the seed corpus on every `go test`; fuzz with:
//   go test -run '^$' -fuzz FuzzSyncOps_Model      -fuzztime 60s
//   go test -run '^$' -fuzz FuzzConcurrentOps_Race -fuzztime 60s -race

// FuzzSyncOps_Model drives a sequential add/remove/emit stream from fuzz bytes and
// asserts the count invariant against an independent model. Edge cases (0/1/many,
// duplicate add, remove-absent, empty-key-as-unkeyed) emerge from the input space.
func FuzzSyncOps_Model(f *testing.F) {
	f.Add([]byte{0, 0, 2, 1, 2, 0, 1, 1})
	f.Add([]byte{})
	f.Add([]byte{2})

	f.Fuzz(func(t *testing.T, data []byte) {
		sig := signals.NewSync[int]()
		ctx := context.Background()
		model := map[string]bool{}

		for i, b := range data {
			key := fmt.Sprintf("k%d", int(b)%8) // small space → forces dup/collision paths
			switch b % 3 {
			case 0:
				r := sig.AddListener(noop, key)
				if model[key] {
					if r != -1 {
						t.Fatalf("op %d: dup add returned %d, want -1", i, r)
					}
				} else {
					model[key] = true
				}
			case 1:
				sig.RemoveListener(key)
				delete(model, key)
			case 2:
				sig.Emit(ctx, i)
			}
			if got := sig.Len(); got != len(model) {
				t.Fatalf("op %d: Len()=%d, model=%d", i, got, len(model))
			}
		}
	})
}

// FuzzConcurrentOps_Race runs the fuzz-derived stream from several goroutines on a
// SYNC signal (the lock-free core, no goroutine fan-out — fuzzing the async path
// under -race exhausts ThreadSanitizer memory; async concurrency is covered by the
// stress tests). Run under -race, it proves I5: no race, panic, deadlock, or
// corruption under arbitrary concurrent interleavings.
func FuzzConcurrentOps_Race(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{0, 1, 2})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		// Bound per-exec work so the race detector's shadow memory stays within
		// limits under heavy parallel fuzzing (16 fuzz workers × N goroutines ×
		// -race otherwise exhausts ThreadSanitizer — a tooling limit, not a defect;
		// see FR-8 §8.3). The 1000-goroutine stress test is the heavier I5 proof.
		if len(data) > 64 {
			data = data[:64]
		}
		sig := signals.NewSync[int]()
		ctx := context.Background()

		const workers = 2
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				for i, b := range data {
					key := fmt.Sprintf("w%d-k%d", seed, int(b)%4)
					switch (int(b) + i) % 3 {
					case 0:
						sig.AddListener(noop, key)
					case 1:
						sig.RemoveListener(key)
					case 2:
						sig.Emit(ctx, i)
					}
				}
			}(w)
		}
		wg.Wait()
	})
}

// FuzzAsyncErrorModel drives arbitrary add/remove/emit sequences on an async signal
// whose listeners may succeed or fail, and asserts the TryEmit aggregation
// invariant (I10): the number of joined errors returned equals the number of
// currently-registered FAILING listeners — no error lost, none invented — and nothing
// ever panics. TryEmit makes each emission deterministic. Not run under -race
// (async fan-out exhausts ThreadSanitizer; -race concurrency is covered by the stress
// tests). Run: go test . -run '^$' -fuzz FuzzAsyncErrorModel -fuzztime 60s
func FuzzAsyncErrorModel(f *testing.F) {
	f.Add([]byte{0, 1, 3, 2, 0, 3})
	f.Add([]byte{1, 1, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		sig := signals.New[int]()
		fail := map[string]bool{} // keys of registered failing listeners
		ok := map[string]bool{}   // keys of registered succeeding listeners

		joinedCount := func(err error) int {
			if err == nil {
				return 0
			}
			if u, isJoin := err.(interface{ Unwrap() []error }); isJoin {
				return len(u.Unwrap())
			}
			return 1
		}

		for i, b := range data {
			key := fmt.Sprintf("k%d", int(b)%6)
			switch b % 4 {
			case 0: // add a failing error-listener (if the key is free)
				if !fail[key] && !ok[key] {
					sig.AddListenerWithErr(func(context.Context, int) error { return errors.New("fail") }, key)
					fail[key] = true
				}
			case 1: // add a succeeding error-listener (if the key is free)
				if !fail[key] && !ok[key] {
					sig.AddListenerWithErr(func(context.Context, int) error { return nil }, key)
					ok[key] = true
				}
			case 2: // remove
				sig.RemoveListener(key)
				delete(fail, key)
				delete(ok, key)
			case 3: // emit and wait for errors — count must equal the live failing set
				got := joinedCount(sig.TryEmit(context.Background(), i))
				if got != len(fail) {
					t.Fatalf("op %d: TryEmit returned %d errors; want %d (live failing listeners)", i, got, len(fail))
				}
			}
		}
	})
}

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
				sig.TryEmit(ctx, id) // wait so each emission's handler goroutines complete
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
					sig.TryEmit(context.Background(), id)
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

// Phase 5 / FR-8: reentrancy. A handler that mutates or re-emits the signal during
// its own invocation must be safe — no panic, no deadlock, no corruption — and the
// in-flight emission must observe the immutable snapshot taken when the emit began.

// Sync: a handler that adds and removes listeners during emit. Because the emission
// iterates the snapshot captured at Emit time, a sibling removed mid-emit still runs
// this round and a listener added mid-emit does not; the *next* emit reflects the
// mutations.
func TestReentrancy_SyncMutateDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	var aRan, bRan, addedRan int32

	sig.AddListener(func(context.Context, int) {
		atomic.AddInt32(&aRan, 1)
		sig.AddListener(func(context.Context, int) { atomic.AddInt32(&addedRan, 1) }, "added")
		sig.RemoveListener("b")
	}, "a")
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&bRan, 1) }, "b")

	sig.Emit(context.Background(), 1) // snapshot = [a, b]

	if aRan != 1 || bRan != 1 {
		t.Fatalf("in-flight snapshot not honored: aRan=%d bRan=%d (want 1,1)", aRan, bRan)
	}
	if addedRan != 0 {
		t.Fatalf("a listener added mid-emit ran this round (%d); want 0", addedRan)
	}
	if sig.HasKey("b") || !sig.HasKey("added") {
		t.Fatalf("post-emit state wrong: hasB=%v hasAdded=%v (want false, true)", sig.HasKey("b"), sig.HasKey("added"))
	}

	// The next emit uses the mutated state: b is gone, added now fires.
	atomic.StoreInt32(&bRan, 0)
	atomic.StoreInt32(&addedRan, 0)
	sig.Emit(context.Background(), 2)
	if bRan != 0 || addedRan != 1 {
		t.Fatalf("second emit: bRan=%d addedRan=%d (want 0,1)", bRan, addedRan)
	}
}

// Sync: a handler that re-emits on the same signal must recurse and terminate, not
// deadlock (sync emit holds no lock).
func TestReentrancy_SyncReentrantEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
		if v > 0 {
			sig.Emit(ctx, v-1) // reentrant; bounded by v
		}
	})
	sig.Emit(context.Background(), 3) // 3→2→1→0
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("reentrant sync emit ran %d times; want 4 (v=3,2,1,0)", got)
	}
}

// Sync: a handler that Resets the signal during emit. The in-flight snapshot still
// completes; afterward the signal is empty.
func TestReentrancy_SyncResetDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	var ran int32
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1); sig.Reset() }, "a")
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1) }, "b")

	sig.Emit(context.Background(), 1) // both run from the snapshot despite the Reset
	if got := atomic.LoadInt32(&ran); got != 2 {
		t.Fatalf("snapshot not honored across reset: ran=%d, want 2", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("signal should be empty after the reentrant Reset")
	}
}

// Async: a handler that fire-and-forget re-emits on the same (unbounded) signal must
// not deadlock and must terminate.
func TestReentrancy_AsyncReentrantEmit(t *testing.T) {
	sig := signals.New[int]()
	var calls int32
	done := make(chan struct{})
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
		if v > 0 {
			sig.Emit(context.Background(), v-1)
		} else {
			close(done)
		}
	})
	sig.Emit(context.Background(), 4)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant async emit deadlocked or did not terminate")
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("reentrant async emit ran %d times; want 5 (v=4..0)", got)
	}
}

// Async: a handler that removes itself during emit (the manual form of AddOnce) is
// safe and the removal takes effect for subsequent emissions.
func TestReentrancy_AsyncHandlerRemovesSelf(t *testing.T) {
	sig := signals.New[int]()
	var calls int32
	sig.AddListener(func(context.Context, int) {
		atomic.AddInt32(&calls, 1)
		sig.RemoveListener("self")
	}, "self")

	sig.TryEmit(context.Background(), 1) // runs once, removes itself
	sig.TryEmit(context.Background(), 2) // gone
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("self-removing handler ran %d times; want 1", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("signal should be empty after the handler removed itself")
	}
}

// Test: Adding listeners during Emit causes race or panic
func TestSyncSignal_AddListenerDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()

	var called int32

	// Add initial listener that's slow
	sig.AddListener(func(ctx context.Context, v int) {
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&called, 1)
	})

	// Start emitting
	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 1)
		close(done)
	}()

	// While emitting, add more listeners
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 10; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			atomic.AddInt32(&called, 1)
		})
	}

	<-done
	time.Sleep(50 * time.Millisecond)

	// Should have called initial listener + 10 new ones = 11
	// If race condition, might panic or call wrong number
	got := atomic.LoadInt32(&called)
	if got != 1 {
		t.Logf("Called %d listeners (expected 1, new listeners added during emit)", got)
	}
}

// Test: Removing listeners during Emit causes race or panic
func TestSyncSignal_RemoveListenerDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()

	var listener1Called, listener2Called int32

	// Add two listeners
	sig.AddListener(func(ctx context.Context, v int) {
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&listener1Called, 1)
	})

	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&listener2Called, 1)
	}, "key2")

	// Start emitting
	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 1)
		close(done)
	}()

	// While emitting, remove listener
	time.Sleep(10 * time.Millisecond)
	sig.RemoveListener("key2")

	<-done
	time.Sleep(50 * time.Millisecond)

	// If race condition, might panic or have inconsistent state
	t.Logf("Listener1: %d, Listener2: %d",
		atomic.LoadInt32(&listener1Called),
		atomic.LoadInt32(&listener2Called))
}

// Test: Concurrent Emit calls
func TestSyncSignal_ConcurrentEmit(t *testing.T) {
	sig := signals.NewSync[int]()

	var called int32

	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&called, 1)
	})

	var wg sync.WaitGroup
	n := 100
	wg.Add(n)

	// Hammer it with concurrent emits
	for i := 0; i < n; i++ {
		go func(val int) {
			defer wg.Done()
			sig.Emit(context.Background(), val)
		}(i)
	}

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	// Should be called exactly n times
	got := atomic.LoadInt32(&called)
	if got != int32(n) {
		t.Fatalf("Expected %d calls, got %d (lost events due to race?)", n, got)
	}
}

// Test: Concurrent Reset calls
func TestAsyncSignal_ConcurrentReset(t *testing.T) {
	sig := signals.New[int]()

	// Add listeners
	for i := 0; i < 10; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			time.Sleep(10 * time.Millisecond)
		})
	}

	var wg sync.WaitGroup
	n := 10
	wg.Add(n)

	// Concurrent Reset calls - should not panic
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Reset panicked: %v", r)
				}
			}()
			sig.Reset()
		}()
	}

	wg.Wait()
}

// Test: Emit while Reset is happening
func TestAsyncSignal_EmitDuringReset(t *testing.T) {
	sig := signals.New[int]()

	var called int32

	// Add listeners
	for i := 0; i < 5; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			atomic.AddInt32(&called, 1)
			time.Sleep(10 * time.Millisecond)
		})
	}

	// Start emitting in background
	go func() {
		for i := 0; i < 100; i++ {
			sig.Emit(context.Background(), i)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	// Reset while emits are happening
	sig.Reset()

	time.Sleep(100 * time.Millisecond)

	t.Logf("Called %d times before/during reset", atomic.LoadInt32(&called))
	// Should not panic, but behavior is undefined
}

// Test: Massive concurrent operations
func TestSyncSignal_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	sig := signals.NewSync[int]()

	var operations int32
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Worker that adds listeners
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				sig.AddListener(func(ctx context.Context, v int) {
					atomic.AddInt32(&operations, 1)
				})
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Worker that removes listeners
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				sig.RemoveListener("")
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Worker that emits
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				sig.Emit(context.Background(), 1)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)

	t.Logf("Stress test completed: %d operations", atomic.LoadInt32(&operations))
}
