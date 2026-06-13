# Signals

**Lightweight, Context-Aware Event System for Go**

`signals` provides typed, thread-safe event dispatch with two variants:
fire-and-forget async signals and error-aware sync signals. It favors
simple APIs, context propagation, and predictable concurrency behavior.

> **Breaking change (v1.4.0):** `AsyncSignal.Emit` is now truly
> fire-and-forget — it schedules listeners and **returns immediately** instead
> of waiting for them. This is a *silent runtime* change (your code still
> compiles). **If you relied on the old blocking behavior, switch
> `Emit` → `TryEmit`.** See [Migration Note](#migration-note-async-emit-semantics).

## Key Features

- **Two Signal Types**: Async for fire-and-forget, Sync for error-aware workflows
- **Context-Aware**: All listeners receive context for cancellation and timeouts
- **Error Handling**: `TryEmit` (sync: stop-on-first-error; async: `errors.Join` of all) and `OnError` sinks on the best-effort `Emit` path
- **Short-Circuit**: a sync listener can return `signals.StopPropagation` to halt the chain early (a control value, not a failure — like `fs.SkipAll`)
- **Lock-Free Reads**: Copy-on-write core — `Emit` is a single atomic load, no lock or allocation on the read path
- **Ordered Dispatch**: Sync `Emit` runs listeners FIFO (default) or `LIFO` (handler-stack / reverse-teardown), and order is **stable across add/remove**
- **Thread-Safe**: Safe for concurrent Add/Remove/Emit, proven under `-race` with property + fuzz + stress tests
- **Rich Subscriptions**: `AddOnce`/`AddOnceWithErr` one-shots, `AddListenerWithCancel`/`AddOnceWithCancel` handle-based teardown, plus `Keys`/`HasKey` introspection
- **Bounded Async Dispatch**: opt-in `MaxConcurrent` counting-semaphore safety valve (unbounded by default)
- **Zero-Value Usable**: Zero-value signals can be used without explicit initialization
- **Zero Dependencies**: Pure Go, no external dependencies

**Production-Ready**: Used by [ManiarTech®](https://maniartech.com) and other companies in mission-critical applications.

[![CI](https://github.com/maniartech/signals/actions/workflows/ci.yml/badge.svg)](https://github.com/maniartech/signals/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/maniartech/signals)](https://goreportcard.com/report/github.com/maniartech/signals)
[![Go Reference](https://pkg.go.dev/badge/github.com/maniartech/signals.svg)](https://pkg.go.dev/github.com/maniartech/signals)
[![made-with-Go](https://img.shields.io/badge/Made%20with-Go-1f425f.svg)](https://go.dev/)

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
        fmt.Printf("Sending welcome email to %s\n", user.Name)
        EmailSent.Emit(ctx, user.Name)
    })

    UserRegistered.AddListener(func(ctx context.Context, user User) {
        fmt.Printf("Adding user %s to analytics\n", user.Name)
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
        fmt.Printf("Processing payment for order %d\n", order.ID)
        if order.Amount > 10000 {
            return errors.New("payment declined: amount too high")
        }
        return nil
    })

    OrderProcessed.AddListenerWithErr(func(ctx context.Context, order Order) error {
        fmt.Printf("Creating shipping label for order %d\n", order.ID)
        return nil // Success
    })

    // Emit with error handling and timeout
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    order := Order{ID: 123, Amount: 15000, UserID: 456}

    if err := OrderProcessed.TryEmit(ctx, order); err != nil {
        fmt.Printf("Order processing failed: %v\n", err)
        // Rollback transaction, notify user, etc.
    } else {
        fmt.Printf("Order %d processed successfully\n", order.ID)
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

// Short-circuit: a listener can halt the chain early so later listeners don't run.
// StopPropagation is a control value, NOT a failure — TryEmit returns nil and Emit
// does not route it to OnError. (A real error still stops AND is reported.)
OrderCreated.AddListenerWithErr(func(ctx context.Context, order Order) error {
    if order.AlreadyHandled {
        return signals.StopPropagation // skip the remaining listeners, cleanly
    }
    return nil
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

// Dynamic listener management — NOTE: AddListener returns the subscriber COUNT
// (or -1 on a duplicate key), NOT a handle. Remove by the key you supplied...
UserRegistered.AddListener(temporaryHandler, "temp")
UserRegistered.RemoveListener("temp")
// ...or skip keys entirely and keep the canceller from AddListenerWithCancel:
cancel := UserRegistered.AddListenerWithCancel(temporaryHandler)
defer cancel()

// Introspection & bulk management
n := UserRegistered.Len()             // number of listeners
empty := UserRegistered.IsEmpty()     // n == 0
has := UserRegistered.HasKey("temp")  // O(1) keyed-existence check
keys := UserRegistered.Keys()         // snapshot of caller-supplied keys (auto/empty omitted)
UserRegistered.Reset()                // remove every listener

// Bounded async dispatch (opt-in safety valve; UNBOUNDED by default). Caps how many
// handlers run concurrently via a counting semaphore; excess parks (never dropped,
// caller never blocked). Size it to your slowest dependency, never the listener count.
bounded := signals.NewWithOptions[User](&signals.SignalOptions{
    MaxConcurrent: signals.DefaultMaxConcurrent(), // = 2 * runtime.NumCPU()
})
_ = bounded

// LIFO sync dispatch: most-recently-added listener runs first (handler stack /
// reverse teardown). Order is stable across add/remove. Sync-only; FIFO is default.
stack := signals.NewSyncWithOptions[User](&signals.SignalOptions{Order: signals.LIFO})
_ = stack

// Context cancellation
ctx, cancel := context.WithCancel(context.Background())
go func() {
    time.Sleep(1*time.Second)
    cancel() // Cancels in-flight TryEmit operations
}()
```

## Migration Note: Async `Emit` Semantics

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
- **Source break (compile-time):** the `Signal[T]` **interface** gained nine methods in
  v1.4 — `TryEmit`, `OnError`, `AddListenerWithErr`, `AddOnce`, `AddOnceWithErr`,
  `AddListenerWithCancel`, `AddOnceWithCancel`, `Keys`, and `HasKey`. If you wrote your
  own type that implements `Signal[T]`, it won't compile until you add them. Code that
  just **uses** signals — holding `*SyncSignal`/`*AsyncSignal` or the value returned by
  `New`/`NewSync` — is unaffected.

## Patterns

A GoF-style [pattern catalog](docs/patterns/README.md) documents the recurring ways to
use signals — each page has intent, structure, a runnable example, and trade-offs. Start
from the problem you have:

| Family | Pattern | What problem it solves |
|--------|---------|------------------------|
| **Dispatch** — how an emission reaches listeners | [Synchronous Sequential Dispatch](docs/patterns/dispatch/synchronous-sequential-dispatch.md) | Run listeners one at a time, in registration order, on the caller's goroutine; the emit blocks until all finish. |
| | [Reverse (LIFO) Dispatch](docs/patterns/dispatch/reverse-dispatch.md) | Run sync listeners newest-first — the handler-stack discipline: unwind handlers in reverse of setup, or let the most-recent override win. |
| | [Short-Circuit Dispatch](docs/patterns/dispatch/short-circuit-dispatch.md) | Let a listener stop the chain early (return `signals.StopPropagation`) so later listeners don't run — first-responder / middleware short-circuit. |
| | [Fire-and-Forget Dispatch](docs/patterns/dispatch/fire-and-forget-dispatch.md) | Notify others and immediately regain control of the caller — listeners run in the background. |
| | [Await-All Dispatch](docs/patterns/dispatch/await-all-dispatch.md) | Run listeners concurrently, but wait for every one to finish before continuing. |
| **Reliability** — how errors & panics are handled | [Transactional Emission](docs/patterns/reliability/transactional-emission.md) | Stop the whole chain on the first failure and return that error (all-or-nothing). |
| | [Async Error Routing](docs/patterns/reliability/async-error-routing.md) | Surface failures from fire-and-forget listeners that have no caller left to return to (`OnError`). |
| | [Result Aggregation](docs/patterns/reliability/result-aggregation.md) | Run concurrently, wait, then collect *every* listener's error (`errors.Join`). |
| | [Panic Isolation](docs/patterns/reliability/panic-isolation.md) | Keep one listener's panic from crashing the process or aborting its siblings. |
| **Flow-Control** — how the system behaves under load | [Bounded Concurrency](docs/patterns/flow-control/bounded-concurrency.md) | Cap how many async listeners run at once to prevent goroutine pile-up (`MaxConcurrent`). |
| | [Load Shedding](docs/patterns/flow-control/load-shedding.md) | Deliberately drop work to stay alive and bounded under sustained overload. *(design; post-v1.4)* |
| | [Backpressure](docs/patterns/flow-control/backpressure.md) | Guarantee zero event loss by slowing the producer under overload (a waiting emit *is* backpressure). |
| **Subscription Lifecycle** — registering & removing listeners | [Keyed Subscription](docs/patterns/subscription/keyed-subscription.md) | Give a listener a stable key so it can be removed, replaced, or de-duplicated later. |
| | [One-Shot Subscription](docs/patterns/subscription/one-shot-subscription.md) | Fire a listener exactly once, then auto-unsubscribe. |
| | [Subscription Teardown](docs/patterns/subscription/subscription-teardown.md) | Remove listeners and reclaim resources to avoid leaks — by key or via a `WithCancel` canceller. |
| **Architectural** — structuring the event system | [Shared Event Registry](docs/patterns/architectural/shared-event-registry.md) | Declare signals as package-level variables so components communicate without coupling. |
| | [Context-Scoped Emission](docs/patterns/architectural/context-scoped-emission.md) | Propagate cancellation, deadlines, and request-scoped values through an emission. |

## Documentation

[![Go Reference](https://pkg.go.dev/badge/github.com/maniartech/signals.svg)](https://pkg.go.dev/github.com/maniartech/signals)

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
