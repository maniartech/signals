package signals

import "context"

// SignalListener defines the function signature for standard signal listeners.
// Listeners are invoked when a signal is emitted and receive both context and payload.
//
// Parameters:
//   - context.Context: Provides cancellation, timeouts, and request-scoped values.
//     Listeners should respect context cancellation and stop processing when ctx.Done() is closed.
//   - T: The payload data emitted with the signal. The type is determined when creating the signal.
//
// The function does not return a value. For error handling, use SignalListenerErr instead.
//
// Example:
//
//	var listener SignalListener[string] = func(ctx context.Context, msg string) {
//	    if ctx.Err() != nil {
//	        return // Context cancelled, stop processing
//	    }
//	    fmt.Println("Received:", msg)
//	}
type SignalListener[T any] func(context.Context, T)

// SignalListenerErr defines the function signature for error-returning signal listeners.
// These listeners are invoked by TryEmit() and can report processing errors, allowing
// the caller to detect and handle failures during signal emission.
//
// Parameters:
//   - context.Context: Provides cancellation, timeouts, and request-scoped values.
//     Listeners should respect context cancellation and stop processing when ctx.Done() is closed.
//   - T: The payload data emitted with the signal. The type is determined when creating the signal.
//
// Returns:
//   - error: nil if processing succeeded, or an error describing what went wrong.
//     When TryEmit() encounters a non-nil error, it stops invoking subsequent listeners.
//
// Example:
//
//	var listener SignalListenerErr[int] = func(ctx context.Context, value int) error {
//	    if value < 0 {
//	        return fmt.Errorf("invalid value: %d", value)
//	    }
//	    return processValue(value)
//	}
type SignalListenerErr[T any] func(context.Context, T) error
