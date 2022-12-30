package signals

import "context"

// Signal is the interface that represents a signal that can be subscribed to
// emitting a payload of type T.
type Signal[T any] interface {

	// Emit notifies all subscribers of the signal and passes the context and the payload.
	// If the context has a deadline or cancellable property, the listeners
	// must respect it. If the signal is async, the listeners are called
	// in a separate goroutine.
	Emit(ctx context.Context, payload T)

	// AddListener adds a listener to the signal. The listener will be called
	// whenever the signal is emitted. It reuturns the number of
	// subscribers after the listener was added. It accepts an optional key
	// that can be used to remove the listener later or to check if the listener
	// was already added. It returns -1 if the listener with the same key
	// was already added to the signal.
	AddListener(handler SignalListener[T], key ...string) int

	// RemoveListener removes a listener from the signal. It returns the number
	// of subscribers after the listener was removed. It returns -1 if the
	// listener was not found.
	RemoveListener(key string) int

	// Reset resets the signal. It removes all subscribers.
	Reset()

	// Len returns the number of listeners subscribed to the signal.
	Len() int

	// IsEmpty returns true if the signal has no subscribers.
	IsEmpty() bool
}
