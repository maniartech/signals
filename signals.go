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

	// Reset resets the signal by removing all subscribers from the signal,
	// effectively clearing the list of subscribers.
	// This can be used when you want to stop all listeners from receiving
	// further signals.
	Reset()

	// Len returns the number of listeners subscribed to the signal.
	// This can be used to check how many listeners are currently waiting for a signal.
	// The returned value is of type int.
	Len() int

	// IsEmpty checks if the signal has any subscribers.
	// It returns true if the signal has no subscribers, and false otherwise.
	// This can be used to check if there are any listeners before emitting a signal.
	IsEmpty() bool
}
