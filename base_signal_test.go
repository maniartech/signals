package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

// Test NewBaseSignal with nil options (uses defaults)
func TestNewBaseSignal_NilOptions(t *testing.T) {
	bs := signals.NewBaseSignal[int](nil)
	if bs == nil {
		t.Fatal("Expected non-nil BaseSignal")
	}

	// Should have default capacity
	if bs.Len() != 0 {
		t.Errorf("Expected initial len 0, got %d", bs.Len())
	}

	if !bs.IsEmpty() {
		t.Error("Expected IsEmpty() to be true initially")
	}
}

// Test NewBaseSignal with custom options
func TestNewBaseSignal_CustomOptions(t *testing.T) {
	customGrowth := func(cap int) int { return cap * 3 }
	opts := &signals.SignalOptions{
		InitialCapacity: 5,
		GrowthFunc:      customGrowth,
	}

	bs := signals.NewBaseSignal[string](opts)
	if bs == nil {
		t.Fatal("Expected non-nil BaseSignal")
	}
}

// Test BaseSignal.Emit panics (it's abstract)
func TestBaseSignal_EmitPanics(t *testing.T) {
	bs := signals.NewBaseSignal[int](nil)

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected BaseSignal.Emit to panic")
		}
	}()

	bs.Emit(context.Background(), 42)
}

// Test BaseSignal interface compliance (AddListenerWithErr is not part of BaseSignal)
func TestBaseSignal_InterfaceCompliance(t *testing.T) {
	bs := signals.NewBaseSignal[string](nil)

	// BaseSignal should only support regular listeners
	count := bs.AddListener(func(ctx context.Context, s string) {
		// Regular listener
	})

	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	// Test with key
	count = bs.AddListener(func(ctx context.Context, s string) {
		// Another listener
	}, "key1")

	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}

	// Note: AddListenerWithErr is only available on SyncSignal, not BaseSignal
} // Test AddListener with nil listener panics
func TestBaseSignal_AddListener_NilPanic(t *testing.T) {
	bs := signals.NewBaseSignal[int](nil)

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected AddListener with nil listener to panic")
		}
	}()

	bs.AddListener(nil)
}

// Test RemoveListener with non-existent key
func TestBaseSignal_RemoveListener_NonExistent(t *testing.T) {
	bs := signals.NewBaseSignal[int](nil)

	count := bs.RemoveListener("non-existent")
	if count != -1 {
		t.Errorf("Expected -1 for non-existent key, got %d", count)
	}
}
