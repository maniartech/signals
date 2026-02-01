package signals

import (
	"context"
	"sync"
)

// AsyncSignal is a struct that implements the Signal interface.
// This is the default implementation. It provides the same functionality as
// the SyncSignal but the listeners are called in a separate goroutine.
// This means that all listeners are called asynchronously. Emit is fire-and-forget
// and does not wait for listeners to finish.
type AsyncSignal[T any] struct {
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once
}

func (s *AsyncSignal[T]) ensureBase() {
	s.baseOnce.Do(func() {
		if s.baseSignal == nil {
			s.baseSignal = NewBaseSignal[T](nil)
		}
	})
}

// AddListener adds a listener to the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) AddListener(listener SignalListener[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListener(listener, key...)
}

// RemoveListener removes a listener from the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) RemoveListener(key string) int {
	s.ensureBase()
	return s.baseSignal.RemoveListener(key)
}

// Reset resets the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) Reset() {
	s.ensureBase()
	s.baseSignal.Reset()
}

// Len returns the number of listeners. Promoted from baseSignal.
func (s *AsyncSignal[T]) Len() int {
	s.ensureBase()
	return s.baseSignal.Len()
}

// IsEmpty checks if the signal has any subscribers. Promoted from baseSignal.
func (s *AsyncSignal[T]) IsEmpty() bool {
	s.ensureBase()
	return s.baseSignal.IsEmpty()
}

func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.ensureBase()
	if ctx != nil && ctx.Err() != nil {
		return
	}

	s.baseSignal.mu.RLock()
	subscribers := s.baseSignal.subscribers
	if len(subscribers) == 0 {
		s.baseSignal.mu.RUnlock()
		return
	}
	snapshot := make([]keyedListener[T], len(subscribers))
	copy(snapshot, subscribers)
	s.baseSignal.mu.RUnlock()

	for i := range snapshot {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				break
			}
		}
		sub := &snapshot[i]
		if sub.listener != nil {
			listener := sub.listener
			go func() {
				defer func() {
					_ = recover()
				}()
				listener(ctx, payload)
			}()
		}
	}
}
