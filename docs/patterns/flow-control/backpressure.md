# Backpressure

**Family:** Flow-Control
· **Also Known As:** Flow Control, Producer Throttling
· **Status:** ✅ v1.4. The v1.4 backpressure path is `TryEmit` (✅) — concurrent
  handlers, waits for all, returns their joined errors. The alternative blocking *policy*
  (`SignalOptions.Overflow = OverflowBlock` on a bounded `Emit`) is 🔭 post-v1.4.

## Intent

Guarantee **zero event loss** under sustained overload by slowing the **producer**
down — making it wait — instead of dropping events. Backpressure is the
loss-intolerant counterpart to [Load Shedding](load-shedding.md): same trilemma,
opposite sacrifice.

> **How v1.4 does backpressure (per [ADR 0001](../../design/0001-async-dispatch-and-error-model.md)).**
> In v1.4 the backpressure path is **`TryEmit`**: the caller waits for the emission's
> handlers to complete (returning their `errors.Join`'d result), so the producer's loop
> self-throttles to the listeners' throughput and no event is lost. This is the
> **shipped, lossless** path for loss-intolerant data. A separate `OverflowBlock` *policy*
> (a bounded `Emit` that blocks for a slot) is **designed but deferred to 🔭 post-v1.4** —
> use `TryEmit` instead today. (Note: v1.4's fire-and-forget `Emit` never drops either —
> it *parks* the excess — but it does not slow the producer, so it is not a backpressure
> path; loss-intolerant work must wait.)

## Motivation

Consider a trade-execution engine. Every time a trade fills, it is published on a
`signals.AsyncSignal[Trade]`, and one listener writes the fill to the **ledger** — the
authoritative record used for settlement, compliance, and the firm's books. Losing a
single fill is not a degraded-quality event; it is a **financial and legal incident**:
the books won't reconcile, a counterparty is owed money that isn't recorded, and an
auditor will eventually find the hole.

Now the ledger's storage has a slow afternoon — replication lag, a failing-over
primary — and writes that took 3ms now take 300ms. The matching engine, however, does
not slow down: a volatile market means fills are arriving faster than ever. If this
stream were fire-and-forget, here is the catastrophe:

```go
var Fills = signals.New[Trade]()
Fills.AddListener(writeToLedger) // now 300ms each; far slower than fills arrive

for t := range fills { // never slows; market is volatile
    Fills.Emit(ctx, t) // fire-and-forget: returns instantly, NEVER throttles the producer
}
```

`Emit` promised the producer would *never wait*. In v1.4 it honours that promise by
**parking** the excess handler work, not by dropping it — but parking does nothing to
*slow the matching engine*. With fills arriving far faster than the ledger drains, the
parked backlog grows without bound: memory climbs until the process is OOM-killed, and
whatever was still parked at that moment is lost with the crash. A future drop policy
([Load Shedding](load-shedding.md), 🔭 post-v1.4) would instead shed fills to stay
bounded — also unacceptable here: every dropped fill is a trade that happened in the
market but does not exist in the firm's books. The fire-and-forget contract simply has
no way to make the producer feel the ledger's slowness, which is exactly what
loss-intolerant data needs.

The fix is to make the *producer* feel the ledger's slowness — to let the backpressure
from a slow consumer **propagate upstream and throttle the source**:

```go
var Fills = signals.NewWithOptions[Trade](&signals.SignalOptions{
    MaxConcurrent: 16, // bound concurrency (optional; caps in-flight ledger writes)
})
Fills.AddListener(writeToLedger)

for t := range fills {
    Fills.TryEmit(ctx, t) // ✅ producer WAITS until the fill is durably written
}
```

Now when the ledger slows, `TryEmit` blocks until the write completes, the
producer's loop self-throttles to the ledger's true throughput, and the channel
feeding `fills` fills up — pushing the slowdown one step further upstream until the
whole pipeline runs at the speed of its slowest *durable* stage. The afternoon costs
you **higher fill-recording latency** — never a **lost fill**. That is exactly the
trade a ledger must make.

## Applicability

**Use this pattern when:**

- The data is **loss-intolerant** — trades, orders, payments, audit entries, anything
  where dropping one is a bug or a financial/legal/compliance incident.
- The producer **can afford to be slowed** — it is not on a latency-critical hot path
  that must never block, and throttling the source is acceptable (or even desirable).
- You want the pipeline to **self-pace to its slowest durable stage** rather than
  outrun it and accumulate risk.

**Avoid it (or prefer another pattern) when:**

- The data is **loss-tolerant** and the producer **must not block** (metrics, presence,
  cache hints) → use [Load Shedding](load-shedding.md). Blocking a hot path to preserve
  a disposable metric is the wrong trade.
- The producer **physically cannot wait** — e.g. it's draining a hardware buffer or a
  UDP socket with no flow control of its own. There is nothing to apply backpressure
  *to*; you must shed instead.
- A blocked producer would **deadlock** — e.g. a listener that re-emits on the same
  bounded+blocking signal (see Implementation, note 5).

## Structure

```
                       MaxConcurrent = N (optional bound on in-flight handlers)
                       ┌──────────────────────────────────────────────┐
  Producer ───TryEmit────▶ [ run this emission's listeners ]           │
   (allowed to wait)        │      │                                  │
        ▲   blocks here ────┘      ├─ slot free ─▶ run listener ──┐    │
        │                          │                              │    │
        │                          └─ all N busy ─▶ WAIT for a slot┘    │
        │                                            (producer parked) │
        └──────── returns only after the work completes ───────────────┘

  v1.4 path: TryEmit — the WAIT is the backpressure (no policy knob).
  The producer's loop runs at the consumer's true throughput.
  Slowness propagates UPSTREAM: the source channel fills, throttling the origin.
  Trilemma corner sacrificed: producer-never-waits (you chose to wait).

  🔭 post-v1.4: an `Overflow = OverflowBlock` policy would give a bounded `Emit` call
  site the same block-on-saturation semantics. NOT in v1.4 — use TryEmit today.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Producer (Emitter)** | Calls a waiting emit variant; **agrees to be slowed** |
| **Signal** | Blocks the producer until a slot is free / the work completes |
| **Concurrency bound** | `MaxConcurrent` (optional) — caps in-flight handlers; with `TryEmit` the producer already waits per emission |
| **Wait path** | `TryEmit` (v1.4) — the caller waits for completion and gets the joined errors; the wait *is* the backpressure |
| **Overflow policy** (🔭 post-v1.4) | `OverflowBlock` — would make a saturated bounded `Emit` wait instead of park/drop. NOT in v1.4 |
| **Listener** | Processes events durably (e.g. writes the ledger); its speed sets the pace |
| **Upstream source** | Receives the propagated backpressure (its buffer fills, it slows too) |

## Collaborations

1. The producer calls `TryEmit(ctx, payload)` (or, 🔭 post-v1.4, a bounded `Emit` under
   `OverflowBlock`) and **agrees that this call may block**.
2. The signal runs the listeners concurrently, up to the `MaxConcurrent` bound.
3. **The producer is parked** until all listeners for this emission complete (under
   `TryEmit`) — or, under the 🔭 post-v1.4 `OverflowBlock` policy, until a slot frees up.
   Either way the producer does not proceed.
4. When the work completes, the call returns and the producer's loop takes its next
   item. Because each iteration waits, the loop **runs at the listeners' throughput**,
   not faster.
5. The producer's own input (a channel, a socket, an upstream call) consequently
   **fills up and slows**, propagating the backpressure one step further toward the
   origin — exactly as a TCP receiver's shrinking window throttles the sender.

## Consequences

**Benefits**

- ✓ **Zero event loss.** No event is ever dropped; every one is processed, just
  possibly later. This is the entire point and the reason loss-intolerant data lives
  here.
- ✓ **Bounded memory.** Because the producer waits rather than queueing ahead, work in
  flight stays bounded by `MaxConcurrent` — no unbounded backlog.
- ✓ **Self-pacing pipeline.** The system automatically runs at the speed of its slowest
  durable stage; no manual rate-limiting needed.
- ✓ **Errors are observable.** `TryEmit` returns the listeners' joined errors,
  so a failed durable write surfaces to the producer instead of vanishing.

**Liabilities**

- ✗ **The producer slows down** — by design. Latency propagates upstream; a hot path
  that cannot tolerate blocking must not use this pattern.
- ✗ **Deadlock / reentrancy risk.** A listener that emits on the same bounded+blocking
  signal can wait for a slot it is itself holding → permanent stall (see
  Implementation).
- ✗ **Stalls propagate.** A wedged listener doesn't just slow this stage — it freezes
  the whole upstream chain. A timeout/cancellation strategy is mandatory.

> **Trilemma corner sacrificed:** Backpressure keeps *bounded memory* and *zero loss*,
> and gives up the *producer-never-waits* guarantee. That sacrifice is the whole point:
> the only way to never lose an event while staying bounded is to let the producer be
> slowed. A method that never makes you wait will, under enough pressure, have to drop
> something — so **loss-intolerant data must travel on a path that is allowed to slow
> you down.**

## Implementation

1. **Real backpressure *requires* slowing the producer — that is the mechanism, not a
   side effect.** Every genuine flow-control system works this way: TCP's congestion
   window shrinks to throttle the *sender*; the LMAX Disruptor makes the *producer*
   wait on a full ring buffer; Kafka's `max.block.ms` blocks the *producer's* `send()`
   when buffers fill. There is no way to guarantee zero loss with bounded memory
   *without* a backward force on the source. If nothing upstream can be slowed, you
   cannot have backpressure — you can only shed.

2. **Therefore fire-and-forget `Emit` structurally cannot provide backpressure.**
   `Emit` made a hard promise: *the producer never waits*. A method that never waits
   has no mechanism to push back on the source. In v1.4 it honours the promise by
   **parking** the excess (never dropping, never blocking the caller) — but parking does
   not throttle the producer, so under *sustained* overload the parked backlog grows
   without bound (and a future drop policy would instead shed — [Load Shedding](load-shedding.md),
   🔭 post-v1.4). Either way `Emit` cannot make the producer feel the consumer's
   slowness. This isn't a missing feature — it is the logical consequence of the
   contract. **Loss-intolerant data must not use `Emit`**; it must use `TryEmit` (and,
   🔭 post-v1.4, a bounded `Emit` under `OverflowBlock`).

3. **The way to get backpressure in v1.4 — `TryEmit`:** the producer's loop
   self-throttles because each iteration *waits for completion* before taking the next
   item. No special policy required — waiting *is* the backpressure. `TryEmit` (✅ v1.4)
   additionally returns the listeners' `errors.Join`'d result so durable-write failures
   surface to the producer.

   - **(🔭 post-v1.4) `SignalOptions.Overflow = OverflowBlock`:** would make an
     otherwise-bounded `Emit` *wait for a free slot* instead of parking, giving an
     `Emit`-shaped call site blocking-on-saturation semantics. Not in v1.4 — use
     `TryEmit` today.

4. **Always pair the (post-v1.4) block policy with a bound.** `OverflowBlock` is
   meaningful only when `MaxConcurrent` is set — the bound is *when* to start waiting.
   (`TryEmit` needs no bound to apply backpressure: it waits per emission
   regardless.) See [Bounded Concurrency](bounded-concurrency.md): bounding is the
   mechanism, blocking is
   the policy layered on it.

5. **Guard against deadlock and reentrancy — the signature failure mode.** If a
   listener on a bounded+blocking signal *emits on that same signal* (directly or
   transitively), it can wait for a slot that it is itself occupying. With all *N* slots
   held by listeners each waiting to re-enter, the signal **deadlocks permanently**.
   Defenses: never re-emit on the same bounded+blocking signal from within its own
   listener; route the re-emission to a *different* signal; or break the cycle with a
   buffered hop. Treat any "listener emits on its own signal" as a red flag here.

6. **Always carry a cancellable `context` and honour deadlines.** Because a stalled
   listener freezes the whole upstream chain, an unbounded wait turns a slow dependency
   into a total outage. Give the emit a `context.WithTimeout`; a canceled context
   short-circuits the emission (no listener runs) so the producer can recover, log, and
   decide whether to retry, buffer durably, or escalate.

7. **Decide what a *bounded* wait does on timeout.** Pure backpressure waits
   indefinitely (correct only if the consumer always eventually drains). In practice you
   usually cap the wait: on timeout, fail loudly (return the error from
   `TryEmit`) and persist the event to a durable fallback (an outbox / WAL)
   rather than dropping it — preserving the zero-loss guarantee through a different
   channel.

8. **Backpressure bounds memory *only if the producer actually yields*.** If the
   producer spawns a new goroutine per emit and *those* don't wait, you've reintroduced
   the unbounded fan-out. The waiting must happen on the **same goroutine that drives
   the source**, so the source itself is throttled.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Producer* that **agrees to be slowed**, the
*Signal* with its (optional) *bound*, and the *Listener* whose speed sets the pace. In
v1.4 the backpressure comes from `TryEmit`, not a policy knob. The defining move
is that the producer's loop *waits for completion*
each iteration, so it runs at the consumer's true throughput and the slowness
propagates upstream. Read this first; the practical examples then apply it.

```go
// 1. SIGNAL: an optional concurrency BOUND caps in-flight handlers. In v1.4 the
//    backpressure comes from TryEmit below (the producer waits per emission),
//    NOT from an overflow policy. `Overflow: OverflowBlock` is 🔭 post-v1.4 and is not
//    needed here — TryEmit already throttles the producer.
sig := signals.NewWithOptions[Record](&signals.SignalOptions{
    MaxConcurrent: 8, // 🔜 v1.4 — optional: ≤ 8 in-flight handlers per emission
})

// 2. LISTENER — the durable work. Its speed sets the pipeline's pace.
sig.AddListenerWithErr(func(ctx context.Context, r Record) error {
    return writeDurably(ctx, r) // e.g. the ledger / audit log; slow under incident
}, "sink") // ✅ v1.4 on async — error-returning listener

// 3. PRODUCER — agrees to be slowed. TryEmit blocks until the work completes,
//    so the loop self-throttles to the consumer's throughput. ZERO loss: nothing is
//    ever dropped; events are processed possibly later, never not at all.
for r := range source {
    if err := sig.TryEmit(ctx, r); err != nil { // ✅ v1.4 — waits; joined errors
        persistFallback(r) // preserve zero-loss through a DIFFERENT channel, not by dropping
    }
    //   ├─ slot free  → run listener, return its error
    //   └─ saturated  → producer PARKS here until a slot frees (backpressure)
}
// Slowness propagates UPSTREAM: while parked, `source` fills and throttles its origin.
```

> **Reentrancy / deadlock caution.** Never let a listener on this signal emit on the
> *same* bounded+blocking signal (directly or transitively). With all *N* slots held by
> listeners each waiting to re-enter, the signal **deadlocks permanently**. Route any
> re-emission to a *different* signal, or break the cycle with a buffered hop — and
> always carry a cancellable `context` so a wedged consumer cannot freeze the chain
> forever (see Implementation, notes 5–6).

### Practical Example 1 — Trade fills to the ledger (never lose a fill)

A trade-execution engine writes every fill to the **ledger** — the authoritative
record for settlement and compliance. Losing one is a financial/legal incident, so the
consuming loop throttles via `TryEmit`, and a durable outbox catches anything
the ledger is too slow to accept within the deadline — zero loss survives even a wedged
ledger.

```go
package execution

import (
    "context"
    "time"

    "github.com/maniartech/signals"
)

type Trade struct {
    ID       string
    Symbol   string
    Qty      int64
    PriceE6  int64 // price × 1e6
}

type Ledger interface {
    Write(ctx context.Context, t Trade) error // durable; slow under incident
}

var fills *signals.AsyncSignal[Trade]

func Init(ledger Ledger, outbox Outbox) {
    // Backpressure comes from TryEmit in Publish (the producer waits per fill);
    // the bound just caps in-flight ledger writes. No overflow policy needed in v1.4.
    fills = signals.NewWithOptions[Trade](&signals.SignalOptions{
        MaxConcurrent: 16, // 🔜 v1.4 — at most 16 ledger writes in flight
    })

    fills.AddListenerWithErr(func(ctx context.Context, t Trade) error {
        return ledger.Write(ctx, t) // its speed sets the pipeline's pace
    }, "ledger")

    _ = outbox // see Publish for the durable fallback
}

// Publish drives the producer loop. TryEmit makes each iteration wait for the
// fill to be durably written, so the loop self-throttles to the ledger's true
// throughput. A bounded context prevents a wedged ledger from freezing forever; on
// timeout we persist to a durable outbox so the fill is still NEVER lost.
func Publish(ctx context.Context, in <-chan Trade, outbox Outbox) {
    for t := range in {
        emitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
        err := fills.TryEmit(emitCtx, t) // ✅ v1.4 — waits; returns joined errors
        cancel()

        if err != nil {
            // Zero-loss preserved through a different channel, not by dropping.
            outbox.Persist(t) // replayed when the ledger recovers
        }
    }
}
```

**Contrast — the loss-tolerant call on loss-intolerant data (a real incident):**

```go
// ❌ Fire-and-forget on trades: Emit promised never to wait, so it can never throttle
//    the matching engine. In v1.4 it PARKS the excess (never drops) — but with fills
//    arriving far faster than the ledger drains, the parked backlog grows unbounded
//    until the process is OOM-killed, losing everything still parked at the crash.
var Fills = signals.New[Trade]()
Fills.AddListener(writeToLedger)
for t := range fills {
    Fills.Emit(ctx, t) // never throttles the producer → unbounded backlog → OOM
}
```

The symptom: during a volatile, high-latency window the ledger falls behind, the parked
backlog and memory climb monotonically, and the process is eventually OOM-killed —
end-of-day reconciliation then surfaces trades that executed in the market but were
never durably recorded. A compliance and settlement incident either way: the cure is to
let the producer *wait* (`TryEmit`), not to fire-and-forget.

### Practical Example 2 — Order events to an immutable audit log

A compliance-sensitive service must record every order lifecycle event (placed,
amended, canceled, filled) to an **append-only audit log** that regulators can inspect.
A gap in the audit trail is itself a violation, so the event is loss-intolerant. A
bounded + blocking signal applies backpressure to the ingest loop: when the audit
store slows, the ingest loop slows with it rather than dropping entries, and the
slowdown propagates back to whatever feeds the loop.

```go
package audit

import (
    "context"
    "time"

    "github.com/maniartech/signals"
)

type OrderEvent struct {
    OrderID string
    Kind    string // "placed" | "amended" | "canceled" | "filled"
    At      time.Time
}

type AuditStore interface {
    Append(ctx context.Context, e OrderEvent) error // durable, append-only; slow under load
}

var events *signals.AsyncSignal[OrderEvent]

func Init(store AuditStore) {
    // Backpressure comes from TryEmit in Ingest (the loop waits per entry);
    // the bound just caps in-flight audit writes. No overflow policy needed in v1.4.
    events = signals.NewWithOptions[OrderEvent](&signals.SignalOptions{
        MaxConcurrent: 8, // 🔜 v1.4 — at most 8 audit writes in flight
    })

    // Reentrancy caution: this listener must NOT emit back onto `events`, or it could
    // wait for a slot it is itself holding and deadlock the signal permanently.
    events.AddListenerWithErr(func(ctx context.Context, e OrderEvent) error {
        return store.Append(ctx, e) // its speed sets the ingest loop's pace
    }, "auditlog") // ✅ v1.4 on async
}

// Ingest drives the producer loop over incoming order events. TryEmit makes
// each iteration wait for the durable append, so the loop self-throttles to the audit
// store's true throughput. The bounded context guards against a wedged store; on
// timeout the entry is escalated, never silently lost.
func Ingest(ctx context.Context, in <-chan OrderEvent) error {
    for e := range in {
        emitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
        err := events.TryEmit(emitCtx, e) // ✅ v1.4 — waits; returns joined errors
        cancel()

        if err != nil {
            return err // surface the failed audit write — do not drop the entry
        }
    }
    return nil
}
```

When the audit store has a slow window, `Ingest` slows in lockstep and its input
channel fills, throttling whatever feeds it — the whole pipeline runs at the speed of
the slowest *durable* stage, and not a single audit entry is lost.

## Variations

- **`TryEmit` self-throttling (the v1.4 path).** The simplest backpressure: just
  wait for completion each iteration. No `OverflowBlock` needed — the wait *is* the
  backpressure. This is what ships in v1.4. The returned `errors.Join` also surfaces a
  failed durable write to the producer so it can retry or escalate, rather than assuming
  success; if you don't care about the errors, simply ignore the result.
- **Bounded `Emit` with `OverflowBlock` (🔭 post-v1.4).** Would keep an `Emit` call site
  but block on saturation — handy when migrating an existing fire-and-forget site to
  loss-intolerant semantics with minimal change. Deferred; use `TryEmit` today.
- **Bounded wait + durable outbox (shown above).** Cap the wait and, on timeout, write
  to a WAL/outbox instead of dropping. Preserves zero-loss without risking an unbounded
  stall.

## Known Uses

- **TCP flow control** — the receiver's advertised window shrinks to *slow the sender*;
  the canonical zero-loss-by-throttling mechanism.
- **LMAX Disruptor** — the producer *waits* when the ring buffer is full rather than
  overwriting unconsumed slots; bounded memory with no loss.
- **Kafka producer `max.block.ms`** — `send()` *blocks the producer* when the accumulator
  buffer is full instead of dropping records.
- **Reactive Streams** (`Flow`, Project Reactor, RxJava, Akka Streams) — the
  `request(n)` protocol is backpressure made explicit: the consumer tells the producer
  how much it may send.
- **Go's unbuffered / bounded channels** — a send on a full channel *blocks the sender*;
  the language's built-in backpressure primitive.

## Related Patterns

- **[Load Shedding](load-shedding.md)** — the exact opposite trade-off on the same
  trilemma: drop to keep the producer non-blocking, for loss-*tolerant* data (🔭 post-v1.4).
  Backpressure waits to keep zero loss, for loss-*intolerant* data, and ships in v1.4 via
  `TryEmit`. Pick by whether losing an event is acceptable.
- **[Bounded Concurrency](bounded-concurrency.md)** — caps in-flight handlers; with
  `TryEmit` the producer already waits per emission so a bound is optional here. The
  🔭 post-v1.4 `OverflowBlock` *policy* would layer block-on-saturation onto a bound.
- **[Await-All Dispatch](../dispatch/await-all-dispatch.md)** — `TryEmit` is both
  the await-all dispatch mode *and* the natural backpressure path; a loop that awaits
  each emission self-throttles.
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** — the path
  that structurally *cannot* provide backpressure (it promised never to wait); the
  reason loss-intolerant data must avoid it.
- **[Result Aggregation](../reliability/result-aggregation.md)** — `TryEmit`
  collects every listener's joined error, letting a backpressured producer detect and
  react to failed durable writes.
