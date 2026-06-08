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

**How v1.4 resolves the trilemma (per [ADR 0001](../design/0001-async-dispatch-and-error-model.md)):**
v1.4 keeps **(2) the producer never waits** and **(3) zero loss**, and therefore gives
up **(1) bounded memory** under sustained overload — excess handlers **park** (cheaply)
rather than being dropped or blocking the caller. This is a deliberate choice: nothing
is silently lost, and `Emit` stays truly fire-and-forget. The cost is that a *sustained*
producer-faster-than-consumer condition lets the parked-work backlog grow without a hard
limit (documented honestly; it is a *load* condition with visible symptoms).

The levers v1.4 actually ships:

- **`Emit`** — fire-and-forget; one dispatcher goroutine, returns immediately. Handlers
  run concurrently. A `MaxConcurrent` bound caps *concurrent* handlers; excess **parks**
  (no drop, no caller-block). Unbounded by default (a bound can starve long-running
  listeners — see [Bounded Concurrency](flow-control/bounded-concurrency.md)).
- **`EmitAndWait` / `EmitAndWaitErr`** — allowed to make the caller wait ⇒ the natural
  **backpressure** path: the producer's own loop self-throttles to the rate handlers
  complete, and no event is lost. This is the path for loss-intolerant work.

The rule of thumb: **loss-intolerant data (trades, orders, audit entries) belongs on
`EmitAndWait`** — the path that lets the producer slow down. See
[Backpressure](flow-control/backpressure.md).

> **Deferred (🔭 post-v1.4):** explicit **drop / block / error overflow policies** and a
> hard backlog cap are *designed but not in v1.4*. The [Load Shedding](flow-control/load-shedding.md)
> pattern documents that future opt-in; it is **not** the default `Emit` behavior in v1.4.

---

## Authoritative API reference (use these signatures exactly)

Every pattern in this catalog must use the signatures below and nothing else.
APIs are tagged **✅ shipped** (available today), **🔜 v1.4** (agreed for the v1.4
release), or **🔭 post-v1.4** (designed but deliberately deferred — see
[ADR 0001](../design/0001-async-dispatch-and-error-model.md)). Never invent APIs
beyond this list.

> **Async dispatch model (ADR 0001, authoritative).** `AsyncSignal.Emit` spawns **one
> dispatcher goroutine** and returns immediately (fire-and-forget, no asterisk); each
> handler then runs **independently/concurrently** in its own goroutine. A configured
> `MaxConcurrent` bounds how many handlers run **at once** via a counting semaphore;
> excess handlers **park** (cheaply) until a slot frees — they are **not dropped** and
> the caller is **never blocked**. With no `MaxConcurrent`, dispatch is **unbounded**
> (the safe default — a bound can starve long-running listeners). Explicit
> drop/block/error overflow *policies* are **🔭 post-v1.4**, not shipped in v1.4.

### Construction
```go
signals.New[T]() *AsyncSignal[T]                               // ✅ async signal (unbounded dispatch)
signals.NewSync[T]() *SyncSignal[T]                            // ✅ sync signal
signals.NewWithOptions[T](*SignalOptions) *AsyncSignal[T]      // ✅
signals.NewSyncWithOptions[T](*SignalOptions) *SyncSignal[T]   // ✅
signals.DefaultMaxConcurrent() int                           // 🔜 v1.4 — recommended bound = 2*NumCPU

type SignalOptions struct {
    InitialCapacity int             // ✅
    GrowthFunc      func(int) int   // ✅
    MaxConcurrent  int             // 🔜 v1.4 — bounds CONCURRENT handlers; 0/unset = unbounded
    // Overflow OverflowPolicy      // 🔭 post-v1.4 — explicit drop/block/error policy (NOT in v1.4)
}

// 🔭 post-v1.4 — NOT shipped in v1.4. In v1.4, excess handlers park (no drop, no
// caller-block) when a MaxConcurrent bound is hit; there is no overflow policy knob yet.
// type OverflowPolicy int
// const ( OverflowDropNewest OverflowPolicy = iota; OverflowBlock; OverflowError )
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
Emit(ctx context.Context, payload T)                  // ✅ fire-and-forget; one dispatcher goroutine, returns immediately
EmitAndWait(ctx context.Context, payload T)           // ✅ concurrent handlers, blocks until all done
EmitAndWaitErr(ctx context.Context, payload T) error  // 🔜 v1.4 — concurrent, waits, errors.Join'd
```

### Failure hooks
```go
signals.SetPanicHandler(func(recovered any))          // ✅ global; routes recovered async panics
(*AsyncSignal[T]).OnError(func(ctx context.Context, err error)) // 🔜 v1.4 — per-signal async error sink (multiple allowed)
// (*AsyncSignal[T]).OnOverflow(func(dropped T))       // 🔭 post-v1.4 — only meaningful with a drop policy (not in v1.4)
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
