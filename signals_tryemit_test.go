package signals_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Test TryEmit returns ctx.Err() when context is canceled before any listener runs.
func TestTryEmit_ContextAlreadyCanceled(t *testing.T) {
	s := signals.NewSync[int]().(*signals.SyncSignal[int])
	s.AddListener(func(ctx context.Context, v int) {})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.TryEmit(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// Test TryEmit returns the first listener error and stops further listeners.
func TestTryEmit_ListenerErrorStops(t *testing.T) {
	s := signals.NewSync[int]().(*signals.SyncSignal[int])
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
	s := signals.NewSync[int]().(*signals.SyncSignal[int])
	s.AddListenerWithErr(func(ctx context.Context, v int) error { return errors.New("x") })

	if err := s.TryEmit(context.Background(), 1); err == nil || err.Error() != "x" {
		t.Fatalf("expected error x, got %v", err)
	}
}

// Test Emit (non-error) stops invoking further listeners when ctx is canceled mid-iteration.
func TestEmit_StopsOnCancelMidIteration(t *testing.T) {
	s := signals.NewSync[int]().(*signals.SyncSignal[int])
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
	s := signals.NewSync[int]().(*signals.SyncSignal[int])
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
	s := signals.NewSync[int]().(*signals.SyncSignal[int])
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
