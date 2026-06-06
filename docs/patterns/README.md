# Signals Pattern Catalog

A classified, professional-grade catalog of design patterns for building in-process
event-driven systems with the `signals` library. The catalog is modeled on the
structure of the *Gang of Four* (GoF) *Design Patterns*: each pattern is a
self-contained document with a fixed anatomy (Intent → Motivation → Applicability →
Structure → Participants → Collaborations → Consequences → Implementation → Sample
Code → Variations → Known Uses → Related Patterns).

> **Audience.** Written for every level. A pattern's **Intent**, **Motivation**, and
> **Sample Code** are readable without concurrency expertise; the **Implementation**
> and **Consequences** sections go deep for architects. You do not need to read the
> catalog front-to-back — find your situation in the [Pattern Map](#pattern-map) and
> jump to the pattern.

---

## How the catalog is classified

Patterns are grouped into five **families**, each answering a different design
question. The families are ordered the way you typically make decisions: *how do I
deliver?* → *how do I stay correct?* → *how do I survive load?* → *how do I manage
subscribers?* → *how do I structure the whole app?*

| # | Family | The question it answers | Patterns |
|---|--------|-------------------------|----------|
| 1 | **[Dispatch](#family-1--dispatch)** | How is an emission delivered to listeners? | Synchronous Sequential · Fire-and-Forget · Await-All |
| 2 | **[Reliability](#family-2--reliability)** | How do failures (errors, panics) get handled? | Transactional Emission · Async Error Routing · Result Aggregation · Panic Isolation |
| 3 | **[Flow-Control](#family-3--flow-control)** | How does the system behave under load? | Bounded Concurrency · Load Shedding · Backpressure |
| 4 | **[Subscription Lifecycle](#family-4--subscription-lifecycle)** | How are listeners registered, replaced, removed? | Keyed Subscription · One-Shot Subscription · Subscription Teardown |
| 5 | **[Architectural](#family-5--architectural)** | How is the event system structured across an app? | Shared Event Registry · Context-Scoped Emission |

---

## Pattern Map

Find your situation, jump to the pattern.

| I want to… | Pattern |
|---|---|
| Run listeners in a guaranteed order and wait for them | [Synchronous Sequential Dispatch](dispatch/synchronous-sequential-dispatch.md) |
| Notify others and keep moving (losing one under extreme load is OK) | [Fire-and-Forget Dispatch](dispatch/fire-and-forget-dispatch.md) |
| Run listeners concurrently but wait for all to finish | [Await-All Dispatch](dispatch/await-all-dispatch.md) |
| Stop the whole chain on the first failure and get the error | [Transactional Emission](reliability/transactional-emission.md) |
| Learn about failures from fire-and-forget async listeners | [Async Error Routing](reliability/async-error-routing.md) |
| Run concurrently, then collect every listener's error | [Result Aggregation](reliability/result-aggregation.md) |
| Stop one buggy listener from crashing the rest | [Panic Isolation](reliability/panic-isolation.md) |
| Cap how many listeners run at once (prevent goroutine pile-up) | [Bounded Concurrency](flow-control/bounded-concurrency.md) |
| Decide what gets dropped when the system is overloaded | [Load Shedding](flow-control/load-shedding.md) |
| Guarantee no event is lost, accepting that the producer slows down | [Backpressure](flow-control/backpressure.md) |
| Remove, replace, or de-duplicate a specific listener | [Keyed Subscription](subscription/keyed-subscription.md) |
| Fire a listener exactly once, then auto-unsubscribe | [One-Shot Subscription](subscription/one-shot-subscription.md) |
| Clean up listeners and avoid leaks | [Subscription Teardown](subscription/subscription-teardown.md) |
| Share events across packages without coupling them | [Shared Event Registry](architectural/shared-event-registry.md) |
| Propagate cancellation/deadlines through emissions | [Context-Scoped Emission](architectural/context-scoped-emission.md) |

---

## The one idea behind the whole catalog

Every flow-control and reliability decision in this library traces back to a single
law from queueing theory:

> **When producers outrun consumers for a sustained period, you cannot have all
> three of: (1) bounded memory, (2) a producer that never waits, (3) zero event
> loss. You must give up one.**

This library encodes the choice in *which method you call*:

- **Fire-and-forget `Emit`** has promised the producer never waits ⇒ under overload
  it is, by definition, a **loss-tolerant** channel: it keeps memory bounded and
  **drops + counts** overflow rather than growing without limit.
- **`EmitAndWait` / `EmitAndWaitErr`** are allowed to make the producer wait ⇒ they
  refuse to lose events and instead apply **backpressure** (the producer slows).

The rule of thumb that falls out of this: **loss-intolerant data (trades, orders,
audit entries) must travel on a path that is allowed to slow you down.** A method
that never makes you wait will, under enough pressure, have to drop something. The
[Load Shedding](flow-control/load-shedding.md) and [Backpressure](flow-control/backpressure.md)
patterns are the two halves of this trade-off.

---

## Authoritative API reference (use these signatures exactly)

Every pattern in this catalog must use the signatures below and nothing else.
APIs are tagged **✅ shipped** (available today) or **🔜 v1.4** (agreed for the v1.4
release; shown so patterns are complete). Never invent APIs beyond this list.

### Construction
```go
signals.New[T]() *AsyncSignal[T]                               // ✅ async signal
signals.NewSync[T]() *SyncSignal[T]                            // ✅ sync signal
signals.NewWithOptions[T](*SignalOptions) *AsyncSignal[T]      // ✅
signals.NewSyncWithOptions[T](*SignalOptions) *SyncSignal[T]   // ✅

type SignalOptions struct {
    InitialCapacity int             // ✅
    GrowthFunc      func(int) int   // ✅
    WorkerPoolSize  int             // 🔜 v1.4 — bounds concurrent async listeners
    Overflow        OverflowPolicy  // 🔜 v1.4 — what to do when the bound is saturated
}

// 🔜 v1.4
type OverflowPolicy int
const (
    OverflowDropNewest OverflowPolicy = iota // default: drop the incoming overflow, count it
    OverflowBlock                            // make Emit wait (turns Emit into backpressure)
    OverflowError                            // report overflow via the overflow hook, do not run
)
```

### Listener types
```go
type SignalListener[T any]    func(context.Context, T)         // ✅ plain listener
type SignalListenerErr[T any] func(context.Context, T) error   // ✅ error-returning listener
```

### Subscription (on both SyncSignal and AsyncSignal)
```go
AddListener(handler SignalListener[T], key ...string) int      // ✅ returns count, or -1 if key dup
AddListenerWithErr(handler SignalListenerErr[T], key ...string) int // ✅ sync; 🔜 v1.4 on async
RemoveListener(key string) int                                 // ✅ returns count, or -1 if not found
Reset()                                                        // ✅ remove all listeners
Len() int                                                      // ✅
IsEmpty() bool                                                 // ✅

AddOnce(handler SignalListener[T]) int                         // 🔜 v1.4 — fire once, auto-remove
AddOnceWithKey(handler SignalListener[T], key string) int      // 🔜 v1.4
Keys() []string                                                // 🔜 v1.4 — snapshot of keys
HasKey(key string) bool                                        // 🔜 v1.4 — O(1) existence check
```

### Emission — SyncSignal
```go
Emit(ctx context.Context, payload T)            // ✅ sequential, blocks; discards listener errors
TryEmit(ctx context.Context, payload T) error   // ✅ sequential, stops on first error/cancel
```

### Emission — AsyncSignal
```go
Emit(ctx context.Context, payload T)                  // ✅ fire-and-forget; returns immediately
EmitAndWait(ctx context.Context, payload T)           // ✅ concurrent listeners, blocks until all done
EmitAndWaitErr(ctx context.Context, payload T) error  // 🔜 v1.4 — concurrent, waits, errors.Join'd
```

### Failure & overflow hooks
```go
signals.SetPanicHandler(func(recovered any))          // ✅ global; routes recovered async panics
(*AsyncSignal[T]).OnError(func(ctx context.Context, err error)) // 🔜 v1.4 — per-signal async error sink
(*AsyncSignal[T]).OnOverflow(func(dropped T))                   // 🔜 v1.4 — per-signal drop notification
```

### Semantics that every pattern can rely on
- **Zero-value usable:** `var s signals.AsyncSignal[T]` works without a constructor
  (lazy `sync.Once` init). ✅
- **Canceled context skips all listeners:** if `ctx.Err() != nil` at emit time, no
  listener runs (all emit variants). ✅
- **Sync stops mid-chain on cancel:** `Emit`/`TryEmit` check `ctx` between listeners. ✅
- **Async panics are recovered**, never crash the process, routed to the panic
  handler. ✅
- **Keyed dedup:** adding a second listener with an existing key returns `-1` and does
  nothing. ✅
- **Order:** sync preserves registration order (subject to swap-remove after a
  removal); async makes **no** ordering guarantee. ✅

---

## Status legend

| Tag | Meaning |
|-----|---------|
| ✅ shipped | Available in the current release |
| 🔜 v1.4 | Agreed for v1.4; shown here so the pattern is complete |

When in doubt about what your installed version exposes, see the
[API Reference](../api_reference.md) and [RELEASENOTES.md](../../RELEASENOTES.md).

---

## Contributing a pattern

Use [`_TEMPLATE.md`](_TEMPLATE.md) verbatim as the skeleton. Match the depth and tone
of an existing pattern such as [Load Shedding](flow-control/load-shedding.md) (the
reference exemplar). Keep all code aligned with the API reference above.
