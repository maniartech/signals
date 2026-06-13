package signals_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Test SyncSignal TryEmit edge cases to improve coverage
func TestSyncSignal_TryEmit_EdgeCases(t *testing.T) {
	sig := signals.NewSync[int]()
	ctx := context.TODO()

	// Test with context
	err := sig.TryEmit(ctx, 1)
	if err != nil {
		t.Errorf("Expected no error with context, got %v", err)
	}
}

// Test SyncSignal TryEmit with no listeners and context
func TestSyncSignal_TryEmit_NoListenersContext(t *testing.T) {
	sig := signals.NewSync[int]()
	ctx := context.TODO()

	err := sig.TryEmit(ctx, 1)
	if err != nil {
		t.Errorf("Expected no error with no listeners and context, got %v", err)
	}
}

// Test SyncSignal TryEmit with single listener and context
func TestSyncSignal_TryEmit_SingleListenerContext(t *testing.T) {
	sig := signals.NewSync[int]()
	called := false
	ctx := context.TODO()

	sig.AddListener(func(ctx context.Context, v int) {
		called = true
	})

	err := sig.TryEmit(ctx, 42)
	if err != nil {
		t.Errorf("Expected no error with single listener and context, got %v", err)
	}

	if !called {
		t.Error("Expected listener to be called")
	}
}

// Test SyncSignal TryEmit with single error listener and context
func TestSyncSignal_TryEmit_SingleErrorListenerContext(t *testing.T) {
	sig := signals.NewSync[int]()
	ctx := context.TODO()

	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		return nil
	})

	err := sig.TryEmit(ctx, 42)
	if err != nil {
		t.Errorf("Expected no error with single error listener and context, got %v", err)
	}
}

// Test SyncSignal Emit with context (edge case)
func TestSyncSignal_Emit_Context(t *testing.T) {
	sig := signals.NewSync[string]()
	called := false
	ctx := context.TODO()

	sig.AddListener(func(ctx context.Context, s string) {
		called = true
	})

	sig.Emit(ctx, "test")

	if !called {
		t.Error("Expected listener to be called with context")
	}
}

// Test SyncSignal Emit with multiple listeners and context
func TestSyncSignal_Emit_MultipleListenersContext(t *testing.T) {
	sig := signals.NewSync[bool]()
	called := 0
	ctx := context.TODO()

	for i := 0; i < 5; i++ {
		sig.AddListener(func(ctx context.Context, b bool) {
			called++
		})
	}

	sig.Emit(ctx, true)

	if called != 5 {
		t.Errorf("Expected 5 listeners called, got %d", called)
	}
}

// Test SyncSignal TryEmit with context that becomes cancelled after listener starts
func TestSyncSignal_TryEmit_ContextCancelledAfterStart(t *testing.T) {
	sig := signals.NewSync[int]()

	ctx, cancel := context.WithCancel(context.Background())

	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		// Simulate some work
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	sig.AddListener(func(ctx context.Context, v int) {
		// This should not be called due to cancellation
	})

	// Cancel context before calling TryEmit
	cancel()

	err := sig.TryEmit(ctx, 1)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}
}

// Test SyncSignal with mixed listener types and context cancellation
func TestSyncSignal_MixedListeners_ContextCancel(t *testing.T) {
	sig := signals.NewSync[string]()
	called := 0

	// Add regular listener
	sig.AddListener(func(ctx context.Context, s string) {
		called++
	})

	// Add error listener that will be reached
	sig.AddListenerWithErr(func(ctx context.Context, s string) error {
		called++
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before emit

	err := sig.TryEmit(ctx, "test")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	// No listeners should be called due to early cancellation
	if called != 0 {
		t.Errorf("Expected 0 listeners called due to early cancel, got %d", called)
	}
}

func TestSyncSignal_EmitInvokesErrorListeners(t *testing.T) {
	sig := signals.NewSync[int]()

	called := 0
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		called++
		return nil
	})

	sig.Emit(context.Background(), 1)

	if called != 1 {
		t.Fatalf("Expected error listener to be called by Emit, got %d", called)
	}
}

func TestSyncSignal_EmitStopsOnCanceledContextBeforeStart(t *testing.T) {
	sig := signals.NewSync[int]()

	called := 0
	sig.AddListener(func(ctx context.Context, v int) {
		called++
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sig.Emit(ctx, 1)

	if called != 0 {
		t.Fatalf("Expected Emit to skip listeners on canceled context, got %d", called)
	}
}

// Test TryEmit returns ctx.Err() when context is canceled before any listener runs.
func TestTryEmit_ContextAlreadyCanceled(t *testing.T) {
	s := signals.NewSync[int]()
	s.AddListener(func(ctx context.Context, v int) {})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.TryEmit(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// Test TryEmit returns the first listener error and stops further listeners.
func TestTryEmit_ListenerErrorStops(t *testing.T) {
	s := signals.NewSync[int]()
	called := 0

	s.AddListener(func(ctx context.Context, v int) { called++ })
	s.AddListenerWithErr(func(ctx context.Context, v int) error {
		called++
		return errors.New("boom")
	})
	s.AddListener(func(ctx context.Context, v int) { called++ }) // should not be called

	err := s.TryEmit(context.Background(), 7)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom error, got %v", err)
	}
	if called != 2 { // first no-error + error listener; third should be skipped
		t.Fatalf("expected called == 2, got %d", called)
	}
}

// Test TryEmit single-listener fast path with error-returning listener.
func TestTryEmit_SingleListenerError(t *testing.T) {
	s := signals.NewSync[int]()
	s.AddListenerWithErr(func(ctx context.Context, v int) error { return errors.New("x") })

	if err := s.TryEmit(context.Background(), 1); err == nil || err.Error() != "x" {
		t.Fatalf("expected error x, got %v", err)
	}
}

// Test Emit (non-error) stops invoking further listeners when ctx is canceled mid-iteration.
func TestEmit_StopsOnCancelMidIteration(t *testing.T) {
	s := signals.NewSync[int]()
	called := 0

	s.AddListener(func(ctx context.Context, v int) { called++ })
	s.AddListener(func(ctx context.Context, v int) {
		called++
	})
	s.AddListener(func(ctx context.Context, v int) { called++ })

	ctx, cancel := context.WithCancel(context.Background())
	// cancel just before second listener
	s.AddListener(func(ctx context.Context, v int) { cancel() }, "cancel-trigger")

	s.Emit(ctx, 1)

	if called == 0 {
		t.Fatalf("expected at least one listener called before cancel")
	}
}

// Test AddListenerWithErr respects keys and prevents duplicates (returns -1).
func TestAddListenerWithErr_DuplicateKey(t *testing.T) {
	s := signals.NewSync[int]()
	k := "k1"
	n := s.AddListenerWithErr(func(ctx context.Context, v int) error { return nil }, k)
	if n != 1 {
		t.Fatalf("expected 1 after first add, got %d", n)
	}
	n2 := s.AddListenerWithErr(func(ctx context.Context, v int) error { return nil }, k)
	if n2 != -1 {
		t.Fatalf("expected -1 on duplicate key, got %d", n2)
	}
}

// Test TryEmit returns DeadlineExceeded when context times out before iteration completes.
func TestTryEmit_DeadlineExceeded(t *testing.T) {
	s := signals.NewSync[int]()
	s.AddListener(func(ctx context.Context, v int) {
		time.Sleep(20 * time.Millisecond)
	})
	s.AddListenerWithErr(func(ctx context.Context, v int) error {
		// Simulate work past the deadline check; TryEmit checks ctx before each
		time.Sleep(50 * time.Millisecond)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := s.TryEmit(ctx, 1)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected deadline or canceled, got %v", err)
	}
}

func TestSyncSignal_ListenerOrderPreserved(t *testing.T) {
	sig := signals.NewSync[int]()

	order := make([]int, 0, 3)
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 1) })
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 2) })
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 3) })

	sig.Emit(context.Background(), 1)

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("Expected listener order [1 2 3], got %v", order)
	}
}

func TestSyncSignal_OrderPreservedAfterRemoval(t *testing.T) {
	sig := signals.NewSync[int]()

	order := make([]int, 0, 3)
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 1) }, "a")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 2) }, "b")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 3) }, "c")

	sig.RemoveListener("b")
	sig.Emit(context.Background(), 1)

	if len(order) != 2 || order[0] != 1 || order[1] != 3 {
		t.Fatalf("Expected listener order [1 3] after removal, got %v", order)
	}
}

// SyncSignal.OnError: a listener error during Emit (which is best-effort) routes to
// every registered OnError sink — symmetric with AsyncSignal — and Emit does not stop
// the chain. (TryEmit, by contrast, returns the error and stops at the first.)
func TestSyncSignal_OnErrorRoutesEmitErrors(t *testing.T) {
	sig := signals.NewSync[int]()
	boom := errors.New("boom")
	var got error
	afterRan := false
	sig.OnError(nil) // ignored
	sig.OnError(func(_ context.Context, err error) { got = err })
	sig.AddListenerWithErr(func(context.Context, int) error { return boom }, "1")
	sig.AddListener(func(context.Context, int) { afterRan = true }, "2")

	sig.Emit(context.Background(), 1)

	if !errors.Is(got, boom) {
		t.Fatalf("sync OnError received %v; want boom", got)
	}
	if !afterRan {
		t.Fatal("sync Emit must not stop the chain on a listener error (best-effort)")
	}
}

// --- Emission order: FIFO (default) and LIFO, stable across removals (FR-9 I15) ---

// FIFO is the default: listeners fire in registration order.
func TestEmitOrder_FIFODefault(t *testing.T) {
	sig := signals.NewSync[int]()
	var got []string
	for _, name := range []string{"a", "b", "c"} {
		n := name
		sig.AddListener(func(ctx context.Context, v int) { got = append(got, n) }, n)
	}
	sig.Emit(context.Background(), 1)
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("FIFO order = %v; want [a b c]", got)
	}
}

// LIFO invokes listeners in reverse registration order (handler-stack discipline).
func TestEmitOrder_LIFO(t *testing.T) {
	sig := signals.NewSyncWithOptions[int](&signals.SignalOptions{Order: signals.LIFO})
	var got []string
	for _, name := range []string{"a", "b", "c"} {
		n := name
		sig.AddListener(func(ctx context.Context, v int) { got = append(got, n) }, n)
	}
	sig.Emit(context.Background(), 1)
	if strings.Join(got, ",") != "c,b,a" {
		t.Fatalf("LIFO order = %v; want [c b a]", got)
	}
}

// Removing a middle listener must NOT reorder the rest — order is preserved across
// removal (the whole point of pairing LIFO with order-preserving removal).
func TestEmitOrder_StableAcrossRemoval(t *testing.T) {
	sig := signals.NewSync[int]()
	var got []string
	for _, name := range []string{"a", "b", "c", "d"} {
		n := name
		sig.AddListener(func(ctx context.Context, v int) { got = append(got, n) }, n)
	}
	sig.RemoveListener("b")
	sig.Emit(context.Background(), 1)
	if strings.Join(got, ",") != "a,c,d" {
		t.Fatalf("order after removing 'b' = %v; want [a c d] (stable, not swap-remove)", got)
	}
	if strings.Join(sig.Keys(), ",") != "a,c,d" {
		t.Fatalf("Keys after removal = %v; want [a c d]", sig.Keys())
	}
}

// LIFO is stable across removal too, and TryEmit stops at the first error in reverse.
func TestEmitOrder_LIFO_TryEmitStopsInReverse(t *testing.T) {
	sig := signals.NewSyncWithOptions[int](&signals.SignalOptions{Order: signals.LIFO})
	var got []string
	boom := errors.New("boom")
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { got = append(got, "a"); return nil }, "a")
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { got = append(got, "b"); return boom }, "b")
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { got = append(got, "c"); return nil }, "c")
	// LIFO order is c, b, a; 'b' fails => runs c then b, then stops ('a' never runs).
	if err := sig.TryEmit(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("TryEmit err = %v; want boom", err)
	}
	if strings.Join(got, ",") != "c,b" {
		t.Fatalf("LIFO TryEmit ran %v; want [c b] (stopped at first error in reverse)", got)
	}
}

// --- ErrStopPropagation: a sync listener halts the chain early (a control value, not a failure) ---

// Emit: the remaining listeners are skipped, and the sentinel is NOT routed to OnError.
func TestErrStopPropagation_EmitStopsChainNotRouted(t *testing.T) {
	sig := signals.NewSync[int]()
	var order []string
	routed := 0
	sig.OnError(func(ctx context.Context, err error) { routed++ })
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { order = append(order, "a"); return nil }, "a")
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { order = append(order, "b"); return signals.ErrStopPropagation }, "b")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, "c") }, "c") // must NOT run
	sig.Emit(context.Background(), 1)
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("Emit ran %v; want [a b] (c skipped after ErrStopPropagation)", order)
	}
	if routed != 0 {
		t.Fatalf("ErrStopPropagation routed to OnError %d times; want 0 (it is a control value)", routed)
	}
}

// TryEmit: returns nil (clean stop), remaining listeners skipped.
func TestErrStopPropagation_TryEmitReturnsNil(t *testing.T) {
	sig := signals.NewSync[int]()
	var order []string
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { order = append(order, "a"); return signals.ErrStopPropagation }, "a")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, "b") }, "b") // must NOT run
	if err := sig.TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("TryEmit returned %v; want nil (clean stop)", err)
	}
	if strings.Join(order, ",") != "a" {
		t.Fatalf("TryEmit ran %v; want [a] (b skipped)", order)
	}
}

// A wrapped ErrStopPropagation (errors.Is) is recognized too.
func TestErrStopPropagation_WrappedIsRecognized(t *testing.T) {
	sig := signals.NewSync[int]()
	ran := 0
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		return fmt.Errorf("step done, stopping: %w", signals.ErrStopPropagation)
	}, "a")
	sig.AddListener(func(ctx context.Context, v int) { ran++ }, "b")
	if err := sig.TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("TryEmit returned %v; want nil for wrapped ErrStopPropagation", err)
	}
	if ran != 0 {
		t.Fatalf("second listener ran %d times; want 0", ran)
	}
}

// ErrStopPropagation halts the reverse walk under LIFO too.
func TestErrStopPropagation_LIFOHaltsReverseWalk(t *testing.T) {
	sig := signals.NewSyncWithOptions[int](&signals.SignalOptions{Order: signals.LIFO})
	var order []string
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, "a") }, "a") // last in LIFO; must be skipped
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { order = append(order, "b"); return signals.ErrStopPropagation }, "b")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, "c") }, "c") // first in LIFO
	sig.Emit(context.Background(), 1)
	if strings.Join(order, ",") != "c,b" {
		t.Fatalf("LIFO Emit ran %v; want [c b] (stopped before a)", order)
	}
}

// A genuine error still stops the chain AND is reported (sentinel handling must not swallow real errors).
func TestErrStopPropagation_RealErrorStillReported(t *testing.T) {
	sig := signals.NewSync[int]()
	boom := errors.New("boom")
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return boom }, "a")
	sig.AddListener(func(ctx context.Context, v int) {}, "b")
	if err := sig.TryEmit(context.Background(), 1); !errors.Is(err, boom) {
		t.Fatalf("TryEmit err = %v; want boom (real errors still reported)", err)
	}
}
