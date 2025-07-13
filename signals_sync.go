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

// Emit notifies all subscribers of the signal and passes the payload in a
// synchronous way.
func (s *SyncSignal[T]) Emit(ctx context.Context, payload T) {
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
