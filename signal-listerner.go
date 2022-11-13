package signals

// SignalListener provides the function signature for a signal listener.
type SignalListener[T any] func(T)
