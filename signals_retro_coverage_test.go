package signals_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

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
