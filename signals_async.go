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
	baseSignal     *BaseSignal[T]
	workerPoolOnce sync.Once
	workerPool     chan *emitTask[T]
	poolSize       int
}

// emitTask is used to avoid per-listener closure allocations in async emit.
type emitTask[T any] struct {
	ctx      context.Context
	payload  T
	listener SignalListener[T]
	wg       *sync.WaitGroup
}

var emitTaskPool = sync.Pool{
	New: func() any { return new(emitTask[any]) },
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
		s.workerPool = make(chan *emitTask[T], size)
		for i := 0; i < size; i++ {
			go func() {
				for task := range s.workerPool {
					task.listener(task.ctx, task.payload)
					task.wg.Done()
					// Reset and put back in pool
					task.ctx = nil
					var zero T
					task.payload = zero
					task.listener = nil
					task.wg = nil
					emitTaskPool.Put(task)
				}
			}()
		}
	})
}

// AddListener adds a listener to the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) AddListener(listener SignalListener[T], key ...string) int {
	return s.baseSignal.AddListener(listener, key...)
}

// RemoveListener removes a listener from the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) RemoveListener(key string) int {
	return s.baseSignal.RemoveListener(key)
}

// Reset resets the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) Reset() {
	s.baseSignal.Reset()
}

// Len returns the number of listeners. Promoted from baseSignal.
func (s *AsyncSignal[T]) Len() int {
	return s.baseSignal.Len()
}

// IsEmpty checks if the signal has any subscribers. Promoted from baseSignal.
func (s *AsyncSignal[T]) IsEmpty() bool {
	return s.baseSignal.IsEmpty()
}

func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.baseSignal.mu.RLock()
	n := len(s.baseSignal.subscribers)
	if n == 0 {
		s.baseSignal.mu.RUnlock()
		return
	}
	// Zero-allocation fast path for single listener, no key
	if n == 1 && s.baseSignal.subscribers[0].key == "" {
		listener := s.baseSignal.subscribers[0].listener
		s.baseSignal.mu.RUnlock()
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
	copy(subscribersCopy, s.baseSignal.subscribers)
	s.baseSignal.mu.RUnlock()

	// Initialize worker pool if not already
	s.ensureWorkerPool(n)

	var wg sync.WaitGroup
	threshold := 16
	if n <= threshold {
		// Fast path: direct goroutine spawn for small N
		for i := range subscribersCopy {
			sub := &subscribersCopy[i]
			if sub.listener != nil {
				wg.Add(1)
				go func(listener SignalListener[T]) {
					defer wg.Done()
					listener(ctx, payload)
				}(sub.listener)
			}
		}
		wg.Wait()
	} else {
		// Pooled worker/tasks for large N
		for i := range subscribersCopy {
			sub := &subscribersCopy[i]
			if sub.listener != nil {
				wg.Add(1)
				poolVal := emitTaskPool.Get()
				var task *emitTask[T]
				if poolVal != nil {
					if t, ok := poolVal.(*emitTask[T]); ok {
						task = t
					} else {
						task = new(emitTask[T])
					}
				} else {
					task = new(emitTask[T])
				}
				task.ctx = ctx
				task.payload = payload
				task.listener = sub.listener
				task.wg = &wg
				s.workerPool <- task
			}
		}
		wg.Wait()
	}
	// Reset and put back in pool
	for i := range subscribersCopy {
		var zero keyedListener[T]
		subscribersCopy[i] = zero
	}
	asyncSubscribersPool.Put(subscribersCopy[:0])
}
