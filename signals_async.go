package signals

import (
	"context"
	"errors"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
)

// AsyncSignal is a struct that implements the Signal interface.
// This is the default implementation. It provides the same functionality as
// the SyncSignal but the listeners are called in a separate goroutine.
// This means that all listeners are called asynchronously. Emit is fire-and-forget
// and does not wait for listeners to finish. Use TryEmit when completion of all
// listeners must be awaited (and to observe their errors).
type AsyncSignal[T any] struct {
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once

	// slots is a counting semaphore bounding how many handler goroutines run at
	// once. It is nil when no MaxConcurrent bound is configured, in which case
	// dispatch is unbounded (a goroutine per handler). Set once at construction.
	slots chan struct{}
}

// DefaultMaxConcurrent returns the recommended MaxConcurrent value (2 × NumCPU) for
// callers who want to bound async dispatch without choosing a number themselves. It
// is a sensible starting point for typical I/O-bound listeners. It is NOT applied
// automatically — leaving SignalOptions.MaxConcurrent unset keeps dispatch unbounded
// (the safe default; a bound can starve long-running listeners).
func DefaultMaxConcurrent() int { return 2 * runtime.NumCPU() }

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
//
// The handler should be cheap and MUST NOT panic; if it does, the secondary panic
// is recovered and discarded (it cannot crash the process), but the originating
// panic value is then lost to that handler. The handler is process-global: the last
// call wins, and a library that sets it overrides its host application's handler —
// so libraries should generally leave it to the application to configure.
func SetPanicHandler(h func(recovered any)) {
	f := panicHandlerFunc(h)
	panicHandler.Store(&f)
}

// handleListenerPanic dispatches a recovered listener panic to the configured panic
// handler, if any.
//
// The handler invocation is itself guarded by a recover: the panic handler is the
// LAST line of defense, so it must be unbreakable. If a (buggy) panic handler panics,
// that secondary panic is recovered and discarded here rather than propagating out of
// the handler goroutine — where, unrecovered, it would crash the entire process and
// thereby defeat the very panic isolation this function exists to provide. We discard
// rather than re-report it: re-invoking any handler to report a handler failure risks
// unbounded recursion. The contract (documented on SetPanicHandler) is simply that the
// panic handler must not panic; if it does, we contain it.
func handleListenerPanic(recovered any) {
	defer func() { _ = recover() }()
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

// AddListenerWithErr registers an error-returning listener. On an AsyncSignal a
// non-nil error returned by the handler is routed to the sinks registered via OnError
// on the fire-and-forget Emit path, or collected and returned by TryEmit. See
// BaseSignal.AddListenerWithErr for registration details.
func (s *AsyncSignal[T]) AddListenerWithErr(handler SignalListenerErr[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListenerWithErr(handler, key...)
}

// OnError registers an error sink for the Emit path. See BaseSignal.OnError. On an
// AsyncSignal the sink runs on the failing listener's handler goroutine (keep it
// cheap and non-blocking — a blocking sink holds its concurrency slot like a blocking
// listener). Errors on the TryEmit path are returned, not routed here.
func (s *AsyncSignal[T]) OnError(sink func(ctx context.Context, err error)) {
	s.ensureBase()
	s.baseSignal.OnError(sink)
}

// AddOnce registers a one-time listener with an optional key. See BaseSignal.AddOnce.
func (s *AsyncSignal[T]) AddOnce(handler SignalListener[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddOnce(handler, key...)
}

// AddOnceWithErr registers an error-returning one-time listener with an optional
// key. See BaseSignal.AddOnceWithErr.
func (s *AsyncSignal[T]) AddOnceWithErr(handler SignalListenerErr[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddOnceWithErr(handler, key...)
}

// Keys returns a snapshot of all listener keys. See BaseSignal.Keys for details.
func (s *AsyncSignal[T]) Keys() []string {
	s.ensureBase()
	return s.baseSignal.Keys()
}

// HasKey reports whether a listener with the given key exists. See BaseSignal.HasKey.
func (s *AsyncSignal[T]) HasKey(key string) bool {
	s.ensureBase()
	return s.baseSignal.HasKey(key)
}

// Emit invokes all current listeners asynchronously (fire-and-forget).
//
// Emit spawns a single dispatcher goroutine and returns immediately — it never
// blocks the caller, even when a MaxConcurrent bound is configured. The dispatcher
// runs each listener in its own goroutine, so listeners execute concurrently. If a
// MaxConcurrent bound is set and saturated, excess handlers park (in the background
// dispatcher) until a slot frees; nothing is dropped and the caller is unaffected.
//
// If ctx is non-nil and already canceled when Emit is called, no listeners are
// invoked. If ctx becomes canceled while dispatching, no further handlers start.
//
// Panics raised by listener callbacks are recovered so a failing listener cannot
// crash the process or prevent other listeners from running. Recovered panics are
// reported to the handler configured via SetPanicHandler.
//
// Use TryEmit if the caller must block until all listeners have finished (and
// optionally observe their errors).
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	s.ensureBase()
	if ctx != nil && ctx.Err() != nil {
		return // already canceled: skip without spawning a dispatcher
	}
	// Snapshot the listener set at CALL time (a single atomic load of the immutable
	// slice), then dispatch it in the background. This keeps "Emit captures the
	// listeners present when it was called" — a later Add/Remove/Reset does not change
	// what this emission delivers — while still returning to the caller immediately.
	subscribers := s.baseSignal.load()
	if len(subscribers) == 0 {
		return
	}
	go s.dispatch(ctx, payload, subscribers, nil, nil)
}

// TryEmit invokes all current listeners concurrently (each in its own goroutine),
// waits for every one to finish, and returns the combined error of any
// error-returning listeners (added via AddListenerWithErr).
//
// Unlike the sequential SyncSignal.TryEmit, it does NOT stop at the first error — all
// listeners run, and TryEmit returns the errors.Join of every non-nil error in
// registration order (deterministic, not completion order). It returns nil iff every
// listener succeeded; plain listeners (no error) never contribute. This is the async
// "wait for completion" emit — call it and ignore the result if you only need to wait.
//
// A configured MaxConcurrent bound is honored. The wait is ctx-aware: if ctx is
// canceled or its deadline expires while listeners are still running, TryEmit returns
// ctx.Err() promptly rather than blocking indefinitely on a slow or hung listener
// (and without reading the still-in-progress results). Go cannot force-cancel a
// running listener goroutine; the guaranteed property is the caller's liveness — a
// detached listener may continue in the background. Panic-recovery matches Emit.
func (s *AsyncSignal[T]) TryEmit(ctx context.Context, payload T) error {
	s.ensureBase()
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	subscribers := s.baseSignal.load() // snapshot at call time
	if len(subscribers) == 0 {
		return nil
	}
	// Each handler writes its error to its own index — distinct slice elements, so no
	// lock is needed; the read below is ordered after wg completion (happens-before).
	errs := make([]error, len(subscribers))
	var wg sync.WaitGroup
	s.dispatch(ctx, payload, subscribers, &wg, errs)

	if ctx == nil || ctx.Done() == nil {
		wg.Wait()
		return errors.Join(errs...)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// All handlers finished — safe to read errs (happens-before via wg).
		return errors.Join(errs...)
	case <-ctx.Done():
		// Canceled: handlers may still be writing errs — do NOT read it (would race).
		return ctx.Err()
	}
}

// dispatch contains the shared scheduling logic for Emit and TryEmit. Each
// listener runs in its own goroutine. When a MaxConcurrent bound is configured,
// a semaphore slot is acquired before each handler is started and released when it
// finishes (on every exit path — including a recovered panic). When wg is non-nil,
// each started handler is tracked on it.
func (s *AsyncSignal[T]) dispatch(ctx context.Context, payload T, subscribers []keyedListener[T], wg *sync.WaitGroup, errs []error) {
	// subscribers is an immutable snapshot captured by the caller at emit-call time;
	// it is never mutated after publication, so iterating it is safe even while a
	// writer concurrently swaps in a new slice. (Emit/TryEmit already returned
	// early for an already-canceled ctx and an empty subscriber set.)
	//
	// errs (non-nil only for TryEmit) collects each error-returning handler's
	// result at its own index. When errs is nil, a non-nil handler error is instead
	// routed to the OnError sinks.
	for i := range subscribers {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return // canceled mid-dispatch: start no further handlers
			}
		}
		kl := subscribers[i]
		if kl.listenerErr != nil || kl.listener != nil {
			// Acquire a concurrency slot if bounded. When saturated, park here — but
			// if ctx is cancellable, abort the parked acquire on cancellation rather
			// than starting a handler after the deadline.
			if s.slots != nil {
				if ctx == nil {
					s.slots <- struct{}{}
				} else {
					select {
					case s.slots <- struct{}{}:
					case <-ctx.Done():
						return
					}
				}
			}

			if wg != nil {
				wg.Add(1)
			}
			idx := i
			go func() {
				// Defers run LIFO: the recover runs first (catching a handler/sink
				// panic), then the slot is released, then wg.Done — so the slot is
				// ALWAYS freed, even on panic (no bounded-pool slot leak/deadlock).
				if wg != nil {
					defer wg.Done()
				}
				if s.slots != nil {
					defer func() { <-s.slots }()
				}
				defer func() {
					if r := recover(); r != nil {
						handleListenerPanic(r)
					}
				}()
				if kl.listenerErr != nil {
					if err := kl.listenerErr(ctx, payload); err != nil {
						if errs != nil {
							errs[idx] = err // distinct index — no lock needed
						} else {
							s.baseSignal.routeError(ctx, err)
						}
					}
					return
				}
				kl.listener(ctx, payload)
			}()
		}
	}
}
