package signals

import "context"

// Signal is the interface that represents a signal that can be subscribed to
// emitting a payload of type T.
type Signal[T any] interface {
	// Emit notifies all subscribers of the signal and passes the context and the payload.
	//
	// If the context has a deadline or cancellable property, the listeners
	// must respect it. If the signal is async (default), the listeners are called
	// in a separate goroutine.
	//
	// Example:
	//	signal := signals.New[int]()
	//	signal.AddListener(func(ctx context.Context, payload int) {
	//		// Listener implementation
	//		// ...
	//	})
	//	signal.Emit(context.Background(), 42)
	Emit(ctx context.Context, payload T)

	// TryEmit invokes all listeners, waits for them to finish, and returns any error.
	// It returns nil if no listener failed.
	//
	// Behavior differs by signal type (both wait for completion and report errors):
	//   - SyncSignal: invokes listeners sequentially and STOPS at the first error
	//     (or canceled context), returning that error — transactional.
	//   - AsyncSignal: invokes listeners concurrently, waits for all, and returns the
	//     errors.Join of every failure (it cannot stop-on-first across goroutines).
	//
	// In both cases a non-nil result means "at least one listener failed". Call it and
	// ignore the result when you only need to wait for completion.
	TryEmit(ctx context.Context, payload T) error

	// OnError registers a sink invoked when an error-returning listener (added via
	// AddListenerWithErr) returns a non-nil error on the Emit (best-effort) path.
	// Multiple sinks may be registered. Errors on the TryEmit path are returned, not
	// routed here. Keep sinks cheap and non-blocking.
	OnError(sink func(ctx context.Context, err error))

	// AddListener adds a listener to the signal.
	//
	// The listener will be called whenever the signal is emitted. It returns the
	// number of subscribers after the listener was added. It accepts an optional key
	// that can be used to remove the listener later or to check if the listener
	// was already added. It returns -1 if the listener with the same key
	// was already added to the signal.
	//
	// Example:
	//	signal := signals.NewSync[int]()
	//	count := signal.AddListener(func(ctx context.Context, payload int) {
	//		// Listener implementation
	//		// ...
	//	})
	//	fmt.Println("Number of subscribers after adding listener:", count)
	AddListener(handler SignalListener[T], key ...string) int

	// AddListenerWithErr adds an error-returning listener. It returns the number of
	// subscribers after adding (or -1 on a duplicate key), like AddListener.
	//
	// How the returned error is used depends on the signal type:
	//   - SyncSignal: the error propagates through TryEmit (which stops on the first
	//     error); plain Emit discards it.
	//   - AsyncSignal: on the fire-and-forget Emit path the error is routed to the
	//     sinks registered via OnError; on TryEmit it is collected and returned.
	AddListenerWithErr(handler SignalListenerErr[T], key ...string) int

	// RemoveListener removes a listener from the signal.
	//
	// It returns the number of subscribers after the listener was removed.
	// It returns -1 if the listener was not found.
	//
	// Example:
	//	signal := signals.NewSync[int]()
	//	signal.AddListener(func(ctx context.Context, payload int) {
	//		// Listener implementation
	//		// ...
	//	}, "key1")
	//	count := signal.RemoveListener("key1")
	//	fmt.Println("Number of subscribers after removing listener:", count)
	RemoveListener(key string) int

	// Reset resets the signal by removing all subscribers from the signal,
	// effectively clearing the list of subscribers.
	//
	// This can be used when you want to stop all listeners from receiving
	// further signals.
	//
	// Example:
	//	signal := signals.New[int]()
	//	signal.AddListener(func(ctx context.Context, payload int) {
	//		// Listener implementation
	//		// ...
	//	})
	//	signal.Reset() // Removes all listeners
	//	fmt.Println("Number of subscribers after resetting:", signal.Len())
	Reset()

	// Len returns the number of listeners subscribed to the signal.
	//
	// This can be used to check how many listeners are currently waiting for a signal.
	// The returned value is of type int.
	//
	// Example:
	//	signal := signals.NewSync[int]()
	//	signal.AddListener(func(ctx context.Context, payload int) {
	//		// Listener implementation
	//		// ...
	//	})
	//	fmt.Println("Number of subscribers:", signal.Len())
	Len() int

	// IsEmpty checks if the signal has any subscribers.
	//
	// It returns true if the signal has no subscribers, and false otherwise.
	// This can be used to check if there are any listeners before emitting a signal.
	//
	// Example:
	//	signal := signals.New[int]()
	//	fmt.Println("Is signal empty?", signal.IsEmpty()) // Should print true
	//	signal.AddListener(func(ctx context.Context, payload int) {
	//		// Listener implementation
	//		// ...
	//	})
	//	fmt.Println("Is signal empty?", signal.IsEmpty()) // Should print false
	IsEmpty() bool

	// AddOnce adds a listener that fires exactly once and then removes itself.
	// The one-shot guarantee is concurrency-safe: even under simultaneous
	// emissions the handler is invoked at most once. An optional key makes the
	// one-shot addressable (to remove it before it fires) and subject to duplicate
	// detection (returns -1 if the key already exists); absent/empty key = unkeyed.
	// It returns the number of subscribers after adding the listener.
	AddOnce(handler SignalListener[T], key ...string) int

	// AddOnceWithErr is the error-returning counterpart of AddOnce, accepting the
	// same optional key — it is to AddOnce what AddListenerWithErr is to AddListener.
	// The listener fires once, removes itself, and reports failure via an error that
	// is routed like any other listener error (OnError on Emit, joined on TryEmit).
	// It is "consumed on attempt": it fires and self-removes even if it errors.
	AddOnceWithErr(handler SignalListenerErr[T], key ...string) int

	// Keys returns a snapshot of all caller-supplied listener keys, safe to read
	// while other goroutines mutate the listener set. Empty-string keys are omitted.
	Keys() []string

	// HasKey reports, in O(1), whether a listener with the given key is registered.
	HasKey(key string) bool
}

// NewWithOptions creates a new async Signal with custom allocation/growth options.
// If opts.MaxConcurrent > 0, async dispatch is bounded to that many concurrent
// handlers via a counting semaphore; otherwise dispatch is unbounded (the default).
func NewWithOptions[T any](opts *SignalOptions) *AsyncSignal[T] {
	s := &AsyncSignal[T]{
		baseSignal: NewBaseSignal[T](opts),
	}
	if opts != nil && opts.MaxConcurrent > 0 {
		s.slots = make(chan struct{}, opts.MaxConcurrent)
	}
	return s
}

// NewSyncWithOptions creates a new sync Signal with custom allocation/growth options.
func NewSyncWithOptions[T any](opts *SignalOptions) *SyncSignal[T] {
	return &SyncSignal[T]{
		baseSignal: NewBaseSignal[T](opts),
	}
}
