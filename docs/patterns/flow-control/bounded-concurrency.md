# Bounded Concurrency

**Family:** Flow-Control
· **Also Known As:** Worker Limiting, Concurrency Cap, Semaphore Dispatch
· **Status:** 🔜 v1.4 (`SignalOptions.WorkerPoolSize`)

## Intent

Cap the number of async listeners running simultaneously so that a fast emitter
with slow listeners **cannot spawn unbounded goroutines** and OOM the process. The
cap turns "one goroutine per emission, forever" into "at most *N* goroutines alive at
any instant."

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
    WorkerPoolSize: 50, // never more than 50 writes in flight — matches the DB pool
})
Orders.AddListener(persistOrder)

for o := range acceptedOrders {
    Orders.EmitAndWait(ctx, o) // at most 50 concurrent; the loop self-paces
}
```

Now at most 50 listener invocations run at once. The goroutine count has a hard
ceiling no matter how fast orders arrive, memory stays flat, and the service survives
the spike at a steady, predictable throughput. What happens to the *excess* arrivals
once the bound is saturated — drop them or wait — is a separate decision (see
Applicability and Related Patterns); bounding the concurrency is the foundation that
makes either choice possible.

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
                       WorkerPoolSize = N  (counting semaphore, N slots)
                       ┌──────────────────────────────────────────┐
  Producer ──Emit──▶  [ acquire a slot ]                          │
                       │      │                                    │
                       │      ├─ slot acquired ─▶ spawn goroutine ─▶ run listener
                       │      │                        └─ on return: release slot
                       │      │
                       │      └─ no slot free ─▶ Overflow policy decides:
                       │                            ├─ drop   (see Load Shedding)
                       │                            └─ wait   (see Backpressure)
                       └──────────────────────────────────────────┘

  At any instant: live listener goroutines ≤ N.
  When the signal is idle: ZERO goroutines alive — nothing to shut down, nothing to leak.
  Effective concurrency = min(N, len(listeners)).
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Producer (Emitter)** | Calls an emit variant to publish a payload |
| **Signal** | Acquires a semaphore slot before spawning each listener goroutine |
| **Counting semaphore** | Holds `WorkerPoolSize` slots; the hard ceiling on concurrency |
| **Listener goroutine** | Spawned per admitted invocation; releases its slot when it returns |
| **Overflow policy** | Decides the fate of an emission that finds the semaphore saturated (drop vs. wait) |
| **Listener** | Processes the payload; oblivious to the bound |

## Collaborations

1. The producer calls an emit variant with a payload.
2. For each registered listener, the signal attempts to **acquire one slot** from the
   counting semaphore (capacity `WorkerPoolSize`).
3. **If a slot is acquired:** the signal spawns a goroutine that runs the listener and
   **releases the slot when the listener returns** (whether it completes, errors, or
   panics — release is guaranteed).
4. **If the semaphore is saturated** (all *N* slots held): the configured `Overflow`
   policy decides — discard the work ([Load Shedding](load-shedding.md)) or make the
   caller wait for a slot ([Backpressure](backpressure.md)).
5. As running listeners return, slots are released and waiting/subsequent invocations
   proceed. The system self-balances at a concurrency of exactly `min(N, listeners)`.

## Consequences

**Benefits**

- ✓ **Hard ceiling on goroutines and memory.** No matter how fast the producer emits,
  at most *N* listener goroutines are alive — no pile-up, no meltdown.
- ✓ **Downstream protection.** Sizing the bound to a dependency's capacity (DB pool,
  API limit) prevents the signal from overwhelming it.
- ✓ **The foundation for flow-control policy.** Once a bound exists, you can choose to
  *drop* or *wait* on overflow. Without it, neither policy has anything to act on.
- ✓ **No lifecycle to manage.** Because slots gate goroutine *spawning* rather than
  feeding a persistent pool, an idle signal holds zero goroutines (see Implementation).

**Liabilities**

- ✗ **Tuning required.** Too small a bound throttles throughput needlessly; too large
  weakens the protection. The right value depends on the workload (see Implementation).
- ✗ **Not free of goroutine churn.** The semaphore bounds *concurrency* but still
  allocates a goroutine per invocation — it does not amortize goroutine creation the
  way a persistent worker pool would (honest caveat in Implementation).
- ✗ **A bound alone does not decide loss vs. wait.** It is the *mechanism*; you still
  must pick the *policy* ([Load Shedding](load-shedding.md) or
  [Backpressure](backpressure.md)).

> **Trilemma corner:** Bounded Concurrency by itself only guarantees *bounded
> memory*. Whether it additionally preserves *never-wait* (by dropping) or *never-lose*
> (by waiting) is decided by the overflow policy layered on top. Bounding is the
> precondition for making that choice meaningful.

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
   `WorkerPoolSize: 64` but register only 3 listeners, you get at most 3 concurrent
   invocations per emit — the bound never forces extra concurrency, it only caps it.

5. **Default is `2 * runtime.NumCPU()`.** When `WorkerPoolSize` is left zero, the
   signal uses `2 * runtime.NumCPU()` — a reasonable middle ground that allows some IO
   overlap without unbounded fan-out. Override it deliberately for your workload.

6. **Sizing guidance — match the bottleneck, not the producer:**
   - **CPU-bound listeners** (parsing, hashing, compression): `runtime.NumCPU()`. More
     goroutines than cores just adds scheduler churn for no throughput.
   - **IO-bound listeners** (network, disk): a **small multiple** of `NumCPU` (e.g.
     `2×`–`8×`) so requests overlap while they wait on IO.
   - **Protecting a specific dependency:** **match its capacity** — e.g. the database
     connection pool's max connections, or an upstream API's concurrent-request limit.
     The bound should be "what the downstream can absorb," never "how many events
     arrive."

7. **Honest caveat — a true pool is a possible future optimization.** Because each
   invocation still allocates a goroutine, the semaphore design does not *amortize*
   goroutine creation; under extreme throughput that allocation is measurable. A
   persistent worker pool would amortize it, at the cost of a lifecycle (`Close`) and
   idle goroutines. The semaphore design is chosen first for its simplicity and
   leak-freedom; a pool is a candidate optimization **gated on benchmarks**, not a
   promise.

8. **The bound is a *mechanism*; shedding vs. blocking is the *policy*.** This pattern
   stops at "no more than *N* at once." Pairing it with `Overflow: OverflowDropNewest`
   gives [Load Shedding](load-shedding.md); pairing it with `Overflow: OverflowBlock`
   (or using `EmitAndWait`) gives [Backpressure](backpressure.md). Always make the
   policy choice explicit — the default (`OverflowDropNewest`) is loss-tolerant.

9. **Canceled context still short-circuits.** As with every emit variant, if
   `ctx.Err() != nil` at emit time, no listener runs and no slot is acquired — the
   bound never interferes with cancellation semantics.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Producer*, the *Signal* with its *counting
semaphore* bound, and the *Listener*. The bound is a counting semaphore that gates
goroutine *spawning*: at most `WorkerPoolSize` listener goroutines are alive at any
instant, and when idle the signal holds **zero** goroutines (no pool, no `Close()`).
Read this first to see the mechanics; the practical examples then apply it.

```go
// 1. SIGNAL with a concurrency BOUND (the counting semaphore: N slots).
sig := signals.NewWithOptions[Job](&signals.SignalOptions{
    WorkerPoolSize: 8, // 🔜 v1.4 — the bound: ≤ 8 listener goroutines at once
})

// 2. LISTENER — the admitted work. Oblivious to the bound.
sig.AddListener(func(ctx context.Context, j Job) {
    process(j) // a slot is held for exactly the duration of this call, then released
}, "worker")

// 3. PRODUCER — a self-pacing loop. EmitAndWait blocks until this emission's
//    listeners complete, so the loop runs at the bounded throughput, not faster.
for j := range jobs {
    sig.EmitAndWait(ctx, j) // ✅ concurrent listeners, ≤ 8 at once
    //   ├─ slot acquired → spawn goroutine → run listener → release slot on return
    //   └─ all N held    → next spawn waits for a slot to free (min(N, listeners))
}
// When `jobs` is drained and the last listener returns, ZERO goroutines remain alive:
// nothing to shut down, nothing to leak — the signal is simply GC-able.
```

The acquire/release of a semaphore slot around each listener goroutine (the heart of
step 3) is what turns "one goroutine per emission, forever" into "at most *N* alive at
any instant." Whether a *saturated* bound drops or waits is the *policy* layered on
top — see [Load Shedding](load-shedding.md) and [Backpressure](backpressure.md).

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
        WorkerPoolSize: dbMaxConns, // 🔜 v1.4 — at most 50 writes in flight
    })

    persisted.AddListener(func(ctx context.Context, o Order) {
        _, _ = db.ExecContext(ctx,
            `INSERT INTO orders (id, user_id, total) VALUES ($1, $2, $3)`,
            o.ID, o.UserID, o.Total)
    }, "persist")
}

// Publish runs the producer loop. EmitAndWait makes each iteration wait for the
// batch to complete, so the loop naturally self-paces to the bounded throughput
// instead of racing ahead and queueing unbounded work.
func Publish(ctx context.Context, accepted <-chan Order) {
    for o := range accepted {
        persisted.EmitAndWait(ctx, o) // ✅ concurrent listeners, ≤ 50 at once
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
        WorkerPoolSize: vendorMaxConcurrent, // 🔜 v1.4 — never more than 20 calls in flight
    })

    enriched.AddListener(func(ctx context.Context, e Event) {
        // At most 20 of these run at once, so the vendor never sees > 20 concurrent
        // requests from us — we stay inside the contract by construction.
        _ = vendor.Verify(ctx, e.Address)
    }, "verify")
}

// Pump drives the producer loop over the event firehose. EmitAndWait makes each
// iteration wait for its enrichment to finish, so the loop self-paces to the
// vendor's true throughput rather than racing ahead and queueing unbounded calls.
func Pump(ctx context.Context, firehose <-chan Event) {
    for e := range firehose {
        enriched.EmitAndWait(ctx, e) // ✅ concurrent listeners, ≤ 20 at once
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
- **Bound + drop = Load Shedding.** Add `Overflow: OverflowDropNewest` to shed the
  excess when saturated — see [Load Shedding](load-shedding.md).
- **Bound + wait = Backpressure.** Add `Overflow: OverflowBlock` (or use
  `EmitAndWait`) to make the producer wait for a slot — see
  [Backpressure](backpressure.md).

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

- **[Load Shedding](load-shedding.md)** — the *drop* policy layered on this bound: when
  the *N* slots are saturated, discard and count the excess. Bounded Concurrency is its
  prerequisite — there is nothing to shed without a bound.
- **[Backpressure](backpressure.md)** — the *wait* policy layered on this bound: when
  saturated, slow the producer instead of dropping. Also requires this bound to exist
  first. Shedding vs. blocking is the *policy*; bounding is the *mechanism* shared by
  both.
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** — the
  unbounded default this pattern tames; pair the bound with a drop policy to keep
  fire-and-forget's non-blocking contract.
- **[Await-All Dispatch](../dispatch/await-all-dispatch.md)** — `EmitAndWait` combined
  with a bound gives a self-pacing producer loop, as in the sample above.
- **[Result Aggregation](../reliability/result-aggregation.md)** — when bounded
  concurrent listeners can each fail, collect their joined errors via
  `EmitAndWaitErr`.
