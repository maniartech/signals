# Await-All Dispatch

**Family:** Dispatch
· **Also Known As:** Scatter-Gather, Fan-Out/Join, Concurrent Barrier
· **Status:** ✅ shipped

## Intent

Dispatch all listeners **concurrently** — each in its own goroutine, for speed — but
**block the caller until every one has completed**. Concurrent execution behind a
completion barrier: you get parallelism *and* the guarantee that, after `TryEmit`
returns, all side effects are done — and, if you want them, every listener's error.

## Motivation

Consider order confirmation in an e-commerce checkout. Before you can flip an order to
`CONFIRMED` and tell the customer, several independent side effects must *all* succeed:
reserve inventory, capture payment authorization, allocate a shipment, and write the
audit record. They don't depend on each other and they don't need to run in order — but
the confirmation must not be sent until **every** one has finished.

Running them sequentially is correct but slow:

```go
func Confirm(ctx context.Context, o *Order) error {
    reserveInventory(ctx, o) // 30ms
    capturePayment(ctx, o)   // 60ms
    allocateShipment(ctx, o) // 40ms
    writeAudit(ctx, o)       // 20ms
    return confirm(ctx, o)   // customer waited 150ms — the SUM of four independent calls
}
```

The four calls touch different services and could happily overlap, yet the customer
waits for their *sum*, 150ms. Reaching for fire-and-forget to "speed it up" is a trap:

```go
var Confirming = signals.New[*Order]()
// ... add the four listeners ...
func Confirm(ctx context.Context, o *Order) error {
    Confirming.Emit(ctx, o) // returns IMMEDIATELY — listeners haven't run
    return confirm(ctx, o)   // confirms an order with no inventory reserved, no payment captured
}
```

`Emit` returns before the listeners have done anything, so `confirm` runs while
inventory is still unreserved and payment uncaptured — you confirm orders you can't
fulfill. Fire-and-forget removed the *wait*, but the wait was load-bearing.

Await-All Dispatch is the dispatch mode that keeps the wait while removing the *summing*.
The listeners run concurrently, and `TryEmit` returns only when the last one
finishes:

```go
var Confirming = signals.New[*Order]()

func init() {
    Confirming.AddListener(reserveInventory, "inventory")
    Confirming.AddListener(capturePayment, "payment")
    Confirming.AddListener(allocateShipment, "shipment")
    Confirming.AddListener(writeAudit, "audit")
}

func Confirm(ctx context.Context, o *Order) error {
    _ = Confirming.TryEmit(ctx, o) // all four run in parallel; returns after the LAST finishes
    return confirm(ctx, o)          // safe: every side effect is complete
}
```

The customer now waits ~60ms (the *slowest* listener, payment) instead of 150ms (the
*sum*), and `confirm` still sees a fully prepared order. Parallel work, joined.

## Applicability

**Use this pattern when:**

- **All listeners must finish before you continue**, but they are **independent** and
  can run in parallel — finish-all-side-effects-then-respond.
- **Latency matters** and the listeners' work overlaps — you want the *max*, not the
  *sum*, of their durations.
- **Draining on shutdown** — emit a final event and block until every handler has
  flushed, so nothing is lost on exit.
- **Fan-out/join** — scatter independent work across listeners, then gather at a barrier.

**Avoid it (or prefer another pattern) when:**

- **Order matters** between listeners (each builds on the previous) → use
  [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md).
- **You must not wait at all** (latency-critical hot path, loss-tolerant data) → use
  [Fire-and-Forget Dispatch](fire-and-forget-dispatch.md).
- **You need each listener's error**, not just "they all finished" → that is the *same*
  `TryEmit`: use its returned `errors.Join`ed error instead of ignoring it, documented
  under [Result Aggregation](../reliability/result-aggregation.md).

## Structure

```
  Caller's goroutine (blocks at the barrier)
  ┌─────────────────────────────────────────────────────────────────┐
  │  TryEmit(ctx, payload)                                           │
  │     │                                                            │
  │     ├─ ctx canceled? ─yes─▶ run nothing, return                 │
  │     │                                                            │
  │     ├─ scatter ───────────────────────────────────────┐         │
  │     │      ▼            ▼            ▼            ▼      │         │
  │     │  goroutine    goroutine    goroutine   goroutine  │ (all    │
  │     │  Listener₁    Listener₂    Listener₃   Listenerₙ  │  run    │
  │     │      │            │            │            │      │ concur- │
  │     │   (done)      (done)      (done)      (slowest) ── │ rently) │
  │     │      └────────────┴────────────┴────────────┘      │         │
  │     │                          │                         │         │
  │     └──── join (barrier) ◀──────┘  all complete           │         │
  │                          │                                          │
  │                          ▼                                          │
  │                       return  ──▶ caller continues; effects done    │
  └─────────────────────────────────────────────────────────────────┘
   Wall-clock ≈ slowest listener (max), NOT the sum.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Calls `TryEmit`; **blocks** at the join barrier; relies on all effects being done afterward; may inspect the returned joined error |
| **AsyncSignal** | Scatters listeners onto goroutines, then joins — returns only when all have completed |
| **Context** | Checked at entry; if already canceled, no listener runs |
| **Listener** | Runs concurrently with the others, in arbitrary order; must finish for the barrier to release |
| **Join barrier** | Internal synchronization (a `WaitGroup`-style join) that holds the caller until the last listener returns |

## Collaborations

1. The caller invokes `TryEmit(ctx, payload)`. The signal checks `ctx`: if already
   canceled, **no listener runs** and it returns (canceled-context-skips-all).
2. Otherwise the signal **scatters** the listeners onto separate goroutines — they begin
   executing concurrently, in **no guaranteed order**.
3. The signal then **waits at the join barrier**: the caller's goroutine is parked until
   *every* listener goroutine has returned.
4. As listeners finish, they signal completion to the barrier. When the **last** one
   returns, the barrier releases.
5. Control returns to the caller, which is now guaranteed that all listeners have run to
   completion — and can safely act on their combined effects.
6. **Outcomes are surfaced if you want them.** `TryEmit` waits for every listener and
   returns their errors joined via `errors.Join`; a listener that returns an error still
   counts as "completed" for the barrier, and its error rides out in the joined result. If
   you don't care about errors, ignore the return (`_ = sig.TryEmit(...)`) — see
   [Result Aggregation](../reliability/result-aggregation.md). Panics are recovered and
   routed to the global panic handler ([Panic Isolation](../reliability/panic-isolation.md)).

## Consequences

**Benefits**

- ✓ **Parallel, not summed.** Wall-clock time is roughly the slowest listener, not the
  total — a large win when listeners are independent and I/O-bound.
- ✓ **Completion guarantee.** When `TryEmit` returns, every listener has finished; the
  next line can rely on all their effects, exactly like the sync pattern.
- ✓ **A real join point.** Gives you a barrier to drain on — invaluable for graceful
  shutdown and "do all of these, then proceed" semantics.
- ✓ **Panic-safe.** A panicking listener is recovered and routed, not allowed to crash the
  process or the barrier.

**Liabilities**

- ✗ **The caller waits for the slowest.** One slow or hung listener holds the barrier — and
  the caller — until it (or the context) gives up. Tail latency is dominated by the worst
  listener.
- ✗ **Outcomes are invisible if you discard them.** `TryEmit` tells you "all finished" *and*
  carries "what failed" in its returned joined error — but ignoring that return (`_ =`)
  silently throws the failures away. Inspect the return when outcomes matter —
  [Result Aggregation](../reliability/result-aggregation.md).
- ✗ **No ordering.** Concurrency means arbitrary execution order; don't put inter-listener
  dependencies here.
- ✗ **Loss-intolerant path → can slow the producer.** Because it waits, this method is, by
  design, allowed to make the producer wait (see the trilemma note). On a latency-critical
  hot path that is a liability, not a feature.

> **Trilemma corner sacrificed:** Await-All keeps **zero loss** and **bounded memory**
> (one bounded burst of work per emit, all joined) by **making the producer wait**. It is
> the loss-intolerant counterpart to fire-and-forget: where fire-and-forget would drop
> under pressure, Await-All slows the producer instead — the same family of trade-off as
> [Backpressure](../flow-control/backpressure.md).

## Implementation

1. **Construct with `New[T]()`** (✅) — the same `AsyncSignal` used for fire-and-forget;
   the dispatch mode is chosen by *which method you call*, not by a different
   constructor. `NewWithOptions[T]` (✅) adds `InitialCapacity`/`GrowthFunc` and, 🔜 v1.4,
   `MaxConcurrent`.

2. **Always pass a context with a deadline.** Because the caller waits for the slowest
   listener, a single hung listener blocks `TryEmit` indefinitely unless bounded.
   Pass `context.WithTimeout` and have listeners honor `ctx` so the barrier can release on
   deadline rather than hanging forever. The signal checks `ctx` at entry but cannot
   forcibly interrupt a listener that ignores it — cancellation is cooperative. (The waiter
   does return when `ctx` is done even if a handler is still hung.)

3. **Outcomes ride out in the return — don't discard them blindly.** `TryEmit` returns the
   listeners' errors joined via `errors.Join`. If you ignore the return (`_ =`), a listener
   can fail and you'll still proceed as if it succeeded. When *any* listener's failure should
   change your control flow, inspect the returned error — see
   [Result Aggregation](../reliability/result-aggregation.md). Treat `_ = sig.TryEmit(...)`
   as "fire-and-join," and the error-checked form as "fire-and-verify." Note `TryEmit` does
   **not** stop at the first failure: it always runs and waits for *all* listeners.

4. **Bound the fan-out for high-fan-in signals.** A signal with many listeners spawns many
   goroutines per emit. For large listener counts or high emit rates, set `MaxConcurrent`
   (🔜 v1.4) so the concurrent fan-out is capped — see
   [Bounded Concurrency](../flow-control/bounded-concurrency.md). The completion guarantee
   still holds; the work is just throttled through the pool.

5. **Panics are recovered and routed.** A listener panic is caught (the barrier still
   releases) and sent to the handler registered with `signals.SetPanicHandler` (✅). Set it
   once at startup so a panicking listener is logged/counted rather than silently swallowed
   — see [Panic Isolation](../reliability/panic-isolation.md).

6. **Mind shared mutable state.** Listeners run concurrently, so two listeners writing the
   same `*T` race even though the caller waits for them. Pass values/snapshots or guard
   shared state. If listeners truly must build on each other's writes in order, you want
   [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md) instead.

7. **This is your graceful-shutdown drain.** On shutdown, emit a final "drain" event with
   `TryEmit` (and a generous deadline) to block until every handler has flushed its
   buffers — the one dispatch mode fire-and-forget can't give you because it offers no join.

8. **Don't use it on the latency-critical hot path for loss-tolerant data.** If you don't
   actually need to wait, waiting is pure cost — and worse, it converts a never-wait path
   into one that can stall the producer. Use
   [Fire-and-Forget Dispatch](fire-and-forget-dispatch.md) there.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Caller* that blocks at the barrier, the *AsyncSignal* that scatters
then joins, the *Context* bounding the wait, the concurrent *Listeners*, and the *Join
barrier*. Read this first to see the mechanics; the practical examples then apply it to
real problems.

```go
// 1. SIGNAL — an AsyncSignal (same constructor as fire-and-forget; the METHOD chooses the mode).
sig := signals.New[Job]()

// 2. LISTENERS — run concurrently, in NO guaranteed order; each must finish to release the barrier.
sig.AddListener(func(ctx context.Context, j Job) {
    taskA(ctx, j) // own goroutine
}, "a")
sig.AddListener(func(ctx context.Context, j Job) {
    taskB(ctx, j) // own goroutine, runs in parallel with A
}, "b")

// 3. CALLER — bound the barrier so one hung listener can't block forever.
ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
defer cancel()

// 4. TryEmit — SCATTER onto goroutines, then JOIN; returns only when the LAST finishes.
err := sig.TryEmit(ctx, job)
//   ├─ ctx canceled at entry?  → run nothing, return
//   ├─ scatter A, B            → both run concurrently
//   └─ join (barrier)          → park caller until A AND B return, THEN return
//                                (err is the errors.Join of every listener's failure)

// 5. Guaranteed: every listener has completed. Wall-clock ≈ slowest listener, not the sum.
if err != nil {
    handleFailure(err) // or ignore the return entirely if outcomes don't matter
}
proceed(job)
```

The scatter-then-join (step 4) is the heart of the pattern: it buys parallelism (the
*max*, not the *sum*, of listener durations) while still giving a completion barrier the
next line can rely on. `TryEmit` waits for all listeners *and* returns their errors joined
via `errors.Join` — so "all finished" carries "what failed" with it. If a caller doesn't
care about errors it ignores the return (`_ = sig.TryEmit(...)`); inspect it
([Result Aggregation](../reliability/result-aggregation.md)) when a listener's failure must
change control flow.

### Practical Example 1 — Order confirmation (reserve + charge + ship, then respond)

Order confirmation that runs independent side effects in parallel and only confirms once
all have completed:

```go
package checkout

import (
    "context"
    "log/slog"
    "time"

    "github.com/maniartech/signals"
)

type Order struct {
    ID      string
    UserID  string
    Lines   []Line
    Total   int64
}

// Same AsyncSignal as fire-and-forget — the dispatch mode is chosen by the method called.
var Confirming = signals.New[*Order]()

func init() {
    signals.SetPanicHandler(func(recovered any) { // ✅ — a panicking listener must not crash checkout
        slog.Error("confirm listener panicked", "recovered", recovered)
    })

    Confirming.AddListener(reserveInventory, "inventory")
    Confirming.AddListener(capturePayment, "payment")
    Confirming.AddListener(allocateShipment, "shipment")
    Confirming.AddListener(writeAudit, "audit")
}

func reserveInventory(ctx context.Context, o *Order) { inventory.Reserve(ctx, o) }
func capturePayment(ctx context.Context, o *Order)   { payments.Capture(ctx, o) }
func allocateShipment(ctx context.Context, o *Order) { shipping.Allocate(ctx, o) }
func writeAudit(ctx context.Context, o *Order)       { audit.Write(ctx, o) }

// Confirm waits for all four side effects, in parallel, before confirming the order.
func Confirm(ctx context.Context, repo Repo, o *Order) error {
    // Bound the barrier: never hang forever on one slow downstream.
    ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()

    _ = Confirming.TryEmit(ctx, o) // scatter onto goroutines, join when the last returns

    // Every side effect has completed; safe to flip the order to CONFIRMED.
    o.confirm()
    return repo.Save(ctx, o)
}
```

**Contrast — discarding `TryEmit`'s return hides a failed listener:**

```go
// ❌ Ignoring the return — a failed payment capture is invisible here.
_ = Confirming.TryEmit(ctx, o)
return repo.Save(ctx, o) // confirms even if capturePayment failed silently

// ✅ Inspect the returned (errors.Join'd) error when failures must abort confirmation.
if err := Confirming.TryEmit(ctx, o); err != nil {
    return fmt.Errorf("confirmation side effects failed: %w", err) // errors.Join'd
}
return repo.Save(ctx, o)
```

The symptom of the wrong choice in production: orders confirmed despite a failed payment
capture, because the `TryEmit` return was discarded and "all listeners finished" was
mistaken for "all listeners succeeded." Reach for
[Result Aggregation](../reliability/result-aggregation.md) when outcomes matter.

### Practical Example 2 — Graceful shutdown drain

On `SIGTERM`, a service must let every subsystem flush before the process exits — drain a
metrics buffer, flush a write-ahead log, close database pools — and these are independent,
so run them concurrently but **do not exit until all have finished**. This is the one
dispatch mode fire-and-forget can't give you: it offers no join point to drain on.

```go
package app

import (
    "context"
    "log/slog"
    "os/signal"
    "syscall"
    "time"

    "github.com/maniartech/signals"
)

// A single "we are shutting down" event; each subsystem registers a flush listener.
type Shutdown struct{ Reason string }

var Draining = signals.New[Shutdown]()

func init() {
    signals.SetPanicHandler(func(recovered any) { // ✅ — one bad flush must not abort the drain
        slog.Error("drain listener panicked", "recovered", recovered)
    })

    Draining.AddListener(flushMetrics, "metrics") // push buffered metrics to the collector
    Draining.AddListener(flushWAL, "wal")         // fsync the write-ahead log
    Draining.AddListener(closePools, "db")        // drain and close DB connection pools
}

func flushMetrics(ctx context.Context, _ Shutdown) { metrics.Flush(ctx) }
func flushWAL(ctx context.Context, _ Shutdown)     { wal.Sync(ctx) }
func closePools(ctx context.Context, _ Shutdown)   { db.CloseAll(ctx) }

// Run blocks until a termination signal, then drains every subsystem concurrently before
// returning — at which point main() can exit cleanly.
func Run(ctx context.Context) {
    ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    serve(ctx)   // ... normal operation until a signal arrives ...
    <-ctx.Done() // SIGTERM received

    // Bound the drain: flush hard for at most 15s, then give up and exit anyway.
    drainCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()

    _ = Draining.TryEmit(drainCtx, Shutdown{Reason: "SIGTERM"}) // all flushes run in parallel; join
    slog.Info("drain complete, exiting")                        // reached only after all finished
}
```

The three flushes overlap (drain takes the slowest, not the sum), and `TryEmit`
returns only when the last has finished — so the process never exits mid-flush. The fresh
`context.Background()` deadline is deliberate: the request-scoped `ctx` is already
canceled by the signal, so the drain needs its own bounded budget.

## Variations

- **Error-aggregating join (the default).** `TryEmit` *is* the error-aggregating form: the
  same concurrent scatter-join returns every listener's error joined with `errors.Join`, so
  you can fail the operation when any listener fails —
  [Result Aggregation](../reliability/result-aggregation.md).
- **Error-ignoring join.** Want "wait for all but ignore failures"? Same `TryEmit`, discard
  the return: `_ = sig.TryEmit(ctx, x)`.
- **Bounded fan-out.** Cap the concurrent listeners with `MaxConcurrent` (🔜 v1.4) for
  high-fan-in signals while keeping the completion guarantee —
  [Bounded Concurrency](../flow-control/bounded-concurrency.md).
- **Deadline-bounded barrier.** Wrap with `context.WithTimeout` so the join releases on a
  deadline; cooperative listeners abandon their work when `ctx` is done.
- **Shutdown drain.** A degenerate-but-vital use: emit once with `TryEmit` during
  shutdown to flush all handlers before exit.

## Known Uses

- **Scatter-gather query engines** (search, federated queries) — fan a request to many
  shards, block until all respond, merge.
- **`sync.WaitGroup` / `errgroup.Group`** (Go stdlib & `golang.org/x/sync`) — the canonical
  primitive: launch goroutines, `Wait()` for all; `errgroup` adds the error-join that
  `TryEmit` mirrors.
- **MapReduce / fork-join frameworks** (Java Fork/Join, parallel streams) — split work,
  process concurrently, join at a barrier.
- **`Promise.all` / `Future.sequence`** (JS, Scala) — run async tasks concurrently, resolve
  only when all complete.
- **Graceful shutdown drains** — service meshes and servers emit a drain signal and wait for
  in-flight handlers to finish before terminating.

## Related Patterns

- **[Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md)** — also blocks
  until done, but runs listeners *sequentially in order*. Choose it when order matters; choose
  Await-All when the listeners are independent and you want parallelism.
- **[Fire-and-Forget Dispatch](fire-and-forget-dispatch.md)** — also concurrent, but does
  *not* wait. Await-All is fire-and-forget *plus a join barrier*; it is the loss-intolerant
  counterpart that may slow the producer.
- **[Result Aggregation](../reliability/result-aggregation.md)** — the error-returning view of
  this very method: `TryEmit` collects every listener's error via `errors.Join` behind the
  same concurrency and barrier. Inspect its return whenever outcomes must change your control
  flow.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — cap the concurrent
  fan-out for signals with many listeners or high emit rates.
- **[Backpressure](../flow-control/backpressure.md)** — the broader flow-control pattern of
  the same trade-off: a path allowed to slow the producer rather than drop. Await-All is the
  dispatch-level expression of that choice.
- **[Panic Isolation](../reliability/panic-isolation.md)** — how a panicking listener is
  recovered without breaking the barrier or crashing the process.
