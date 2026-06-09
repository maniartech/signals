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
// and does not wait for listeners to finish. Use EmitAndWait when completion
// of all listeners must be awaited.
type AsyncSignal[T any] struct {
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once

	// slots is a counting semaphore bounding how many handler goroutines run at
	// once. It is nil when no MaxConcurrent bound is configured, in which case
	// dispatch is unbounded (a goroutine per handler). Set once at construction.
	slots chan struct{}

	// onErr holds the per-signal error sinks registered via OnError. Errors returned
	// by AddListenerWithErr handlers on the fire-and-forget Emit path are routed to
	// every sink. Stored as an immutable slice behind an atomic pointer (copy-on-write
	// registration under onErrMu) so routing is lock-free and concurrency-safe.
	onErr   atomic.Pointer[[]func(context.Context, error)]
	onErrMu sync.Mutex
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

// AddListenerWithErr registers an error-returning listener. Unlike SyncSignal (where
// errors propagate through TryEmit), on an AsyncSignal a non-nil error returned by
// the handler is routed to the sinks registered via OnError on the fire-and-forget
// Emit path, or collected and returned by EmitAndWaitErr. See
// BaseSignal.AddListenerWithErr for registration details.
func (s *AsyncSignal[T]) AddListenerWithErr(handler SignalListenerErr[T], key ...string) int {
	s.ensureBase()
	return s.baseSignal.AddListenerWithErr(handler, key...)
}

// OnError registers a sink invoked when an error-returning listener (added via
// AddListenerWithErr) returns a non-nil error during a fire-and-forget Emit. Multiple
// sinks may be registered; all are invoked for every error. Sinks run on the handler's
// own goroutine, so keep them cheap and non-blocking — a blocking sink holds its
// concurrency slot exactly like a blocking listener would. A panic inside a sink is
// recovered and routed to SetPanicHandler and does not stop the remaining sinks.
// OnError is safe for concurrent use. Errors on the EmitAndWaitErr path are returned,
// not routed here.
func (s *AsyncSignal[T]) OnError(sink func(ctx context.Context, err error)) {
	if sink == nil {
		return
	}
	s.onErrMu.Lock()
	defer s.onErrMu.Unlock()
	var next []func(context.Context, error)
	if cur := s.onErr.Load(); cur != nil {
		next = append(next, *cur...)
	}
	next = append(next, sink)
	s.onErr.Store(&next)
}

// routeError delivers err to every registered OnError sink. Each sink is isolated:
// a panicking sink is recovered (routed to SetPanicHandler) and does not prevent the
// remaining sinks from running.
func (s *AsyncSignal[T]) routeError(ctx context.Context, err error) {
	p := s.onErr.Load()
	if p == nil {
		return
	}
	for _, sink := range *p {
		func() {
			defer func() {
				if r := recover(); r != nil {
					handleListenerPanic(r)
				}
			}()
			sink(ctx, err)
		}()
	}
}

// AddOnce registers a one-time listener. See BaseSignal.AddOnce for details.
func (s *AsyncSignal[T]) AddOnce(handler SignalListener[T]) int {
	s.ensureBase()
	return s.baseSignal.AddOnce(handler)
}

// AddOnceWithKey registers a keyed one-time listener. See BaseSignal.AddOnceWithKey.
func (s *AsyncSignal[T]) AddOnceWithKey(handler SignalListener[T], key string) int {
	s.ensureBase()
	return s.baseSignal.AddOnceWithKey(handler, key)
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
// Use EmitAndWait if the caller must block until all listeners have finished.
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

// EmitAndWait invokes all current listeners asynchronously (each in its own
// goroutine, concurrently) and blocks until every listener has returned.
//
// Unlike Emit, EmitAndWait runs the dispatcher on the caller's goroutine and waits.
// A configured MaxConcurrent bound is honored. The wait is ctx-aware: if ctx is
// canceled or its deadline expires while listeners are still running, EmitAndWait
// returns promptly rather than blocking indefinitely on a slow or hung listener.
// (Go cannot force-cancel a running listener goroutine; the guaranteed property is
// the caller's liveness — the detached listener may continue in the background.)
//
// Cancellation and panic-recovery semantics otherwise match Emit.
func (s *AsyncSignal[T]) EmitAndWait(ctx context.Context, payload T) {
	s.ensureBase()
	if ctx != nil && ctx.Err() != nil {
		return
	}
	subscribers := s.baseSignal.load() // snapshot at call time
	if len(subscribers) == 0 {
		return
	}
	var wg sync.WaitGroup
	s.dispatch(ctx, payload, subscribers, &wg, nil)
	waitForOrCancel(ctx, &wg)
}

// EmitAndWaitErr is EmitAndWait that also collects and returns the errors of any
// error-returning listeners (added via AddListenerWithErr). It runs all listeners
// concurrently, waits for completion, and returns the errors.Join of every non-nil
// error in registration order (deterministic, not completion order); it returns nil
// iff every listener succeeded. Plain listeners (no error) never contribute.
//
// Like EmitAndWait it is ctx-aware: if ctx is canceled or its deadline expires while
// listeners are still running, EmitAndWaitErr returns ctx.Err() promptly without
// waiting for the stragglers (and without reading their still-in-progress results).
func (s *AsyncSignal[T]) EmitAndWaitErr(ctx context.Context, payload T) error {
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

// waitForOrCancel blocks until wg is done, or (if ctx is cancellable) until ctx is
// done — whichever comes first. A non-cancellable ctx (nil or one whose Done()
// returns nil, e.g. context.Background) takes the plain wg.Wait() path with no
// extra goroutine.
func waitForOrCancel(ctx context.Context, wg *sync.WaitGroup) {
	if ctx == nil || ctx.Done() == nil {
		wg.Wait()
		return
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// dispatch contains the shared scheduling logic for Emit and EmitAndWait. Each
// listener runs in its own goroutine. When a MaxConcurrent bound is configured,
// a semaphore slot is acquired before each handler is started and released when it
// finishes (on every exit path — including a recovered panic). When wg is non-nil,
// each started handler is tracked on it.
func (s *AsyncSignal[T]) dispatch(ctx context.Context, payload T, subscribers []keyedListener[T], wg *sync.WaitGroup, errs []error) {
	// subscribers is an immutable snapshot captured by the caller at emit-call time;
	// it is never mutated after publication, so iterating it is safe even while a
	// writer concurrently swaps in a new slice. (Emit/EmitAndWait already returned
	// early for an already-canceled ctx and an empty subscriber set.)
	//
	// errs (non-nil only for EmitAndWaitErr) collects each error-returning handler's
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
							s.routeError(ctx, err)
						}
					}
					return
				}
				kl.listener(ctx, payload)
			}()
		}
	}
}
