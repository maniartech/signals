package signals

import (
	"context"
	"sync"
)

// SyncSignal is a struct that implements the Signal interface.
// It provides a synchronous way of notifying all subscribers of a signal.
// The type parameter `T` is a placeholder for any type.
type SyncSignal[T any] struct {
	BaseSignal[T]
}

var syncSubscribersPool = sync.Pool{
	New: func() any { return make([]keyedListener[any], 0, 16) },
}

// AddListenerErr adds an error-returning listener. It behaves like AddListener
// but the listener may return an error. If a key is provided and already
// exists, it returns -1.
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
		s.subscribersMap[key[0]] = struct{}{}
		s.subscribers = append(s.subscribers, keyedListener[T]{
			key:         key[0],
			listenerErr: listener,
		})
	} else {
		s.subscribers = append(s.subscribers, keyedListener[T]{
			listenerErr: listener,
		})
	}

	return len(s.subscribers)
}

// Emit notifies all subscribers of the signal and passes the payload in a
// synchronous way.
func (s *SyncSignal[T]) Emit(ctx context.Context, payload T) {
	// If context already canceled, bail out early
	if ctx != nil && ctx.Err() != nil {
		return
	}
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
			if ctx != nil && ctx.Err() != nil {
				return
			}
			listener(ctx, payload)
		}
		return
	}
	var subscribersCopy []keyedListener[T]
	// Use sync.Pool to reduce allocations
	poolVal := syncSubscribersPool.Get()
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
	// Use references in loop to avoid copying
	for i := range subscribersCopy {
		// Stop invoking further listeners if the context is canceled
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				break
			}
		}
		sub := &subscribersCopy[i]
		if sub.listener != nil {
			sub.listener(ctx, payload)
		}
	}
	// Reset and put back in pool
	for i := range subscribersCopy {
		var zero keyedListener[T]
		subscribersCopy[i] = zero
	}
	syncSubscribersPool.Put(subscribersCopy[:0])
}

// TryEmit behaves like Emit but returns an error when the provided context is
// canceled or when any error-returning listener returns a non-nil error. It
// stops invoking further listeners as soon as an error or cancellation is
// observed. If no error occurs, it returns nil.
func (s *SyncSignal[T]) TryEmit(ctx context.Context, payload T) error {
	// If context already canceled, bail out early with error
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	s.mu.RLock()
	n := len(s.subscribers)
	if n == 0 {
		s.mu.RUnlock()
		if ctx != nil {
			return ctx.Err()
		}
		return nil
	}
	// Zero-allocation fast path for single listener, no key
	if n == 1 && s.subscribers[0].key == "" {
		l := s.subscribers[0]
		s.mu.RUnlock()
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if l.listenerErr != nil {
			return l.listenerErr(ctx, payload)
		}
		if l.listener != nil {
			l.listener(ctx, payload)
		}
		if ctx != nil {
			return ctx.Err()
		}
		return nil
	}

	var subscribersCopy []keyedListener[T]
	// Use sync.Pool to reduce allocations
	poolVal := syncSubscribersPool.Get()
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

	// Ensure cleanup even on early return
	defer func() {
		for i := range subscribersCopy {
			var zero keyedListener[T]
			subscribersCopy[i] = zero
		}
		syncSubscribersPool.Put(subscribersCopy[:0])
	}()

	// Use references in loop to avoid copying
	for i := range subscribersCopy {
		// Stop invoking further listeners if the context is canceled
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		sub := &subscribersCopy[i]
		if sub.listenerErr != nil {
			if err := sub.listenerErr(ctx, payload); err != nil {
				return err
			}
			continue
		}
		if sub.listener != nil {
			sub.listener(ctx, payload)
		}
	}

	if ctx != nil {
		return ctx.Err()
	}
	return nil
}
