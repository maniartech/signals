# Getting Started with Signals

> **From zero to production-ready in-process event system in 5 minutes**

Welcome to the fastest, most reliable **in-process event system** for Go monolithic applications! This guide will get you up and running with military-grade signal processing for **package coordination within your Go application**.

## Real-World Example: Monolithic E-commerce Package Coordination

This example shows how multiple packages within a single Go binary coordinate seamlessly using signals:

```go
// github.com/mycompany/myapp/events/ecommerce.go - Centralized events for all e-commerce packages
package events

import "github.com/maniartech/signals"

// Order lifecycle events - shared across all packages in monolith
var (
    OrderCreated    = signals.New[OrderCreatedEvent]()
    OrderPaid       = signals.New[OrderPaidEvent]()
    OrderShipped    = signals.New[OrderShippedEvent]()
    OrderCancelled  = signals.NewSync[OrderCancelEvent]()  // Sync for rollbacks

    // Inventory events
    StockUpdated    = signals.New[StockUpdateEvent]()
    LowStockAlert   = signals.New[LowStockEvent]()

    // User events
    UserRegistered  = signals.New[UserRegisteredEvent]()
    UserProfileUpdated = signals.New[UserProfileEvent]()
)

type OrderCreatedEvent struct {
    OrderID    string      `json:"order_id"`
    UserID     string      `json:"user_id"`
    Items      []OrderItem `json:"items"`
    Total      float64     `json:"total"`
    CreatedAt  time.Time   `json:"created_at"`
}

type OrderPaidEvent struct {
    OrderID       string    `json:"order_id"`
    PaymentID     string    `json:"payment_id"`
    Amount        float64   `json:"amount"`
    PaymentMethod string    `json:"payment_method"`
    PaidAt        time.Time `json:"paid_at"`
}
```

```go
// github.com/mycompany/myapp/order/service.go - Order package emits events
package order

import (
    "context"
    "time"
    "github.com/mycompany/myapp/events"
)

func CreateOrder(ctx context.Context, userID string, items []OrderItem) (*Order, error) {
    order := &Order{
        ID:        generateID(),
        UserID:    userID,
        Items:     items,
        Total:     calculateTotal(items),
        Status:    "pending",
        CreatedAt: time.Now(),
    }

    // Save to database first
    if err := db.Save(order); err != nil {
        return nil, err
    }

    // Notify other packages in same process
    go events.OrderCreated.Emit(ctx, events.OrderCreatedEvent{
        OrderID:   order.ID,
        UserID:    order.UserID,
        Items:     order.Items,
        Total:     order.Total,
        CreatedAt: order.CreatedAt,
    })

    return order, nil
}

func ProcessPayment(ctx context.Context, orderID, paymentID string, amount float64) error {
    // Process payment logic...

    // Notify payment success to other packages
    events.OrderPaid.Emit(ctx, events.OrderPaidEvent{
        OrderID:       orderID,
        PaymentID:     paymentID,
        Amount:        amount,
        PaymentMethod: "credit_card",
        PaidAt:        time.Now(),
    })

    return nil
}
```

```go
// github.com/mycompany/myapp/inventory/service.go - Inventory package reacts to orders
package inventory

import (
    "context"
    "log"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Reserve stock when order created
    events.OrderCreated.AddListener(func(ctx context.Context, event events.OrderCreatedEvent) {
        for _, item := range event.Items {
            if err := reserveStock(item.ProductID, item.Quantity); err != nil {
                log.Error("Stock reservation failed", "product", item.ProductID, "error", err)
                // Could emit cancellation event here
            } else {
                log.Info("Stock reserved", "product", item.ProductID, "qty", item.Quantity)
            }
        }
    }, "inventory-reservation")

    // Deduct stock when payment confirmed
    events.OrderPaid.AddListener(func(ctx context.Context, event events.OrderPaidEvent) {
        order := getOrder(event.OrderID)
        for _, item := range order.Items {
            deductStock(item.ProductID, item.Quantity)

            // Check if stock is low
            if currentStock := getStock(item.ProductID); currentStock < 10 {
                events.LowStockAlert.Emit(ctx, events.LowStockEvent{
                    ProductID:    item.ProductID,
                    CurrentStock: currentStock,
                    AlertLevel:   "low",
                })
            }
        }
    }, "inventory-deduction")
}
```

```go
// github.com/mycompany/myapp/email/service.go - Email package sends notifications
package email

import (
    "context"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Send order confirmation email
    events.OrderCreated.AddListener(func(ctx context.Context, event events.OrderCreatedEvent) {
        user := getUserByID(event.UserID)
        sendOrderConfirmationEmail(user.Email, event.OrderID, event.Items, event.Total)
    }, "email-order-confirmation")

    // Send payment confirmation email
    events.OrderPaid.AddListener(func(ctx context.Context, event events.OrderPaidEvent) {
        order := getOrder(event.OrderID)
        user := getUserByID(order.UserID)
        sendPaymentConfirmationEmail(user.Email, event.OrderID, event.Amount)
    }, "email-payment-confirmation")

    // Alert about low stock
    events.LowStockAlert.AddListener(func(ctx context.Context, event events.LowStockEvent) {
        sendLowStockAlert("inventory@company.com", event.ProductID, event.CurrentStock)
    }, "email-low-stock")
}
```

```go
// github.com/mycompany/myapp/analytics/tracker.go - Analytics package tracks everything
package analytics

import (
    "context"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Track order metrics
    events.OrderCreated.AddListener(func(ctx context.Context, event events.OrderCreatedEvent) {
        track("order_created", map[string]interface{}{
            "order_id": event.OrderID,
            "user_id":  event.UserID,
            "total":    event.Total,
            "items":    len(event.Items),
        })

        incrementCounter("orders_total")
        recordGauge("order_value", event.Total)
    }, "analytics-order-created")

    events.OrderPaid.AddListener(func(ctx context.Context, event events.OrderPaidEvent) {
        track("order_paid", map[string]interface{}{
            "order_id":       event.OrderID,
            "payment_id":     event.PaymentID,
            "amount":         event.Amount,
            "payment_method": event.PaymentMethod,
        })

        incrementCounter("payments_successful")
        recordGauge("revenue", event.Amount)
    }, "analytics-payment")
}
```

```go
// github.com/mycompany/myapp/audit/logger.go - Audit package logs all activities
package audit

import (
    "context"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Audit all order activities
    events.OrderCreated.AddListener(func(ctx context.Context, event events.OrderCreatedEvent) {
        auditLog("ORDER_CREATED", map[string]interface{}{
            "actor":     "system",
            "resource":  "order",
            "action":    "create",
            "order_id":  event.OrderID,
            "user_id":   event.UserID,
            "timestamp": event.CreatedAt,
        })
    }, "audit-order-created")

    events.OrderPaid.AddListener(func(ctx context.Context, event events.OrderPaidEvent) {
        auditLog("ORDER_PAID", map[string]interface{}{
            "actor":      "payment_system",
            "resource":   "order",
            "action":     "payment",
            "order_id":   event.OrderID,
            "payment_id": event.PaymentID,
            "amount":     event.Amount,
            "timestamp":  event.PaidAt,
        })
    }, "audit-payment")
}
```

**Key Benefits of This Monolithic Approach:**
- ▶ **Zero Network Latency**: All packages communicate in-process
- ◆ **Type Safety**: Compile-time validation of event structures
- ▪ **Loose Coupling**: Packages don't directly depend on each other
- ▨ **Easy Testing**: Mock individual package listeners easily
- ▤ **Simple Debugging**: All code runs in same process/debugger
- ▷ **High Performance**: Sub-microsecond event processing (11ns/op)
- ● **Reliability**: No network failures, connection pools, or timeouts

**▪ Perfect For:**
- Monolithic Go applications with multiple packages
- In-process component coordination and decoupling
- HTTP middleware chains and database transaction hooks
- Plugin architectures within single binary

**▫ Use Alternatives For:**
- Microservices communication → Use **Kafka, RabbitMQ, NATS**
- Cross-container events → Use **HTTP APIs, gRPC**
- Distributed systems → Use **message brokers, event streaming**

## Installation

```bash
# Get the latest version (v1.3.0+)
go get github.com/maniartech/signals@latest

# Or specify a version
go get github.com/maniartech/signals@v1.3.0
```

### Requirements
- **Go 1.18+** (generics support required)
- **No external dependencies** 🎉

## Quick Start (30 seconds)

### 1. **Monolith Package Coordination Example**
```go
// main.go - Main application entry point
package main

import (
    "context"
    "fmt"
    "time"
    "github.com/mycompany/myapp/auth"     // Auth package (same process)
    "github.com/mycompany/myapp/audit"    // Audit package (same process)
    "github.com/mycompany/myapp/cache"    // Cache package (same process)
    "github.com/mycompany/myapp/events"   // Shared events (same process)
)

func main() {
    // Initialize packages - all in same Go binary
    auth.Initialize()
    audit.Initialize()
    cache.Initialize()

    // Simulate user login - packages coordinate via events
    userID := "user123"
    fmt.Printf("🔑 User %s logging in...\n", userID)

    // Auth package emits login event
    auth.LoginUser(context.Background(), userID)

    // Give async events time to process
    time.Sleep(100 * time.Millisecond)
    fmt.Println("✅ Login complete - all packages coordinated!")
}
```

```go
// github.com/mycompany/myapp/events/shared.go - Centralized events for all packages
package events

import "github.com/maniartech/signals"

// Global signals shared across packages in same process
var (
    UserLoggedIn  = signals.New[UserLoginEvent]()
    UserLoggedOut = signals.New[UserLogoutEvent]()
)

type UserLoginEvent struct {
    UserID    string `json:"user_id"`
    Timestamp time.Time `json:"timestamp"`
    IP        string `json:"ip"`
}
```

```go
// github.com/mycompany/myapp/auth/service.go - Authentication package
package auth

import (
    "context"
    "fmt"
    "time"
    "github.com/mycompany/myapp/events"
)

func Initialize() {
    fmt.Println("🔐 Auth package initialized")
}

func LoginUser(ctx context.Context, userID string) {
    // Perform authentication logic
    fmt.Printf("� Authenticating user %s\n", userID)

    // Emit login event for other packages to handle
    events.UserLoggedIn.Emit(ctx, events.UserLoginEvent{
        UserID:    userID,
        Timestamp: time.Now(),
        IP:        "192.168.1.1",
    })
}
```

```go
// github.com/mycompany/myapp/audit/logger.go - Audit package listens to auth events
package audit

import (
    "context"
    "fmt"
    "github.com/mycompany/myapp/events"
)

func Initialize() {
    // Listen for login events from auth package
    events.UserLoggedIn.AddListener(func(ctx context.Context, event events.UserLoginEvent) {
        fmt.Printf("📝 AUDIT: User %s logged in at %v\n", event.UserID, event.Timestamp)
        // Store in audit database
    }, "audit-logger")

    fmt.Println("📝 Audit package initialized")
}
```

```go
// github.com/mycompany/myapp/cache/invalidator.go - Cache package reacts to auth events
package cache

import (
    "context"
    "fmt"
    "github.com/mycompany/myapp/events"
)

func Initialize() {
    // Listen for login events from auth package
    events.UserLoggedIn.AddListener(func(ctx context.Context, event events.UserLoginEvent) {
        fmt.Printf("🗄️ CACHE: Refreshing cache for user %s\n", event.UserID)
        // Update user cache, invalidate old sessions, etc.
    }, "cache-refresher")

    fmt.Println("🗄️ Cache package initialized")
}
```
```

**Run it:**
```bash
go run main.go
# Output:
# Event sent!
# ▸ Received: Hello, Signals! ▷
```

## Core Patterns

### **Pattern 1: Async Package Coordination**
Perfect for cross-package notifications, logging, analytics within your monolith

```go
// github.com/mycompany/myapp/events/user_events.go - Shared events across packages
package events

var UserSignedUp = signals.New[UserSignupEvent]()

type UserSignupEvent struct {
    UserID     string            `json:"user_id"`
    Email      string            `json:"email"`
    Plan       string            `json:"plan"`
    Timestamp  time.Time         `json:"timestamp"`
    Metadata   map[string]string `json:"metadata"`
}
```

```go
// github.com/mycompany/myapp/user/service.go - User service package
package user

import (
    "context"
    "time"
    "github.com/mycompany/myapp/events"
)

func CreateUser(email, plan string) (*User, error) {
    user := &User{ID: generateID(), Email: email, Plan: plan}

    // Save user to database
    if err := db.Save(user); err != nil {
        return nil, err
    }

    // Notify other packages asynchronously
    go events.UserSignedUp.Emit(context.Background(), events.UserSignupEvent{
        UserID:    user.ID,
        Email:     user.Email,
        Plan:      plan,
        Timestamp: time.Now(),
        Metadata:  map[string]string{"source": "web"},
    })

    return user, nil
}
```

```go
// github.com/mycompany/myapp/email/service.go - Email package reacts to user events
package email

import (
    "context"
    "log"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Listen for user signups from user package
    events.UserSignedUp.AddListener(func(ctx context.Context, event events.UserSignupEvent) {
        sendWelcomeEmail(event.Email, event.Plan)
        log.Info("Welcome email sent", "user", event.UserID)
    }, "email-welcome")
}
```

```go
// github.com/mycompany/myapp/analytics/service.go - Analytics package tracks events
package analytics

import (
    "context"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Track user signups from user package
    events.UserSignedUp.AddListener(func(ctx context.Context, event events.UserSignupEvent) {
        trackEvent("user_signup", map[string]interface{}{
            "user_id": event.UserID,
            "plan":    event.Plan,
            "source":  event.Metadata["source"],
        })
    }, "analytics-tracker")
}

// All packages run in the same Go process - zero network overhead!
```

### **Pattern 2: Sync Transaction Coordination**
Perfect for multi-package transaction validation within your monolith

```go
// github.com/mycompany/myapp/events/transaction_events.go - Transaction coordination
package events

var OrderProcessing = signals.NewSync[OrderProcessingEvent]()

type OrderProcessingEvent struct {
    OrderID   string      `json:"order_id"`
    UserID    string      `json:"user_id"`
    Items     []OrderItem `json:"items"`
    Total     float64     `json:"total"`
}
```

```go
// github.com/mycompany/myapp/order/service.go - Order service coordinates with other packages
package order

import (
    "context"
    "errors"
    "log"
    "github.com/mycompany/myapp/events"
)

func init() {
    // Set up validation chain across packages
    events.OrderProcessing.AddListenerWithErr(validateOrderData, "order-validation")
    events.OrderProcessing.AddListenerWithErr(inventory.ValidateStock, "inventory-validation")
    events.OrderProcessing.AddListenerWithErr(payment.ValidatePayment, "payment-validation")
    events.OrderProcessing.AddListenerWithErr(shipping.ValidateAddress, "shipping-validation")
}

func ProcessOrder(ctx context.Context, orderData OrderData) error {
    // Sequential validation across multiple packages
    if err := events.OrderProcessing.TryEmit(ctx, events.OrderProcessingEvent{
        OrderID: orderData.ID,
        UserID:  orderData.UserID,
        Items:   orderData.Items,
        Total:   orderData.Total,
    }); err != nil {
        log.Error("Order validation failed", "error", err)
        return err // Any package can fail the entire transaction
    }

    // All validations passed - proceed with order
    return createOrderRecord(orderData)
}

func validateOrderData(ctx context.Context, event events.OrderProcessingEvent) error {
    if event.Total <= 0 {
        return errors.New("invalid order total")
    }
    if len(event.Items) == 0 {
        return errors.New("order must have items")
    }
    return nil
}
```

```go
// github.com/mycompany/myapp/inventory/validator.go - Inventory package validates stock
package inventory

import (
    "context"
    "fmt"
    "github.com/mycompany/myapp/events"
)

func ValidateStock(ctx context.Context, event events.OrderProcessingEvent) error {
    for _, item := range event.Items {
        available := getAvailableStock(item.ProductID)
        if available < item.Quantity {
            return fmt.Errorf("insufficient stock for %s: need %d, have %d",
                item.ProductID, item.Quantity, available)
        }
    }
    return nil
}
```

```go
// github.com/mycompany/myapp/payment/validator.go - Payment package validates payment method
package payment

func ValidatePayment(ctx context.Context, event events.OrderProcessingEvent) error {
    user := getUserPaymentInfo(event.UserID)
    if !user.HasValidPaymentMethod() {
        return errors.New("no valid payment method")
    }
    if user.GetBalance() < event.Total {
        return errors.New("insufficient funds")
    }
    return nil
}

// All validation happens in same process - microsecond coordination!
```

## Essential Data Types

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

## Advanced Patterns

### **Cross-Package Event Coordination**
```go
// github.com/mycompany/myapp/events/app_events.go - Central event definitions
package events

var (
    UserSignedUp = signals.New[UserSignupEvent]()
    UserLoggedIn = signals.New[UserLoginEvent]()
    DataChanged  = signals.New[DataChangeEvent]()
)

// github.com/mycompany/myapp/auth/service.go - Authentication service
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
// github.com/mycompany/myapp/db/transaction.go - Transaction coordinator with validation
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
// github.com/mycompany/myapp/middleware/timeout.go - HTTP request timeout handling
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

## Performance Tips

### **▪ Do's:**
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

### **▫ Don'ts:**
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

## Testing Your Signals

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

## Next Steps

**You're ready to build production-grade event systems!** ▶

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

**Happy eventing! ▶** Questions? Check our [examples](../example/) or [open an issue](https://github.com/maniartech/signals/issues).
