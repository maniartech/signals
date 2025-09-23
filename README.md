# Signals

**High-Performance, Production-Ready Event System for Go**

The `signals` library delivers **sub-10 nanosecond performance** with **zero-allocation** critical paths, making it perfect for mission-critical applications like high-frequency trading, real-time control systems, and embedded applications.

## Key Features

- ⚡ **Ultra-Fast Performance**: 5.66ns/op single listener emit with zero allocations
- 🛡️ **Context-Aware**: All listeners receive context for cancellation and timeouts
- 🚨 **Error-Safe Operations**: Fast-failing error propagation with `TryEmit` for transaction safety
- 🔒 **Thread-Safe**: Race-condition free design tested under extreme concurrency
- 🎯 **Transaction-Safe**: Perfect for database transactions and critical workflows
- 📦 **Zero Dependencies**: Pure Go, no external dependencies
- 🚀 **Async & Sync**: Both fire-and-forget and error-handling patterns

💯 **93.5% test coverage** 💯 | **Enterprise-grade reliability**

✅ **Production-Ready**: Used by [ManiarTech®️](https://maniartech.com) and other companies in mission-critical applications.

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

// Zero-allocation performance for critical paths
PriceUpdated.AddListener(func(ctx context.Context, update PriceUpdate) {
    // Process price update with sub-10ns latency
    handlePriceChange(update)
})

// Context cancellation for graceful shutdowns
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

if err := SystemAlert.TryEmit(ctx, criticalAlert); err != nil {
    // Handle system failure
}
```


## Performance Benchmarks

### **Zero-Allocation Critical Paths**

| Benchmark | Iterations | Time/Op | Memory/Op | Allocs/Op | Performance Rating |
|-----------|------------|---------|-----------|-----------|-------------------|
| **Single Listener** | 196,613,109 | **5.66ns** | **0 B** | **0 allocs** | ⚡ **Sub-10ns** |
| **Concurrent Emit** | 41,751,328 | **28.55ns** | **0 B** | **0 allocs** | 🚀 **Race-free** |
| **100 Listeners** | 34,066 | 35.87μs | 42 B | 2 allocs | 🎯 **Optimized** |

### **Why These Numbers Matter**

- **5.66ns Single Listener**: Faster than most function calls - suitable for **high-frequency trading**
- **Zero Allocations**: No GC pressure in critical paths - perfect for **real-time control systems**
- **28.55ns Concurrent**: Extreme thread safety without performance compromise
- **Stress-Tested**: 100 goroutines × 1000 operations under adversarial conditions

### **Real-World Performance**

```go
// This emits 1 million events in ~5.66ms
for i := 0; i < 1_000_000; i++ {
    signal.Emit(ctx, data) // 5.66ns per emit
}

// Perfect for:
// ✅ High-frequency trading (microsecond latency requirements)
// ✅ Real-time control systems (deterministic timing)
// ✅ Embedded applications (memory-constrained environments)
// ✅ Mission-critical workflows (zero-failure tolerance)
```

## API Reference

### **AsyncSignal** (Fire-and-Forget)

```go
// Create async signal
var UserLoggedIn = signals.New[User]()

// Add listeners
UserLoggedIn.AddListener(func(ctx context.Context, user User) {
    // Handle event (no error return)
}, "optional-key")

// Emit (waits for all listeners to complete)
UserLoggedIn.Emit(ctx, user)

// Non-blocking emit
go UserLoggedIn.Emit(ctx, user)

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
