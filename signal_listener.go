package signals

import (
	"context"
	"errors"
)

// ErrStopPropagation is a sentinel a SignalListenerErr returns to stop a SyncSignal's
// emission early: when a listener returns it (or an error wrapping it), the remaining
// listeners for that emission are not invoked. It is a CONTROL value, not a failure —
// SyncSignal.TryEmit returns nil (not this sentinel) when a listener stops the chain,
// and SyncSignal.Emit does not route it to the OnError sinks. It mirrors the standard
// library's fs.SkipAll / filepath.SkipDir convention.
//
// Because AsyncSignal invokes listeners concurrently, it has no sequential propagation
// to stop, so it IGNORES ErrStopPropagation entirely: a listener returning it is neither
// reported as a failure (never joined by TryEmit, never routed to OnError) nor given
// any other effect. This keeps a listener that may run on either signal type
// well-defined: ErrStopPropagation is never mistaken for an error anywhere.
//
// Only error-returning listeners (AddListenerWithErr / AddOnceWithErr) can stop the
// chain — a plain SignalListener has no return value. Detect it with errors.Is.
var ErrStopPropagation = errors.New("signals: stop propagation")

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
//
// How a non-nil error is handled depends on the signal type and the emit method:
//   - SyncSignal.TryEmit stops at the first error and returns it (transactional).
//   - AsyncSignal.TryEmit runs every listener, then returns the errors.Join of all
//     failures (it cannot stop across goroutines).
//   - On the best-effort Emit path (sync or async), the error does not stop the
//     chain; it is routed to the sinks registered via OnError.
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
