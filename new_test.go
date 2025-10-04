package signals_test

import (
	"context"
	"fmt"
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

// Test NewSyncWithOptions function
func TestNewSyncWithOptions(t *testing.T) {
	customGrowth := func(cap int) int { return cap + 5 }
	opts := &signals.SignalOptions{
		InitialCapacity: 15,
		GrowthFunc:      customGrowth,
	}

	sig := signals.NewSyncWithOptions[int](opts)
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewSyncWithOptions")
	}

	// Test that it behaves as a sync signal
	if !sig.IsEmpty() {
		t.Error("Expected new sync signal to be empty")
	}

	// Test sync-specific behavior - TryEmit method should be available
	err := sig.TryEmit(context.Background(), 42)
	if err != nil {
		t.Errorf("Expected no error from TryEmit, got %v", err)
	}

	// Add a listener and verify synchronous behavior
	called := false
	receivedValue := 0
	sig.AddListener(func(ctx context.Context, v int) {
		called = true
		receivedValue = v
	})

	if sig.Len() != 1 {
		t.Errorf("Expected 1 listener, got %d", sig.Len())
	}

	// Emit should be synchronous - listener called immediately
	sig.Emit(context.Background(), 123)
	if !called {
		t.Error("Expected listener to be called synchronously")
	}
	if receivedValue != 123 {
		t.Errorf("Expected payload 123, got %d", receivedValue)
	}

	// Test TryEmit with listener
	called = false
	receivedValue = 0
	err = sig.TryEmit(context.Background(), 456)
	if err != nil {
		t.Errorf("Expected no error from TryEmit with listener, got %v", err)
	}
	if !called {
		t.Error("Expected listener to be called synchronously with TryEmit")
	}
	if receivedValue != 456 {
		t.Errorf("Expected payload 456, got %d", receivedValue)
	}
}

// Test NewSyncWithOptions with nil options
func TestNewSyncWithOptions_NilOptions(t *testing.T) {
	sig := signals.NewSyncWithOptions[string](nil)
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewSyncWithOptions with nil opts")
	}

	// Should behave like regular NewSync()
	if !sig.IsEmpty() {
		t.Error("Expected new sync signal to be empty")
	}

	// Test that sync-specific methods are available
	err := sig.TryEmit(context.Background(), "test")
	if err != nil {
		t.Errorf("Expected no error from TryEmit with nil options, got %v", err)
	}

	// Add listener and test synchronous behavior
	received := ""
	sig.AddListener(func(ctx context.Context, s string) {
		received = s
	})

	sig.Emit(context.Background(), "hello")
	if received != "hello" {
		t.Errorf("Expected 'hello', got %q", received)
	}
}

// Test NewSyncWithOptions with custom options behavior
func TestNewSyncWithOptions_CustomOptions(t *testing.T) {
	customGrowth := func(cap int) int { return cap * 2 }
	opts := &signals.SignalOptions{
		InitialCapacity: 10,
		GrowthFunc:      customGrowth,
	}

	sig := signals.NewSyncWithOptions[bool](opts)
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewSyncWithOptions")
	}

	// Test that custom options are applied by adding many listeners
	// and verifying the signal can handle them (indirect test of capacity/growth)
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("listener_%d", i)
		sig.AddListener(func(ctx context.Context, b bool) {
			// Test listener
		}, key)
	}

	if sig.Len() != 20 {
		t.Errorf("Expected 20 listeners, got %d", sig.Len())
	}

	// Test that all listeners receive the signal synchronously
	counter := 0
	sig.Reset() // Clear previous listeners

	for i := 0; i < 5; i++ {
		sig.AddListener(func(ctx context.Context, b bool) {
			counter++
		})
	}

	sig.Emit(context.Background(), true)
	if counter != 5 {
		t.Errorf("Expected all 5 listeners to be called synchronously, got %d calls", counter)
	}
}

// Test NewSyncWithOptions with zero capacity
func TestNewSyncWithOptions_ZeroCapacity(t *testing.T) {
	opts := &signals.SignalOptions{
		InitialCapacity: 0,
		GrowthFunc:      func(cap int) int { return cap + 1 },
	}

	sig := signals.NewSyncWithOptions[int](opts)
	if sig == nil {
		t.Fatal("Expected non-nil signal from NewSyncWithOptions with zero capacity")
	}

	// Should still work with zero initial capacity
	sig.AddListener(func(ctx context.Context, v int) {})
	if sig.Len() != 1 {
		t.Errorf("Expected 1 listener with zero initial capacity, got %d", sig.Len())
	}

	err := sig.TryEmit(context.Background(), 100)
	if err != nil {
		t.Errorf("Expected no error from TryEmit with zero capacity, got %v", err)
	}
}
