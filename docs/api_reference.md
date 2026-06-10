# API Reference

> **Type-safe in-process event processing for Go.** The sync read path runs in ~9 ns/op with zero allocations (1 listener, AMD Ryzen 7 5700G, Windows, `go test -count=6`).

Complete documentation for all methods, interfaces, and advanced usage patterns for **in-process communication within Go monolithic applications**.

> **About the numbers in this document.** Every performance figure below comes from the v1.4 benchmark suite on an AMD Ryzen 7 5700G (Windows, `go test -count=6`). They are order-of-magnitude guides, not guarantees — reproduce on your own hardware with:
>
> ```bash
> go test -run '^$' -bench=. -benchmem -count=6 ./tests/
> ```

## Scope: In-Process Events Only

**Important**: This library is designed for **package-to-package communication within the same Go process**, not for distributed systems or microservices communication.

- ✅ **Monolith Architecture**: Perfect for coordinating packages within single binary
- ✅ **In-Process Events**: Zero network overhead, type-safe, microsecond latency
- ❌ **Distributed Systems**: Use message brokers (Kafka, RabbitMQ) for cross-service communication

## Core Constructors

### **`signals.New[T any]() *AsyncSignal[T]`**
Creates a new **asynchronous** signal for concurrent, non-blocking event processing.

```go
// Create async signal for order processing
orderSignal := signals.New[Order]()

// All listeners execute concurrently in separate goroutines
orderSignal.AddListener(inventory.UpdateStock)      // Goroutine 1
orderSignal.AddListener(email.SendConfirmation)    // Goroutine 2
orderSignal.AddListener(analytics.TrackOrder)      // Goroutine 3

// Non-blocking emit - returns immediately
orderSignal.Emit(ctx, order)
fmt.Println("Order processing started!") // Executes instantly
```

**Performance (dispatch rate, not completion):** async `Emit` measures only the cost of snapshotting listeners and spawning goroutines — ~260 ns/op, 208 B, 2 allocs for a single listener. The listeners themselves complete later, on their own goroutines. To wait for completion, use `TryEmit`.

### **`signals.NewSync[T any]() *SyncSignal[T]`**
Creates a new **synchronous** signal for sequential, error-aware processing.

```go
// Create sync signal for payment workflow
paymentFlow := signals.NewSync[Payment]()

// Sequential execution with error propagation
paymentFlow.AddListenerWithErr(validatePayment)     // Step 1
paymentFlow.AddListenerWithErr(chargeCard)          // Step 2 (if Step 1 succeeds)
paymentFlow.AddListenerWithErr(recordTransaction)   // Step 3 (if Step 2 succeeds)

// Blocking emit with error handling
if err := paymentFlow.TryEmit(ctx, payment); err != nil {
    log.Error("Payment failed:", err)
    return rollbackPayment(payment)
}
```

**Performance:** the sync read path is allocation-free. `Emit` with one listener is ~9 ns/op (0 B, 0 allocs); `TryEmit` with one listener is ~11 ns/op (0 B, 0 allocs).

### **`signals.NewWithOptions[T any](opts *SignalOptions) *AsyncSignal[T]`**
Creates an asynchronous signal with custom optimization settings.

```go
type SignalOptions struct {
    InitialCapacity int                       // Pre-allocate listener slice
    GrowthFunc      func(currentCap int) int  // Custom growth algorithm
    MaxConcurrent   int                       // Async only: cap on concurrent handlers (0 = unbounded)
}

// Custom growth strategy for specific use cases
opts := &signals.SignalOptions{
    InitialCapacity: 100,      // Start with 100 listener capacity
    GrowthFunc: func(currentCap int) int {
        return currentCap * 2  // Double capacity instead of default
    },
}
signal := signals.NewWithOptions[Event](opts)

// High-capacity signal for known listener count
highCapOpts := &signals.SignalOptions{
    InitialCapacity: 1000,     // Avoid reallocations for 1000+ listeners
}
highCapSignal := signals.NewWithOptions[Event](highCapOpts)

// Bounded async dispatch: park excess handlers behind a counting semaphore.
// This is a safety valve to limit goroutine fan-out — it does not make
// dispatch faster. MaxConcurrent <= 0 means unbounded (the default).
// Sync signals ignore MaxConcurrent.
boundedOpts := &signals.SignalOptions{
    MaxConcurrent: signals.DefaultMaxConcurrent(), // 2 x NumCPU (a suggestion, not auto-applied)
}
boundedSignal := signals.NewWithOptions[Event](boundedOpts)
```

### **`signals.NewSyncWithOptions[T any](opts *SignalOptions) *SyncSignal[T]`**
Creates a synchronous signal with custom optimization settings.

```go
// Sync signal with custom options for critical workflows
opts := &signals.SignalOptions{
    InitialCapacity: 50,       // Pre-allocate for expected validators
    GrowthFunc: func(currentCap int) int {
        return currentCap + 25 // Conservative growth for sync operations
    },
}
validationFlow := signals.NewSyncWithOptions[ValidationEvent](opts)

// High-performance transaction processing
txOpts := &signals.SignalOptions{
    InitialCapacity: 200,      // Expect many transaction validators
}
txSignal := signals.NewSyncWithOptions[TransactionEvent](txOpts)

// Add error-returning listeners for validation chains
validationFlow.AddListenerWithErr(validateInput, "input-validation")
validationFlow.AddListenerWithErr(validateBusiness, "business-rules")
validationFlow.AddListenerWithErr(validateSecurity, "security-check")

// Execute with error propagation
if err := validationFlow.TryEmit(ctx, validationData); err != nil {
    return fmt.Errorf("validation failed: %w", err)
}
```

**Key Differences from Async:**
- **Synchronous execution**: Listeners run sequentially in the calling goroutine
- **Error propagation**: Use `TryEmit()` for error-aware workflows
- **Transaction safety**: `TryEmit` stops at the first listener error (or canceled ctx) and returns it immediately
- **Performance**: the sync emit path is allocation-free — ~9 ns/op for `Emit` and ~11 ns/op for `TryEmit` with a single listener (0 B, 0 allocs)

---

## Core Type Definitions

### **`SignalListener[T any]`**
Standard listener function type that processes events without error returns.

```go
type SignalListener[T any] func(context.Context, T)

// Example implementation
func logUserAction(ctx context.Context, user UserEvent) {
    log.Info("User action", "user", user.ID, "action", user.Action)
}

// Usage
signal.AddListener(logUserAction)
```

### **`SignalListenerErr[T any]`**
Error-returning listener function type for critical workflows that need error propagation.

```go
type SignalListenerErr[T any] func(context.Context, T) error

// Example implementation
func validateUser(ctx context.Context, user UserEvent) error {
    if user.ID == "" {
        return errors.New("user ID cannot be empty")
    }
    return database.ValidateUser(ctx, user.ID)
}

// Usage — works on BOTH sync and async signals
syncSignal.AddListenerWithErr(validateUser)
asyncSignal.AddListenerWithErr(validateUser)
```

---

## Signal Interface Methods

Both `SyncSignal[T]` and `AsyncSignal[T]` implement the same `Signal[T]` interface. The surface is symmetric and compile-time enforced — every method below is available on both types:

```go
type Signal[T any] interface {
    // Subscription
    AddListener(handler SignalListener[T], key ...string) int
    AddListenerWithErr(handler SignalListenerErr[T], key ...string) int
    AddOnce(handler SignalListener[T], key ...string) int
    AddOnceWithErr(handler SignalListenerErr[T], key ...string) int
    RemoveListener(key string) int

    // Emission
    Emit(ctx context.Context, payload T)
    TryEmit(ctx context.Context, payload T) error
    OnError(sink func(ctx context.Context, err error))

    // Introspection / lifecycle
    Reset()
    Len() int
    IsEmpty() bool
    Keys() []string
    HasKey(key string) bool
}
```

The behavior of `Emit`, `TryEmit`, and `OnError` differs between sync and async signals (sequential vs. concurrent, returned vs. routed errors); each is documented in detail below.

### **`AddListener(listener func(context.Context, T), key ...string) int`**
Adds a standard listener that processes events without returning errors.

```go
// Basic listener
signal.AddListener(func(ctx context.Context, user User) {
    fmt.Printf("User %s logged in\n", user.Name)
})

// Keyed listener for later removal
signal.AddListener(func(ctx context.Context, user User) {
    analytics.TrackLogin(user)
}, "analytics-tracker")

// Keyed listener with a single key (only the first key is used)
signal.AddListener(handleUserEvent, "user-handler")
```

> The key is an optional variadic for ergonomic call sites; only the first
> key is significant. An absent or empty key registers an unkeyed listener.

**Returns:** the subscriber count after addition, or `-1` if the key already exists (duplicate keys are rejected).

**Use Cases:**
- ✅ **Notifications**: Email, SMS, push notifications
- ✅ **Logging**: Audit trails, analytics, metrics
- ✅ **Background Tasks**: File processing, data cleanup
- ✅ **Fire-and-Forget**: Operations that don't need error feedback

### **`AddListenerWithErr(handler SignalListenerErr[T], key ...string) int`**
Adds an error-returning listener. Available on **both sync and async signals** (this was sync-only in v1.3; it is symmetric in v1.4).

- On a **sync** signal, a returned error is routed to `OnError` sinks during `Emit` (the chain continues), or returned by `TryEmit` (stopping at the first error).
- On an **async** signal, a returned error is routed to `OnError` sinks during `Emit`, or collected into the `errors.Join` returned by `TryEmit`.

```go
syncSignal := signals.NewSync[OrderEvent]()

// Error-returning listeners for critical workflows
syncSignal.AddListenerWithErr(func(ctx context.Context, event OrderEvent) error {
    if event.Amount > 10000 {
        return errors.New("order amount exceeds limit")
    }
    return processOrder(event)
}, "order-validator")

// Multiple validation steps
syncSignal.AddListenerWithErr(validateInventory, "inventory-check")
syncSignal.AddListenerWithErr(validatePayment, "payment-check")
syncSignal.AddListenerWithErr(createShipment, "fulfillment")

// Execute - stops on first error (sync TryEmit is transactional)
if err := syncSignal.TryEmit(ctx, orderEvent); err != nil {
    // Handle validation failure
    log.Error("Order processing failed:", err)
}
```

**Returns:** the subscriber count after addition, or `-1` if the key already exists.

**Use Cases:**
- ✅ **Validation Chains**: Multi-step validation workflows
- ✅ **Financial Transactions**: Payment processing, banking
- ✅ **Critical Workflows**: User registration, order fulfillment
- ✅ **API Calls**: External service integrations with timeout

### **`AddOnce(handler SignalListener[T], key ...string) int`**
Adds a listener that fires **exactly once** and then removes itself. The one-shot guard is concurrency-safe (atomic), so even under a concurrent async `Emit` the handler runs at most once.

```go
// Run a warm-up handler the first time the signal fires, then forget it
signal.AddOnce(func(ctx context.Context, e Event) {
    initializeCaches(e)
}, "warmup")
```

**Returns:** the subscriber count after addition, or `-1` if the key already exists.

### **`AddOnceWithErr(handler SignalListenerErr[T], key ...string) int`**
Error-returning one-shot listener. It is **consumed on attempt**: it fires and removes itself even if it returns an error (the error is routed/returned exactly like `AddListenerWithErr`).

```go
signal.AddOnceWithErr(func(ctx context.Context, e Event) error {
    return runOneTimeMigration(e)
}, "migrate")
```

**Returns:** the subscriber count after addition, or `-1` if the key already exists.

### **`RemoveListener(key string) int`**
Removes a listener by its key identifier.

```go
// Add keyed listeners
signal.AddListener(emailHandler, "email-service")
signal.AddListener(smsHandler, "sms-service")
signal.AddListener(pushHandler, "push-service")

fmt.Printf("Listeners: %d\n", signal.Len()) // Output: 3

// Remove specific listener
removed := signal.RemoveListener("sms-service")
fmt.Printf("Removed: %d, Remaining: %d\n", removed, signal.Len()) // Output: 1, 2

// Try to remove non-existent listener
result := signal.RemoveListener("non-existent")
fmt.Printf("Result: %d\n", result) // Output: -1 (not found)
```

**Returns:**
- Number of listeners remaining after removal
- `-1` if key not found

> Removal is **by key only**. There is no remove-by-position/index — the
> `int` returned by the `Add*` methods is a subscriber **count**, not an
> index handle. Register with a key if you need to remove later.

**Use Cases:**
- ✅ **Dynamic Configuration**: Enable/disable features at runtime
- ✅ **Plugin Management**: Add/remove plugins dynamically
- ✅ **A/B Testing**: Switch between different handlers
- ✅ **Resource Cleanup**: Remove listeners on service shutdown

### **`Emit(ctx context.Context, payload T)`**
Emits an event to all registered listeners. If `ctx` is already canceled, all listeners are skipped.

```go
// Basic emit
signal.Emit(context.Background(), userData)

// With timeout context
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
signal.Emit(ctx, timeoutSensitiveData)

// With cancellation
ctx, cancel := context.WithCancel(context.Background())
go func() {
    time.Sleep(1 * time.Second)
    cancel() // Cancel after 1 second
}()
signal.Emit(ctx, cancellableData)

// With custom context values
ctx = context.WithValue(context.Background(), "trace-id", "abc-123")
signal.Emit(ctx, tracedData)
```

**Behavior:**
- **AsyncSignal**: Fire-and-forget. Snapshots the listener set, spawns a dispatcher, and returns immediately. Each listener runs on its own goroutine; errors from `AddListenerWithErr` handlers are routed to `OnError` sinks. `Emit` does **not** wait for completion — use `TryEmit` if you need to wait.
- **SyncSignal**: Runs listeners sequentially in the calling goroutine (best-effort). An error-returning listener's error is routed to `OnError` sinks and the chain continues to the next listener.

### **`TryEmit(ctx context.Context, payload T) error`**
Emits an event and reports failures via the returned error. Available on **both** signal types, with different semantics:

- **SyncSignal** (transactional): runs listeners sequentially and **stops at the first listener error or canceled context**, returning that error. `nil` means every listener succeeded.
- **AsyncSignal** (concurrent, waits): runs all listeners concurrently, **waits for all of them**, and returns `errors.Join` of every failure in registration order (`nil` if all succeed). It is context-aware for caller liveness — it returns at the context deadline even if a handler hangs. Call it and ignore the result when you only need to wait for async completion.

```go
syncSignal := signals.NewSync[PaymentData]()

syncSignal.AddListenerWithErr(func(ctx context.Context, payment PaymentData) error {
    return validateCreditCard(payment.CardNumber)
})

syncSignal.AddListenerWithErr(func(ctx context.Context, payment PaymentData) error {
    return chargePayment(payment.Amount)
})

// Execute with error handling
if err := syncSignal.TryEmit(ctx, paymentData); err != nil {
    switch {
    case errors.Is(err, ErrInvalidCard):
        return fmt.Errorf("payment failed: invalid card")
    case errors.Is(err, ErrInsufficientFunds):
        return fmt.Errorf("payment failed: insufficient funds")
    case errors.Is(err, context.DeadlineExceeded):
        return fmt.Errorf("payment timeout")
    default:
        return fmt.Errorf("payment processing error: %w", err)
    }
}
```

**Returns:**
- `nil` if all listeners succeed
- **Sync**: the first error encountered (subsequent listeners are skipped)
- **Async**: `errors.Join` of all listener errors (all listeners are run)
- `context.DeadlineExceeded` if the context times out
- `context.Canceled` if the context is cancelled

#### **Error Types Reference**
```go
// Context-related errors
var (
    ErrDeadlineExceeded = context.DeadlineExceeded  // Operation timed out
    ErrCanceled         = context.Canceled         // Operation cancelled
)

// Common listener error patterns
type ValidationError struct {
    Field   string
    Message string
}

func (e ValidationError) Error() string {
    return fmt.Sprintf("validation failed for %s: %s", e.Field, e.Message)
}

// Example error handling
if err := syncSignal.TryEmit(ctx, data); err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        log.Warn("Operation timed out", "timeout", timeout)
    case errors.Is(err, context.Canceled):
        log.Info("Operation cancelled by user")
    case errors.As(err, &ValidationError{}):
        log.Error("Validation failed", "error", err)
    default:
        log.Error("Unexpected error", "error", err)
    }
}
```

### **`OnError(sink func(ctx context.Context, err error))`**
Registers an error sink for the **best-effort `Emit` path**, on both sync and async signals. When an `AddListenerWithErr` / `AddOnceWithErr` handler returns a non-nil error during `Emit`, that error is delivered to every registered sink. Multiple sinks may be added (they are additive).

```go
signal.OnError(func(ctx context.Context, err error) {
    metrics.IncErrorCount()
    log.Error("listener failed during Emit", "error", err)
})

// A failing listener's error now flows to the sink above instead of being lost.
signal.AddListenerWithErr(func(ctx context.Context, e Event) error {
    return process(e)
}, "processor")

signal.Emit(ctx, event) // errors routed to OnError sinks
```

**Important:**
- Errors on the **`TryEmit` path are RETURNED, not routed here.** `OnError` only applies to `Emit`.
- Sinks run **on the goroutine that invoked the failing listener** — the caller's goroutine for sync signals, the handler's goroutine for async signals. Keep them cheap and non-blocking.

### **`Keys() []string`**
Returns the caller-supplied keys of currently registered listeners. Internal/empty keys (unkeyed listeners) are omitted.

```go
signal.AddListener(h1, "email")
signal.AddListener(h2)            // unkeyed
signal.AddListener(h3, "sms")
fmt.Println(signal.Keys())        // ["email", "sms"] (order not guaranteed)
```

### **`HasKey(key string) bool`**
Reports whether a listener with the given key is registered. O(1).

```go
if !signal.HasKey("audit") {
    signal.AddListener(auditHandler, "audit")
}
```

### **`Reset()`**
Removes all listeners from the signal.

```go
signal := signals.New[string]()
signal.AddListener(handler1, "h1")
signal.AddListener(handler2, "h2")
signal.AddListener(handler3, "h3")

fmt.Printf("Before reset: %d listeners\n", signal.Len()) // Output: 3

signal.Reset()
fmt.Printf("After reset: %d listeners\n", signal.Len())  // Output: 0
fmt.Printf("Is empty: %v\n", signal.IsEmpty())          // Output: true
```

**Use Cases:**
- ✅ **Service Restart**: Clean slate for reinitialization
- ✅ **Memory Cleanup**: Release references for garbage collection
- ✅ **Testing**: Reset state between test cases
- ✅ **Configuration Changes**: Clear all listeners before reload

### **`Len() int`**
Returns the current number of registered listeners.

```go
signal := signals.New[int]()
fmt.Printf("Initial: %d\n", signal.Len()) // Output: 0

signal.AddListener(handler1)
signal.AddListener(handler2)
fmt.Printf("After adding: %d\n", signal.Len()) // Output: 2

signal.RemoveListener("handler1")
fmt.Printf("After removal: %d\n", signal.Len()) // Output: 1
```

### **`IsEmpty() bool`**
Returns true if no listeners are registered.

```go
signal := signals.New[string]()
fmt.Printf("Empty: %v\n", signal.IsEmpty()) // Output: true

signal.AddListener(handler)
fmt.Printf("Empty: %v\n", signal.IsEmpty()) // Output: false

signal.Reset()
fmt.Printf("Empty: %v\n", signal.IsEmpty()) // Output: true
```

---

## Package-Level Functions

### **`signals.SetPanicHandler(func(recovered any))`**
Process-global. Controls how a recovered **async** listener panic is reported. By default, panics are logged via the standard library `log` package. Passing `nil` discards them silently.

```go
signals.SetPanicHandler(func(recovered any) {
    metrics.IncPanicCount()
    log.Printf("async listener panicked: %v", recovered)
})
```

This affects async dispatch only — a panic in a synchronous `Emit`/`TryEmit` listener propagates up the calling goroutine as usual.

### **`signals.DefaultMaxConcurrent() int`**
Returns `2 × runtime.NumCPU()` — a suggested value for `SignalOptions.MaxConcurrent`. It is **not** applied automatically; pass it explicitly via options if you want bounded async dispatch.

```go
opts := &signals.SignalOptions{MaxConcurrent: signals.DefaultMaxConcurrent()}
signal := signals.NewWithOptions[Event](opts)
```

---

## Advanced Usage Patterns

### **Context Handling**
Proper context usage for timeouts, cancellation, and tracing:

```go
// Timeout pattern
func ProcessWithTimeout[T any](signal signals.Signal[T], data T, timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    if syncSig, ok := signal.(interface{ TryEmit(context.Context, T) error }); ok {
        return syncSig.TryEmit(ctx, data)
    }

    signal.Emit(ctx, data)
    return nil
}

// Cancellation pattern
func ProcessWithCancellation[T any](signal signals.Signal[T], data T, cancel <-chan struct{}) {
    ctx, ctxCancel := context.WithCancel(context.Background())
    defer ctxCancel()

    go func() {
        select {
        case <-cancel:
            ctxCancel()
        case <-ctx.Done():
        }
    }()

    signal.Emit(ctx, data)
}

// Tracing pattern
func ProcessWithTracing[T any](signal signals.Signal[T], data T, traceID string) {
    ctx := context.WithValue(context.Background(), "trace-id", traceID)
    ctx = context.WithValue(ctx, "start-time", time.Now())

    signal.Emit(ctx, data)
}
```

### **Error Handling Strategies**
Comprehensive error handling for production systems:

```go
// Custom error types
type ValidationError struct {
    Field   string
    Message string
}

func (e ValidationError) Error() string {
    return fmt.Sprintf("validation failed for %s: %s", e.Field, e.Message)
}

type BusinessLogicError struct {
    Code    string
    Details string
}

func (e BusinessLogicError) Error() string {
    return fmt.Sprintf("business logic error [%s]: %s", e.Code, e.Details)
}

// Error-aware workflow
func CreateOrderWorkflow(ctx context.Context, order Order) error {
    workflow := signals.NewSync[Order]()

    // Add validation steps with custom errors
    workflow.AddListenerWithErr(func(ctx context.Context, order Order) error {
        if order.CustomerID == "" {
            return ValidationError{Field: "CustomerID", Message: "required field missing"}
        }
        if order.Total <= 0 {
            return ValidationError{Field: "Total", Message: "must be positive"}
        }
        return nil
    }, "input-validation")

    workflow.AddListenerWithErr(func(ctx context.Context, order Order) error {
        available, err := inventory.CheckAvailability(order.Items)
        if err != nil {
            return fmt.Errorf("inventory check failed: %w", err)
        }
        if !available {
            return BusinessLogicError{Code: "OUT_OF_STOCK", Details: "insufficient inventory"}
        }
        return nil
    }, "inventory-validation")

    workflow.AddListenerWithErr(func(ctx context.Context, order Order) error {
        if err := payment.ValidateCard(order.PaymentInfo); err != nil {
            return fmt.Errorf("payment validation failed: %w", err)
        }
        return nil
    }, "payment-validation")

    // Execute with comprehensive error handling
    if err := workflow.TryEmit(ctx, order); err != nil {
        var validationErr ValidationError
        var businessErr BusinessLogicError

        switch {
        case errors.As(err, &validationErr):
            return fmt.Errorf("invalid order data: %w", err)
        case errors.As(err, &businessErr):
            return fmt.Errorf("business rule violation: %w", err)
        case errors.Is(err, context.DeadlineExceeded):
            return fmt.Errorf("order processing timeout: %w", err)
        case errors.Is(err, context.Canceled):
            return fmt.Errorf("order processing cancelled: %w", err)
        default:
            return fmt.Errorf("unexpected error during order processing: %w", err)
        }
    }

    return nil
}
```

### **Performance Optimization Patterns**
Optimize for high-throughput scenarios:

```go
// Pre-allocated signal for known capacity
func NewOptimizedEventBus(expectedListeners int) *EventBus {
    opts := &signals.SignalOptions{
        InitialCapacity: expectedListeners,
        MaxConcurrent:   runtime.NumCPU() * 2, // bound async fan-out
    }

    return &EventBus{
        UserEvents:  signals.NewWithOptions[UserEvent](opts),
        OrderEvents: signals.NewWithOptions[OrderEvent](opts),
        SystemLogs:  signals.NewWithOptions[LogEvent](opts),
    }
}

// Batch processing pattern
func BatchEmit[T any](signal signals.Signal[T], items []T, batchSize int) {
    for i := 0; i < len(items); i += batchSize {
        end := i + batchSize
        if end > len(items) {
            end = len(items)
        }

        batch := items[i:end]
        for _, item := range batch {
            signal.Emit(context.Background(), item)
        }

        // Optional: add backpressure control
        if len(batch) == batchSize {
            time.Sleep(1 * time.Millisecond) // Prevent overwhelming
        }
    }
}

// Context reuse pattern (avoid allocations)
func ProcessEvents[T any](signal signals.Signal[T], events []T) {
    ctx := context.Background() // Reuse same context

    for _, event := range events {
        signal.Emit(ctx, event) // No new context allocation
    }
}
```

### **Dynamic Listener Management**
Runtime listener management for flexible systems:

```go
type ListenerManager[T any] struct {
    signal    signals.Signal[T]
    listeners map[string]func(context.Context, T)
    mu        sync.RWMutex
}

func NewListenerManager[T any]() *ListenerManager[T] {
    return &ListenerManager[T]{
        signal:    signals.New[T](),
        listeners: make(map[string]func(context.Context, T)),
    }
}

func (lm *ListenerManager[T]) RegisterListener(key string, listener func(context.Context, T)) {
    lm.mu.Lock()
    defer lm.mu.Unlock()

    // Remove existing if present
    if _, exists := lm.listeners[key]; exists {
        lm.signal.RemoveListener(key)
    }

    // Add new listener
    lm.listeners[key] = listener
    lm.signal.AddListener(listener, key)
}

func (lm *ListenerManager[T]) UnregisterListener(key string) bool {
    lm.mu.Lock()
    defer lm.mu.Unlock()

    if _, exists := lm.listeners[key]; exists {
        delete(lm.listeners, key)
        return lm.signal.RemoveListener(key) > -1
    }
    return false
}

func (lm *ListenerManager[T]) Emit(ctx context.Context, data T) {
    lm.signal.Emit(ctx, data)
}

func (lm *ListenerManager[T]) ListActiveListeners() []string {
    lm.mu.RLock()
    defer lm.mu.RUnlock()

    keys := make([]string, 0, len(lm.listeners))
    for key := range lm.listeners {
        keys = append(keys, key)
    }
    return keys
}
```

---

## Metrics & Monitoring

The library does not ship a built-in metrics struct. Observability is achieved by wrapping a signal or by using `OnError` for error counting. The pattern below shows a Prometheus-backed wrapper.

> For error accounting on the best-effort `Emit` path, register an `OnError`
> sink and increment a counter there. For the `TryEmit` path, inspect the
> returned error directly.

### **Custom Instrumentation**
Add your own monitoring layer:

```go
type InstrumentedSignal[T any] struct {
    signal      signals.Signal[T]
    emitCounter prometheus.Counter
    errorCounter prometheus.Counter
    latencyHist  prometheus.Histogram
}

func NewInstrumentedSignal[T any](name string) *InstrumentedSignal[T] {
    return &InstrumentedSignal[T]{
        signal: signals.New[T](),
        emitCounter: prometheus.NewCounter(prometheus.CounterOpts{
            Name: fmt.Sprintf("%s_emits_total", name),
            Help: "Total number of signal emits",
        }),
        errorCounter: prometheus.NewCounter(prometheus.CounterOpts{
            Name: fmt.Sprintf("%s_errors_total", name),
            Help: "Total number of emit errors",
        }),
        latencyHist: prometheus.NewHistogram(prometheus.HistogramOpts{
            Name: fmt.Sprintf("%s_emit_duration_seconds", name),
            Help: "Emit duration in seconds",
        }),
    }
}

func (is *InstrumentedSignal[T]) Emit(ctx context.Context, data T) {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        is.emitCounter.Inc()
        is.latencyHist.Observe(duration.Seconds())
    }()

    is.signal.Emit(ctx, data)
}

func (is *InstrumentedSignal[T]) TryEmit(ctx context.Context, data T) error {
    start := time.Now()
    defer func() {
        duration := time.Since(start)
        is.emitCounter.Inc()
        is.latencyHist.Observe(duration.Seconds())
    }()

    // TryEmit is part of the Signal[T] interface on both sync and async signals.
    if err := is.signal.TryEmit(ctx, data); err != nil {
        is.errorCounter.Inc()
        return err
    }
    return nil
}
```

---

## Performance Benchmarks

All numbers below were measured on an **AMD Ryzen 7 5700G (Windows, `go test -count=6`)**. They are order-of-magnitude guides, not guarantees. Reproduce with:

```bash
go test -run '^$' -bench=. -benchmem -count=6 ./tests/
```

### **Sync signal — allocation-free read path**

| **Operation** | **Time** | **Memory** |
|---------------|----------|------------|
| `Emit`, 1 listener | ~9 ns/op | 0 B, 0 allocs |
| `Emit`, 10 listeners | ~39 ns/op | 0 B, 0 allocs |
| `Emit`, concurrent (16 threads) | ~1.3 ns/op | 0 B, 0 allocs |
| `TryEmit`, 1 listener | ~11 ns/op | 0 B, 0 allocs |
| `Emit` error → `OnError`, 1 listener | ~20 ns/op | 0 B, 0 allocs |

### **Async signal — `Emit` is DISPATCH rate, not completion**

Async `Emit` measures only the cost of snapshotting listeners and spawning goroutines; the listeners complete later. It is **not** allocation-free.

| **Operation** | **Time** | **Memory** |
|---------------|----------|------------|
| `Emit` dispatch, 1 listener | ~260 ns/op | 208 B, 2 allocs |
| `Emit` dispatch, 100 listeners | ~28 µs/op | ~11 KB, ~85 allocs |
| `Emit` dispatch, concurrent | ~475 ns/op | — |

### **Async signal — `TryEmit` waits for completion**

| **Operation** | **Time** | **Memory** |
|---------------|----------|------------|
| `TryEmit`, 10 listeners | ~6.5 µs/op | 1.5 KB, 12 allocs |
| `TryEmit`, bounded (`MaxConcurrent=4`), 10 listeners | ~9.2 µs/op | — |

> Bounded dispatch is **slower**, not faster — it is a safety valve that
> caps goroutine fan-out, not a performance optimization.

### **Write path (copy-on-write, O(n))**

| **Operation** | **Time** | **Memory** |
|---------------|----------|------------|
| Add/remove churn | ~30 µs/op | ~82 KB, 5 allocs |

Subscription mutations copy the listener slice (copy-on-write) so the read path can stay lock-free and allocation-free. Optimize for read throughput, not write throughput.

### **Benchmark Examples**

```go
func BenchmarkSignalEmit(b *testing.B) {
    signal := signals.New[int]()
    signal.AddListener(func(ctx context.Context, n int) {
        // Simulate work
        _ = n * 2
    })

    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        signal.Emit(ctx, i)
    }
}

func BenchmarkSyncSignalTryEmit(b *testing.B) {
    signal := signals.NewSync[int]()
    signal.AddListenerWithErr(func(ctx context.Context, n int) error {
        if n < 0 {
            return errors.New("negative value")
        }
        return nil
    })

    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        signal.TryEmit(ctx, i)
    }
}
```

---

## Production Examples

### **Complete E-Commerce Event System**

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/maniartech/signals"
)

// Event types
type OrderCreated struct {
    OrderID    string
    CustomerID string
    Total      float64
    Items      []OrderItem
}

type PaymentProcessed struct {
    OrderID   string
    Amount    float64
    Method    string
    Timestamp time.Time
}

type OrderShipped struct {
    OrderID      string
    TrackingCode string
    Carrier      string
}

// E-commerce platform
type ECommercePlatform struct {
    // Async signals for notifications
    orderEvents    signals.Signal[OrderCreated]
    shippingEvents signals.Signal[OrderShipped]

    // Sync signals for critical workflows
    paymentWorkflow signals.Signal[PaymentProcessed]

    // Services
    inventory   *InventoryService
    email       *EmailService
    analytics   *AnalyticsService
}

func NewECommercePlatform() *ECommercePlatform {
    platform := &ECommercePlatform{
        orderEvents:     signals.New[OrderCreated](),
        shippingEvents:  signals.New[OrderShipped](),
        paymentWorkflow: signals.NewSync[PaymentProcessed](),
        inventory:       NewInventoryService(),
        email:          NewEmailService(),
        analytics:      NewAnalyticsService(),
    }

    platform.setupEventHandlers()
    return platform
}

func (e *ECommercePlatform) setupEventHandlers() {
    // Order created handlers (async - fire and forget)
    e.orderEvents.AddListener(func(ctx context.Context, order OrderCreated) {
        e.email.SendOrderConfirmation(order.CustomerID, order.OrderID)
    }, "order-confirmation-email")

    e.orderEvents.AddListener(func(ctx context.Context, order OrderCreated) {
        e.analytics.TrackPurchase(order)
    }, "purchase-analytics")

    e.orderEvents.AddListener(func(ctx context.Context, order OrderCreated) {
        e.inventory.UpdateStockLevels(order.Items)
    }, "inventory-update")

    // Payment processing (sync - transaction critical)
    e.paymentWorkflow.AddListenerWithErr(func(ctx context.Context, payment PaymentProcessed) error {
        return e.validatePayment(payment)
    }, "payment-validation")

    e.paymentWorkflow.AddListenerWithErr(func(ctx context.Context, payment PaymentProcessed) error {
        return e.recordTransaction(payment)
    }, "transaction-recording")

    e.paymentWorkflow.AddListenerWithErr(func(ctx context.Context, payment PaymentProcessed) error {
        return e.updateOrderStatus(payment.OrderID, "paid")
    }, "order-status-update")

    // Shipping notifications (async)
    e.shippingEvents.AddListener(func(ctx context.Context, shipment OrderShipped) {
        e.email.SendShippingNotification(shipment)
    }, "shipping-email")

    e.shippingEvents.AddListener(func(ctx context.Context, shipment OrderShipped) {
        e.analytics.TrackShipment(shipment)
    }, "shipment-analytics")
}

// Public API methods
func (e *ECommercePlatform) CreateOrder(ctx context.Context, order OrderCreated) error {
    // Emit order created event (async)
    e.orderEvents.Emit(ctx, order)

    fmt.Printf("Order %s created for customer %s\n", order.OrderID, order.CustomerID)
    return nil
}

func (e *ECommercePlatform) ProcessPayment(ctx context.Context, payment PaymentProcessed) error {
    // Process payment synchronously with error handling
    if err := e.paymentWorkflow.TryEmit(ctx, payment); err != nil {
        return fmt.Errorf("payment processing failed: %w", err)
    }

    fmt.Printf("Payment processed for order %s\n", payment.OrderID)
    return nil
}

func (e *ECommercePlatform) ShipOrder(ctx context.Context, shipment OrderShipped) {
    // Emit shipping event (async)
    e.shippingEvents.Emit(ctx, shipment)

    fmt.Printf("Order %s shipped via %s\n", shipment.OrderID, shipment.Carrier)
}

// Helper methods (simplified for example)
func (e *ECommercePlatform) validatePayment(payment PaymentProcessed) error {
    if payment.Amount <= 0 {
        return errors.New("invalid payment amount")
    }
    return nil
}

func (e *ECommercePlatform) recordTransaction(payment PaymentProcessed) error {
    // Simulate database operation
    fmt.Printf("Recording transaction for order %s\n", payment.OrderID)
    return nil
}

func (e *ECommercePlatform) updateOrderStatus(orderID, status string) error {
    // Simulate database update
    fmt.Printf("Updating order %s status to %s\n", orderID, status)
    return nil
}

func main() {
    platform := NewECommercePlatform()
    ctx := context.Background()

    // Create an order
    order := OrderCreated{
        OrderID:    "order-123",
        CustomerID: "customer-456",
        Total:      99.99,
        Items:      []OrderItem{{ProductID: "product-1", Quantity: 2}},
    }

    platform.CreateOrder(ctx, order)

    // Process payment
    payment := PaymentProcessed{
        OrderID:   "order-123",
        Amount:    99.99,
        Method:    "credit_card",
        Timestamp: time.Now(),
    }

    if err := platform.ProcessPayment(ctx, payment); err != nil {
        log.Fatal("Payment failed:", err)
    }

    // Ship order
    shipment := OrderShipped{
        OrderID:      "order-123",
        TrackingCode: "TRACK123",
        Carrier:      "UPS",
    }

    platform.ShipOrder(ctx, shipment)

    time.Sleep(100 * time.Millisecond) // Allow async events to complete
}
```

---

## Migration Guide: v1.3 → v1.4

### **Removed / Renamed APIs**

The following methods were **removed in v1.4**. They were folded into the
methods listed in the right-hand column.

| **Removed in v1.4** | **Replacement** |
|---------------------|-----------------|
| `EmitAndWait(ctx, payload)` | `TryEmit(ctx, payload)` (ignore the returned error if you only need to wait) |
| `EmitAndWaitErr(ctx, payload) error` | `TryEmit(ctx, payload) error` |
| `AddOnceWithKey(handler, key)` | `AddOnce(handler, key)` — the key is now an optional variadic argument |

```go
// ❌ v1.3
async.EmitAndWait(ctx, payload)
err := async.EmitAndWaitErr(ctx, payload)
sig.AddOnceWithKey(handler, "key")

// ✅ v1.4
async.TryEmit(ctx, payload)        // call and ignore result to just wait
err := async.TryEmit(ctx, payload) // returns errors.Join of all failures (async)
sig.AddOnce(handler, "key")        // optional variadic key
```

### **Symmetric Surface (the big change)**

In v1.3, error-returning listeners (`AddListenerWithErr`) and `TryEmit` were
effectively a **sync-only** feature. In v1.4 the API surface is **symmetric**:
`AddListenerWithErr`, `AddOnceWithErr`, `TryEmit`, and `OnError` are all
available on **both** `SyncSignal[T]` and `AsyncSignal[T]`, and on the
`Signal[T]` interface. The behavior differs (sequential vs. concurrent;
returned vs. routed errors) but the methods exist on both.

```go
// ✅ v1.4 — error-returning listeners and TryEmit work on async too
async := signals.New[UserData]()
async.AddListenerWithErr(func(ctx context.Context, u UserData) error {
    return validateUser(u)
}, "validation")

// Async TryEmit runs all listeners concurrently, waits, and returns
// errors.Join of every failure (nil if all succeed).
if err := async.TryEmit(ctx, userData); err != nil {
    // Handle joined errors
}
```

### **New in v1.4**

- **`OnError(sink)`** on both signal types — error sink for the best-effort `Emit` path.
- **Bounded async dispatch** via `SignalOptions.MaxConcurrent` and the helper `signals.DefaultMaxConcurrent()` (a counting semaphore that parks excess handlers; it caps fan-out, it does not speed dispatch up).
- **`signals.SetPanicHandler(func(recovered any))`** — process-global reporting of recovered async listener panics.
- **`Keys()`** and **`HasKey(key)`** introspection helpers.

### **Migration Steps**

1. **Update the import / module version**
   ```bash
   go get github.com/maniartech/signals@v1.4.0
   ```

2. **Replace removed methods**
   ```go
   // EmitAndWait / EmitAndWaitErr  -> TryEmit
   // AddOnceWithKey                -> AddOnce(handler, key)
   ```

3. **Adopt the symmetric surface (optional)**
   ```go
   // Async error handling is now first-class: AddListenerWithErr + TryEmit,
   // or OnError for the fire-and-forget Emit path.
   ```

4. **Test Thoroughly**
   ```bash
   go test -race ./...  # Ensure no race conditions
   ```

---

## Troubleshooting

### **Common Issues & Solutions**

#### **"Race Condition Detected"**
```go
// ❌ Problem: Modifying listeners while emitting
go signal.Emit(ctx, data)
signal.AddListener(newHandler)  // Race condition!

// ✅ Solution: Use proper synchronization
var mu sync.Mutex
mu.Lock()
signal.AddListener(newHandler)
mu.Unlock()

// Or use keyed listeners for safe removal
signal.AddListener(handler, "my-key")
// Later, in a different goroutine:
signal.RemoveListener("my-key")  // Safe
```

#### **"Context Cancellation Not Working"**
```go
// ❌ Problem: Not checking context in long-running listeners
signal.AddListener(func(ctx context.Context, data Data) {
    for i := 0; i < 1000000; i++ {
        process(data)  // Doesn't respect cancellation
    }
})

// ✅ Solution: Check context regularly
signal.AddListener(func(ctx context.Context, data Data) {
    for i := 0; i < 1000000; i++ {
        select {
        case <-ctx.Done():
            return  // Respect cancellation
        default:
        }
        process(data)
    }
})
```

#### **"Memory Leak with Listeners"**
```go
// ❌ Problem: Not removing listeners
for i := 0; i < 1000; i++ {
    signal.AddListener(createHandler(i))  // Accumulates listeners
}

// ✅ Solution: Use keyed listeners and cleanup
for i := 0; i < 1000; i++ {
    key := fmt.Sprintf("handler-%d", i)
    signal.AddListener(createHandler(i), key)
}

// Cleanup when done
for i := 0; i < 1000; i++ {
    key := fmt.Sprintf("handler-%d", i)
    signal.RemoveListener(key)
}

// Or reset all at once
signal.Reset()
```

#### **"Poor Performance with Many Listeners"**
```go
// ❌ Problem: Default capacity too small
signal := signals.New[Event]()  // Starts with a small default capacity

// Add 1000 listeners (causes multiple reallocations)
for i := 0; i < 1000; i++ {
    signal.AddListener(handlers[i])
}

// ✅ Solution: Pre-allocate expected capacity
opts := &signals.SignalOptions{
    InitialCapacity: 1000,  // Avoid reallocations
}
signal := signals.NewWithOptions[Event](opts)

// Add 1000 listeners (single allocation)
for i := 0; i < 1000; i++ {
    signal.AddListener(handlers[i])
}
```

#### **"Deadlock with Sync Signals"**
```go
// ❌ Problem: Listener tries to emit on same signal
syncSignal := signals.NewSync[Event]()
syncSignal.AddListener(func(ctx context.Context, event Event) {
    syncSignal.Emit(ctx, event)  // Deadlock! Same goroutine
})

// ✅ Solution: Use async signal or separate goroutine
syncSignal.AddListener(func(ctx context.Context, event Event) {
    go asyncSignal.Emit(ctx, event)  // Safe - different signal
    // Or
    go func() {
        syncSignal.Emit(ctx, newEvent)  // Safe - different goroutine
    }()
})
```

### **Performance Debugging**

```go
// Enable pprof for performance analysis
import _ "net/http/pprof"

go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()

// Then profile your application:
// go tool pprof http://localhost:6060/debug/pprof/profile
```

### **Testing Patterns**

```go
// Test signal emission with timeout
func TestSignalEmission(t *testing.T) {
    signal := signals.NewSync[string]()

    var received string
    var wg sync.WaitGroup

    wg.Add(1)
    signal.AddListener(func(ctx context.Context, msg string) {
        received = msg
        wg.Done()
    })

    // Emit signal
    go signal.Emit(context.Background(), "test message")

    // Wait with timeout
    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        assert.Equal(t, "test message", received)
    case <-time.After(time.Second):
        t.Fatal("Signal emission timed out")
    }
}
```

---

## API Summary

Every method is available on **both** `SyncSignal[T]` and `AsyncSignal[T]`. Where behavior differs, it is noted.

| **Method** | **Behavior** | **Returns** |
|------------|--------------|-------------|
| **`AddListener`** | Add a listener (optional key) | `int` (count, or `-1` on duplicate key) |
| **`AddListenerWithErr`** | Add an error-returning listener | `int` (count, or `-1` on duplicate key) |
| **`AddOnce`** | Add a one-shot listener | `int` (count, or `-1` on duplicate key) |
| **`AddOnceWithErr`** | Add an error-returning one-shot listener | `int` (count, or `-1` on duplicate key) |
| **`RemoveListener`** | Remove by key | `int` (remaining count, or `-1` if not found) |
| **`Emit`** | Sync: run sequentially, best-effort. Async: dispatch and return immediately | — |
| **`TryEmit`** | Sync: stop at first error (transactional). Async: run all, wait, join errors | `error` |
| **`OnError`** | Register an error sink for the `Emit` path | — |
| **`Reset`** | Clear all listeners | — |
| **`Len`** | Count listeners | `int` |
| **`IsEmpty`** | Check if empty | `bool` |
| **`Keys`** | List caller-supplied keys | `[]string` |
| **`HasKey`** | Check key membership (O(1)) | `bool` |

---

## Related Documentation

| **Topic** | **Link** | **Focus** |
|-----------|----------|-----------|
| **Quick Start** | [Getting Started](getting_started.md) | Implementation guide |
| **Design Patterns** | [Concepts](concepts.md) | Advanced usage patterns |
| **Internal Architecture** | [Architecture](architecture.md) | Performance deep dive |
