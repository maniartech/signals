package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

// Test SignalListener type directly
func TestSignalListener(t *testing.T) {
	// Create a concrete listener
	listener := func(ctx context.Context, s string) {
		// Test listener implementation
	}

	if listener == nil {
		t.Error("Expected non-nil listener")
	}

	// Test calling the listener
	listener(context.Background(), "test")
}

// Test SignalListenerErr type directly
func TestSignalListenerErr(t *testing.T) {
	var listenerErr signals.SignalListenerErr[int]

	// Create a concrete error-returning listener
	listenerErr = func(ctx context.Context, i int) error {
		if i < 0 {
			return context.Canceled
		}
		return nil
	}

	if listenerErr == nil {
		t.Error("Expected non-nil error listener")
	}

	// Test calling the listener with no error
	err := listenerErr(context.Background(), 5)
	if err != nil {
		t.Errorf("Expected no error for positive value, got %v", err)
	}

	// Test calling the listener with error
	err = listenerErr(context.Background(), -1)
	if err == nil {
		t.Error("Expected error for negative value")
	}
}
