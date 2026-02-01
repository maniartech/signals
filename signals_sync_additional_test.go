package signals_test

import (
	"context"
	"errors"
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
