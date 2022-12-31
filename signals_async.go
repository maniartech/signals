package signals

import "context"

// AsyncSignal is the implementation of the Signal interface.
// It provides the same functionality as the SyncSignal but
// the listeners are called in a separate goroutine.
type AsyncSignal[T any] struct {
	BaseSignal[T]
}

// Emit notifies all subscribers of the signal and passes the payload.
// If the context has a deadline or cancellable property, the listeners
// must respect it.
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	for _, sub := range s.subscribers {
		go sub.listener(ctx, payload)
	}
}
