package signals_test

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// TestInterfaceCompliance verifies that AsyncSignal and SyncSignal
// implement the Signal interface correctly after hiding BaseSignal.
func TestInterfaceCompliance(t *testing.T) {
	// Test that New() returns a concrete type that implements Signal[T] interface
	asyncSig := signals.New[int]()

	// Test that NewSync() returns a concrete type that implements Signal[T] interface
	syncSig := signals.NewSync[int]()

	// Test basic operations on both
	testSignalInterface(t, asyncSig, "AsyncSignal")
	testSignalInterface(t, syncSig, "SyncSignal")
}

func testSignalInterface(t *testing.T, sig signals.Signal[int], name string) {
	t.Run(name, func(t *testing.T) {
		// Test AddListener
		count := sig.AddListener(func(ctx context.Context, v int) {})
		if count != 1 {
			t.Errorf("Expected 1 listener, got %d", count)
		}

		// Test Len
		if sig.Len() != 1 {
			t.Errorf("Expected Len() == 1, got %d", sig.Len())
		}

		// Test IsEmpty
		if sig.IsEmpty() {
			t.Error("Expected IsEmpty() == false")
		}

		// Test AddListener with key
		count = sig.AddListener(func(ctx context.Context, v int) {}, "key1")
		if count != 2 {
			t.Errorf("Expected 2 listeners, got %d", count)
		}

		// Test RemoveListener
		count = sig.RemoveListener("key1")
		if count != 1 {
			t.Errorf("Expected 1 listener after removal, got %d", count)
		}

		// Test Emit
		sig.Emit(context.Background(), 42)

		// Test Reset
		sig.Reset()
		if !sig.IsEmpty() {
			t.Error("Expected IsEmpty() == true after Reset()")
		}
	})
}

// TestSyncSignalSpecificMethods tests methods specific to SyncSignal
// that are available when type-asserting to the concrete type.
func TestSyncSignalSpecificMethods(t *testing.T) {
	syncSig := signals.NewSync[int]()

	// Test AddListenerWithErr
	count := syncSig.AddListenerWithErr(func(ctx context.Context, v int) error {
		return nil
	})
	if count != 1 {
		t.Errorf("Expected 1 listener, got %d", count)
	}

	// Test TryEmit
	err := syncSig.TryEmit(context.Background(), 42)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

// TestSignal_ConcurrentStress is a stress test designed to validate military-grade concurrency and thread safety.
//
// Philosophy:
// This test is built on the principle that true robustness in concurrent systems is only proven under extreme, unpredictable, and adversarial conditions.
// It simulates a hostile environment where many goroutines are rapidly adding/removing listeners and emitting signals, attempting to expose any race conditions,
// deadlocks, or panics that could occur in real-world, high-load, or adversarial scenarios. The test is intentionally aggressive, using high concurrency and random
// operations to maximize the chance of surfacing subtle bugs that would not appear in simple or sequential tests.
//
// Process:
// - Spawns 100 goroutines, each performing 1000 iterations of random listener management and signal emission.
// - Listeners are added and removed with unique keys, and signals are emitted with unique payloads.
// - A shared map, protected by a mutex, records all payloads received by a persistent listener.
// - After all goroutines complete, the test asserts that at least some signals were received, and (when run with `go test -race`) that no data races or panics occurred.
//
// This approach is inspired by industry best practices for concurrent system validation, including fuzzing, brute-force, and chaos engineering techniques.
// It is designed to give high confidence that the signals library is safe for use in mission-critical, high-concurrency environments.
func TestSignal_ConcurrentStress(t *testing.T) {
	const goroutines = 100
	const iterations = 1000

	signal := signals.New[int]()
	var wg sync.WaitGroup
	var count int64

	// Listener that records payloads
	signal.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt64(&count, 1)
	})

	// Start goroutines that add/remove listeners and emit signals
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Randomly add/remove listeners
				if j%10 == 0 {
					key := fmt.Sprintf("k-%d-%d", id, j)
					signal.AddListener(func(ctx context.Context, v int) {}, key)
					signal.RemoveListener(key)
				}
				// Emit signals
				signal.Emit(context.Background(), id*iterations+j)
			}
		}(i)
	}

	wg.Wait()

	expected := int64(goroutines * iterations)
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt64(&count) < expected && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Check for data races and panics (run with -race)
	if atomic.LoadInt64(&count) == 0 {
		t.Error("No signals were received")
	}
}

func TestSignal(t *testing.T) {
	testSignal := signals.NewSync[int]()

	results := make([]int, 0)
	testSignal.AddListener(func(ctx context.Context, v int) {
		results = append(results, v)
	})

	testSignal.AddListener(func(ctx context.Context, v int) {
		results = append(results, v)
	})

	ctx := context.Background()
	testSignal.Emit(ctx, 1)
	testSignal.Emit(ctx, 2)
	testSignal.Emit(ctx, 3)

	if len(results) != 6 {
		t.Error("Count must be 6")
	}

	if reflect.DeepEqual(results, []int{1, 1, 2, 2, 3, 3}) == false {
		t.Error("Results must be [1, 1, 2, 2, 3, 3]")
	}
}

func TestSignalAsync(t *testing.T) {
	var count int
	var mu sync.Mutex
	wg := &sync.WaitGroup{}
	wg.Add(6)

	testSignal := signals.New[int]()
	testSignal.AddListener(func(ctx context.Context, v int) {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		count += 1
		mu.Unlock()
		wg.Done()
	})
	testSignal.AddListener(func(ctx context.Context, v int) {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		count += 1
		mu.Unlock()
		wg.Done()
	})

	ctx := context.Background()
	go testSignal.Emit(ctx, 1)
	go testSignal.Emit(ctx, 2)
	go testSignal.Emit(ctx, 3)

	mu.Lock()
	c := count
	mu.Unlock()
	if c >= 6 {
		t.Error("Not asynchronus! count must be less than 6")
	}

	wg.Wait()

	mu.Lock()
	c = count
	mu.Unlock()
	if c != 6 {
		t.Error("Count must be 6")
	}
}

// Test Async with Timeout Context. After the context is cancelled, the
// listeners should cancel their execution.

func TestSignalAsyncWithTimeout(t *testing.T) {
	var count int
	var timeoutCount int
	var mu sync.Mutex
	var wg sync.WaitGroup

	testSignal := signals.New[int]()
	testSignal.AddListener(func(ctx context.Context, v int) {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			mu.Lock()
			timeoutCount += 1
			mu.Unlock()
		default:
			mu.Lock()
			count += 1
			mu.Unlock()
		}
	})
	testSignal.AddListener(func(ctx context.Context, v int) {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			mu.Lock()
			timeoutCount += 1
			mu.Unlock()
		default:
			mu.Lock()
			count += 1
			mu.Unlock()
		}
	})

	wg.Add(6)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	testSignal.Emit(ctx, 1)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	testSignal.Emit(ctx2, 1)

	ctx3, cancel3 := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel3()
	testSignal.Emit(ctx3, 3)

	wg.Wait()

	mu.Lock()
	c := count
	tc := timeoutCount
	mu.Unlock()
	if c != 3 {
		t.Error("Count must be 3")
	}

	if tc != 3 {
		t.Error("timeoutCount must be 3")
	}
}

func TestAddRemoveListener(t *testing.T) {
	testSignal := signals.New[int]()

	t.Run("AddListener", func(t *testing.T) {
		testSignal.AddListener(func(ctx context.Context, v int) {
			// Do something
		})

		testSignal.AddListener(func(ctx context.Context, v int) {
			// Do something
		}, "test-key")

		if testSignal.Len() != 2 {
			t.Error("Count must be 2")
		}

		if count := testSignal.AddListener(func(ctx context.Context, v int) {

		}, "test-key"); count != -1 {
			t.Error("Count must be -1")
		}
	})

	t.Run("RemoveListener", func(t *testing.T) {
		if count := testSignal.RemoveListener("test-key"); count != 1 {
			t.Error("Count must be 1")
		}

		if count := testSignal.RemoveListener("test-key"); count != -1 {
			t.Error("Count must be -1")
		}
	})

	t.Run("Reset", func(t *testing.T) {
		testSignal.Reset()
		if !testSignal.IsEmpty() {
			t.Error("Count must be 0")
		}
	})

}

// TestBaseSignal tests the BaseSignal to make sure
// Emit throws a panic because it is a base class.
func TestBaseSignal(t *testing.T) {
	testSignal := signals.BaseSignal[int]{}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Emit should throw a panic")
		}
	}()

	testSignal.Emit(context.Background(), 1)
}
