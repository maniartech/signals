package signals

import (
	"context"
	"runtime"
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
	workerPoolOnce sync.Once
	workerPool     chan func()
	poolSize       int
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
var asyncSubscribersPool = sync.Pool{
	New: func() any { return make([]keyedListener[any], 0, 16) },
}

func (s *AsyncSignal[T]) ensureWorkerPool(size int) {
	s.workerPoolOnce.Do(func() {
		if size <= 0 {
			size = 2 * runtime.NumCPU()
		}
		s.poolSize = size
		s.workerPool = make(chan func(), size)
		for i := 0; i < size; i++ {
			go func() {
				for f := range s.workerPool {
					f()
				}
			}()
		}
	})
}

func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.mu.RLock()
	n := len(s.subscribers)
	if n == 0 {
		s.mu.RUnlock()
		return
	}
	// Zero-allocation fast path for single listener, no key
	if n == 1 && s.subscribers[0].key == "" {
		listener := s.subscribers[0].listener
		s.mu.RUnlock()
		if listener != nil {
			listener(ctx, payload)
		}
		return
	}
	var subscribersCopy []keyedListener[T]
	// Use sync.Pool to reduce allocations
	poolVal := asyncSubscribersPool.Get()
	if poolVal != nil {
		if tmp, ok := poolVal.([]keyedListener[T]); ok && cap(tmp) >= n {
			subscribersCopy = tmp[:n]
		} else {
			subscribersCopy = make([]keyedListener[T], n)
		}
	} else {
		subscribersCopy = make([]keyedListener[T], n)
	}
	copy(subscribersCopy, s.subscribers)
	s.mu.RUnlock()

	// Initialize worker pool if not already
	s.ensureWorkerPool(n)

	var wg sync.WaitGroup
	// Use references in loop to avoid copying
	for i := range subscribersCopy {
		sub := &subscribersCopy[i]
		if sub.listener != nil {
			wg.Add(1)
			listener := sub.listener
			s.workerPool <- func() {
				defer wg.Done()
				listener(ctx, payload)
			}
		}
	}
	wg.Wait()
	// Reset and put back in pool
	for i := range subscribersCopy {
		var zero keyedListener[T]
		subscribersCopy[i] = zero
	}
	asyncSubscribersPool.Put(subscribersCopy[:0])
}
