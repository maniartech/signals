package signals

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
)

// AsyncSignal is a struct that implements the Signal interface.
// This is the default implementation. It provides the same functionality as
// the SyncSignal but the listeners are called in a separate goroutine.
// This means that all listeners are called asynchronously. Emit is fire-and-forget
// and does not wait for listeners to finish. Use EmitAndWait when completion
// of all listeners must be awaited.
type AsyncSignal[T any] struct {
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once
}

func (s *AsyncSignal[T]) ensureBase() {
	s.baseOnce.Do(func() {
		if s.baseSignal == nil {
			s.baseSignal = NewBaseSignal[T](nil)
		}
	})
}

// panicHandlerFunc is the type of the function invoked when an asynchronous
// listener panics.
type panicHandlerFunc func(recovered any)

// panicHandler holds the currently configured panic handler. It is accessed
// atomically so SetPanicHandler is safe to call concurrently with emissions.
var panicHandler atomic.Pointer[panicHandlerFunc]

func init() {
	SetPanicHandler(defaultPanicHandler)
}

// defaultPanicHandler logs recovered listener panics via the standard library
// logger so failures are visible without crashing the process.
func defaultPanicHandler(recovered any) {
	log.Printf("signals: recovered panic in async listener: %v", recovered)
}

// SetPanicHandler configures the handler invoked when an asynchronous listener
// panics. The default handler logs the recovered value using the standard
// library logger. Pass nil to silently discard panics. SetPanicHandler is safe
// for concurrent use.
func SetPanicHandler(h func(recovered any)) {
	f := panicHandlerFunc(h)
	panicHandler.Store(&f)
}

// handleListenerPanic dispatches a recovered listener panic to the configured
// panic handler, if any.
func handleListenerPanic(recovered any) {
	if p := panicHandler.Load(); p != nil {
		if h := *p; h != nil {
			h(recovered)
		}
	}
}

// AddListener adds a listener to the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) AddListener(listener SignalListener[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListener(listener, key...)
}

// RemoveListener removes a listener from the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) RemoveListener(key string) int {
	s.ensureBase()
	return s.baseSignal.RemoveListener(key)
}

// Reset resets the signal. Promoted from baseSignal.
func (s *AsyncSignal[T]) Reset() {
	s.ensureBase()
	s.baseSignal.Reset()
}

// Len returns the number of listeners. Promoted from baseSignal.
func (s *AsyncSignal[T]) Len() int {
	s.ensureBase()
	return s.baseSignal.Len()
}

// IsEmpty checks if the signal has any subscribers. Promoted from baseSignal.
func (s *AsyncSignal[T]) IsEmpty() bool {
	s.ensureBase()
	return s.baseSignal.IsEmpty()
}

// Emit invokes all current listeners asynchronously (fire-and-forget).
//
// Emit schedules each subscribed listener in its own goroutine and returns
// immediately without waiting for listeners to complete. If ctx is non-nil
// and already canceled when Emit is called, no listeners are invoked. While
// scheduling, if ctx becomes done, Emit stops starting new goroutines but
// does not affect listeners already started.
//
// Panics raised by listener callbacks are recovered so a failing listener
// cannot crash the process or prevent other listeners from being scheduled.
// Recovered panics are reported to the handler configured via SetPanicHandler.
//
// Use EmitAndWait if the caller must block until all listeners have finished.
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.dispatch(ctx, payload, nil)
}

// EmitAndWait invokes all current listeners asynchronously and blocks until
// every listener that was started has returned.
//
// Each listener still runs in its own goroutine (so listeners execute
// concurrently with one another), but unlike Emit, EmitAndWait does not
// return until all started listeners have completed. This matches the
// blocking behavior that Emit had in earlier releases of this library.
//
// Cancellation and panic-recovery semantics are identical to Emit: a context
// that is already done prevents listeners from being scheduled, and panics
// are reported to the handler configured via SetPanicHandler.
func (s *AsyncSignal[T]) EmitAndWait(ctx context.Context, payload T) {
	var wg sync.WaitGroup
	s.dispatch(ctx, payload, &wg)
	wg.Wait()
}

// dispatch contains the shared scheduling logic for Emit and EmitAndWait.
// When wg is non-nil, each started listener goroutine is tracked on it.
func (s *AsyncSignal[T]) dispatch(ctx context.Context, payload T, wg *sync.WaitGroup) {
	s.ensureBase()
	if ctx != nil && ctx.Err() != nil {
		return
	}

	// Lock-free read: a single atomic load of the immutable subscriber slice.
	// The slice is never mutated after publication, so iterating it while a writer
	// concurrently swaps in a new one is safe.
	subscribers := s.baseSignal.load()
	for i := range subscribers {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				break
			}
		}
		sub := &subscribers[i]
		if sub.listener != nil {
			listener := sub.listener
			if wg != nil {
				wg.Add(1)
			}
			go func() {
				if wg != nil {
					defer wg.Done()
				}
				defer func() {
					if r := recover(); r != nil {
						handleListenerPanic(r)
					}
				}()
				listener(ctx, payload)
			}()
		}
	}
}
