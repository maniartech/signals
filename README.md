# Signals

**Lightweight, Context-Aware Event System for Go**

`signals` provides typed, thread-safe event dispatch with two variants:
fire-and-forget async signals and error-aware sync signals. It favors
simple APIs, context propagation, and predictable concurrency behavior.

## Key Features

- 🧭 **Two Signal Types**: Async for fire-and-forget, Sync for error-aware workflows
- 🛡️ **Context-Aware**: All listeners receive context for cancellation and timeouts
- 🚨 **Error-Safe Operations**: `TryEmit` stops on the first error or canceled context
- 🔒 **Thread-Safe**: Safe for concurrent Add/Remove/Emit
- 🧰 **Zero-Value Usable**: Zero-value signals can be used without explicit initialization
- 📦 **Zero Dependencies**: Pure Go, no external dependencies
- 🚀 **Async & Sync**: Both fire-and-forget and error-handling patterns

[![GoReportCard example](https://goreportcard.com/badge/github.com/nanomsg/mangos)](https://goreportcard.com/report/github.com/maniartech/signals)
[![<ManiarTech®️>](https://circleci.com/gh/maniartech/signals.svg?style=shield)](https://circleci.com/gh/maniartech/signals)
[![made-with-Go](https://img.shields.io/badge/Made%20with-Go-1f425f.svg)](https://go.dev/)
[![GoDoc reference example](https://img.shields.io/badge/godoc-reference-blue.svg)](https://godoc.org/github.com/maniartech/signals)

## Quick Start

### Installation

```bash
go get github.com/maniartech/signals@latest
```

### Choose Your Signal Type

```go
// For fire-and-forget async operations
var UserRegistered = signals.New[User]()

// For transaction-safe operations with error handling
var OrderProcessed = signals.NewSync[Order]()
```

## Practical Examples

### 1. **Simple Async Events** (Fire-and-Forget)

```go
package main

import (
    "context"
    "fmt"
    "github.com/maniartech/signals"
)

type User struct {
    ID   int
    Name string
}

// Async signals for non-critical events
var UserRegistered = signals.New[User]()
var EmailSent = signals.New[string]()

func main() {
    // Add listeners for user registration
    UserRegistered.AddListener(func(ctx context.Context, user User) {
        fmt.Printf("📧 Sending welcome email to %s\n", user.Name)
        EmailSent.Emit(ctx, user.Name)
    })

    UserRegistered.AddListener(func(ctx context.Context, user User) {
        fmt.Printf("📊 Adding user %s to analytics\n", user.Name)
    })

    // Emit user registration event
    ctx := context.Background()
    UserRegistered.Emit(ctx, User{ID: 1, Name: "John Doe"})
}
```

### 2. **Transaction-Safe Error Handling** (Mission-Critical)

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "time"
    "github.com/maniartech/signals"
)

type Order struct {
    ID     int
    Amount float64
    UserID int
}

// Sync signal for transaction-safe operations
var OrderProcessed = signals.NewSync[Order]()

func main() {
    // Add error-returning listeners for critical operations
    OrderProcessed.AddListenerWithErr(func(ctx context.Context, order Order) error {
        fmt.Printf("💳 Processing payment for order %d\n", order.ID)
        if order.Amount > 10000 {
            return errors.New("payment declined: amount too high")
        }
        return nil
    })

    OrderProcessed.AddListenerWithErr(func(ctx context.Context, order Order) error {
        fmt.Printf("📦 Creating shipping label for order %d\n", order.ID)
        return nil // Success
    })

    // Emit with error handling and timeout
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    order := Order{ID: 123, Amount: 15000, UserID: 456}

    if err := OrderProcessed.TryEmit(ctx, order); err != nil {
        fmt.Printf("❌ Order processing failed: %v\n", err)
        // Rollback transaction, notify user, etc.
    } else {
        fmt.Printf("✅ Order %d processed successfully\n", order.ID)
    }
}
```

### 3. **Real-Time System Events**

```go
// High-frequency trading or real-time control systems
var PriceUpdated = signals.New[PriceUpdate]()
var SystemAlert = signals.NewSync[Alert]()

// Process high-frequency updates
PriceUpdated.AddListener(func(ctx context.Context, update PriceUpdate) {
    handlePriceChange(update)
})

// Context cancellation for graceful shutdowns
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

if err := SystemAlert.TryEmit(ctx, criticalAlert); err != nil {
    // Handle system failure
}
```


## Performance & Benchmarks

Benchmarks are included in the repo so you can measure on your own
hardware and Go version:

```bash
go test -bench . -benchmem
```

Notes:

- **AsyncSignal** spawns a goroutine per listener; expect allocations
  and scheduling overhead per emit.
- **SyncSignal** is designed to be low-allocation in steady state after
  listeners are registered.
- Results will vary by CPU, Go version, and runtime settings; treat
  numbers as guidance, not guarantees.

## API Reference

### **AsyncSignal** (Fire-and-Forget)

```go
// Create async signal
var UserLoggedIn = signals.New[User]()

// Add listeners
UserLoggedIn.AddListener(func(ctx context.Context, user User) {
    // Handle event (no error return)
}, "optional-key")

// Emit (schedules listeners and returns immediately)
UserLoggedIn.Emit(ctx, user)

// Remove listener
UserLoggedIn.RemoveListener("optional-key")
```

### **SyncSignal** (Error-Safe, Transaction-Ready)

```go
// Create sync signal
var OrderCreated = signals.NewSync[Order]()

// Add error-returning listeners
OrderCreated.AddListenerWithErr(func(ctx context.Context, order Order) error {
    return processPayment(order) // Can return errors
})

// Error-safe emit with context cancellation
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := OrderCreated.TryEmit(ctx, order); err != nil {
    // Handle error or timeout
    // Subsequent listeners won't execute if error occurs
}
```

### **Advanced Patterns**

```go
// Conditional listeners
if isProduction {
    UserRegistered.AddListener(sendToAnalytics)
}

// Dynamic listener management
key := UserRegistered.AddListener(temporaryHandler)
// Later...
UserRegistered.RemoveListener(key)

// Context cancellation
ctx, cancel := context.WithCancel(context.Background())
go func() {
    time.Sleep(1*time.Second)
    cancel() // Cancels in-flight TryEmit operations
}()
```

## Documentation

[![GoDoc](https://godoc.org/github.com/maniartech/signals?status.svg)](https://godoc.org/github.com/maniartech/signals)

## License

![License](https://img.shields.io/badge/license-MIT-blue.svg)

## You Need Some Go Experts, Right?

As a software development firm, ManiarTech® specializes in Golang-based projects. Our team has an in-depth understanding of Enterprise Process Automation, Open Source, and SaaS. Also, we have extensive experience porting code from Python and Node.js to Golang. We have a team of Golang experts here at ManiarTech® that is well-versed in all aspects of the language and its ecosystem.
At ManiarTech®, we have a team of Golang experts who are well-versed in all facets of the technology.

In short, if you're looking for experts to assist you with Golang-related projects, don't hesitate to get in touch with us. Send an email to <contact@maniartech.com> to get in touch.

## Do you consider yourself an "Expert Golang Developer"?

If so, you may be interested in the challenging and rewarding work that is waiting for you. Use <careers@maniartech.com> to submit your resume.
