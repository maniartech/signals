package signals

import (
	"context"
	"sync"
)

// AsyncSignal is a struct that implements the Signal interface.
// This is the default implementation. It provides the same functionality as
// the SyncSignal but the listeners are called in a separate goroutine.
// This means that all listeners are called asynchronously. However, the method
// waits for all the listeners to finish before returning. If you don't want
// to wait for the listeners to finish, you can call the Emit method
// in a separate goroutine.
type AsyncSignal[T any] struct {
	BaseSignal[T]

	mu sync.Mutex
}

// Emit notifies all subscribers of the signal and passes the payload in a
// asynchronous way.
//
// If the context has a deadline or cancellable property, the listeners
// must respect it. This means that the listeners should stop processing when
// the context is cancelled. While emtting it calls the listeners in separate
// goroutines, so the listeners are called asynchronously. However, it
// waits for all the listeners to finish before returning. If you don't want
// to wait for the listeners to finish, you can call the Emit method. Also,
// you must know that Emit does not guarantee the type safety of the emitted value.
//
// Example:
//
//	signal := signals.New[string]()
//	signal.AddListener(func(ctx context.Context, payload string) {
//		// Listener implementation
//		// ...
//	})
//
//	signal.Emit(context.Background(), "Hello, world!")
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var wg sync.WaitGroup

	for _, sub := range s.subscribers {
		wg.Add(1)
		go func(listener func(context.Context, T)) {
			defer wg.Done()
			listener(ctx, payload)
		}(sub.listener)
	}

	wg.Wait()
}
