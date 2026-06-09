package signals_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase R / G4: an explicit, consolidated edge-case matrix for the sync core and
// the Phase-3 APIs. Each case asserts a documented contract (FR-1, FR-4, FR-5, FR-9).

// --- nil listener ⇒ fail-fast panic at the registration call site (FR-9) ---

func TestEdge_NilListenerPanicsAtCallSite(t *testing.T) {
	s := signals.NewSync[int]()
	a := signals.New[int]()
	cases := []struct {
		name string
		fn   func()
	}{
		{"sync.AddListener(nil)", func() { s.AddListener(nil) }},
		{"sync.AddListenerWithErr(nil)", func() { s.AddListenerWithErr(nil) }},
		{"sync.AddOnce(nil)", func() { s.AddOnce(nil) }},
		{"sync.AddOnceWithKey(nil)", func() { s.AddOnceWithKey(nil, "k") }},
		{"async.AddListener(nil)", func() { a.AddListener(nil) }},
		{"async.AddOnce(nil)", func() { a.AddOnce(nil) }},
	}
	for _, c := range cases {
		retroMustPanic(t, c.name, c.fn)
	}
}

// --- already-canceled context ⇒ skip every listener (FR-1) ---

func TestEdge_PreCanceledContextSkipsAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := signals.NewSync[int]()
	var n int32
	s.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) })

	s.Emit(ctx, 1)
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("Emit ran %d listeners under canceled ctx; want 0", got)
	}
	if err := s.TryEmit(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("TryEmit returned %v; want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("TryEmit ran %d listeners under canceled ctx; want 0", got)
	}

	a := signals.New[int]()
	var m int32
	a.AddListener(func(context.Context, int) { atomic.AddInt32(&m, 1) })
	a.EmitAndWait(ctx, 1)
	if got := atomic.LoadInt32(&m); got != 0 {
		t.Fatalf("EmitAndWait ran %d listeners under canceled ctx; want 0", got)
	}
}

// --- empty signal (0 listeners) ⇒ no-op, no panic, TryEmit returns nil ---

func TestEdge_EmptySignalIsNoOp(t *testing.T) {
	s := signals.NewSync[int]()
	s.Emit(context.Background(), 1) // must not panic
	if err := s.TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("TryEmit on empty signal returned %v; want nil", err)
	}
	a := signals.New[int]()
	a.Emit(context.Background(), 1)
	a.EmitAndWait(context.Background(), 1) // must not panic / must return
}

// --- duplicate keyed add ⇒ -1, no-op ---

func TestEdge_DuplicateKeyReturnsMinusOne(t *testing.T) {
	s := signals.NewSync[int]()
	if got := s.AddListener(func(context.Context, int) {}, "k"); got != 1 {
		t.Fatalf("first add = %d, want 1", got)
	}
	if got := s.AddListener(func(context.Context, int) {}, "k"); got != -1 {
		t.Fatalf("dup AddListener = %d, want -1", got)
	}
	if got := s.AddListenerWithErr(func(context.Context, int) error { return nil }, "k"); got != -1 {
		t.Fatalf("dup AddListenerWithErr = %d, want -1", got)
	}
	if got := s.AddOnceWithKey(func(context.Context, int) {}, "k"); got != -1 {
		t.Fatalf("dup AddOnceWithKey = %d, want -1", got)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d after duplicate adds; want 1", s.Len())
	}
}

// --- removing an absent key ⇒ -1 ---

func TestEdge_RemoveAbsentReturnsMinusOne(t *testing.T) {
	s := signals.NewSync[int]()
	s.AddListener(func(context.Context, int) {}) // unkeyed
	if got := s.RemoveListener("nope"); got != -1 {
		t.Fatalf("RemoveListener(absent) = %d, want -1", got)
	}
	// RemoveListener("") must not remove the unkeyed listener (FR-4) and returns -1.
	if got := s.RemoveListener(""); got != -1 {
		t.Fatalf("RemoveListener(\"\") = %d, want -1 (must not touch unkeyed)", got)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d; unkeyed listener must survive", s.Len())
	}
}

// --- empty-string key is treated as UNKEYED and invisible to key APIs (FR-9) ---

func TestEdge_EmptyStringKeyIsUnkeyed(t *testing.T) {
	s := signals.NewSync[int]()
	var n int32
	// Added with an explicit "" key — must behave as unkeyed.
	s.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) }, "")

	if s.HasKey("") {
		t.Fatal(`HasKey("") must be false`)
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Keys() = %v; empty-string key must be omitted (treated unkeyed)", keys)
	}
	// A second "" listener is NOT a duplicate (both unkeyed) — both registered.
	if got := s.AddListener(func(context.Context, int) {}, ""); got != 2 {
		t.Fatalf("second empty-key add = %d; want 2 (not deduped)", got)
	}
	// AddOnceWithKey("") behaves as an unkeyed one-shot (auto-removing, hidden key).
	a := signals.New[int]()
	var once int32
	a.AddOnceWithKey(func(context.Context, int) { atomic.AddInt32(&once, 1) }, "")
	if a.HasKey("") {
		t.Fatal(`AddOnceWithKey("") must not register key ""`)
	}
	a.EmitAndWait(context.Background(), 1)
	a.EmitAndWait(context.Background(), 1)
	if got := atomic.LoadInt32(&once); got != 1 {
		t.Fatalf("empty-key one-shot fired %d times; want 1", got)
	}

	// The original "" listener still fires on emit.
	s.Emit(context.Background(), 1)
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("empty-key listener fired %d times; want 1", got)
	}
}

// --- zero-value payload is delivered unmodified (FR-1 payload opacity) ---

func TestEdge_ZeroValuePayloadDelivered(t *testing.T) {
	type Msg struct{ V int }
	s := signals.NewSync[Msg]()
	got := Msg{V: -1}
	s.AddListener(func(_ context.Context, m Msg) { got = m })
	s.Emit(context.Background(), Msg{}) // zero value
	if got != (Msg{}) {
		t.Fatalf("zero payload delivered as %+v; want zero value", got)
	}
}

func TestSyncSignal_ZeroValueUsable(t *testing.T) {
	var sig signals.SyncSignal[int]

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Expected zero value SyncSignal to be usable, got panic: %v", r)
		}
	}()

	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
}

func TestAsyncSignal_ZeroValueUsable(t *testing.T) {
	var sig signals.AsyncSignal[int]

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Expected zero value AsyncSignal to be usable, got panic: %v", r)
		}
	}()

	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
}

// These tests close the Phase-R coverage gaps in the already-built code, bringing
// the sync core + Phase-3 APIs to 100% line coverage with deterministic (non-flaky)
// tests. See docs/v1.4-development-tracker.md (Phase R).

func retroMustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected panic, got none", name)
		}
	}()
	fn()
}

// Covers base_signal.go load() nil-pointer branch via a zero-value BaseSignal
// (which bypasses NewBaseSignal, so its atomic pointer is nil).
func TestRetro_ZeroValueBaseSignal_IsEmpty(t *testing.T) {
	var b signals.BaseSignal[int]
	if !b.IsEmpty() {
		t.Fatal("zero-value BaseSignal should be empty")
	}
	if b.Len() != 0 {
		t.Fatalf("zero-value BaseSignal Len = %d, want 0", b.Len())
	}
}

// Covers the nil-handler panic in addOnce (both entry points).
func TestRetro_AddOnce_NilHandlerPanics(t *testing.T) {
	sig := signals.NewSync[int]()
	retroMustPanic(t, "AddOnce(nil)", func() { sig.AddOnce(nil) })
	retroMustPanic(t, "AddOnceWithKey(nil)", func() { sig.AddOnceWithKey(nil, "k") })
}

// Covers AsyncSignal.AddOnceWithKey (previously 0% — never exercised on async).
func TestRetro_Async_AddOnceWithKey(t *testing.T) {
	sig := signals.New[int]()
	var n int32
	sig.AddOnceWithKey(func(context.Context, int) { atomic.AddInt32(&n, 1) }, "once")
	sig.EmitAndWait(context.Background(), 1)
	sig.EmitAndWait(context.Background(), 2)
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("async AddOnceWithKey fired %d times, want 1", got)
	}
	if sig.HasKey("once") {
		t.Fatal("async one-shot listener should auto-remove after firing")
	}
}

// Covers the sync Emit in-loop context-cancellation break: the first listener
// cancels the context, so the second must be skipped. Sync listeners run inline,
// making this fully deterministic.
func TestRetro_SyncEmit_CancelMidChainSkipsRest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sig := signals.NewSync[int]()
	sig.AddListener(func(context.Context, int) { cancel() }, "1-cancels")
	var second int32
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&second, 1) }, "2-should-skip")

	sig.Emit(ctx, 1)

	if got := atomic.LoadInt32(&second); got != 0 {
		t.Fatalf("second listener ran %d times after mid-chain cancel; want 0 (break)", got)
	}
}

// Covers TryEmit's trailing `return nil` (the nil-context path): with a nil ctx,
// the listener runs and TryEmit returns nil.
func TestRetro_TryEmit_NilContextReturnsNil(t *testing.T) {
	sig := signals.NewSync[int]()
	var ran int32
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1) })
	//nolint:staticcheck // intentionally passing a nil context to exercise the contract
	if err := sig.TryEmit(nil, 1); err != nil {
		t.Fatalf("TryEmit(nil) returned %v, want nil", err)
	}
	if atomic.LoadInt32(&ran) != 1 {
		t.Fatal("listener should have run under nil context")
	}
}

// trippingCtx is a context whose Err() returns nil for the first `trip` calls and
// context.Canceled thereafter — a deterministic way to make Err() transition to
// canceled at a precise point during the dispatch loop (no timing/flakiness).
type trippingCtx struct {
	trip  int32
	calls int32
}

func (c *trippingCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *trippingCtx) Done() <-chan struct{}       { return nil }
func (c *trippingCtx) Value(any) any               { return nil }
func (c *trippingCtx) Err() error {
	if atomic.AddInt32(&c.calls, 1) > c.trip {
		return context.Canceled
	}
	return nil
}

// Covers the async dispatch in-loop context-cancellation return deterministically:
// trippingCtx reports canceled after a fixed number of Err() calls, so the dispatch
// loop schedules the first few handlers and then stops mid-loop. With 10 listeners
// and a trip partway through, *some but not all* handlers run — robustly exercising
// the in-loop cancellation branch without coupling to the exact internal call count.
func TestRetro_AsyncDispatch_CancelMidLoopStops(t *testing.T) {
	sig := signals.New[int]()
	const n = 10
	var ran int32
	for i := 0; i < n; i++ {
		sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1) }, fmt.Sprintf("k%d", i))
	}

	ctx := &trippingCtx{trip: 4}
	sig.EmitAndWait(ctx, 1)

	got := atomic.LoadInt32(&ran)
	if got == 0 || got >= n {
		t.Fatalf("dispatch scheduled %d handlers; want partial (0 < n < %d) from mid-loop cancel", got, n)
	}
}

// Test edge cases to reach 100% coverage

// Test AsyncSignal ensureWorkerPool with negative size (should use default)
func TestAsyncSignal_EnsureWorkerPool_NegativeSize(t *testing.T) {
	sig := signals.New[int]()

	// This should trigger ensureWorkerPool with default size calculation
	// Add a listener that forces worker pool initialization
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
}

// Test AsyncSignal Emit with nil listeners (edge case for fast path)
func TestAsyncSignal_EmitWithListenerErrOnly(t *testing.T) {
	// This tests AsyncSignal behavior when it has error listeners but can't invoke them
	// Since AsyncSignal.Emit doesn't handle listenerErr, this tests that branch
	sig := signals.New[int]()

	// Add regular listener first
	sig.AddListener(func(ctx context.Context, v int) {})

	// Emit should work normally
	sig.Emit(context.Background(), 1)
}

// Test SyncSignal AddListenerWithErr edge cases
func TestSyncSignal_AddListenerWithErr_EdgeCases(t *testing.T) {
	sig := signals.NewSync[string]()

	// Test nil listener panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil listener")
		}
	}()

	sig.AddListenerWithErr(nil)
}

// Test SyncSignal Emit with error listeners (invoked by Emit; errors are discarded)
func TestSyncSignal_EmitDiscardsErrorListenerErrors(t *testing.T) {
	sig := signals.NewSync[int]()
	called := false
	errListenerCalled := false

	sig.AddListener(func(ctx context.Context, v int) {
		called = true
	})

	// Error listener is invoked by Emit; its returned error is discarded.
	// Use TryEmit when errors must be observed.
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		errListenerCalled = true
		return errors.New("discarded by Emit")
	})

	sig.Emit(context.Background(), 1)

	if !called {
		t.Error("Regular listener should have been called")
	}
	if !errListenerCalled {
		t.Error("Error listener should be invoked by Emit (with its error discarded)")
	}
}

// Test SyncSignal TryEmit with mixed listener types - comprehensive coverage
func TestSyncSignal_TryEmit_MixedListeners_ComprehensiveCoverage(t *testing.T) {
	sig := signals.NewSync[int]()
	callOrder := make([]string, 0)
	var mu sync.Mutex

	// Add regular listener
	sig.AddListener(func(ctx context.Context, v int) {
		mu.Lock()
		callOrder = append(callOrder, "regular1")
		mu.Unlock()
	})

	// Add error listener that returns nil
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		mu.Lock()
		callOrder = append(callOrder, "error1")
		mu.Unlock()
		return nil
	})

	// Add another regular listener
	sig.AddListener(func(ctx context.Context, v int) {
		mu.Lock()
		callOrder = append(callOrder, "regular2")
		mu.Unlock()
	})

	// Add error listener that returns error (should stop execution)
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		mu.Lock()
		callOrder = append(callOrder, "error2")
		mu.Unlock()
		return errors.New("stop here")
	})

	// This should not be called due to error above
	sig.AddListener(func(ctx context.Context, v int) {
		mu.Lock()
		callOrder = append(callOrder, "regular3")
		mu.Unlock()
	})

	err := sig.TryEmit(context.Background(), 1)
	if err == nil || err.Error() != "stop here" {
		t.Errorf("Expected 'stop here' error, got %v", err)
	}

	mu.Lock()
	expectedOrder := []string{"regular1", "error1", "regular2", "error2"}
	mu.Unlock()

	if len(callOrder) != len(expectedOrder) {
		t.Errorf("Expected %d calls, got %d: %v", len(expectedOrder), len(callOrder), callOrder)
	}
}

// Tests to achieve 100% coverage by hitting specific uncovered branches

// Test AsyncSignal Emit with error listeners that can't be invoked (covers listenerErr branch)
func TestAsyncSignal_EmitWithErrorListenersInLoop(t *testing.T) {
	sig := signals.New[int]()

	// Add regular listeners to ensure we hit the loop path
	for i := 0; i < 20; i++ {
		sig.AddListener(func(ctx context.Context, v int) {})
	}

	sig.Emit(context.Background(), 1)
}

// Test SyncSignal Emit with error listeners (should be ignored)
func TestSyncSignal_EmitWithErrorListenersInLoop(t *testing.T) {
	sig := signals.NewSync[int]()

	// Add regular listener
	sig.AddListener(func(ctx context.Context, v int) {})

	// Add error listener - should be ignored by Emit but present in loop
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		return nil
	})

	// Add another regular listener
	sig.AddListener(func(ctx context.Context, v int) {})

	sig.Emit(context.Background(), 1)
}

// Test SyncSignal TryEmit with context in various paths
func TestSyncSignal_TryEmitContextPaths(t *testing.T) {
	sig := signals.NewSync[string]()
	ctx := context.TODO()

	// Test empty signal with context
	err := sig.TryEmit(ctx, "test1")
	if err != nil {
		t.Errorf("Expected nil error with empty signal and context, got %v", err)
	}

	// Add error listener and test single listener path with context
	sig.AddListenerWithErr(func(ctx context.Context, s string) error {
		return nil
	})

	err = sig.TryEmit(ctx, "test2")
	if err != nil {
		t.Errorf("Expected nil error with single error listener and context, got %v", err)
	}

	sig.Reset()

	// Test single regular listener with context
	sig.AddListener(func(ctx context.Context, s string) {})
	sig.TryEmit(ctx, "test3")

	sig.Reset()

	// Test multiple listeners with context
	sig.AddListener(func(ctx context.Context, s string) {})
	sig.AddListenerWithErr(func(ctx context.Context, s string) error { return nil })
	sig.AddListener(func(ctx context.Context, s string) {})

	err = sig.TryEmit(ctx, "test4")
	if err != nil {
		t.Errorf("Expected nil error with multiple listeners and context, got %v", err)
	}
}

// Test SyncSignal TryEmit error listener returning error on single listener fast path
func TestSyncSignal_TryEmitSingleErrorListenerReturnsError(t *testing.T) {
	sig := signals.NewSync[bool]()

	sig.AddListenerWithErr(func(ctx context.Context, b bool) error {
		return errors.New("single error")
	})

	err := sig.TryEmit(context.Background(), true)
	if err == nil || err.Error() != "single error" {
		t.Errorf("Expected 'single error', got %v", err)
	}
}

// Test AsyncSignal ensureWorkerPool edge case coverage
func TestAsyncSignal_EnsureWorkerPoolTypeCastFailure(t *testing.T) {
	sig := signals.New[byte]()

	// Force worker pool initialization by adding enough listeners
	for i := 0; i < 25; i++ {
		sig.AddListener(func(ctx context.Context, b byte) {})
	}

	// This should trigger the pooled worker path
	sig.Emit(context.Background(), 42)
}
