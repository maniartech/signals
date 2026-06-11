package signals_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maniartech/signals"
)

// RemoveListener must never remove unkeyed listeners (FR-4), and an empty-string key
// is treated as unkeyed (FR-9) — so RemoveListener("") is a no-op returning -1, and
// only a real key removes its listener.
func TestRemoveListenerEmptyKeyOnlyRemovesKeyed(t *testing.T) {
	sig := signals.NewSync[int]()

	var unkeyedCalled int
	var keyedCalled int

	sig.AddListener(func(ctx context.Context, v int) {
		unkeyedCalled++
	})
	sig.AddListener(func(ctx context.Context, v int) {
		keyedCalled++
	}, "k")

	// Empty-string key is unkeyed (FR-9): RemoveListener("") removes nothing and must
	// not touch the unkeyed listener (FR-4).
	if got := sig.RemoveListener(""); got != -1 {
		t.Fatalf("RemoveListener(\"\") = %d; want -1 (no-op, must not remove unkeyed)", got)
	}
	// Removing the real key leaves the unkeyed listener in place.
	if got := sig.RemoveListener("k"); got != 1 {
		t.Fatalf("RemoveListener(\"k\") left %d listeners, want 1 (the unkeyed one)", got)
	}

	sig.Emit(context.Background(), 1)

	if unkeyedCalled != 1 {
		t.Fatalf("Expected unkeyed listener to remain, got %d calls", unkeyedCalled)
	}
	if keyedCalled != 0 {
		t.Fatalf("Expected keyed listener to be removed, got %d calls", keyedCalled)
	}
}

func noop(context.Context, int) {}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --- AddOnce ---

func TestAddOnce_SyncFiresOnceThenAutoRemoves(t *testing.T) {
	sig := signals.NewSync[int]()
	var count int32
	sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&count, 1) })

	if got := sig.Len(); got != 1 {
		t.Fatalf("expected 1 listener after AddOnce, got %d", got)
	}
	sig.Emit(context.Background(), 1)
	sig.Emit(context.Background(), 2)
	sig.Emit(context.Background(), 3)

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("expected handler to fire exactly once, fired %d times", got)
	}
	if got := sig.Len(); got != 0 {
		t.Fatalf("expected listener to auto-remove after firing, Len=%d", got)
	}
}

func TestAddOnce_AsyncFiresOnceThenAutoRemoves(t *testing.T) {
	sig := signals.New[int]()
	var count int32
	sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&count, 1) })

	// TryEmit blocks until the (single) listener goroutine — including the
	// internal self-removal — has completed.
	sig.TryEmit(context.Background(), 1)
	sig.TryEmit(context.Background(), 2)

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("expected handler to fire exactly once, fired %d times", got)
	}
	if got := sig.Len(); got != 0 {
		t.Fatalf("expected auto-remove after firing, Len=%d", got)
	}
}

// TestAddOnce_ConcurrentExactlyOnce is the Phase 3 gate: many simultaneous
// emissions must not double-fire a one-time listener.
func TestAddOnce_ConcurrentExactlyOnce(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		sig := signals.New[int]()
		var count int32
		sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&count, 1) })

		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sig.TryEmit(context.Background(), 1)
			}()
		}
		wg.Wait()

		if got := atomic.LoadInt32(&count); got != 1 {
			t.Fatalf("trial %d: expected exactly one invocation, got %d", trial, got)
		}
	}
}

func TestAddOnce_UnkeyedHiddenFromKeys(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddOnce(noop)

	if got := sig.Len(); got != 1 {
		t.Fatalf("expected the once-listener to be registered, Len=%d", got)
	}
	if keys := sig.Keys(); len(keys) != 0 {
		t.Fatalf("internally generated once key should be hidden from Keys(), got %v", keys)
	}
}

func TestAddOnce_KeyedDedupAndRemoveOnFire(t *testing.T) {
	sig := signals.NewSync[int]()

	if got := sig.AddOnce(noop, "once"); got != 1 {
		t.Fatalf("expected count 1, got %d", got)
	}
	if got := sig.AddOnce(noop, "once"); got != -1 {
		t.Fatalf("expected -1 for duplicate key, got %d", got)
	}
	if !sig.HasKey("once") {
		t.Fatal("expected HasKey(once) true")
	}
	if keys := sig.Keys(); !contains(keys, "once") {
		t.Fatalf("expected keyed once listener in Keys(), got %v", keys)
	}

	sig.Emit(context.Background(), 1)

	if sig.HasKey("once") {
		t.Fatal("expected keyed once listener removed after firing")
	}
}

// --- AddOnceWithErr (error-returning one-shot; completes the 2x2 registration matrix) ---

// On the sync Emit (best-effort) path, a one-shot error listener fires once,
// self-removes, and its error is routed to OnError.
func TestAddOnceWithErr_SyncRoutesToOnErrorAndFiresOnce(t *testing.T) {
	sig := signals.NewSync[int]()
	boom := errors.New("boom")
	var calls, routed int32
	sig.OnError(func(_ context.Context, err error) {
		if errors.Is(err, boom) {
			atomic.AddInt32(&routed, 1)
		}
	})
	sig.AddOnceWithErr(func(context.Context, int) error {
		atomic.AddInt32(&calls, 1)
		return boom
	})

	sig.Emit(context.Background(), 1)
	sig.Emit(context.Background(), 2) // listener already consumed — must not run again

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("one-shot error listener fired %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&routed); got != 1 {
		t.Fatalf("OnError received the error %d times, want 1", got)
	}
	if got := sig.Len(); got != 0 {
		t.Fatalf("expected auto-remove after firing, Len=%d", got)
	}
}

// On TryEmit the one-shot error is collected into the returned (joined) error.
func TestAddOnceWithErr_TryEmitReturnsError(t *testing.T) {
	sig := signals.New[int]()
	boom := errors.New("boom")
	sig.AddOnceWithErr(func(context.Context, int) error { return boom })

	if err := sig.TryEmit(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("TryEmit error = %v, want boom", err)
	}
	if err := sig.TryEmit(context.Background(), 2); err != nil {
		t.Fatalf("second TryEmit error = %v, want nil (one-shot consumed)", err)
	}
}

// A keyed one-shot error listener participates in dedup (returns -1) and is
// addressable/removable before it fires.
func TestAddOnceWithErr_KeyedDedup(t *testing.T) {
	sig := signals.NewSync[int]()
	h := func(context.Context, int) error { return nil }

	if got := sig.AddOnceWithErr(h, "once"); got != 1 {
		t.Fatalf("expected count 1, got %d", got)
	}
	if got := sig.AddOnceWithErr(h, "once"); got != -1 {
		t.Fatalf("expected -1 for duplicate key, got %d", got)
	}
	if !sig.HasKey("once") {
		t.Fatal("expected HasKey(once) true")
	}
}

// A nil handler panics, like every other registration method.
func TestAddOnceWithErr_NilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil handler")
		}
	}()
	signals.NewSync[int]().AddOnceWithErr(nil)
}

// Many simultaneous async emissions must invoke a one-shot error listener exactly
// once (covers the already-fired CAS branch of the error wrapper).
func TestAddOnceWithErr_ConcurrentExactlyOnce(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		sig := signals.New[int]()
		var count int32
		sig.AddOnceWithErr(func(context.Context, int) error {
			atomic.AddInt32(&count, 1)
			return nil
		})

		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = sig.TryEmit(context.Background(), 1)
			}()
		}
		wg.Wait()

		if got := atomic.LoadInt32(&count); got != 1 {
			t.Fatalf("trial %d: expected exactly one invocation, got %d", trial, got)
		}
	}
}

// --- Keys / HasKey ---

func TestKeysAndHasKey(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddListener(noop, "a")
	sig.AddListener(noop, "b")
	sig.AddListener(noop) // unkeyed — must not appear in Keys()

	keys := sig.Keys()
	if len(keys) != 2 || !contains(keys, "a") || !contains(keys, "b") {
		t.Fatalf("expected exactly [a b], got %v", keys)
	}
	if !sig.HasKey("a") || !sig.HasKey("b") {
		t.Fatal("expected HasKey true for a and b")
	}
	if sig.HasKey("missing") {
		t.Fatal("expected HasKey false for missing key")
	}

	sig.RemoveListener("a")
	if sig.HasKey("a") {
		t.Fatal("expected HasKey(a) false after removal")
	}
	if keys := sig.Keys(); len(keys) != 1 || !contains(keys, "b") {
		t.Fatalf("expected [b] after removing a, got %v", keys)
	}
}

func TestKeys_ZeroValueSafe(t *testing.T) {
	var sig signals.SyncSignal[int]
	if keys := sig.Keys(); len(keys) != 0 {
		t.Fatalf("expected empty keys on zero-value signal, got %v", keys)
	}
	if sig.HasKey("x") {
		t.Fatal("expected HasKey false on zero-value signal")
	}
	sig.AddOnce(noop) // must not panic on a zero-value signal
	if sig.Len() != 1 {
		t.Fatalf("expected 1 after AddOnce on zero-value signal, got %d", sig.Len())
	}
}

// TestKeys_ConcurrentSafe exercises Keys()/HasKey() while listeners churn, to be
// run under -race.
func TestKeys_ConcurrentSafe(t *testing.T) {
	sig := signals.New[int]()
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			key := "k" + string(rune('0'+i%10))
			sig.AddListener(noop, key)
			sig.RemoveListener(key)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = sig.Keys()
			_ = sig.HasKey("k1")
		}
	}()

	wg.Wait()
}

func TestSignalOptionsGrowthFuncIsUsed(t *testing.T) {
	var called int32
	opts := &signals.SignalOptions{
		InitialCapacity: 1,
		GrowthFunc: func(currentCap int) int {
			atomic.AddInt32(&called, 1)
			return currentCap + 1
		},
	}

	sig := signals.NewSyncWithOptions[int](opts)
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.AddListener(func(ctx context.Context, v int) {})

	if atomic.LoadInt32(&called) == 0 {
		t.Fatalf("Expected GrowthFunc to be called when capacity grows")
	}
}

func TestSignalOptionsGrowthFuncIsUsedForErrorListeners(t *testing.T) {
	var called int32
	opts := &signals.SignalOptions{
		InitialCapacity: 1,
		GrowthFunc: func(currentCap int) int {
			atomic.AddInt32(&called, 1)
			return currentCap + 1
		},
	}

	sig := signals.NewSyncWithOptions[int](opts)
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })

	if atomic.LoadInt32(&called) == 0 {
		t.Fatalf("Expected GrowthFunc to be called when capacity grows for error listeners")
	}
}

// Test SignalListener type directly
func TestSignalListener(t *testing.T) {
	// Create a concrete listener
	listener := func(ctx context.Context, s string) {
		// Test listener implementation
	}

	// Test calling the listener
	listener(context.Background(), "test")
}

// Test SignalListenerErr type directly
func TestSignalListenerErr(t *testing.T) {
	// Create a concrete error-returning listener
	listenerErr := func(ctx context.Context, i int) error {
		if i < 0 {
			return context.Canceled
		}
		return nil
	}

	// Test calling the listener with no error
	err := listenerErr(context.Background(), 5)
	if err != nil {
		t.Errorf("Expected no error for positive value, got %v", err)
	}

	// Test calling the listener with error
	err = listenerErr(context.Background(), -1)
	if err == nil {
		t.Error("Expected error for negative value")
	}
}

// --- AddListenerWithCancel: handle-based repeating subscription ---

func TestAddListenerWithCancel_NilPanics(t *testing.T) {
	sig := signals.NewSync[int]()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil listener")
		}
	}()
	sig.AddListenerWithCancel(nil)
}

func TestAddListenerWithCancel_CancelRemovesListener(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	cancel := sig.AddListenerWithCancel(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
	})
	sig.Emit(context.Background(), 1) // fires
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 call before cancel, got %d", got)
	}
	cancel()
	if sig.Len() != 0 {
		t.Fatalf("expected 0 listeners after cancel, got %d", sig.Len())
	}
	sig.Emit(context.Background(), 2) // must not fire
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected still 1 call after cancel, got %d", got)
	}
}

// Calling the canceller twice is safe, and a stale canceller must never remove a
// later re-add of the same caller key (the sync.Once guard).
func TestAddListenerWithCancel_IdempotentAndStaleSafe(t *testing.T) {
	sig := signals.NewSync[int]()
	cancel := sig.AddListenerWithCancel(func(ctx context.Context, v int) {}, "k")
	cancel()
	cancel() // double cancel: safe no-op

	var calls int32
	if n := sig.AddListener(func(ctx context.Context, v int) { atomic.AddInt32(&calls, 1) }, "k"); n != 1 {
		t.Fatalf("expected re-add under same key to succeed (count 1), got %d", n)
	}
	cancel() // stale: sync.Once already fired, must NOT remove the re-added listener
	sig.Emit(context.Background(), 1)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("stale canceller removed the re-added listener; got %d calls", got)
	}
}

// A duplicate caller key adds nothing and the returned canceller must be a no-op
// (it must not remove the pre-existing listener that owns that key).
func TestAddListenerWithCancel_DuplicateKeyNoOpCanceller(t *testing.T) {
	sig := signals.NewSync[int]()
	var first int32
	sig.AddListener(func(ctx context.Context, v int) { atomic.AddInt32(&first, 1) }, "dup")
	cancel := sig.AddListenerWithCancel(func(ctx context.Context, v int) {}, "dup")
	if sig.Len() != 1 {
		t.Fatalf("expected duplicate not added (len 1), got %d", sig.Len())
	}
	cancel()
	if sig.Len() != 1 {
		t.Fatalf("no-op canceller removed the pre-existing listener; len %d", sig.Len())
	}
	sig.Emit(context.Background(), 1)
	if atomic.LoadInt32(&first) != 1 {
		t.Fatal("pre-existing listener was wrongly removed")
	}
}

func TestAddListenerWithCancel_AsyncRemoves(t *testing.T) {
	sig := signals.New[int]()
	var calls int32
	cancel := sig.AddListenerWithCancel(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
	})
	cancel()
	_ = sig.TryEmit(context.Background(), 1) // waits; handler removed, must not run
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected 0 calls after cancel, got %d", got)
	}
}

// --- AddOnceWithCancel: handle-based one-shot, cancellable before it fires ---

func TestAddOnceWithCancel_NilPanics(t *testing.T) {
	sig := signals.NewSync[int]()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil listener")
		}
	}()
	sig.AddOnceWithCancel(nil)
}

func TestAddOnceWithCancel_CancelBeforeFire(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	cancel := sig.AddOnceWithCancel(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
	})
	if sig.Len() != 1 {
		t.Fatalf("expected 1 pending one-shot, got %d", sig.Len())
	}
	cancel() // remove before it ever fires
	if sig.Len() != 0 {
		t.Fatalf("expected 0 listeners after cancel, got %d", sig.Len())
	}
	sig.Emit(context.Background(), 1)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("one-shot fired after being canceled; got %d", got)
	}
}

func TestAddOnceWithCancel_FiresOnceThenCancelNoOp(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	cancel := sig.AddOnceWithCancel(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
	})
	sig.Emit(context.Background(), 1) // fires once, self-removes
	sig.Emit(context.Background(), 2) // one-shot is gone
	cancel()                          // after fire: safe no-op
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 fire, got %d", got)
	}
}

// The canceller racing the fire must never let the handler run more than once
// (the shared atomic fired-guard). Run under -race.
func TestAddOnceWithCancel_ConcurrentCancelVsFireAtMostOnce(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		sig := signals.New[int]()
		var calls int32
		cancel := sig.AddOnceWithCancel(func(ctx context.Context, v int) {
			atomic.AddInt32(&calls, 1)
		})
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _ = sig.TryEmit(context.Background(), 1) }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); cancel() }()
		wg.Wait()
		if got := atomic.LoadInt32(&calls); got > 1 {
			t.Fatalf("trial %d: one-shot fired %d times (must be at most once)", trial, got)
		}
	}
}

func TestAddOnceWithCancel_DuplicateKeyNoOp(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddOnce(func(ctx context.Context, v int) {}, "once")
	cancel := sig.AddOnceWithCancel(func(ctx context.Context, v int) {}, "once") // duplicate
	if sig.Len() != 1 {
		t.Fatalf("expected duplicate not added (len 1), got %d", sig.Len())
	}
	cancel() // no-op, must not remove the pre-existing one-shot
	if sig.Len() != 1 {
		t.Fatalf("no-op canceller removed pre-existing one-shot; len %d", sig.Len())
	}
}
