package signals

import "context"

// SyncSignal is the implementation of the Signal interface.
//
type SyncSignal[T any] struct {
	BaseSignal[T]
}

// Emit notifies all subscribers of the signal and passes the payload.
// If the context has a deadline or cancellable property, the listeners
// must respect it. If the signal is async, the listeners are called
// in a separate goroutine.
func (s *SyncSignal[T]) Emit(ctx context.Context, payload T) {
	for _, sub := range s.subscribers {
		sub.listener(ctx, payload)
	}
}
