package signals

import (
	"context"
	"sync"
)

// SyncSignal implements synchronous signal emission, invoking all listeners
// sequentially in the same goroutine. This ensures predictable execution order
// and allows listeners to block the emission process.
//
// The type parameter T specifies the payload type that will be passed to listeners.
//
// SyncSignal is ideal for scenarios requiring:
//   - Guaranteed execution order
//   - Completion guarantees before Emit() returns
//   - Minimal goroutine overhead
//   - Sequential processing of events
type SyncSignal[T any] struct {
	// baseSignal handles listener management and storage
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once
}

func (s *SyncSignal[T]) ensureBase() {
	s.baseOnce.Do(func() {
		if s.baseSignal == nil {
			s.baseSignal = NewBaseSignal[T](nil)
		}
	})
}

// AddListenerWithErr registers an error-returning listener that can report processing failures.
// These listeners are particularly useful with TryEmit(), which can detect and return errors.
//
// Parameters:
//   - listener: The error-returning callback function (must not be nil, will panic otherwise)
//   - key: Optional unique identifier for the listener
//
// Returns:
//   - The total number of subscribers after adding the listener
//   - Returns -1 if a keyed listener with the same key already exists
//
// Note: When both listener and listenerErr are set, listenerErr takes precedence during TryEmit().
func (s *BaseSignal[T]) AddListenerWithErr(listener SignalListenerErr[T], key ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if listener == nil {
		panic("listener cannot be nil")
	}

	if len(key) > 0 {
		if _, ok := s.subscribersMap[key[0]]; ok {
			return -1
		}
		s.ensureCapacity(1)
		s.subscribersMap[key[0]] = struct{}{}
		s.subscribers = append(s.subscribers, keyedListener[T]{
			key:         key[0],
			keyed:       true,
			listenerErr: listener,
		})
	} else {
		s.ensureCapacity(1)
		s.subscribers = append(s.subscribers, keyedListener[T]{
			listenerErr: listener,
		})
	}

	return len(s.subscribers)
}

// AddListener registers a new listener. See BaseSignal.AddListener for details.
func (s *SyncSignal[T]) AddListener(listener SignalListener[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListener(listener, key...)
}

// AddListenerWithErr registers an error-returning listener. See BaseSignal.AddListenerWithErr for details.
func (s *SyncSignal[T]) AddListenerWithErr(listener SignalListenerErr[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListenerWithErr(listener, key...)
}

// RemoveListener removes a keyed listener. See BaseSignal.RemoveListener for details.
func (s *SyncSignal[T]) RemoveListener(key string) int {
	s.ensureBase()
	return s.baseSignal.RemoveListener(key)
}

// Reset removes all subscribers. See BaseSignal.Reset for details.
func (s *SyncSignal[T]) Reset() {
	s.ensureBase()
	s.baseSignal.Reset()
}

// Len returns the current number of subscribers. See BaseSignal.Len for details.
func (s *SyncSignal[T]) Len() int {
	s.ensureBase()
	return s.baseSignal.Len()
}

// IsEmpty returns true if there are no subscribers. See BaseSignal.IsEmpty for details.
func (s *SyncSignal[T]) IsEmpty() bool {
	s.ensureBase()
	return s.baseSignal.IsEmpty()
}

// Emit synchronously invokes all registered listeners with the given payload.
// Listeners are called sequentially in the order they were registered (though order
// may change after removals due to swap-remove optimization).
//
// The method blocks until all listeners have completed execution. If the provided
// context is cancelled or times out, remaining listeners will not be invoked.
//
// Performance optimizations:
//   - Early return if no subscribers or context is already cancelled
//   - Zero-allocation fast path for single anonymous listeners
//   - Pooled buffer reuse to minimize allocations for multiple listeners
//
// Parameters:
//   - ctx: Context for cancellation and timeout. Checked before each listener invocation.
//   - payload: Data to pass to all listeners
func (s *SyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.ensureBase()
	// If context already canceled, bail out early
	if ctx != nil && ctx.Err() != nil {
		return
	}
	s.baseSignal.mu.RLock()
	subscribers := s.baseSignal.subscribers
	if len(subscribers) == 0 {
		s.baseSignal.mu.RUnlock()
		return
	}
	for i := range subscribers {
		// Stop invoking further listeners if the context is canceled
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				break
			}
		}
		sub := &subscribers[i]
		if sub.listenerErr != nil {
			_ = sub.listenerErr(ctx, payload)
			continue
		}
		if sub.listener != nil {
			sub.listener(ctx, payload)
		}
	}
	s.baseSignal.mu.RUnlock()
}

// TryEmit synchronously invokes all registered listeners and returns any errors encountered.
// This method is similar to Emit but provides error handling and propagation capabilities.
//
// Behavior:
//   - Invokes listeners sequentially in registration order
//   - Stops immediately if context is cancelled or any error-returning listener fails
//   - Returns the first error encountered (context error or listener error)
//   - Returns nil if all listeners complete successfully
//
// Error priority:
//  1. Context errors (cancellation/timeout) are checked before invoking each listener
//  2. Listener errors from SignalListenerErr callbacks are returned immediately
//  3. Standard SignalListener callbacks cannot return errors
//
// Use TryEmit when you need to:
//   - Detect and handle listener failures
//   - Stop emission on first error
//   - Implement transactional event handling
//
// Parameters:
//   - ctx: Context for cancellation and timeout. Checked before each listener invocation.
//   - payload: Data to pass to all listeners
//
// Returns:
//   - nil if all listeners complete successfully
//   - context.Err() if the context is cancelled or times out
//   - The first non-nil error returned by any SignalListenerErr
func (s *SyncSignal[T]) TryEmit(ctx context.Context, payload T) error {
	s.ensureBase()
	// If context already canceled, bail out early with error
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	s.baseSignal.mu.RLock()
	subscribers := s.baseSignal.subscribers
	if len(subscribers) == 0 {
		s.baseSignal.mu.RUnlock()
		if ctx != nil {
			return ctx.Err()
		}
		return nil
	}
	for i := range subscribers {
		// Stop invoking further listeners if the context is canceled
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				s.baseSignal.mu.RUnlock()
				return err
			}
		}
		sub := &subscribers[i]
		if sub.listenerErr != nil {
			if err := sub.listenerErr(ctx, payload); err != nil {
				s.baseSignal.mu.RUnlock()
				return err
			}
			continue
		}
		if sub.listener != nil {
			sub.listener(ctx, payload)
		}
	}

	s.baseSignal.mu.RUnlock()
	if ctx != nil {
		return ctx.Err()
	}
	return nil
}
