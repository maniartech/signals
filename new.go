package signals

// NewSync creates a new synchronous signal with the specified payload type.
// Synchronous signals invoke all listeners sequentially in the same goroutine
// that calls Emit(), blocking until all listeners have completed.
//
// Use NewSync when:
//   - You need guaranteed execution order
//   - Listeners must complete before continuing
//   - You want to avoid the overhead of goroutine scheduling
//
// Example:
//
//	signal := signals.NewSync[int]()
//	signal.AddListener(func(ctx context.Context, payload int) {
//	    fmt.Printf("Received: %d\n", payload)
//	})
//	signal.Emit(context.Background(), 42) // Blocks until listener completes
func NewSync[T any]() *SyncSignal[T] {
	s := &SyncSignal[T]{
		baseSignal: NewBaseSignal[T](nil),
	}
	return s
}

// New creates a new asynchronous signal with the specified payload type.
// Asynchronous signals invoke each listener in its own goroutine, allowing
// Emit() to return immediately without waiting for listener completion.
//
// Use New (async) when:
//   - Listeners perform I/O or other blocking operations
//   - You want non-blocking signal emission
//   - Execution order between listeners doesn't matter
//   - You can handle potential concurrency issues in listeners
//
// Example:
//
//	signal := signals.New[int]()
//	signal.AddListener(func(ctx context.Context, payload int) {
//	    // This runs in its own goroutine
//	    time.Sleep(time.Second)
//	    fmt.Printf("Received: %d\n", payload)
//	})
//	signal.Emit(context.Background(), 42) // Returns immediately
func New[T any]() *AsyncSignal[T] {
	s := &AsyncSignal[T]{
		baseSignal: NewBaseSignal[T](nil),
	}
	return s
}
