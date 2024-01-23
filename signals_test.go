package signals_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

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
	wg := &sync.WaitGroup{}
	wg.Add(6)

	testSignal := signals.New[int]()
	testSignal.AddListener(func(ctx context.Context, v int) {
		time.Sleep(100 * time.Millisecond)
		count += 1
		wg.Done()
	})
	testSignal.AddListener(func(ctx context.Context, v int) {
		time.Sleep(100 * time.Millisecond)
		count += 1
		wg.Done()
	})

	ctx := context.Background()
	go testSignal.Emit(ctx, 1)
	go testSignal.Emit(ctx, 2)
	go testSignal.Emit(ctx, 3)

	if count >= 6 {
		t.Error("Not asynchronus! count must be less than 6")
	}

	wg.Wait()

	if count != 6 {
		t.Error("Count must be 6")
	}
}

// Test Async with Timeout Context. After the context is cancelled, the
// listeners should cancel their execution.
func TestSignalAsyncWithTimeout(t *testing.T) {

	var count int

	timeoutCount := 0

	testSignal := signals.New[int]()
	testSignal.AddListener(func(ctx context.Context, v int) {
		time.Sleep(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timeoutCount += 1
		default:
			count += 1
		}
	})
	testSignal.AddListener(func(ctx context.Context, v int) {
		time.Sleep(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timeoutCount += 1
		default:
			count += 1
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	testSignal.Emit(ctx, 1)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	testSignal.Emit(ctx2, 1)

	ctx3, cancel3 := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel3()
	testSignal.Emit(ctx3, 3)

	// The code is checking if the value of the `count` variable is equal to 3 and if
	// the value of the `timeoutCount` variable is equal to 3. If either of these
	// conditions is not met, an error message is printed.
	if count != 3 {
		t.Error("Count must be 3")
	}

	if timeoutCount != 3 {
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
