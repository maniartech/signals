package signals

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestAddListener(t *testing.T) {
	t.Run("listener map should not be overridden when called from coroutines concurrently", func(t *testing.T) {
		signal := BaseSignal[int]{
			mu:             &sync.Mutex{},
			subscribers:    make([]keyedListener[int], 0),
			subscribersMap: make(map[string]SignalListener[int]),
		}
		wg := sync.WaitGroup{}
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go func(i int) {
				signal.AddListener(func(ctx context.Context, payload int) {}, fmt.Sprintf("key-%d", i))
				wg.Done()
			}(i)
		}
		wg.Wait()
		if len(signal.subscribersMap) != 1000 {
			t.Fatalf("expected 1000 listeners, got %d", len(signal.subscribersMap))
		}
	})
}

func TestRemoveListener(t *testing.T) {
	t.Run("listener map should not be overridden when called from coroutines concurrently", func(t *testing.T) {
		signal := BaseSignal[int]{
			mu:             &sync.Mutex{},
			subscribers:    make([]keyedListener[int], 0),
			subscribersMap: make(map[string]SignalListener[int]),
		}
		for i := 0; i < 1000; i++ {
			signal.AddListener(func(ctx context.Context, payload int) {}, fmt.Sprintf("key-%d", i))
		}
		wg := sync.WaitGroup{}
		for i := 0; i < 1000; i++ {
			wg.Add(1)
			go func(i int) {
				signal.RemoveListener(fmt.Sprintf("key-%d", i))
				wg.Done()
			}(i)
		}
		wg.Wait()
		if len(signal.subscribersMap) != 0 {
			t.Fatalf("expected 0 listeners, got %d", len(signal.subscribersMap))
		}
	})
}
