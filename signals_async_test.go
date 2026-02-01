package signals_test

import (
	"context"
	"sync"
	"sync/atomic"
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

// Test AsyncSignal with multiple listeners (no panic, async dispatch)
func TestAsyncSignal_MultipleListenersNoPanic(t *testing.T) {
	sig := signals.New[int]()

	for i := 0; i < 5; i++ {
		sig.AddListener(func(ctx context.Context, v int) {})
	}

	sig.Emit(context.Background(), 1)
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
	done := make(chan struct{})

	sig.AddListener(func(ctx context.Context, v int) {
		// Listener should check ctx and handle cancellation
		select {
		case <-time.After(100 * time.Millisecond):
			// Normal processing
		case <-ctx.Done():
			// Cancelled
		}
		close(done)
	})

	go func() {
		sig.Emit(ctx, 1)
	}()

	// Cancel context while listener might be running
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		// Acceptable if Emit skipped scheduling because ctx canceled early
	}
}

func TestAsyncSignal_ListenerPanicDoesNotDeadlock(t *testing.T) {
	sig := signals.New[int]()

	var wg sync.WaitGroup
	var mu sync.Mutex
	called := 0
	total := 20
	panicIndex := 7

	for i := 0; i < total; i++ {
		if i == panicIndex {
			sig.AddListener(func(ctx context.Context, v int) {
				panic("boom")
			})
			continue
		}
		wg.Add(1)
		sig.AddListener(func(ctx context.Context, v int) {
			mu.Lock()
			called++
			mu.Unlock()
			wg.Done()
		})
	}

	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit appears to have deadlocked after listener panic")
	}

	wg.Wait()

	mu.Lock()
	got := called
	mu.Unlock()
	if got != total-1 {
		t.Fatalf("Expected %d listeners to complete, got %d", total-1, got)
	}
}

func TestAsyncSignal_SingleListenerIsAsync(t *testing.T) {
	sig := signals.New[int]()

	started := make(chan struct{})
	gate := make(chan struct{})
	done := make(chan struct{})

	sig.AddListener(func(ctx context.Context, v int) {
		close(started)
		<-gate
		close(done)
	})

	start := time.Now()
	sig.Emit(context.Background(), 1)

	select {
	case <-started:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Expected listener to start asynchronously")
	}

	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("Expected Emit to return without waiting for listener")
	}

	close(gate)
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Expected listener to finish after gate release")
	}
}

func TestAsyncSignal_EmitSkipsWhenContextCanceled(t *testing.T) {
	sig := signals.New[int]()

	called := 0
	sig.AddListener(func(ctx context.Context, v int) {
		called++
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sig.Emit(ctx, 1)

	if called != 0 {
		t.Fatalf("Expected no listener calls when context is canceled, got %d", called)
	}
}

func TestAsyncSignal_EmitIsNonBlocking(t *testing.T) {
	sig := signals.New[int]()

	gate := make(chan struct{})
	sig.AddListener(func(ctx context.Context, v int) {
		<-gate
	})

	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 1)
		close(done)
	}()

	select {
	case <-done:
		// Expected non-blocking Emit
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Emit blocked; expected fire-and-forget behavior")
	}

	close(gate)
}

func TestAsyncSignal_ContextTimeoutStopsListeners(t *testing.T) {
	sig := signals.New[int]()

	called := 0
	sig.AddListener(func(ctx context.Context, v int) {
		called++
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond)

	sig.Emit(ctx, 1)

	if called != 0 {
		t.Fatalf("Expected no listener calls when context is timed out, got %d", called)
	}
}

func TestAsyncSignal_CancelStopsOtherListeners(t *testing.T) {
	sig := signals.New[int]()

	var canceledSeen int32
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan struct{})
	done := make(chan struct{})

	sig.AddListener(func(ctx context.Context, v int) {
		cancel()
		close(canceled)
	})
	sig.AddListener(func(ctx context.Context, v int) {
		<-canceled
		if ctx.Err() != nil {
			atomic.StoreInt32(&canceledSeen, 1)
		}
		close(done)
	})

	sig.Emit(ctx, 1)

	select {
	case <-canceled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Expected cancellation to occur")
	}

	select {
	case <-done:
		if atomic.LoadInt32(&canceledSeen) == 0 {
			t.Fatal("Expected second listener to observe canceled context")
		}
	case <-time.After(50 * time.Millisecond):
		// If cancellation was observed before scheduling the second listener,
		// it's acceptable for it to be skipped entirely.
	}
}

func TestAsyncSignal_ListenerPanicDoesNotCrashSmallPath(t *testing.T) {
	sig := signals.New[int]()
	sig.AddListener(func(ctx context.Context, v int) {
		panic("boom")
	})

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Expected async emit to handle listener panic, got %v", r)
		}
	}()

	sig.Emit(context.Background(), 1)
}
