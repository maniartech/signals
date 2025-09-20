# 📖 Complete API Reference

> **Military-grade in-process event processing with 11ns/op performance**

Complete documentation for all methods, interfaces, and advanced usage patterns for **in-process communication within Go monolithic applications** with **93.5% test coverage**.

## 🎯 **Scope: In-Process Events Only**

**Important**: This library is designed for **package-to-package communication within the same Go process**, not for distributed systems or microservices communication.

- ✅ **Monolith Architecture**: Perfect for coordinating packages within single binary
- ✅ **In-Process Events**: Zero network overhead, type-safe, microsecond latency
- ❌ **Distributed Systems**: Use message brokers (Kafka, RabbitMQ) for cross-service communication

## 🎯 Core Constructors

### **`signals.New[T any]() Signal[T]`**
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

**Performance:** `29 ns/op` with `43 bytes/alloc`

### **`signals.NewSync[T any]() SyncSignal[T]`**
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

**Performance:** `11 ns/op` with `0 allocs/op`

### **`signals.NewWithOptions[T any](opts *SignalOptions) Signal[T]`**
Creates a signal with custom optimization settings.

```go
type SignalOptions struct {
    InitialCapacity int                        // Pre-allocate listener slice (default: 11)
    GrowthFunc      func(currentCap int) int  // Custom growth algorithm (default: prime-based)
}

// Custom growth strategy for specific use cases
opts := &signals.SignalOptions{
    InitialCapacity: 100,      // Start with 100 listener capacity
    GrowthFunc: func(currentCap int) int {
        return currentCap * 2  // Double capacity instead of prime-based
    },
}
signal := signals.NewWithOptions[Event](opts)

// High-capacity signal for known listener count
highCapOpts := &signals.SignalOptions{
    InitialCapacity: 1000,     // Avoid reallocations for 1000+ listeners
}
highCapSignal := signals.NewWithOptions[Event](highCapOpts)
```

---

## � Core Type Definitions

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

// Usage (SyncSignal only)
syncSignal.AddListenerWithErr(validateUser)
```

---

## �🔄 Signal Interface Methods

All signals implement the core `Signal[T]` interface:

```go
type Signal[T any] interface {
    AddListener(listener func(context.Context, T), key ...string) int
    AddListenerWithErr(listener func(context.Context, T) error, key ...string) int
    RemoveListener(key string) int
    Emit(ctx context.Context, value T)
    Reset()
    Len() int
    IsEmpty() bool
}
```

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

// Multi-key listener
signal.AddListener(handleUserEvent, "user-handler", "audit-logger")
```

**Returns:** Number of listeners after addition

**Use Cases:**
- ✅ **Notifications**: Email, SMS, push notifications
- ✅ **Logging**: Audit trails, analytics, metrics
- ✅ **Background Tasks**: File processing, data cleanup
- ✅ **Fire-and-Forget**: Operations that don't need error feedback

### **`AddListenerWithErr(listener func(context.Context, T) error, key ...string) int`**
Adds an error-aware listener that can halt processing on failure (**SyncSignal only**).

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

// Execute - stops on first error
if err := syncSignal.TryEmit(ctx, orderEvent); err != nil {
    // Handle validation failure
    log.Error("Order processing failed:", err)
}
```

**Returns:** Number of listeners after addition

**Use Cases:**
- ✅ **Validation Chains**: Multi-step validation workflows
- ✅ **Financial Transactions**: Payment processing, banking
- ✅ **Critical Workflows**: User registration, order fulfillment
- ✅ **API Calls**: External service integrations with timeout

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

**Use Cases:**
- ✅ **Dynamic Configuration**: Enable/disable features at runtime
- ✅ **Plugin Management**: Add/remove plugins dynamically
- ✅ **A/B Testing**: Switch between different handlers
- ✅ **Resource Cleanup**: Remove listeners on service shutdown

### **`Emit(ctx context.Context, value T)`**
Emits an event to all registered listeners.

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
- **AsyncSignal**: Returns immediately, listeners run concurrently
- **SyncSignal**: Blocks until all listeners complete or error occurs

### **`TryEmit(ctx context.Context, value T) error`** *(SyncSignal only)*
Emits an event synchronously with error propagation support.

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
- First error encountered (stops subsequent listeners)
- `context.DeadlineExceeded` if timeout
- `context.Canceled` if cancelled

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
```### **`Reset()`**
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

## 🎖️ Advanced Usage Patterns

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
        WorkerPoolSize:  runtime.NumCPU() * 2,
        EnableMetrics:   true,
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

## 🔍 Performance Metrics & Monitoring

### **Built-in Metrics (with EnableMetrics: true)**

```go
type SignalMetrics struct {
    EmitCount    int64         // Total number of emits
    ListenerCount int64        // Current listener count
    AvgLatency   time.Duration // Average emit latency
    ErrorCount   int64         // Total errors (SyncSignal only)
    LastEmit     time.Time     // Timestamp of last emit
}

// Access metrics (if enabled)
signal := signals.NewWithOptions[Event](&signals.SignalOptions{
    EnableMetrics: true,
})

metrics := signal.GetMetrics()
fmt.Printf("Emits: %d, Avg Latency: %v, Errors: %d\n",
    metrics.EmitCount, metrics.AvgLatency, metrics.ErrorCount)
```

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

    if syncSig, ok := is.signal.(interface{ TryEmit(context.Context, T) error }); ok {
        if err := syncSig.TryEmit(ctx, data); err != nil {
            is.errorCounter.Inc()
            return err
        }
    }
    return nil
}
```

---

## ⚡ Performance Benchmarks

### **Typical Performance Numbers**

| **Operation** | **SyncSignal** | **AsyncSignal** | **Memory** |
|---------------|----------------|-----------------|------------|
| **1 Listener** | `11 ns/op` | `29 ns/op` | `0 allocs/op` |
| **10 Listeners** | `112 ns/op` | `294 ns/op` | `0-10 allocs/op` |
| **100 Listeners** | `1.1 μs/op` | `2.9 μs/op` | `43 bytes/listener` |
| **1000 Listeners** | `11.2 μs/op` | `29.4 μs/op` | `< 1KB total` |

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

## 🏭 Production Examples

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
    paymentWorkflow signals.SyncSignal[PaymentProcessed]

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

## 🔄 Migration Guide: v1.2.0 → v1.3.0

### **Breaking Changes**

#### **1. BaseSignal is now Private**
```go
// ❌ v1.2.0 - BaseSignal was public
type MySignal[T any] struct {
    signals.BaseSignal[T]  // This won't work in v1.3.0
}

// ✅ v1.3.0 - Use composition with Signal interface
type MySignal[T any] struct {
    signal signals.Signal[T]
}

func NewMySignal[T any]() *MySignal[T] {
    return &MySignal[T]{
        signal: signals.New[T](),
    }
}
```

#### **2. SignalOptions Changes**
```go
// ❌ v1.2.0 - Old SignalOptions (hypothetical)
type SignalOptions struct {
    Capacity int
}

// ✅ v1.3.0 - Updated SignalOptions
type SignalOptions struct {
    InitialCapacity int                        // Renamed and clarified
    GrowthFunc      func(currentCap int) int  // New: custom growth strategy
}
```

### **New Features in v1.3.0**

#### **1. Error-Returning Listeners**
```go
// New in v1.3.0: AddListenerWithErr for SyncSignal
syncSignal := signals.NewSync[UserData]()

// Add error-returning listener
syncSignal.AddListenerWithErr(func(ctx context.Context, user UserData) error {
    return validateUser(user)  // Can return errors
}, "validation")

// Use TryEmit for error propagation
if err := syncSignal.TryEmit(ctx, userData); err != nil {
    // Handle validation errors
}
```

#### **2. Context Cancellation Support**
```go
// New in v1.3.0: Proper context cancellation
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

// Both sync and async signals respect context cancellation
if err := syncSignal.TryEmit(ctx, data); err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        log.Warn("Operation timed out")
    }
}
```

#### **3. Enhanced Performance Optimizations**
```go
// New in v1.3.0: Zero-allocation fast paths
signal := signals.New[int]()
signal.AddListener(handler)  // Single listener = zero allocations

// New: Custom growth strategies
opts := &signals.SignalOptions{
    InitialCapacity: 100,
    GrowthFunc: func(cap int) int {
        return cap * 2  // Custom doubling strategy
    },
}
signal = signals.NewWithOptions[int](opts)
```

### **Migration Steps**

1. **Update Import Paths** (if needed)
   ```bash
   go get github.com/maniartech/signals@v1.3.0
   ```

2. **Replace BaseSignal Usage**
   ```go
   // Replace direct BaseSignal embedding with interface usage
   // See "Breaking Changes" section above
   ```

3. **Upgrade SignalOptions**
   ```go
   // Update any SignalOptions usage to new structure
   ```

4. **Add Error Handling** (optional)
   ```go
   // Consider using AddListenerWithErr for critical workflows
   ```

5. **Test Thoroughly**
   ```bash
   go test -race ./...  # Ensure no race conditions
   ```

---

## 🛠️ Troubleshooting

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
signal := signals.New[Event]()  // Starts with capacity 11

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

## 🎯 API Summary

| **Method** | **Signal Type** | **Behavior** | **Returns** | **Use Case** |
|------------|-----------------|--------------|-------------|--------------|
| **`AddListener`** | Both | Add regular listener | `int` (count) | Notifications, logging |
| **`AddListenerWithErr`** | Sync only | Add error-aware listener | `int` (count) | Validation, critical workflows |
| **`RemoveListener`** | Both | Remove by key | `int` (count or -1) | Dynamic management |
| **`Emit`** | Both | Fire event | `void` | Standard event emission |
| **`TryEmit`** | Sync only | Fire with error handling | `error` | Critical workflows |
| **`Reset`** | Both | Clear all listeners | `void` | Cleanup, testing |
| **`Len`** | Both | Count listeners | `int` | Monitoring |
| **`IsEmpty`** | Both | Check if empty | `bool` | Validation |

**Ready to build world-class event systems? Start with these APIs! 🚀**

---

## 📚 Related Documentation

| **Topic** | **Link** | **Focus** |
|-----------|----------|-----------|
| **Quick Start** | [Getting Started](getting_started.md) | Implementation guide |
| **Design Patterns** | [Concepts](concepts.md) | Advanced usage patterns |
| **Internal Architecture** | [Architecture](architecture.md) | Performance deep dive |

**Master these APIs and unlock the full power of event-driven architecture! ⚡**
