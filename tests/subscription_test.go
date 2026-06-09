package signals_test

import (
	"context"
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

	// EmitAndWait blocks until the (single) listener goroutine — including the
	// internal self-removal — has completed.
	sig.EmitAndWait(context.Background(), 1)
	sig.EmitAndWait(context.Background(), 2)

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
				sig.EmitAndWait(context.Background(), 1)
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

func TestAddOnceWithKey_DedupAndRemoveOnFire(t *testing.T) {
	sig := signals.NewSync[int]()

	if got := sig.AddOnceWithKey(noop, "once"); got != 1 {
		t.Fatalf("expected count 1, got %d", got)
	}
	if got := sig.AddOnceWithKey(noop, "once"); got != -1 {
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
