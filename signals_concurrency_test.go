package signals_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Test: Adding listeners during Emit causes race or panic
func TestSyncSignal_AddListenerDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	
	var called int32
	
	// Add initial listener that's slow
	sig.AddListener(func(ctx context.Context, v int) {
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&called, 1)
	})
	
	// Start emitting
	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 1)
		close(done)
	}()
	
	// While emitting, add more listeners
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 10; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			atomic.AddInt32(&called, 1)
		})
	}
	
	<-done
	time.Sleep(50 * time.Millisecond)
	
	// Should have called initial listener + 10 new ones = 11
	// If race condition, might panic or call wrong number
	got := atomic.LoadInt32(&called)
	if got != 1 {
		t.Logf("Called %d listeners (expected 1, new listeners added during emit)", got)
	}
}

// Test: Removing listeners during Emit causes race or panic
func TestSyncSignal_RemoveListenerDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	
	var listener1Called, listener2Called int32
	
	// Add two listeners
	sig.AddListener(func(ctx context.Context, v int) {
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&listener1Called, 1)
	})
	
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&listener2Called, 1)
	}, "key2")
	
	// Start emitting
	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 1)
		close(done)
	}()
	
	// While emitting, remove listener
	time.Sleep(10 * time.Millisecond)
	sig.RemoveListener("key2")
	
	<-done
	time.Sleep(50 * time.Millisecond)
	
	// If race condition, might panic or have inconsistent state
	t.Logf("Listener1: %d, Listener2: %d", 
		atomic.LoadInt32(&listener1Called),
		atomic.LoadInt32(&listener2Called))
}

// Test: Concurrent Emit calls
func TestSyncSignal_ConcurrentEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	
	var called int32
	
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&called, 1)
	})
	
	var wg sync.WaitGroup
	n := 100
	wg.Add(n)
	
	// Hammer it with concurrent emits
	for i := 0; i < n; i++ {
		go func(val int) {
			defer wg.Done()
			sig.Emit(context.Background(), val)
		}(i)
	}
	
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	
	// Should be called exactly n times
	got := atomic.LoadInt32(&called)
	if got != int32(n) {
		t.Fatalf("Expected %d calls, got %d (lost events due to race?)", n, got)
	}
}

// Test: Concurrent Reset calls
func TestAsyncSignal_ConcurrentReset(t *testing.T) {
	sig := signals.New[int]()
	
	// Add listeners
	for i := 0; i < 10; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			time.Sleep(10 * time.Millisecond)
		})
	}
	
	var wg sync.WaitGroup
	n := 10
	wg.Add(n)
	
	// Concurrent Reset calls - should not panic
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Reset panicked: %v", r)
				}
			}()
			sig.Reset()
		}()
	}
	
	wg.Wait()
}

// Test: Emit while Reset is happening
func TestAsyncSignal_EmitDuringReset(t *testing.T) {
	sig := signals.New[int]()
	
	var called int32
	
	// Add listeners
	for i := 0; i < 5; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			atomic.AddInt32(&called, 1)
			time.Sleep(10 * time.Millisecond)
		})
	}
	
	// Start emitting in background
	go func() {
		for i := 0; i < 100; i++ {
			sig.Emit(context.Background(), i)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	
	time.Sleep(50 * time.Millisecond)
	
	// Reset while emits are happening
	sig.Reset()
	
	time.Sleep(100 * time.Millisecond)
	
	t.Logf("Called %d times before/during reset", atomic.LoadInt32(&called))
	// Should not panic, but behavior is undefined
}

// Test: Massive concurrent operations
func TestSyncSignal_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}
	
	sig := signals.NewSync[int]()
	
	var operations int32
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	// Worker that adds listeners
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				sig.AddListener(func(ctx context.Context, v int) {
					atomic.AddInt32(&operations, 1)
				})
				time.Sleep(time.Millisecond)
			}
		}
	}()
	
	// Worker that removes listeners
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				sig.RemoveListener("")
				time.Sleep(time.Millisecond)
			}
		}
	}()
	
	// Worker that emits
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				sig.Emit(context.Background(), 1)
				time.Sleep(time.Millisecond)
			}
		}
	}()
	
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	
	t.Logf("Stress test completed: %d operations", atomic.LoadInt32(&operations))
}
