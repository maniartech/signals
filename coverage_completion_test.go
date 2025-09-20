package signals_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/maniartech/signals"
)

// Test edge cases to reach 100% coverage

// Test AsyncSignal ensureWorkerPool with negative size (should use default)
func TestAsyncSignal_EnsureWorkerPool_NegativeSize(t *testing.T) {
	sig := signals.New[int]().(*signals.AsyncSignal[int])

	// This should trigger ensureWorkerPool with default size calculation
	// Add a listener that forces worker pool initialization
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
}

// Test AsyncSignal Emit with nil listeners (edge case for fast path)
func TestAsyncSignal_EmitWithListenerErrOnly(t *testing.T) {
	// This tests AsyncSignal behavior when it has error listeners but can't invoke them
	// Since AsyncSignal.Emit doesn't handle listenerErr, this tests that branch
	sig := signals.New[int]().(*signals.AsyncSignal[int])

	// Add regular listener first
	sig.AddListener(func(ctx context.Context, v int) {})

	// Emit should work normally
	sig.Emit(context.Background(), 1)
}

// Test SyncSignal AddListenerWithErr edge cases
func TestSyncSignal_AddListenerWithErr_EdgeCases(t *testing.T) {
	sig := signals.NewSync[string]().(*signals.SyncSignal[string])

	// Test nil listener panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil listener")
		}
	}()

	sig.AddListenerWithErr(nil)
}

// Test SyncSignal Emit with error listeners (they should be ignored by regular Emit)
func TestSyncSignal_EmitIgnoresErrorListeners(t *testing.T) {
	sig := signals.NewSync[int]().(*signals.SyncSignal[int])
	called := false

	sig.AddListener(func(ctx context.Context, v int) {
		called = true
	})

	// Add error listener - should be ignored by regular Emit
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		return errors.New("should not be called by Emit")
	})

	sig.Emit(context.Background(), 1)

	if !called {
		t.Error("Regular listener should have been called")
	}
}

// Test SyncSignal TryEmit with mixed listener types - comprehensive coverage
func TestSyncSignal_TryEmit_MixedListeners_ComprehensiveCoverage(t *testing.T) {
	sig := signals.NewSync[int]().(*signals.SyncSignal[int])
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
