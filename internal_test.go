package signals

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
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

// TestDefaultPanicHandler covers the default panic handler deterministically (the
// global handler is swapped by other tests, so cover it by direct invocation), and
// restores it as the active handler afterward to limit cross-test global-state
// pollution from blackbox tests that set a custom/nil handler.
func TestDefaultPanicHandler(t *testing.T) {
	defaultPanicHandler("coverage: default panic handler logs this")
	handleListenerPanic("coverage: routed via handleListenerPanic")
	SetPanicHandler(defaultPanicHandler) // restore the library default
}

func TestAsyncSignal_NoGoroutineLeakAfterEmit(t *testing.T) {
	base := runtime.NumGoroutine()

	sig := New[int]()
	var wg sync.WaitGroup
	n := 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {
			wg.Done()
		})
	}

	sig.Emit(context.Background(), 1)
	wg.Wait()
	// Poll with a deadline instead of a fixed sleep to avoid flakiness on
	// slow or heavily loaded machines.
	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > base+5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > base+5 {
		t.Fatalf("Expected goroutine count to return near baseline; baseline=%d after=%d", base, after)
	}
}

func TestAsyncSignal_NoGoroutineLeakAfterManyEmits(t *testing.T) {
	base := runtime.NumGoroutine()

	sig := New[int]()
	var wg sync.WaitGroup
	n := 20
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {})
	}

	for i := 0; i < 100; i++ {
		wg.Add(n)
		sig.Reset()
		for j := 0; j < n; j++ {
			sig.AddListener(func(_ context.Context, _ int) {
				wg.Done()
			})
		}
		sig.Emit(context.Background(), 1)
	}
	wg.Wait()
	// Poll with a deadline instead of a fixed sleep to avoid flakiness on
	// slow or heavily loaded machines.
	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > base+5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > base+5 {
		t.Fatalf("Expected goroutine count to return near baseline; baseline=%d after=%d", base, after)
	}
}
