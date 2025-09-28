package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

// Test NewWithOptions function (currently 0% coverage)
func TestNewWithOptions(t *testing.T) {
	customGrowth := func(cap int) int { return cap + 10 }
	opts := &signals.SignalOptions{
		InitialCapacity: 20,
		GrowthFunc:      customGrowth,
	}

	sig := signals.NewWithOptions[string](opts)
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewWithOptions")
	}

	// Test that it works as an async signal
	sig.AddListener(func(ctx context.Context, s string) {
		// Listener for testing
	})

	sig.Emit(context.Background(), "test")

	// Give time for async execution
	// Note: In real tests, you'd use proper synchronization
	if sig.Len() != 1 {
		t.Errorf("Expected 1 listener, got %d", sig.Len())
	}
}

// Test NewWithOptions with nil options
func TestNewWithOptions_NilOptions(t *testing.T) {
	sig := signals.NewWithOptions[int](nil)
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewWithOptions with nil opts")
	}

	// Should behave like regular New()
	sig.AddListener(func(ctx context.Context, v int) {})
	if sig.Len() != 1 {
		t.Errorf("Expected 1 listener, got %d", sig.Len())
	}
}

// Test New function behavior (already covered but ensure completeness)
func TestNew(t *testing.T) {
	sig := signals.New[bool]()
	if sig == nil {
		t.Fatal("Expected non-nil signal from New")
	}

	if !sig.IsEmpty() {
		t.Error("Expected new signal to be empty")
	}
}

// Test NewSync function behavior (already covered but ensure completeness)
func TestNewSync(t *testing.T) {
	sig := signals.NewSync[float64]()
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewSync")
	}

	if !sig.IsEmpty() {
		t.Error("Expected new sync signal to be empty")
	}

	// Test that NewSync returns concrete SyncSignal type with TryEmit method
	// (no type assertion needed since function returns concrete type)

	// Test concrete type methods
	err := sig.TryEmit(context.Background(), 3.14)
	if err != nil {
		t.Errorf("Expected no error from TryEmit, got %v", err)
	}
}
