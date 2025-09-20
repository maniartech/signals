# 🚀 Getting Started with Signals

> **From zero to production-ready event system in 5 minutes**

Welcome to the fastest, most reliable event system for Go! This guide will get you up and running with military-grade signal processing in no time.

## 📦 Installation

```bash
# Get the latest version (v1.3.0+)
go get github.com/maniartech/signals@latest

# Or specify a version
go get github.com/maniartech/signals@v1.3.0
```

### Requirements
- **Go 1.18+** (generics support required)
- **No external dependencies** 🎉

## ⚡ Quick Start (30 seconds)

### 1. **Hello World Example**
```go
package main

import (
    "context"
    "fmt"
    "github.com/maniartech/signals"
)

func main() {
    // Create a signal for string messages
    signal := signals.New[string]()

    // Add a listener
    signal.AddListener(func(ctx context.Context, msg string) {
        fmt.Println("📢 Received:", msg)
    })

    // Emit an event (async, non-blocking)
    signal.Emit(context.Background(), "Hello, Signals! 🎯")

    fmt.Println("✅ Event sent!")
}
```

**Run it:**
```bash
go run main.go
# Output:
# ✅ Event sent!
# 📢 Received: Hello, Signals! 🎯
```

## 🎯 Core Patterns

### **Pattern 1: Async Events (Fire & Forget)**
Perfect for notifications, logging, analytics

```go
// Create async signal
notifications := signals.New[UserEvent]()

// Add multiple listeners
notifications.AddListener(func(ctx context.Context, event UserEvent) {
    log.Info("User action", "user", event.UserID, "action", event.Action)
})

notifications.AddListener(func(ctx context.Context, event UserEvent) {
    analytics.Track(event.UserID, event.Action, event.Properties)
})

notifications.AddListener(func(ctx context.Context, event UserEvent) {
    metrics.Increment("user.actions", 1, []string{"action:" + event.Action})
})

// Fire and forget - all listeners run concurrently
go notifications.Emit(ctx, UserEvent{
    UserID: "user123",
    Action: "signup",
    Properties: map[string]string{"plan": "premium"},
})

fmt.Println("🚀 User signup processing started!")
```

### **Pattern 2: Sync Events with Error Handling**
Perfect for transactions, critical workflows, validation chains

```go
// Create sync signal for critical operations
workflow := signals.NewSync[PaymentRequest]()

// Add error-returning listeners
workflow.AddListenerWithErr(func(ctx context.Context, payment PaymentRequest) error {
    if payment.Amount <= 0 {
        return errors.New("invalid amount")
    }
    return nil // validation passed
})

workflow.AddListenerWithErr(func(ctx context.Context, payment PaymentRequest) error {
    return paymentGateway.Charge(payment.CardToken, payment.Amount)
})

workflow.AddListenerWithErr(func(ctx context.Context, payment PaymentRequest) error {
    return database.RecordTransaction(payment.UserID, payment.Amount)
})

// Execute with automatic error handling
payment := PaymentRequest{
    UserID: "user123",
    Amount: 99.99,
    CardToken: "tok_visa_4242",
}

if err := workflow.TryEmit(ctx, payment); err != nil {
    log.Error("Payment failed", "error", err)
    return // Automatic rollback - later listeners won't execute
}

fmt.Println("💳 Payment processed successfully!")
```

## 🛠️ Essential Data Types

Define your event structures for type safety:

```go
// User events
type UserEvent struct {
    UserID     string            `json:"user_id"`
    Action     string            `json:"action"`
    Timestamp  time.Time         `json:"timestamp"`
    Properties map[string]string `json:"properties"`
}

// Order events
type OrderEvent struct {
    OrderID    string    `json:"order_id"`
    UserID     string    `json:"user_id"`
    Total      float64   `json:"total"`
    Items      []Item    `json:"items"`
    Status     string    `json:"status"`
    CreatedAt  time.Time `json:"created_at"`
}

// System events
type SystemEvent struct {
    Service   string                 `json:"service"`
    Level     string                 `json:"level"` // info, warn, error
    Message   string                 `json:"message"`
    Metadata  map[string]interface{} `json:"metadata"`
    Timestamp time.Time              `json:"timestamp"`
}
```

## 🔧 Advanced Patterns

### **Cross-Package Event Coordination**
```go
// events/app_events.go - Central event definitions
package events

var (
    UserSignedUp = signals.New[UserSignupEvent]()
    UserLoggedIn = signals.New[UserLoginEvent]()
    DataChanged  = signals.New[DataChangeEvent]()
)

// auth/service.go - Authentication service
package auth

func init() {
    // React to user events from other packages
    events.UserLoggedIn.AddListener(func(ctx context.Context, event UserLoginEvent) {
        sessionStore.CreateSession(event.UserID, event.IP)
    }, "create-session")
}

func LoginUser(email, password string) error {
    user, err := validateCredentials(email, password)
    if err != nil {
        return err
    }

    // Notify other packages of successful login
    go events.UserLoggedIn.Emit(context.Background(), UserLoginEvent{
        UserID: user.ID,
        Email:  user.Email,
        IP:     getClientIP(),
    })

    return nil
}
```

### **Database Transaction Validation**
```go
// db/transaction.go - Transaction coordinator with validation
package db

var TransactionValidation = signals.NewSync[TransactionEvent]()

func init() {
    // Multiple packages can add transaction validators
    TransactionValidation.AddListenerWithErr(permissions.ValidateAccess, "permissions")
    TransactionValidation.AddListenerWithErr(audit.ValidateCompliance, "audit")
    TransactionValidation.AddListenerWithErr(business.ValidateRules, "business")
}

func ExecuteTransaction(ctx context.Context, tx Transaction) error {
    // Validate transaction across all registered validators
    if err := TransactionValidation.TryEmit(ctx, TransactionEvent{
        Operation: tx.Operation,
        UserID:    tx.UserID,
        Data:      tx.Data,
    }); err != nil {
        return fmt.Errorf("transaction validation failed: %w", err)
    }

    // All validators passed - execute transaction
    return tx.Execute()
}
```

### **Context Cancellation & Timeouts**
```go
// middleware/timeout.go - HTTP request timeout handling
package middleware

var RequestProcessing = signals.NewSync[RequestContext]()

func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Create timeout context
            ctx, cancel := context.WithTimeout(r.Context(), timeout)
            defer cancel()

            // Process request with timeout
            if err := RequestProcessing.TryEmit(ctx, RequestContext{
                Path:   r.URL.Path,
                Method: r.Method,
                UserID: getUserID(r),
            }); err != nil {
                if errors.Is(err, context.DeadlineExceeded) {
                    http.Error(w, "Request timeout", http.StatusRequestTimeout)
                    return
                }
                http.Error(w, "Internal error", http.StatusInternalServerError)
                return
            }

            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// Various packages listen with timeout awareness
func init() {
    RequestProcessing.AddListenerWithErr(func(ctx context.Context, req RequestContext) error {
        // Check for cancellation before expensive operations
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        return rateLimit.CheckLimit(req.UserID)  // Fast check
    }, "rate-limiter")

    RequestProcessing.AddListenerWithErr(func(ctx context.Context, req RequestContext) error {
        // Long-running validation with cancellation support
        return database.ValidateUserPermissions(ctx, req.UserID, req.Path)
    }, "permissions")
}
```

### **Keyed Listeners for Dynamic Management**
```go
signal := signals.New[MetricEvent]()

// Add listeners with keys for later removal
signal.AddListener(prometheusCollector, "prometheus")
signal.AddListener(datadogCollector, "datadog")
signal.AddListener(grafanaCollector, "grafana")

// Dynamically remove specific listeners
if !config.EnableDatadog {
    signal.RemoveListener("datadog")
}

// Add conditional listeners
if config.Environment == "production" {
    signal.AddListener(alertManager, "alerts")
}
```

### **High-Performance Request Processing**
```go
// main.go - Optimize for high request volume
package main

func init() {
    // Pre-allocate capacity for expected listeners
    opts := &signals.SignalOptions{
        InitialCapacity: 50,  // Expect ~50 middleware/handlers
    }

    middleware.RequestStarted = signals.NewWithOptions[RequestEvent](opts)
    middleware.RequestCompleted = signals.NewWithOptions[RequestEvent](opts)

    // Add all middleware listeners at startup
    middleware.RequestStarted.AddListener(logger.LogRequestStart, "logger")
    middleware.RequestStarted.AddListener(metrics.RecordRequestStart, "metrics")
    middleware.RequestStarted.AddListener(rateLimit.CheckRate, "rate-limiter")
    middleware.RequestStarted.AddListener(auth.ValidateToken, "auth")
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
    start := time.Now()

    // Fast async processing for non-critical middleware
    go middleware.RequestStarted.Emit(r.Context(), RequestEvent{
        Path:      r.URL.Path,
        Method:    r.Method,
        UserAgent: r.UserAgent(),
        IP:        getClientIP(r),
    })

    // Handle actual request
    processRequest(w, r)

    // Fast async completion processing
    go middleware.RequestCompleted.Emit(r.Context(), RequestEvent{
        Path:     r.URL.Path,
        Duration: time.Since(start),
        Status:   getResponseStatus(w),
    })
}
```

## 📊 Performance Tips

### **✅ Do's:**
```go
// ✅ Use async for non-critical events
notifications.Emit(ctx, event)

// ✅ Use sync for critical workflows
if err := workflow.TryEmit(ctx, event); err != nil { /* handle */ }

// ✅ Pre-allocate for high throughput
opts := &signals.SignalOptions{InitialCapacity: 100}

// ✅ Use keyed listeners for dynamic management
signal.AddListener(handler, "module-name")
```

### **❌ Don'ts:**
```go
// ❌ Don't use sync signals for fire-and-forget
workflow.Emit(ctx, notification) // Use async instead

// ❌ Don't ignore TryEmit errors
workflow.TryEmit(ctx, event) // Always check errors

// ❌ Don't create signals in hot paths
func handleRequest() {
    sig := signals.New[Event]() // Move to package level
}
```

## 🧪 Testing Your Signals

```go
func TestUserSignup(t *testing.T) {
    signal := signals.NewSync[UserEvent]()

    var capturedEvent UserEvent
    signal.AddListener(func(ctx context.Context, event UserEvent) {
        capturedEvent = event
    })

    // Test emission
    testEvent := UserEvent{UserID: "test123", Action: "signup"}
    signal.Emit(context.Background(), testEvent)

    // Verify
    assert.Equal(t, "test123", capturedEvent.UserID)
    assert.Equal(t, "signup", capturedEvent.Action)
}

func TestErrorHandling(t *testing.T) {
    signal := signals.NewSync[PaymentEvent]()

    signal.AddListenerWithErr(func(ctx context.Context, payment PaymentEvent) error {
        if payment.Amount < 0 {
            return errors.New("negative amount")
        }
        return nil
    })

    // Test error case
    err := signal.TryEmit(context.Background(), PaymentEvent{Amount: -10})
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "negative amount")
}
```

## 🏁 Next Steps

**You're ready to build production-grade event systems!** 🎉

### **Choose your learning path:**

| **I want to...** | **Go to** | **Time** |
|-------------------|-----------|----------|
| **Understand sync vs async patterns** | [Core Concepts](concepts.md) | 10 min |
| **See real-world examples** | [Use Cases & Examples](concepts.md#real-world-examples) | 15 min |
| **Learn architecture & internals** | [Architecture Guide](architecture.md) | 20 min |
| **Browse all available methods** | [API Reference](api_reference.md) | Reference |

### **Common Next Steps:**

1. **[📖 Read Core Concepts](concepts.md)** - Understanding when to use sync vs async
2. **[🏗️ Explore Architecture](architecture.md)** - How signals achieve sub-10ns performance
3. **[🔧 Check Real Examples](concepts.md#production-examples)** - Battle-tested patterns from production systems
4. **[📚 API Reference](api_reference.md)** - Complete method documentation

---

**Happy eventing! 🚀** Questions? Check our [examples](../example/) or [open an issue](https://github.com/maniartech/signals/issues).
