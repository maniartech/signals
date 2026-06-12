# Signals

**Lightweight, Context-Aware Event System for Go**

`signals` provides typed, thread-safe event dispatch with two variants:
fire-and-forget async signals and error-aware sync signals. It favors
simple APIs, context propagation, and predictable concurrency behavior.

> ⚠️ **Breaking change (v1.4.0):** `AsyncSignal.Emit` is now truly
> fire-and-forget — it schedules listeners and **returns immediately** instead
> of waiting for them. This is a *silent runtime* change (your code still
> compiles). **If you relied on the old blocking behavior, switch
> `Emit` → `TryEmit`.** See [Migration Note](#-migration-note-async-emit-semantics).

## Key Features

- 🧭 **Two Signal Types**: Async for fire-and-forget, Sync for error-aware workflows
- 🛡️ **Context-Aware**: All listeners receive context for cancellation and timeouts
- 🚨 **Error-Safe Operations**: `TryEmit` stops on the first error or canceled context
- 🔒 **Thread-Safe**: Safe for concurrent Add/Remove/Emit
- 🧰 **Zero-Value Usable**: Zero-value signals can be used without explicit initialization
- 📦 **Zero Dependencies**: Pure Go, no external dependencies
- 🚀 **Async & Sync**: Both fire-and-forget and error-handling patterns

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

Every number below comes from a committed benchmark (`tests/bench_test.go`) and is
reproducible on your own hardware and Go version:

```bash
go test -run '^$' -bench=. -benchmem -count=6 ./tests/
```

**Measured on:** `goos: windows · goarch: amd64 · AMD Ryzen 7 5700G (16 threads) · go test -count=6`.
Numbers are machine-specific — **reproduce locally; treat these as guidance, not guarantees.**

### Sync emission — lock-free copy-on-write read path (0 allocations)

| Benchmark | Time | Allocs |
|---|---|---|
| `SyncEmit` · 1 listener | ~8 ns | 0 B · 0 allocs |
| `SyncEmit` · 10 listeners | ~33 ns | 0 B · 0 allocs |
| `SyncEmit` · concurrent (16 threads) | ~1.1 ns | 0 B · 0 allocs |
| `SyncTryEmit` · 1 listener | ~9 ns | 0 B · 0 allocs |
| `SyncEmit` · error routed to `OnError` | ~16 ns | 0 B · 0 allocs |

The sync read path is a single atomic load of an immutable slice — no lock, no per-emit
snapshot copy, no allocation. Concurrent sync emission scales near-linearly (the ~1.3 ns is
aggregate across 16 threads).

### Async emission — `Emit` is **dispatch rate**, not listener completion

`AsyncSignal.Emit` is fire-and-forget: it spawns a dispatcher and returns. These numbers
measure how fast it *schedules* listeners (a goroutine per listener), **not** how long
listeners take to run.

| Benchmark | Time | Allocs |
|---|---|---|
| `Emit` (dispatch) · 1 listener | ~230 ns | 208 B · 2 allocs |
| `Emit` (dispatch) · 100 listeners | ~24 µs | ~12 KB · ~95 allocs |
| `TryEmit` (waits for all) · 10 listeners | ~4 µs | 1.5 KB · 12 allocs |
| `TryEmit` **bounded** (`MaxConcurrent=4`) · 10 listeners | ~6 µs | 1.5 KB · 12 allocs |

`TryEmit` waits for every listener (goroutine spawn + `WaitGroup` synchronization), so it is
necessarily slower than fire-and-forget `Emit`. **Bounding is a safety valve, not a speed
win** — the bounded variant is *slower* than unbounded here by design: it caps how many
handlers run at once to protect a slow downstream dependency, trading throughput for control.

### Write path — O(n) by design

| Benchmark | Time | Allocs |
|---|---|---|
| `AddListener`/`RemoveListener` churn (~1000 listeners, concurrent) | ~23 µs | ~82 KB · 5 allocs |

Copy-on-write rebuilds the whole subscriber slice on every mutation, so writes are O(n).
This is the deliberate trade behind lock-free, allocation-free reads: a signal emits far
more often than it changes its listener set. Workloads that churn listeners as hot as they
emit are not a good fit for this design.

> Earlier docs quoted "sub-10 ns / zero-allocation" as a blanket headline and attributed
> ~11 ns to async emission. Those claims are retired: ~8 ns / 0-alloc is the **sync** read
> path specifically; async `Emit` is a few hundred nanoseconds of *dispatch* and allocates.

## API Reference

### **AsyncSignal** (Fire-and-Forget)

```go
// Create async signal
var UserLoggedIn = signals.New[User]()

// Add listeners
UserLoggedIn.AddListener(func(ctx context.Context, user User) {
    // Handle event (no error return)
}, "optional-key")

// Add error-returning listeners (errors routed to OnError on the Emit path)
UserLoggedIn.AddListenerWithErr(func(ctx context.Context, user User) error {
    return notify(ctx, user)
}, "optional-key")

// One-shot listeners (consumed on first attempt)
UserLoggedIn.AddOnce(func(ctx context.Context, user User) { /* ... */ })
UserLoggedIn.AddOnceWithErr(func(ctx context.Context, user User) error { return nil })

// Handle-based subscription: get a canceller func instead of inventing a key.
cancel := UserLoggedIn.AddListenerWithCancel(func(ctx context.Context, user User) { /* ... */ })
defer cancel() // idempotent; removes the listener, no key bookkeeping

// One-shot with a canceller — can be removed BEFORE it fires (abandon a wait)
stop := UserLoggedIn.AddOnceWithCancel(func(ctx context.Context, user User) { /* ... */ })
// ... stop() removes it if it hasn't fired yet; a cancel that wins the race guarantees it won't run

// Emit (schedules listeners and returns immediately; listener errors go to OnError)
UserLoggedIn.Emit(ctx, user)

// Wait for all listeners and collect their errors (errors.Join)
if err := UserLoggedIn.TryEmit(ctx, user); err != nil {
    // one or more listeners failed (or ctx deadline reached)
}

// Route async listener errors to a sink (additive; multiple sinks allowed)
UserLoggedIn.OnError(func(ctx context.Context, err error) {
    log.Printf("listener error: %v", err)
})

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

// One-shot error-returning listener (consumed on first attempt)
OrderCreated.AddOnceWithErr(func(ctx context.Context, order Order) error {
    return auditOnce(order)
})

// Error-safe emit with context cancellation
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

if err := OrderCreated.TryEmit(ctx, order); err != nil {
    // Handle error or timeout
    // Subsequent listeners won't execute if error occurs
}

// Best-effort Emit: runs all listeners; listener errors are routed to OnError
OrderCreated.OnError(func(ctx context.Context, err error) {
    log.Printf("listener error: %v", err)
})
OrderCreated.Emit(ctx, order)
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

## ⚠️ Migration Note: Async `Emit` Semantics

`AsyncSignal.Emit` is now truly fire-and-forget: it schedules each listener
in its own goroutine and returns immediately. In earlier releases, `Emit`
waited for all listeners to finish.

- If you relied on the old blocking "wait for all listeners" behavior, call
  `TryEmit`. If you don't care about listener errors, simply ignore its return
  value: `_ = sig.TryEmit(ctx, payload)`.
- If the supplied context is already canceled, `Emit`/`TryEmit` skip all listeners.
- Panics in async listeners are recovered and reported via `signals.SetPanicHandler`
  (default: logged with the standard library `log` package).
- `SyncSignal.Emit` now also invokes error-returning listeners best-effort; their
  errors are routed to any sinks registered via `OnError` rather than discarded.
  Use `TryEmit` when an error must stop emission and be returned to the caller.
- `OnError(func(ctx, err error))` is available on both `SyncSignal` and
  `AsyncSignal` (and on the `Signal[T]` interface). It receives error-returning
  listeners' errors on the `Emit` path; on the `TryEmit` path those errors are
  returned to the caller instead.

## Documentation

[![GoDoc](https://godoc.org/github.com/maniartech/signals?status.svg)](https://godoc.org/github.com/maniartech/signals)

## Contributing

`signals` accepts a **deliberately narrow scope** of contributions. Small,
specific fixes tied to a filed issue are welcome. **Public API changes,
behavioral/semantic changes, and architectural changes are maintainer-led and
require an approved issue first.** Please read [CONTRIBUTING.md](CONTRIBUTING.md)
before opening a pull request.

## License

![License](https://img.shields.io/badge/license-MIT-blue.svg)

## You Need Some Go Experts, Right?

As a software development firm, ManiarTech® specializes in Golang-based projects. Our team has an in-depth understanding of Enterprise Process Automation, Open Source, and SaaS. Also, we have extensive experience porting code from Python and Node.js to Golang. We have a team of Golang experts here at ManiarTech® that is well-versed in all aspects of the language and its ecosystem.
At ManiarTech®, we have a team of Golang experts who are well-versed in all facets of the technology.

In short, if you're looking for experts to assist you with Golang-related projects, don't hesitate to get in touch with us. Send an email to <contact@maniartech.com> to get in touch.

## Do you consider yourself an "Expert Golang Developer"?

If so, you may be interested in the challenging and rewarding work that is waiting for you. Use <careers@maniartech.com> to submit your resume.
