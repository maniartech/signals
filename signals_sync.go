package signals

import (
	"context"
	"errors"
	"sync"
)

// SyncSignal implements synchronous signal emission, invoking all listeners
// sequentially in the same goroutine. This ensures predictable execution order
// and allows listeners to block the emission process.
//
// The type parameter T specifies the payload type that will be passed to listeners.
//
// SyncSignal is ideal for scenarios requiring:
//   - Guaranteed execution order
//   - Completion guarantees before Emit() returns
//   - Minimal goroutine overhead
//   - Sequential processing of events
type SyncSignal[T any] struct {
	// baseSignal handles listener management and storage
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once

	// order is the listener invocation order (FIFO default, or LIFO). It is set once at
	// construction (from SignalOptions.Order) and read on the lock-free emit path; the
	// zero value is FIFO, so a zero-value SyncSignal emits in registration order.
	order EmitOrder
}

func (s *SyncSignal[T]) ensureBase() {
	s.baseOnce.Do(func() {
		if s.baseSignal == nil {
			s.baseSignal = NewBaseSignal[T](nil)
		}
	})
}

// AddListener registers a new listener. See BaseSignal.AddListener for details.
func (s *SyncSignal[T]) AddListener(listener SignalListener[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListener(listener, key...)
}

// AddListenerWithErr registers an error-returning listener. See BaseSignal.AddListenerWithErr for details.
func (s *SyncSignal[T]) AddListenerWithErr(listener SignalListenerErr[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListenerWithErr(listener, key...)
}

// RemoveListener removes a keyed listener. See BaseSignal.RemoveListener for details.
func (s *SyncSignal[T]) RemoveListener(key string) int {
	s.ensureBase()
	return s.baseSignal.RemoveListener(key)
}

// Reset removes all subscribers. See BaseSignal.Reset for details.
func (s *SyncSignal[T]) Reset() {
	s.ensureBase()
	s.baseSignal.Reset()
}

// Len returns the current number of subscribers. See BaseSignal.Len for details.
func (s *SyncSignal[T]) Len() int {
	s.ensureBase()
	return s.baseSignal.Len()
}

// IsEmpty returns true if there are no subscribers. See BaseSignal.IsEmpty for details.
func (s *SyncSignal[T]) IsEmpty() bool {
	s.ensureBase()
	return s.baseSignal.IsEmpty()
}

// AddOnce registers a one-time listener with an optional key. See BaseSignal.AddOnce.
func (s *SyncSignal[T]) AddOnce(handler SignalListener[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddOnce(handler, key...)
}

// AddOnceWithErr registers an error-returning one-time listener with an optional
// key. See BaseSignal.AddOnceWithErr.
func (s *SyncSignal[T]) AddOnceWithErr(handler SignalListenerErr[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddOnceWithErr(handler, key...)
}

// AddListenerWithCancel registers a repeating listener and returns a canceller func
// that removes it. See BaseSignal.AddListenerWithCancel.
func (s *SyncSignal[T]) AddListenerWithCancel(handler SignalListener[T], key ...string) func() {
	s.ensureBase()
	return s.baseSignal.AddListenerWithCancel(handler, key...)
}

// AddOnceWithCancel registers a one-time listener and returns a canceller func that
// removes it (including before it fires). See BaseSignal.AddOnceWithCancel.
func (s *SyncSignal[T]) AddOnceWithCancel(handler SignalListener[T], key ...string) func() {
	s.ensureBase()
	return s.baseSignal.AddOnceWithCancel(handler, key...)
}

// Keys returns a snapshot of all listener keys. See BaseSignal.Keys for details.
func (s *SyncSignal[T]) Keys() []string {
	s.ensureBase()
	return s.baseSignal.Keys()
}

// HasKey reports whether a listener with the given key exists. See BaseSignal.HasKey.
func (s *SyncSignal[T]) HasKey(key string) bool {
	s.ensureBase()
	return s.baseSignal.HasKey(key)
}

// OnError registers an error sink for the Emit path. See BaseSignal.OnError. On a
// SyncSignal the sink runs inline on the caller's goroutine when an error-returning
// listener fails during Emit (which is best-effort and does not stop the chain). Use
// TryEmit to instead have errors returned and stop on the first.
func (s *SyncSignal[T]) OnError(sink func(ctx context.Context, err error)) {
	s.ensureBase()
	s.baseSignal.OnError(sink)
}

// iterStart returns the starting index and step for iterating n listeners in this
// signal's configured order: forward (0, +1) for FIFO, backward (n-1, -1) for LIFO.
func (s *SyncSignal[T]) iterStart(n int) (i, step int) {
	if s.order == LIFO {
		return n - 1, -1
	}
	return 0, 1
}

// Emit synchronously invokes all registered listeners with the given payload.
// Listeners are called sequentially in the signal's configured order — FIFO
// (registration order, the default) or LIFO (reverse, most-recently-added first).
// The order is stable across add/remove because removal preserves order.
//
// The method blocks until all listeners have completed execution. If the provided
// context is cancelled or times out, remaining listeners will not be invoked. An
// error-returning listener (AddListenerWithErr) may stop the chain early by returning
// signals.ErrStopPropagation: the remaining listeners are skipped, and the sentinel is a
// control value — it is not routed to the OnError sinks.
//
// Parameters:
//   - ctx: Context for cancellation and timeout. Checked before each listener invocation.
//   - payload: Data to pass to all listeners
func (s *SyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.ensureBase()
	// If context already canceled, bail out early
	if ctx != nil && ctx.Err() != nil {
		return
	}
	// Lock-free read: a single atomic load of the immutable subscriber slice.
	// No lock and no snapshot copy — the slice is never mutated after publication,
	// so iterating it is safe even if a concurrent writer swaps in a new one.
	subscribers := s.baseSignal.load()
	i, step := s.iterStart(len(subscribers))
	for k := 0; k < len(subscribers); k++ {
		// Stop invoking further listeners if the context is canceled
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				break
			}
		}
		sub := &subscribers[i]
		if sub.listenerErr != nil {
			err := sub.listenerErr(ctx, payload)
			if errors.Is(err, ErrStopPropagation) {
				return // listener stopped the chain: skip the rest (not a failure, not routed)
			}
			// Emit is best-effort: any other returned error does not stop the chain. Route
			// it to the OnError sinks (symmetric with AsyncSignal) instead of discarding it —
			// use TryEmit if you need errors returned and the chain to stop on the first.
			if err != nil {
				s.baseSignal.routeError(ctx, err)
			}
		} else if sub.listener != nil {
			sub.listener(ctx, payload)
		}
		i += step
	}
}

// TryEmit synchronously invokes all registered listeners and returns any errors encountered.
// This method is similar to Emit but provides error handling and propagation capabilities.
//
// Behavior:
//   - Invokes listeners sequentially in the signal's configured order (FIFO default, or LIFO)
//   - A listener returning signals.ErrStopPropagation stops the chain early; TryEmit returns
//     nil (a clean stop, not an error — the remaining listeners are simply skipped)
//   - Stops immediately if context is cancelled or any error-returning listener fails
//   - Returns the first error encountered (context error or listener error)
//   - Returns nil if all listeners complete successfully
//
// Error priority:
//  1. Context errors (cancellation/timeout) are checked before invoking each listener
//  2. Listener errors from SignalListenerErr callbacks are returned immediately
//  3. Standard SignalListener callbacks cannot return errors
//
// Use TryEmit when you need to:
//   - Detect and handle listener failures
//   - Stop emission on first error
//   - Implement transactional event handling
//
// Parameters:
//   - ctx: Context for cancellation and timeout. Checked before each listener invocation.
//   - payload: Data to pass to all listeners
//
// Returns:
//   - nil if all listeners complete successfully
//   - context.Err() if the context is cancelled or times out
//   - The first non-nil error returned by any SignalListenerErr
func (s *SyncSignal[T]) TryEmit(ctx context.Context, payload T) error {
	s.ensureBase()
	// If context already canceled, bail out early with error
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Lock-free read: a single atomic load of the immutable subscriber slice.
	subscribers := s.baseSignal.load()
	i, step := s.iterStart(len(subscribers))
	for k := 0; k < len(subscribers); k++ {
		// Stop invoking further listeners if the context is canceled
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		sub := &subscribers[i]
		if sub.listenerErr != nil {
			err := sub.listenerErr(ctx, payload)
			if errors.Is(err, ErrStopPropagation) {
				return nil // clean stop: remaining listeners skipped, not reported as an error
			}
			if err != nil {
				return err
			}
		} else if sub.listener != nil {
			sub.listener(ctx, payload)
		}
		i += step
	}
	if ctx != nil {
		return ctx.Err()
	}
	return nil
}
