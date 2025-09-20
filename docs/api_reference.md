sig.Connect(func(n int) { fmt.Println(n) })
sig.Emit(123)
sig.Connect(func(msg string) { fmt.Println(msg) })
sig.Emit("hello async")
sig.Disconnect(id)
# API Reference

## Signal Interface

### `signals.New[T any]() Signal[T]`
Creates a new asynchronous signal for type `T`.

### `signals.NewSync[T any]() Signal[T]`
Creates a new synchronous signal for type `T`.

---

## Methods

### `AddListener(listener func(context.Context, T), key ...string) int`
Adds a listener to the signal. Returns the number of listeners after adding. Optional key for removal.

#### Example
```go
sig := signals.New[int]()
sig.AddListener(func(ctx context.Context, n int) {
    fmt.Println(n)
}, "my-key")
```

### `RemoveListener(key string) int`
Removes a listener by key. Returns the number of listeners after removal, or -1 if not found.

#### Example
```go
sig.RemoveListener("my-key")
```

### `Emit(ctx context.Context, value T)`
Emits a value to all connected listeners. For async signals, listeners are called in separate goroutines.

#### Example
```go
sig.Emit(context.Background(), 123)
```

### `Reset()`
Removes all listeners from the signal.

### `Len() int`
Returns the number of listeners.

### `IsEmpty() bool`
Returns true if there are no listeners.

---

## Advanced Usage

### Synchronous vs Asynchronous
- Use `signals.NewSync[T]()` for synchronous delivery (listeners called in emitter's goroutine).
- Use `signals.New[T]()` for asynchronous delivery (listeners called in separate goroutines).

### Example: Synchronous
```go
sig := signals.NewSync[string]()
sig.AddListener(func(ctx context.Context, msg string) {
    fmt.Println(msg)
})
sig.Emit(context.Background(), "sync event")
```

### Example: Asynchronous
```go
sig := signals.New[string]()
sig.AddListener(func(ctx context.Context, msg string) {
    fmt.Println(msg)
})
sig.Emit(context.Background(), "async event")
```

### Benchmarking
See `signals_benchmark_test.go` for performance examples.

---

- [Introduction](introduction.md)
- [Getting Started](getting_started.md)
- [Concepts](concepts.md)
