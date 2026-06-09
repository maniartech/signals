package signals

import (
	"context"
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
	go s.dispatch(ctx, payload, subscribers, nil)
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
	s.dispatch(ctx, payload, subscribers, &wg)
	waitForOrCancel(ctx, &wg)
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
func (s *AsyncSignal[T]) dispatch(ctx context.Context, payload T, subscribers []keyedListener[T], wg *sync.WaitGroup) {
	// subscribers is an immutable snapshot captured by the caller at emit-call time;
	// it is never mutated after publication, so iterating it is safe even while a
	// writer concurrently swaps in a new slice. (Emit/EmitAndWait already returned
	// early for an already-canceled ctx and an empty subscriber set.)
	for i := range subscribers {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return // canceled mid-dispatch: start no further handlers
			}
		}
		if listener := subscribers[i].listener; listener != nil {
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
			go func() {
				// Defers run LIFO: the recover runs first (catching a listener
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
				listener(ctx, payload)
			}()
		}
	}
}
