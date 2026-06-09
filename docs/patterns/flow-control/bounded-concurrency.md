# Bounded Concurrency

**Family:** Flow-Control
· **Also Known As:** Worker Limiting, Concurrency Cap, Semaphore Dispatch
· **Status:** `SignalOptions.MaxConcurrent` and `DefaultMaxConcurrent()` are 🔜 v1.4.
  A hard backlog ceiling (overflow mode H1) is 🔭 post-v1.4.

## Intent

Cap the number of async listeners running **at once** so that a fast emitter with
slow listeners **cannot run unbounded handler goroutines concurrently** and OOM the
process. The cap turns "every handler runs immediately, no matter how many" into "at
most *N* handlers run at any instant; the excess **parks** cheaply until a slot
frees."

> **What a bound does and does not do (ADR 0001).** `MaxConcurrent` is a counting
> semaphore that bounds *concurrent* handler execution. When all *N* slots are held,
> excess handler work **parks** (cheaply, ~2 KB idle) until a slot frees — it is
> **never dropped** and the caller is **never blocked** (`Emit` already returned; the
> parking happens in the background dispatcher goroutine). A bound caps *execution*,
> not the *backlog*: under sustained overload the parked backlog can still grow (the
> honest caveat below). A hard ceiling on the backlog (drop or block on overflow) is
> 🔭 post-v1.4 — see [Load Shedding](load-shedding.md).

## Motivation

Consider an order-ingestion service. Each accepted order is published on a
`signals.AsyncSignal[Order]`, and one listener writes the order to the primary
database. The database connection pool is sized at 50 connections — a hard limit set
by the DBA. On a calm day the writes complete in a few milliseconds and nobody
notices anything.

Then a marketing email goes out and traffic spikes 40×. With the naive default — a
fresh goroutine per listener per emit — the producer keeps accepting orders at full
speed while each database write now queues behind the others:

```go
var Orders = signals.New[Order]()
Orders.AddListener(persistOrder) // each write contends for 1 of 50 DB connections

for o := range acceptedOrders { // spikes to tens of thousands/sec
    Orders.Emit(ctx, o) // spawns a goroutine that blocks on the DB pool
}
```

Every emit spawns a goroutine. Each goroutine tries to grab a database connection;
only 50 succeed, the rest **pile up waiting**. Within seconds there are 80,000 live
goroutines, all parked on the connection pool's internal lock, each pinning an
`Order` and a stack in memory. The connection pool's wait queue grows without bound,
write latency climbs into the tens of seconds, the garbage collector thrashes over
the goroutine stacks, and the process is eventually **OOM-killed** — taking the order
service down at the exact moment it is busiest. The database itself was never the
bottleneck that killed you; the *unbounded fan-out in front of it* was.

The root cause is that the number of concurrent listener invocations is governed by
the *producer's* rate, not by what the *downstream* can absorb. Bounded Concurrency
fixes this by introducing a hard ceiling on simultaneous invocations:

```go
var Orders = signals.NewWithOptions[Order](&signals.SignalOptions{
    MaxConcurrent: 50, // never more than 50 writes in flight — matches the DB pool
})
Orders.AddListener(persistOrder)

for o := range acceptedOrders {
    Orders.TryEmit(ctx, o) // at most 50 concurrent; the loop self-paces
}
```

Now at most 50 listener invocations run at once. The number of *concurrently
executing* handlers has a hard ceiling no matter how fast orders arrive, and the
service survives the spike at a steady, predictable throughput. The *excess* arrivals
once the bound is saturated are not lost and do not block the caller — in v1.4 they
**park** cheaply until a slot frees (the `TryEmit` loop above also self-paces, so
the producer never races ahead of the bound). A *hard ceiling on the parked backlog*
— dropping the excess ([Load Shedding](load-shedding.md), 🔭 post-v1.4) or blocking on
a bounded `Emit` ([Backpressure](backpressure.md), 🔭 post-v1.4) — is a separate, later
decision; bounding the concurrency is the foundation that makes either choice possible.

> **The decisive trade-off: a bound can *starve* long-running listeners.** This is the
> reason v1.4's default is **unbounded**, not a default bound. If you set
> `MaxConcurrent: N` and your listeners are long-running (a streaming RPC, a tail
> follower, a subscription that lives for minutes), the first *N* handlers can hold all
> the slots indefinitely and the remaining listeners **never start** — they park
> forever behind handlers that never return. An unbounded default guarantees every
> listener at least *runs*; a bound trades that guarantee for resource ceilings. Choose
> a bound only when your handlers are *short-lived* relative to the emit rate.

## Applicability

**Use this pattern when:**

- Async listeners are **slower than the producer** can emit, so a naive
  goroutine-per-emit model would accumulate goroutines without limit.
- You must **protect a downstream dependency** with a finite capacity — a database
  connection pool, an external API rate limit, a disk, a thread-bounded service.
- You need **predictable, bounded resource use** — embedded, real-time, or any system
  that must not OOM under bursty or adversarial input.
- You are about to apply [Load Shedding](load-shedding.md) or
  [Backpressure](backpressure.md): both *require* a bound to exist first.

**Avoid it (or prefer another pattern) when:**

- Listeners are trivially fast and the producer can never realistically outrun them —
  the default unbounded dispatch is fine and a bound only adds tuning surface.
- You need strict ordering — async dispatch (bounded or not) makes no ordering
  guarantee → use [Synchronous Sequential Dispatch](../dispatch/synchronous-sequential-dispatch.md).
- You only ever emit a handful of events total — bounding solves a load problem you
  don't have.

## Structure

```
                       MaxConcurrent = N  (counting semaphore, N slots)
                       ┌──────────────────────────────────────────┐
  Producer ──Emit──▶  (one dispatcher goroutine, in the background)│
   (returns at once)   │                                          │
                       │  for each listener: acquire a slot        │
                       │      ├─ slot free ─▶ spawn goroutine ─▶ run listener
                       │      │                   └─ on return: release slot
                       │      │
                       │      └─ all N held ─▶ PARK cheaply until a slot frees
                       │                         (no drop, caller NOT blocked)
                       └──────────────────────────────────────────┘

  At any instant: CONCURRENTLY running listener goroutines ≤ N.
  Excess handler work parks in the background dispatcher; it is never dropped.
  When the signal is idle: ZERO goroutines alive — nothing to shut down, nothing to leak.
  Effective concurrency = min(N, len(listeners)).

  🔭 post-v1.4: a hard backlog ceiling (drop → Load Shedding, or block → Backpressure)
  would replace "park forever" with an explicit overflow policy. NOT in v1.4.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Producer (Emitter)** | Calls an emit variant to publish a payload |
| **Signal** | Acquires a semaphore slot before spawning each listener goroutine |
| **Counting semaphore** | Holds `MaxConcurrent` slots; the hard ceiling on concurrency |
| **Listener goroutine** | Spawned per admitted invocation; releases its slot when it returns |
| **Parked dispatch** | When all slots are held, excess handler spawns park (cheaply) in the background dispatcher until a slot frees — not dropped, caller not blocked |
| **Overflow policy** (🔭 post-v1.4) | Would decide the fate of an emission when the semaphore is saturated (drop vs. block) instead of parking. NOT in v1.4 |
| **Listener** | Processes the payload; oblivious to the bound |

## Collaborations

1. The producer calls an emit variant with a payload.
2. For each registered listener, the signal attempts to **acquire one slot** from the
   counting semaphore (capacity `MaxConcurrent`).
3. **If a slot is acquired:** the signal spawns a goroutine that runs the listener and
   **releases the slot when the listener returns** (whether it completes, errors, or
   panics — release is guaranteed).
4. **If the semaphore is saturated** (all *N* slots held): in v1.4 the excess handler
   spawn **parks** cheaply in the background dispatcher until a slot frees — nothing is
   dropped and the caller (whose `Emit` already returned) is never blocked. An explicit
   overflow *policy* that instead discards ([Load Shedding](load-shedding.md)) or blocks
   ([Backpressure](backpressure.md)) is 🔭 post-v1.4.
5. As running listeners return, slots are released and parked/subsequent invocations
   proceed. The system self-balances at a concurrency of exactly `min(N, listeners)`.

## Consequences

**Benefits**

- ✓ **Hard ceiling on *concurrent* execution.** No matter how fast the producer emits,
  at most *N* listener goroutines *run at once* — no concurrent pile-up overwhelming a
  downstream. (This bounds execution, not the backlog — see Liabilities.)
- ✓ **Downstream protection.** Sizing the bound to a dependency's capacity (DB pool,
  API limit) prevents the signal from overwhelming it.
- ✓ **No event loss, no caller blocking.** Excess work parks cheaply instead of being
  dropped, and `Emit` still returns immediately — the fire-and-forget contract holds.
- ✓ **No lifecycle to manage.** Because slots gate goroutine *spawning* rather than
  feeding a persistent pool, an idle signal holds zero goroutines (see Implementation).

**Liabilities**

- ✗ **Bounds concurrency, not the backlog.** Under *sustained* overload, parked
  dispatch accumulates without a hard limit (cheap, ~2 KB, idle — but unbounded). A
  hard backlog ceiling needs an explicit drop/block overflow policy, which is
  🔭 post-v1.4. This is the trilemma cost v1.4 accepts (see below).
- ✗ **A bound can starve long-running listeners.** If handlers do not return promptly,
  the first *N* hold every slot and the rest never start. This is the decisive reason
  the v1.4 default is **unbounded** — only set a bound when handlers are short-lived.
- ✗ **Tuning required.** Too small a bound throttles throughput needlessly; too large
  weakens the protection. The right value depends on the workload (see Implementation).
- ✗ **Not free of goroutine churn.** The semaphore bounds *concurrency* but still
  allocates a goroutine per invocation — it does not amortize goroutine creation the
  way a persistent worker pool would (honest caveat in Implementation).

> **Trilemma corner (per [ADR 0001](../../design/0001-async-dispatch-and-error-model.md)):**
> v1.4 keeps *never block the caller* and *never lose an event*, and therefore gives up
> *bounded memory* under sustained overload — excess handlers **park** rather than being
> dropped or blocking the producer. A bound caps how many handlers *execute* at once; it
> does **not** cap the parked backlog. Adding that hard ceiling (an opt-in drop or block
> overflow policy) is 🔭 post-v1.4.

## Implementation

1. **A counting semaphore, not a persistent worker pool.** This is the agreed v1.4
   design and the key thing to understand. The bound is enforced by acquiring one slot
   of a counting semaphore *before* spawning a goroutine per listener-invocation, and
   releasing the slot when that goroutine returns. There is **no standing pool of
   worker goroutines** waiting for jobs.

2. **Consequence — zero persistent/idle goroutines.** Because slots gate *spawning*
   rather than dispatching to long-lived workers, when the signal is idle there are
   **no goroutines alive at all**. Nothing is parked waiting for work.

3. **Consequence — no `Close()` method, and no leak.** Since there are no background
   workers to shut down, the type needs no `Close()`/`Stop()` lifecycle call. An idle
   or abandoned signal is simply **garbage-collected** like any other value — there is
   nothing left running to keep it alive or to leak. This is a deliberate ergonomic
   win: you can create signals freely (per-request, per-tenant) without remembering to
   tear them down.

4. **Effective concurrency is `min(N, len(listeners))`, automatically.** If you set
   `MaxConcurrent: 64` but register only 3 listeners, you get at most 3 concurrent
   invocations per emit — the bound never forces extra concurrency, it only caps it.

5. **The default is UNBOUNDED, by design.** When `MaxConcurrent` is left zero/unset,
   dispatch is **unbounded** — one goroutine per handler, none parked. v1.4 deliberately
   does **not** apply a default bound, because any bound can *starve long-running
   listeners* (note 2 above and the Liabilities): a silent default could make some
   listeners never run. If you want bounding without choosing a number, opt in with
   `DefaultMaxConcurrent()` (🔜 v1.4 — returns `2 * runtime.NumCPU()`), the
   *recommended* value; it is recommended, not automatic.

   ```go
   // Opt in to the recommended bound explicitly — unset stays unbounded.
   sig := signals.NewWithOptions[T](&signals.SignalOptions{
       MaxConcurrent: signals.DefaultMaxConcurrent(), // 🔜 v1.4 — = 2*NumCPU
   })
   ```

6. **`MaxConcurrent` ≠ subscriber count — size to the *bottleneck*, not the listener count.**
   The bound is "how much concurrency my dependencies can absorb," never "how many
   listeners (or events) there are." A worked example: suppose 100 listeners all write
   to a database fronted by **20** connections.
   - Set `MaxConcurrent: 20` (match the DB pool) → at most 20 writes contend for 20
     connections; the other 80 handler spawns **park** until a connection frees. The
     bound *is* the protection.
   - Set `MaxConcurrent: 100` (= subscriber count) → all 100 run at once and 80 of them
     immediately pile up *inside* the 20-connection pool's wait queue. A bound equal to
     the subscriber count is **no cap at all** — you are back to unbounded fan-out onto
     the real bottleneck. The whole point of the bound is to be *smaller* than the work
     it protects.

7. **Sizing guidance — match the bottleneck, not the producer:**
   - **CPU-bound listeners** (parsing, hashing, compression): `runtime.NumCPU()`. More
     goroutines than cores just adds scheduler churn for no throughput.
   - **IO-bound listeners** (network, disk): a **small multiple** of `NumCPU` (e.g.
     `2×`–`8×`) so requests overlap while they wait on IO.
   - **Protecting a specific dependency:** **match its capacity** — e.g. the database
     connection pool's max connections, or an upstream API's concurrent-request limit.
     The bound should be "what the downstream can absorb," never "how many events
     arrive."

8. **Honest caveat — a true pool is a possible future optimization.** Because each
   invocation still allocates a goroutine, the semaphore design does not *amortize*
   goroutine creation; under extreme throughput that allocation is measurable. A
   persistent worker pool would amortize it, at the cost of a lifecycle (`Close`) and
   idle goroutines. The semaphore design is chosen first for its simplicity and
   leak-freedom; a pool is a candidate optimization **gated on benchmarks**, not a
   promise.

9. **The bound is a *mechanism*; what to do when it saturates is a separate question.**
   This pattern stops at "no more than *N* at once; the excess parks (no drop, no
   caller-block)." In v1.4, parking *is* the saturation behavior — there is no overflow
   knob. The 🔭 post-v1.4 overflow policies would let you *replace* parking with an
   explicit choice: `Overflow: OverflowDropNewest` → [Load Shedding](load-shedding.md),
   or `Overflow: OverflowBlock` → [Backpressure](backpressure.md). Until then, the
   loss-intolerant path is `TryEmit` (see Backpressure), and there
   is no drop-by-default behavior to opt out of.

10. **Canceled context still short-circuits.** As with every emit variant, if
   `ctx.Err() != nil` at emit time, no listener runs and no slot is acquired — the
   bound never interferes with cancellation semantics.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Producer*, the *Signal* with its *counting
semaphore* bound, and the *Listener*. The bound is a counting semaphore that gates
goroutine *spawning*: at most `MaxConcurrent` listener goroutines are alive at any
instant, and when idle the signal holds **zero** goroutines (no pool, no `Close()`).
Read this first to see the mechanics; the practical examples then apply it.

```go
// 1. SIGNAL with a concurrency BOUND (the counting semaphore: N slots).
sig := signals.NewWithOptions[Job](&signals.SignalOptions{
    MaxConcurrent: 8, // 🔜 v1.4 — the bound: ≤ 8 listener goroutines at once
})

// 2. LISTENER — the admitted work. Oblivious to the bound.
sig.AddListener(func(ctx context.Context, j Job) {
    process(j) // a slot is held for exactly the duration of this call, then released
}, "worker")

// 3. PRODUCER — a self-pacing loop. TryEmit blocks until this emission's
//    listeners complete, so the loop runs at the bounded throughput, not faster.
for j := range jobs {
    sig.TryEmit(ctx, j) // ✅ concurrent listeners, ≤ 8 at once
    //   ├─ slot acquired → spawn goroutine → run listener → release slot on return
    //   └─ all N held    → next spawn waits for a slot to free (min(N, listeners))
}
// When `jobs` is drained and the last listener returns, ZERO goroutines remain alive:
// nothing to shut down, nothing to leak — the signal is simply GC-able.
```

The acquire/release of a semaphore slot around each listener goroutine (the heart of
step 3) is what turns "every handler runs at once" into "at most *N* run at once; the
excess parks." In v1.4 a saturated bound **parks** the excess (no drop, no
caller-block). Replacing parking with an explicit drop or block policy is 🔭 post-v1.4
— see [Load Shedding](load-shedding.md) and [Backpressure](backpressure.md). For
lossless backpressure today, drive the bounded signal with `TryEmit` as shown.

### Practical Example 1 — Order writes bounded to the DB connection pool

An order-ingestion service writes every accepted order to its primary database. The
pool is capped at 50 connections (a hard DBA limit), so listener concurrency must be
bounded to **exactly** that — otherwise a traffic spike spawns tens of thousands of
goroutines all parked on the 50-connection pool, and the box OOMs while the database
itself sits healthy.

```go
package orders

import (
    "context"
    "database/sql"

    "github.com/maniartech/signals"
)

type Order struct {
    ID     string
    UserID string
    Total  int64 // cents
}

// The DB pool is the bottleneck we must not overwhelm.
const dbMaxConns = 50

var persisted *signals.AsyncSignal[Order]

func Init(db *sql.DB) {
    db.SetMaxOpenConns(dbMaxConns)

    // Bound listener concurrency to exactly what the pool can serve.
    // No Close() needed: when idle, this signal holds zero goroutines and is GC-able.
    persisted = signals.NewWithOptions[Order](&signals.SignalOptions{
        MaxConcurrent: dbMaxConns, // 🔜 v1.4 — at most 50 writes in flight
    })

    persisted.AddListener(func(ctx context.Context, o Order) {
        _, _ = db.ExecContext(ctx,
            `INSERT INTO orders (id, user_id, total) VALUES ($1, $2, $3)`,
            o.ID, o.UserID, o.Total)
    }, "persist")
}

// Publish runs the producer loop. TryEmit makes each iteration wait for the
// batch to complete, so the loop naturally self-paces to the bounded throughput
// instead of racing ahead and queueing unbounded work.
func Publish(ctx context.Context, accepted <-chan Order) {
    for o := range accepted {
        persisted.TryEmit(ctx, o) // ✅ concurrent listeners, ≤ 50 at once
    }
}
```

**Contrast — unbounded fan-out that survives load tests and dies in production:**

```go
// ❌ No bound: the producer's rate, not the DB pool, governs concurrency.
var Orders = signals.New[Order]()
Orders.AddListener(persistOrder)
for o := range accepted {
    Orders.Emit(ctx, o) // 80,000 goroutines all parked on the 50-conn pool → OOM
}
```

The symptom in production: goroutine count climbs into the tens of thousands the
moment the database slows, memory grows monotonically, and the process is OOM-killed
while the database itself sits at a healthy 50 busy connections.

### Practical Example 2 — Outbound calls bounded to a third-party API's cap

A firehose of domain events fans out to a listener that enriches each one by calling a
rate-limited third-party API (an address-verification or fraud-scoring vendor). The
vendor's contract permits at most 20 concurrent requests; exceed it and they return
`429`s and may throttle the whole account. Bounding listener concurrency to the
vendor's cap means the signal **physically cannot** issue more than 20 in-flight calls,
no matter how fast events arrive — the bound, not hope, enforces the contract.

```go
package enrich

import (
    "context"

    "github.com/maniartech/signals"
)

type Event struct {
    ID      string
    Address string
}

// The vendor's published concurrency limit — our hard ceiling.
const vendorMaxConcurrent = 20

var enriched *signals.AsyncSignal[Event]

func Init(vendor VendorClient) {
    // Match the bound to the dependency's capacity, not to the event rate.
    // No standing pool: idle between bursts, this signal holds zero goroutines.
    enriched = signals.NewWithOptions[Event](&signals.SignalOptions{
        MaxConcurrent: vendorMaxConcurrent, // 🔜 v1.4 — never more than 20 calls in flight
    })

    enriched.AddListener(func(ctx context.Context, e Event) {
        // At most 20 of these run at once, so the vendor never sees > 20 concurrent
        // requests from us — we stay inside the contract by construction.
        _ = vendor.Verify(ctx, e.Address)
    }, "verify")
}

// Pump drives the producer loop over the event firehose. TryEmit makes each
// iteration wait for its enrichment to finish, so the loop self-paces to the
// vendor's true throughput rather than racing ahead and queueing unbounded calls.
func Pump(ctx context.Context, firehose <-chan Event) {
    for e := range firehose {
        enriched.TryEmit(ctx, e) // ✅ concurrent listeners, ≤ 20 at once
    }
}
```

When the vendor slows, the loop self-throttles to whatever throughput keeps ≤ 20
calls in flight; goroutine count and memory stay flat, and you never trip the vendor's
rate limiter by fanning out faster than the contract allows.

## Variations

- **CPU-bound cap (`NumCPU`).** For compute listeners, size the bound to the core
  count; extra goroutines only add scheduler overhead.
- **Per-dependency cap.** When a single signal fans out to multiple downstreams with
  different limits, split into separate signals each bounded to its own dependency
  rather than one bound that fits none of them well.
- **Bound + wait = Backpressure (v1.4).** Drive the bounded signal with
  `TryEmit` so the producer's loop self-throttles to the listeners'
  throughput — the lossless path that ships in v1.4. See
  [Backpressure](backpressure.md).
- **Bound + drop = Load Shedding (🔭 post-v1.4).** A future `Overflow: OverflowDropNewest`
  would shed the excess instead of parking it when saturated — see
  [Load Shedding](load-shedding.md). Not in v1.4.
- **Bound + block policy (🔭 post-v1.4).** A future `Overflow: OverflowBlock` would make a
  bounded `Emit` call site wait for a slot — see [Backpressure](backpressure.md). Not in
  v1.4; use `TryEmit` today.

## Known Uses

- **Database connection pools** (`database/sql` `SetMaxOpenConns`, HikariCP) — the
  canonical bound: never run more queries concurrently than the pool can serve.
- **Go's `golang.org/x/sync/semaphore` and the buffered-channel-as-semaphore idiom** —
  the standard Go way to cap concurrent goroutines; this pattern packages it into the
  signal.
- **Worker-limiting libraries** (`errgroup.SetLimit`, `ants`, `tunny`) — bound the
  number of concurrent tasks against a resource.
- **HTTP server connection limits / thread pools** (Tomcat, nginx `worker_connections`)
  — cap concurrent request handling to protect the box.
- **Operating-system file-descriptor and thread limits** — the OS itself bounds
  concurrency to keep the kernel's resource use predictable.

## Related Patterns

- **[Load Shedding](load-shedding.md)** — the 🔭 post-v1.4 *drop* policy that would layer
  on this bound: when the *N* slots are saturated, discard and count the excess instead
  of parking it. Bounded Concurrency is its prerequisite — there is nothing to shed
  without a bound. Not in v1.4.
- **[Backpressure](backpressure.md)** — the *wait* path for this bound: drive it with
  `TryEmit` (v1.4) so the producer self-throttles instead of
  letting the backlog grow. A bounded `Emit` blocking *policy* (`OverflowBlock`) is
  🔭 post-v1.4. Bounding is the *mechanism*; the saturation behavior is the *policy*.
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** — the
  unbounded default this pattern tames; the bound caps *concurrent* execution while
  keeping fire-and-forget's non-blocking contract (excess parks, never blocks the
  caller).
- **[Await-All Dispatch](../dispatch/await-all-dispatch.md)** — `TryEmit` combined
  with a bound gives a self-pacing producer loop, as in the sample above.
- **[Result Aggregation](../reliability/result-aggregation.md)** — when bounded
  concurrent listeners can each fail, collect their joined errors via
  `TryEmit`.
