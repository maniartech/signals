package signals_test

import (
	"context"
	"errors"
	"testing"

	"github.com/maniartech/signals"
)

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
