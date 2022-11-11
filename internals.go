package signals

type keyedListener[T any] struct {
	key      string
	listener SignalListener[T]
}
