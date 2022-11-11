package signals

type SignalListener[T any] func(T)
