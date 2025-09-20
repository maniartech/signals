package signals

import (
	"context"
	"testing"
)

// Whitebox test for defaultGrowthFunc to achieve 100% coverage
func TestDefaultGrowthFunc(t *testing.T) {
	tests := []struct {
		input    int
		expected int
		name     string
	}{
		{5, 11, "small capacity"},
		{15, 17, "medium capacity"},
		{100, 127, "larger capacity"},
		{2147483648, 4294967297, "fallback case - beyond primes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultGrowthFunc(tt.input)
			if result != tt.expected {
				t.Errorf("defaultGrowthFunc(%d) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

// Test NewBaseSignal with zero/negative InitialCapacity (should use default)
func TestNewBaseSignal_ZeroCapacity(t *testing.T) {
	opts := &SignalOptions{InitialCapacity: 0}
	bs := NewBaseSignal[int](opts)

	if bs == nil {
		t.Fatal("Expected non-nil BaseSignal")
	}

	// Should still work normally
	count := bs.AddListener(func(ctx context.Context, v int) {})
	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}
}

// Test NewBaseSignal with nil GrowthFunc (should use default)
func TestNewBaseSignal_NilGrowthFunc(t *testing.T) {
	opts := &SignalOptions{
		InitialCapacity: 5,
		GrowthFunc:      nil, // Should use default
	}

	bs := NewBaseSignal[string](opts)
	if bs == nil {
		t.Fatal("Expected non-nil BaseSignal")
	}
}
