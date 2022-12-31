package signals

import "context"

// SignalListener provides the function signature for a signal listener.
// It accepts a context and a payload of type T.
type SignalListener[T any] func(context.Context, T)
