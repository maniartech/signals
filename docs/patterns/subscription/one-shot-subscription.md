# One-Shot Subscription

**Family:** Subscription Lifecycle
· **Also Known As:** Once Listener, Self-Unsubscribing Handler
· **Status:** 🔜 v1.4 (`AddOnce`, `AddOnceWithErr`)

## Intent

Register a listener that fires **exactly once** and then removes itself — with a
**concurrency-safe guarantee** that it cannot double-fire, even when several emits
arrive simultaneously from different goroutines.

## Motivation

Consider a database client that must run a one-time schema migration the **first**
time it successfully connects, and never again. Connection events arrive on a
`signals.AsyncSignal[Conn]`. The obvious hand-rolled approach is a listener that
removes itself from inside its own body:

```go
var Connected = signals.New[Conn]()

func armMigration() {
    const key = "migrate-on-first-connect"
    Connected.AddListener(func(ctx context.Context, c Conn) {
        runMigration(ctx, c)             // expensive, must run once
        Connected.RemoveListener(key)    // "unsubscribe myself"
    }, key)
}
```

This looks correct and works in a demo. In production it has a **race**. Async
dispatch runs listeners concurrently, and reconnect storms are real: the pool
re-establishes several connections at once, so `Emit` is called from multiple
goroutines in the same instant. Each emit schedules the listener **before** any of
them has reached the `RemoveListener` line. The result: `runMigration` runs **two,
three, five times in parallel** — duplicate DDL, "column already exists" errors,
corrupted state, or a deadlock as concurrent migrations contend for the same locks.
The self-removal is a check-then-act with no atomicity: between "I fired" and "I
removed myself," other emits have already captured the still-present listener.

Tightening it by hand means introducing your own `sync.Once` or atomic flag and
threading it through the closure — easy to get subtly wrong, and noise in every
call site.

One-Shot Subscription puts the guarantee in the library:

```go
func armMigration() {
    // Fires exactly once, even under simultaneous emits; then removes itself.
    Connected.AddOnce(func(ctx context.Context, c Conn) { // 🔜 v1.4
        runMigration(ctx, c)
    })
}
```

`AddOnce` wraps the handler in an **atomic one-shot guard**: the very first emit to
reach it wins the guard and runs the handler; every concurrent or subsequent emit
sees the guard already taken and is skipped. The listener then removes itself. No
duplicate migration, no hand-rolled flag, no race.

## Applicability

**Use this pattern when:**

- An action must run **once on a recurring event** — a one-time migration on first
  connect, lazy initialization on first use, a single warm-up.
- You need to **signal readiness exactly once** — fan out a "ready" notification the
  first time a condition is met, ignoring later repeats.
- You want **"alert me the next time X happens — but only the next time"** semantics:
  a transient, self-cleaning subscription.

**Avoid it (or prefer another pattern) when:**

- The listener should react to **every** occurrence → a normal
  [Keyed Subscription](keyed-subscription.md) (or anonymous `AddListener`).
- You need the listener to fire a **bounded number > 1** of times — one-shot is
  exactly one; for N-times you must track a counter yourself.
- You need to remove the listener **before** it ever fires under a known name →
  pass a key to `AddOnce` so you retain a handle for early cancellation
  (e.g. `AddOnce(handler, key)`).

## Structure

```
   Emit #1 ─┐
   Emit #2 ─┤  (concurrent, from different goroutines)
   Emit #3 ─┘
        │
        ▼
  ┌───────────────────────────────────────────────┐
  │  One-shot wrapper around the handler           │
  │                                                 │
  │   atomic guard:  taken?  ── yes ──▶ skip (no-op)│  ◀─ losers of the race
  │        │                                        │
  │        └── claim (CAS) ──▶ run handler ONCE     │  ◀─ exactly one winner
  │                              │                  │
  │                              ▼                  │
  │                       remove self from signal   │
  └───────────────────────────────────────────────┘
        │
        ▼
   handler body executes exactly once, ever
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Producer (Emitter)** | Emits the recurring event, possibly concurrently |
| **`AddOnce`** (🔜 v1.4) | Registers the handler wrapped in a one-shot guard; takes an optional key |
| **One-shot guard** | An atomic flag claimed by the first emit; makes "fire once" race-safe |
| **Wrapped handler** | The user's logic; invoked by exactly one emit |
| **Signal** | Removes the wrapped listener after it has fired |
| **Key** (optional) | Passed to keyed `AddOnce`; lets the one-shot listener also be removed *before* it fires |

## Collaborations

1. The caller registers a handler via `AddOnce(handler)` (or keyed
   `AddOnce(handler, key)`). The library installs a **wrapper** that owns an
   atomic one-shot guard, plus the user's handler.
2. One or more emits reach the wrapper, possibly **concurrently** from different
   goroutines.
3. Each wrapper invocation attempts to **claim the guard** with a single atomic
   compare-and-swap. Exactly one invocation succeeds (the winner).
4. The **winner** runs the user's handler exactly once, then the listener is **removed**
   from the signal.
5. Every **loser** — whether it arrived in the same instant or after the fact — sees
   the guard already claimed and returns immediately without touching the handler.
6. After removal the listener is gone; future emits never reach it at all.

## Consequences

**Benefits**

- ✓ **Exactly-once, race-free.** The atomic guard guarantees a single execution even
  under simultaneous emits from many goroutines — the property hand-rolled self-removal
  cannot provide.
- ✓ **Self-cleaning.** The listener removes itself; no teardown code to remember and no
  leak from a listener that has outlived its purpose.
- ✓ **Intent is explicit.** `AddOnce` states "this runs once" at the call site, where a
  reader can see it — far clearer than a hidden flag.
- ✓ **Composes with keys.** A keyed `AddOnce` keeps a handle so the one-shot can also be
  cancelled *before* it fires.

**Liabilities**

- ✗ **Fires at most once — then it's gone.** If "once" was the wrong cardinality, you
  must re-arm manually; there is no automatic re-subscribe.
- ✗ **The winner is non-deterministic** under concurrent emits. *Which* payload triggers
  the single fire is whichever emit wins the guard — do not assume it's the first by
  wall-clock time.
- ✗ **No built-in timeout.** "The next time X happens" waits indefinitely; if X never
  happens, the listener sits armed forever. Pair with a key so you can cancel it.

## Implementation

1. **The guarantee is an atomic guard, not lock-free luck.** The wrapper claims the
   one-shot with a single CAS (or `sync.Once`-style primitive). This is what makes the
   difference from hand-rolled self-removal, which performs a non-atomic check-then-act
   and therefore races. Do not try to reproduce one-shot semantics with a plain boolean
   in your closure — under async dispatch it *will* double-fire.

2. **Why hand-rolled self-removal is racy (in detail).** `RemoveListener` from inside
   the handler removes the listener for *future* emits, but emits already in flight have
   already captured the listener reference and will still run it. Async dispatch offers
   no ordering or mutual exclusion between listeners, so N concurrent emits run the body
   N times before any removal takes effect. The library's guard closes this window by
   gating on execution, not on registration.

3. **Unkeyed vs keyed `AddOnce`.** `AddOnce(handler)` is anonymous: fire-once-then-vanish
   with no handle. Passing a key — `AddOnce(handler, key)` — assigns a name so you can
   `RemoveListener(key)` to cancel the one-shot *before* it ever fires (e.g. on shutdown
   while still waiting for the event); the key also dedup's the registration. Prefer the
   keyed form whenever the armed listener might need to be torn down early — see
   [Keyed Subscription](keyed-subscription.md).

4. **The handler runs under the dispatch semantics of its signal.** On an
   `AsyncSignal` the single fire runs concurrently with other listeners and its panics
   are recovered to the panic handler; on a `SyncSignal` it runs inline in registration
   order. One-shot governs *cardinality*, not *delivery mode* — all the usual dispatch
   and reliability rules still apply to that one execution.

5. **Keep the once-handler self-contained.** Because it runs exactly once and then
   disappears, it should not assume it will see later state changes. If the work needs
   the *latest* value rather than the *first* triggering one, a one-shot is the wrong
   tool — use a durable listener.

6. **Re-arming is explicit.** If you genuinely need "once per epoch," call `AddOnce`
   again at the start of each epoch. There is deliberately no auto-rearm; that keeps the
   exactly-once contract unambiguous.

7. **Canceled context still gates the fire.** If `ctx.Err() != nil` at emit time, no
   listener runs — including a one-shot — and the guard is *not* consumed, so the
   one-shot remains armed for a later, non-canceled emit. A cancelled emit does not
   "spend" the single fire.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Producer* emitting (possibly concurrently), the
`AddOnce` registration (unkeyed or keyed) that wraps the handler in the *one-shot
guard*, the single *winner* that runs the handler, and the *self-removal*. Read this
first to see the mechanics; the practical examples then apply it to real problems.

```go
// 1. SIGNAL — a recurring event; emits may arrive concurrently.
sig := signals.New[Event]()

// 2a. ANONYMOUS one-shot: fire once, then vanish. No handle.
sig.AddOnce(func(ctx context.Context, e Event) { // 🔜 v1.4
    initOnce(e) // runs for exactly one emit, ever — even under simultaneous emits
})

// 2b. KEYED one-shot: same exactly-once guarantee, plus a handle so it can be
//     cancelled BEFORE it ever fires (e.g. on shutdown while still waiting).
sig.AddOnce(func(ctx context.Context, e Event) { // 🔜 v1.4
    initOnce(e)
}, "domain/once-on-first")

// 3. PRODUCER — emits, possibly from many goroutines at the same instant.
sig.Emit(ctx, e)
//   ├─ first emit to claim the atomic guard (CAS) → runs handler ONCE, removes self
//   ├─ concurrent / later emits → see guard taken → skipped, no-op
//   └─ emit with a CANCELED ctx → no listener runs, guard NOT consumed (still armed)

// 4. EARLY CANCEL — only possible for the keyed form: remove before it fires.
sig.RemoveListener("domain/once-on-first") // tear down an armed-but-unfired one-shot
```

The atomic guard (step 3) is the heart of the pattern: it gates on *execution*, not
on *registration*, which is exactly why it survives a burst of concurrent emits that
hand-rolled self-removal cannot.

### Practical Example 1 — One-time DB migration on first successful connect

A database client must run its schema migration the **first** time the pool
establishes a connection and never again — but reconnect storms open several
connections at once, so the migration listener can be hit concurrently. A keyed
`AddOnce` makes it exactly-once and cancellable if the process shuts down before ever
connecting.

```go
package db

import (
    "context"

    "github.com/maniartech/signals"
)

type Conn struct {
    ID   string
    Pool *Pool
}

// Emitted every time the pool establishes a connection — possibly several at once.
var Connected = signals.New[Conn]()

// ArmMigration installs a one-time migration that runs on the first connect and
// then removes itself. Even if the pool opens five connections simultaneously,
// runMigration executes exactly once — the atomic guard admits a single winner.
func ArmMigration(migrate func(context.Context, Conn) error) {
    Connected.AddOnce(func(ctx context.Context, c Conn) { // 🔜 v1.4
        if err := migrate(ctx, c); err != nil {
            // handle/log; the one-shot has already been consumed by this fire
        }
    }, "db/migrate-on-first-connect")
}

// CancelMigration tears down the armed one-shot if we shut down before ever
// connecting — possible only because we gave it a key.
func CancelMigration() {
    Connected.RemoveListener("db/migrate-on-first-connect")
}
```

Under a reconnect burst that opens five connections in the same instant, the DDL runs
once; the other four emits find the guard claimed and skip — no "column already
exists" errors, no concurrent migrations contending for the same locks.

### Practical Example 2 — "Notify me the next time this product is back in stock"

A storefront lets a shopper request a single back-in-stock alert. The next restock
event fires the alert exactly once and auto-unsubscribes — later restocks of the same
product do not re-spam the shopper, and there is no teardown to remember.

```go
package inventory

import (
    "context"
    "fmt"

    "github.com/maniartech/signals"
)

type Restock struct {
    SKU      string
    Quantity int
}

// Fires whenever any SKU is replenished.
var Restocked = signals.New[Restock]()

// WatchOnce arms a single back-in-stock alert for one shopper + SKU. The first
// matching restock fires the alert once and the listener removes itself; a later
// restock of the same SKU does nothing. Keyed so the shopper can cancel the watch.
func WatchOnce(shopperID, sku string, notify func(context.Context, string)) {
    key := fmt.Sprintf("restock-watch/%s/%s", shopperID, sku)
    Restocked.AddOnce(func(ctx context.Context, r Restock) { // 🔜 v1.4
        if r.SKU != sku {
            return // not the SKU this shopper is waiting on
        }
        notify(ctx, r.SKU) // single alert, then this listener is gone
    }, key)
}

// CancelWatch lets the shopper withdraw the request before it ever fires.
func CancelWatch(shopperID, sku string) {
    Restocked.RemoveListener(fmt.Sprintf("restock-watch/%s/%s", shopperID, sku))
}
```

Note the **first-match filter** idiom: the one-shot guards a single *execution*, but
here only the matching SKU should consume it. If a non-matching restock must not spend
the single fire, re-arm inside the handler instead of returning (see Variations);
this example accepts that the next restock of any kind triggers the check.

**Contrast — the hand-rolled self-removal that double-fires under load:**

```go
// ❌ Racy: concurrent emits all capture the listener before RemoveListener lands.
Connected.AddListener(func(ctx context.Context, c Conn) {
    runMigration(ctx, c)               // can run 2–5× in parallel under a reconnect storm
    Connected.RemoveListener("migrate") // too late — other emits already in flight
}, "migrate")
```

The symptom in production: during a reconnect burst the migration runs several times
concurrently, producing duplicate-DDL errors or lock contention — an outage that never
reproduces under single-threaded testing.

## Variations

- **Keyed one-shot (cancellable).** Pass a key to `AddOnce` (`AddOnce(handler, key)`)
  when the armed listener might need early teardown (shutdown before the event ever
  fires).
- **Error-returning one-shot.** `AddOnceWithErr` is the error-returning one-shot — same
  fire-once semantics, but the handler returns an `error` that routes to `OnError` on
  `Emit` or is returned by `TryEmit`. It is "consumed on attempt": it fires and
  self-removes even when it returns an error. (Like `AddOnce`, it takes an optional key.)
- **Once-per-epoch.** Re-call `AddOnce` at the boundary of each epoch (per session, per
  reconnect cycle) to get "once within this window" semantics without weakening the
  per-arming exactly-once guarantee.
- **First-match filter.** Inside the one-shot, check a condition and — if it doesn't
  match — re-arm; effectively "fire once on the first event that satisfies P." (You own
  the re-arm; the guard still protects each individual arming.)
- **Readiness latch.** A one-shot that flips a latch and notifies all current waiters,
  approximating a `sync.Once`-backed broadcast over the signal.

## Known Uses

- **Node.js `EventEmitter.once`** — the canonical "fire once then auto-remove" listener;
  this pattern is its concurrency-safe analogue.
- **`sync.Once` (Go stdlib)** — the exactly-once primitive; One-Shot Subscription is the
  event-driven generalization, with the guard managed for you.
- **RxJS / Reactive Streams `take(1)` / `first()`** — operators that complete a stream
  after the first element, auto-unsubscribing the upstream.
- **Promise resolution** — a promise settles at most once regardless of how many times
  `resolve` is called; the one-shot guard mirrors that single-settlement rule.

## Related Patterns

- **[Keyed Subscription](keyed-subscription.md)** — a keyed `AddOnce` combines one-shot
  cardinality with a removable handle; keys are how you cancel an armed one-shot early.
- **[Subscription Teardown](subscription-teardown.md)** — one-shot is *self*-teardown
  for the single-fire case; the teardown pattern covers the general lifecycle. A
  one-shot needs no manual cleanup precisely because it removes itself.
- **[Shared Event Registry](../architectural/shared-event-registry.md)** — a common
  home for one-shot listeners: many packages arm a single "first ready / first connect"
  reaction on a shared signal.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — relevant when the
  one-shot's single fire is itself expensive and dispatched async; the bound governs how
  that work runs, while the guard governs that it runs only once.
