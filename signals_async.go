package signals

import "context"

// AsyncSignal is a struct that implements the Signal interface.
// It provides the same functionality as the SyncSignal but
// the listeners are called in a separate goroutine. This means that
// the listeners can run concurrently, not blocking the main thread.
// The type parameter `T` is a placeholder for any type.
type AsyncSignal[T any] struct {
	BaseSignal[T]
}

// Emit notifies all subscribers of the signal and passes the payload.
// The payload is of the same type as the AsyncSignal's type parameter `T`.
// The method iterates over the subscribers slice of the AsyncSignal,
// and for each subscriber, it calls the subscriber's listener function in a new goroutine,
// passing the context and the payload.
// If the context has a deadline or cancellable property, the listeners
// must respect it. This means that the listeners should stop processing when the context is cancelled.
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	for _, sub := range s.subscribers {
		go sub.listener(ctx, payload)
	}
}
