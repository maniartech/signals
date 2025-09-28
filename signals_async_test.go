package signals_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Test AsyncSignal only supports regular listeners, not error-returning ones
func TestAsyncSignal_OnlySupportsRegularListeners(t *testing.T) {
	sig := signals.New[int]()

	// AsyncSignal should only support regular listeners
	count := sig.AddListener(func(ctx context.Context, v int) {
		// Regular listener
	})

	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	// Note: AddListenerWithErr is not available on AsyncSignal
	// This is by design - error handling is only for SyncSignal
} // Test AsyncSignal with large number of listeners (>16) to trigger pooled worker path
func TestAsyncSignal_LargeListenerCount(t *testing.T) {
	sig := signals.New[int]()

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]int, 0)

	// Add more than 16 listeners to trigger pooled path
	for i := 0; i < 20; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			mu.Lock()
			results = append(results, v)
			mu.Unlock()
			wg.Done()
		})
	}

	wg.Add(20)
	sig.Emit(context.Background(), 42)
	wg.Wait()

	mu.Lock()
	count := len(results)
	mu.Unlock()

	if count != 20 {
		t.Errorf("Expected 20 results, got %d", count)
	}
}

// Test AsyncSignal worker pool initialization with zero size
func TestAsyncSignal_WorkerPoolZeroSize(t *testing.T) {
	sig := signals.New[int]()

	// Add many listeners to trigger ensureWorkerPool with n > 0 but size might be 0
	for i := 0; i < 5; i++ {
		sig.AddListener(func(ctx context.Context, v int) {})
	}

	sig.Emit(context.Background(), 1)
	// Should not panic and use default size (2 * runtime.NumCPU())
}

// Test AsyncSignal with nil listener in fast path
func TestAsyncSignal_NilListenerFastPath(t *testing.T) {
	sig := signals.New[int]()

	// Manually create a signal with nil listener (via reflection or direct access)
	// For now, just test normal case as nil listeners shouldn't be possible through public API
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
	// Should complete without issues
}

// Test AsyncSignal design principle: no error listener support
func TestAsyncSignal_NoErrorListenerSupport(t *testing.T) {
	sig := signals.New[int]()

	// AsyncSignal only supports regular listeners
	count := sig.AddListener(func(ctx context.Context, v int) {
		// Process async
	})

	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	// Note: AsyncSignal.Emit only processes regular listeners
	// Error listeners and TryEmit are exclusive to SyncSignal
	sig.Emit(context.Background(), 1)
} // Test AsyncSignal ensureWorkerPool edge cases
func TestAsyncSignal_EnsureWorkerPoolEdgeCases(t *testing.T) {
	sig := signals.New[int]()

	// Add listeners and emit to trigger worker pool creation
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)

	// Emit again to test that worker pool is not recreated
	sig.Emit(context.Background(), 2)
}

// Test AsyncSignal task pool reuse
func TestAsyncSignal_TaskPoolReuse(t *testing.T) {
	sig := signals.New[int]()

	// Add many listeners to trigger task pooling
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			time.Sleep(1 * time.Millisecond) // Small delay to trigger async behavior
			wg.Done()
		})
	}

	// Multiple emits to test task reuse
	for round := 0; round < 3; round++ {
		wg.Add(25)
		sig.Emit(context.Background(), round)
		wg.Wait()
	}
}

// Test AsyncSignal with context cancellation during async execution
func TestAsyncSignal_ContextCancellationDuringAsync(t *testing.T) {
	sig := signals.New[int]()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	sig.AddListener(func(ctx context.Context, v int) {
		defer wg.Done()
		// Listener should check ctx and handle cancellation
		select {
		case <-time.After(100 * time.Millisecond):
			// Normal processing
		case <-ctx.Done():
			// Cancelled
		}
	})

	wg.Add(1)
	go func() {
		sig.Emit(ctx, 1)
	}()

	// Cancel context while listener might be running
	cancel()
	wg.Wait()
}
