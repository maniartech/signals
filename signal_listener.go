package signals

import "context"

// SignalListener is a type definition for a function that will act as a
// listener for signals. This function takes two parameters:
// 1. A context of type `context.Context`. This is typically used for timeout
//    and cancellation signals, and can carry request-scoped values across API
//    boundaries and between processes.
// 2. A payload of generic type `T`. This can be any type, and represents the
//    data or signal that the listener function will process.
//
// The function does not return any value.
type SignalListener[T any] func(context.Context, T)

// SignalListenerErr is a type definition for a function that will act as an
// error-returning listener for signals. This function takes two parameters:
// 1. A context of type `context.Context`. This is typically used for timeout
//    and cancellation signals, and can carry request-scoped values across API
//    boundaries and between processes.
// 2. A payload of generic type `T`. This can be any type, and represents the
//    data or signal that the listener function will process.
//
// The function returns an error value, which can be used to indicate if the
// listener encountered any issues while processing the signal.
type SignalListenerErr[T any] func(context.Context, T) error
