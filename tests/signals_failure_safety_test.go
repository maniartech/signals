package signals_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/maniartech/signals"
)

// Phase 4 gate / FR-8 I14: the failure-handling path must itself be failure-safe.
// These cover the two I14 sites not exercised by TestAsyncErr_SinkPanicIsolated:
// a panic INSIDE the panic handler, and a panic inside a one-shot (AddOnce) handler.

// I14: a buggy panic handler that itself panics must NOT crash the process. If the
// secondary panic escaped the handler goroutine it would be unrecovered and abort the
// whole test binary; surviving (and the sibling listener still running) proves the
// panic handler is contained — it is the last line of defense and must be unbreakable.
func TestI14_PanicHandlerThatPanicsIsContained(t *testing.T) {
	signals.SetPanicHandler(func(any) { panic("the panic handler is itself buggy") })
	defer signals.SetPanicHandler(nil)

	sig := signals.New[int]()
	var sibling int32
	sig.AddListener(func(context.Context, int) { panic("listener panic") }, "boom")
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&sibling, 1) }, "ok")

	sig.EmitAndWait(context.Background(), 1) // must return; process must survive

	if atomic.LoadInt32(&sibling) != 1 {
		t.Fatal("sibling listener did not run — the failure path was not contained")
	}
}

// I14: an async one-shot handler that panics on its only fire is consumed on attempt
// (not retried) and is still auto-removed.
func TestI14_AsyncOnceHandlerPanicConsumed(t *testing.T) {
	signals.SetPanicHandler(func(any) {}) // swallow
	defer signals.SetPanicHandler(nil)

	sig := signals.New[int]()
	var calls int32
	sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&calls, 1); panic("once boom") })

	sig.EmitAndWait(context.Background(), 1) // fires, panics (recovered), self-removes
	sig.EmitAndWait(context.Background(), 2) // must NOT fire again

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("async once handler fired %d times; want 1 (consumed on attempt)", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("async once handler not auto-removed after panicking")
	}
}

// I14: a sync one-shot handler that panics propagates to the Emit caller (as any sync
// listener does), but is still consumed on attempt and auto-removed — so a second
// emission does not fire it again.
func TestI14_SyncOnceHandlerPanicConsumed(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&calls, 1); panic("once boom") })

	// Sync listener panics propagate to the caller — recover the expected one.
	func() {
		defer func() { _ = recover() }()
		sig.Emit(context.Background(), 1)
	}()
	sig.Emit(context.Background(), 2) // already removed → must not fire

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("sync once handler fired %d times; want 1 (consumed on attempt)", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("sync once handler not auto-removed after panicking")
	}
}
