package signals_test

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

func TestSignal(t *testing.T) {
	testSignal := signals.New[int]()

	results := make([]int, 0)
	testSignal.AddListener(func(v int) {
		results = append(results, v)
	})

	testSignal.AddListener(func(v int) {
		results = append(results, v)
	})

	testSignal.Emit(1)
	testSignal.Emit(2)
	testSignal.Emit(3)

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

	testSignal := signals.NewAsync[int]()
	testSignal.AddListener(func(v int) {
		time.Sleep(100 * time.Millisecond)
		count += 1
		wg.Done()
	})
	testSignal.AddListener(func(v int) {
		time.Sleep(100 * time.Millisecond)
		count += 1
		wg.Done()
	})

	testSignal.Emit(1)
	testSignal.Emit(2)
	testSignal.Emit(3)

	if count >= 6 {
		t.Error("Not asynchronus! count must be less than 6")
	}

	wg.Wait()

	if count != 6 {
		t.Error("Count must be 6")
	}
}

func TestAddRemoveListener(t *testing.T) {
	testSignal := signals.New[int]()

	t.Run("AddListener", func(t *testing.T) {
		testSignal.AddListener(func(v int) {
			// Do something
		})

		testSignal.AddListener(func(v int) {
			// Do something
		}, "test-key")

		if testSignal.Len() != 2 {
			t.Error("Count must be 2")
		}

		if count := testSignal.AddListener(func(v int) {

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
