package signals

// NewSync creates a new signal that can be used to emit and listen to events
// synchronously.
//
// Example:
//  signal := signals.NewSync[int]()
//  signal.AddListener(func(ctx context.Context, payload int) {
//      // Listener implementation
//      // ...
//  })
//  signal.Emit(context.Background(), 42)
func NewSync[T any]() Signal[T] {
	s := &SyncSignal[T]{}
	s.Reset()
	return s
}

// New creates a new signal that can be used to emit and listen to events
// asynchronously.
//
// Example:
//  signal := signals.New[int]()
//  signal.AddListener(func(ctx context.Context, payload int) {
//      // Listener implementation
//      // ...
//  })
//  signal.Emit(context.Background(), 42)
func New[T any]() Signal[T] {
	s := &AsyncSignal[T]{}
	s.Reset() // Reset the signal
	return s
}
