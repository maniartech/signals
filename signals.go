package signals

// Signal is the interface that represents a signal that can be subscribed to
// emitting a payload of type T.
type Signal[T any] interface {

	// Emit notifies all subscribers of the signal and passes the payload.
	Emit(payload T)

	// Add adds a handler to the signal. The handler will be called
	// whenever the signal is emitted. It reuturns the number of
	// subscribers after the handler was added. It returns -1 if the
	// handler was already added to the signal.
	Add(handler SignalListener[T], key ...string) int

	// Remove removes a handler from the signal. It returns the number
	// of subscribers after the handler was removed. It returns -1 if the
	// handler was not found.
	Remove(key string) int

	// Reset resets the signal. It removes all subscribers.
	Reset()

	// Len returns the number of handlers subscribed to the signal.
	Len() int
}
