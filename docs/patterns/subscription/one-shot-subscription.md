# One-Shot Subscription

**Family:** Subscription Lifecycle
· **Also Known As:** Once Listener, Self-Unsubscribing Handler
· **Status:** ✅ shipped (`AddOnce`, `AddOnceWithErr`, `AddOnceWithCancel`)

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
    Connected.AddOnce(func(ctx context.Context, c Conn) {
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
- You need to remove the listener **before** it ever fires → use
  `AddOnceWithCancel(handler)` and keep the returned canceller, or — when the
  one-shot must be addressable by *name* across modules — pass a key to `AddOnce`
  and call `RemoveListener(key)`.

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
| **`AddOnce`** | Registers the handler wrapped in a one-shot guard; takes an optional key |
| **`AddOnceWithCancel`** | Same one-shot guard, but returns an idempotent canceller that can remove the pending one-shot — and that *wins* against an in-flight fire |
| **One-shot guard** | An atomic flag claimed by the first emit (or by a winning cancel); makes "fire once" race-safe |
| **Wrapped handler** | The user's logic; invoked by exactly one emit |
| **Signal** | Removes the wrapped listener after it has fired |
| **Key** (optional) | Names the one-shot so it can also be removed *before* it fires via `RemoveListener` |

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
- ✓ **Cancellable while pending.** `AddOnceWithCancel` returns a canceller that removes
  an armed-but-unfired one-shot; a keyed `AddOnce` offers the same via `RemoveListener`.
  The canceller is stronger: a cancel that wins the race against an in-flight emission
  guarantees the handler never runs.

**Liabilities**

- ✗ **Fires at most once — then it's gone.** If "once" was the wrong cardinality, you
  must re-arm manually; there is no automatic re-subscribe.
- ✗ **The winner is non-deterministic** under concurrent emits. *Which* payload triggers
  the single fire is whichever emit wins the guard — do not assume it's the first by
  wall-clock time.
- ✗ **No built-in timeout.** "The next time X happens" waits indefinitely; if X never
  happens, the listener sits armed forever. Use `AddOnceWithCancel` (or a key) so you
  can abandon the wait — see the await-with-abandon example below.

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

3. **Three registration forms: unkeyed, keyed, and `WithCancel`.** `AddOnce(handler)`
   is anonymous: fire-once-then-vanish with no handle. Passing a key —
   `AddOnce(handler, key)` — assigns a name so any module can `RemoveListener(key)` to
   cancel the one-shot before it fires; the key also dedup's the registration. And
   `AddOnceWithCancel(handler)` returns an idempotent canceller func — the handle-based
   form for a one-shot armed and abandoned by the *same* code, with no key to invent
   (a duplicate caller key adds nothing and yields a no-op canceller). Prefer
   `WithCancel` for scoped waits, a key for cross-module addressability — see
   [Keyed Subscription](keyed-subscription.md).

4. **The `WithCancel` canceller is strictly stronger than `RemoveListener`.**
   `RemoveListener` only affects *subsequent* emits: an emission already in flight has
   snapshotted the listener and may still run it. The canceller from `AddOnceWithCancel`
   closes that window — it flips the *same atomic fired-guard* the one-shot uses, so a
   cancel that wins the race against an in-flight fire makes the handler's CAS fail and
   **guarantees the handler does not run**. Either the handler fired or the cancel won;
   never both, never a half-state. Calling the canceller after the fire, or calling it
   repeatedly, is a safe no-op.

5. **The handler runs under the dispatch semantics of its signal.** On an
   `AsyncSignal` the single fire runs concurrently with other listeners and its panics
   are recovered to the panic handler; on a `SyncSignal` it runs inline in registration
   order. One-shot governs *cardinality*, not *delivery mode* — all the usual dispatch
   and reliability rules still apply to that one execution.

6. **Keep the once-handler self-contained.** Because it runs exactly once and then
   disappears, it should not assume it will see later state changes. If the work needs
   the *latest* value rather than the *first* triggering one, a one-shot is the wrong
   tool — use a durable listener.

7. **Re-arming is explicit.** If you genuinely need "once per epoch," call `AddOnce`
   again at the start of each epoch. There is deliberately no auto-rearm; that keeps the
   exactly-once contract unambiguous.

8. **Canceled context still gates the fire.** If `ctx.Err() != nil` at emit time, no
   listener runs — including a one-shot — and the guard is *not* consumed, so the
   one-shot remains armed for a later, non-canceled emit. A cancelled emit does not
   "spend" the single fire.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Producer* emitting (possibly concurrently), the
registration (unkeyed, keyed, or `WithCancel`) that wraps the handler in the *one-shot
guard*, the single *winner* that runs the handler, and the *self-removal*. Read this
first to see the mechanics; the practical examples then apply it to real problems.

```go
// 1. SIGNAL — a recurring event; emits may arrive concurrently.
sig := signals.New[Event]()

// 2a. ANONYMOUS one-shot: fire once, then vanish. No handle.
sig.AddOnce(func(ctx context.Context, e Event) {
    initOnce(e) // runs for exactly one emit, ever — even under simultaneous emits
})

// 2b. KEYED one-shot: same exactly-once guarantee, plus a NAME so any module can
//     cancel it BEFORE it ever fires (e.g. on shutdown while still waiting).
sig.AddOnce(func(ctx context.Context, e Event) {
    initOnce(e)
}, "domain/once-on-first")

// 2c. CANCELLABLE one-shot: same guarantee, plus an idempotent canceller HANDLE —
//     no key to invent; the canceller even beats an in-flight fire if it wins.
cancel := sig.AddOnceWithCancel(func(ctx context.Context, e Event) {
    initOnce(e)
})

// 3. PRODUCER — emits, possibly from many goroutines at the same instant.
sig.Emit(ctx, e)
//   ├─ first emit to claim the atomic guard (CAS) → runs handler ONCE, removes self
//   ├─ concurrent / later emits → see guard taken → skipped, no-op
//   └─ emit with a CANCELED ctx → no listener runs, guard NOT consumed (still armed)

// 4. EARLY CANCEL — abandon a one-shot that hasn't fired yet:
sig.RemoveListener("domain/once-on-first") // keyed form: affects subsequent emits only
cancel() // WithCancel form: claims the SAME fired-guard, so a winning cancel
//          guarantees the handler does not run, even against an in-flight emission.
//          After-fire or repeated calls are safe no-ops.
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
    Connected.AddOnce(func(ctx context.Context, c Conn) {
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
    Restocked.AddOnce(func(ctx context.Context, r Restock) {
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

### Practical Example 3 — Await with abandon: wait for the next event, or give up

The capability unique to `AddOnceWithCancel`: turning "the next time X happens" into
a **bounded** wait. The caller arms a one-shot that forwards the event to a channel,
then `select`s between the event and `ctx.Done()`. If the context expires first, the
canceller removes the pending one-shot — and because a winning cancel flips the same
atomic fired-guard the one-shot uses, the handler is **guaranteed not to run** even
if an emission was already in flight. No late write, no leaked armed listener.

```go
package gateway

import (
    "context"

    "github.com/maniartech/signals"
)

type Ready struct{ Endpoint string }

// Fires when the upstream reports readiness — which may be never.
var UpstreamReady = signals.New[Ready]()

// AwaitReady blocks until the next Ready event or ctx expires, whichever wins.
func AwaitReady(ctx context.Context) (Ready, error) {
    done := make(chan Ready, 1) // buffered: the handler never blocks dispatch

    cancel := UpstreamReady.AddOnceWithCancel(func(_ context.Context, r Ready) {
        done <- r
    })
    defer cancel() // fired already? safe no-op. Abandoning? removes the pending one-shot.

    select {
    case r := <-done:
        return r, nil // the one-shot fired and self-removed
    case <-ctx.Done():
        // Give up waiting. The deferred cancel() claims the one-shot's fired-guard:
        // if it wins the race against an in-flight emit, the handler never runs,
        // so nothing is written to done after we return.
        return Ready{}, ctx.Err()
    }
}
```

A plain `RemoveListener(key)` could not close this race — it only stops *subsequent*
emits, so an in-flight emission could still run the handler after the caller had
already returned. The canceller's guarantee is exactly what makes the abandon path
airtight.

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

- **Keyed one-shot (cancellable by name).** Pass a key to `AddOnce`
  (`AddOnce(handler, key)`) when the armed listener must be cancellable from *another*
  module via `RemoveListener(key)` (shutdown before the event ever fires).
- **Handle-cancellable one-shot.** `cancel := sig.AddOnceWithCancel(handler)` when the
  same code arms and may abandon the wait — no key to invent, idempotent canceller, and
  a winning cancel guarantees the handler does not run even against an in-flight emit.
- **Await-with-abandon.** Forward the one-shot's payload to a buffered channel and
  `select` on it vs `ctx.Done()`; on timeout, the canceller removes the pending
  one-shot race-free (Practical Example 3).
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
  cardinality with a *named* handle; keys are how another module cancels an armed
  one-shot early. For same-scope cancellation, `AddOnceWithCancel` is the lighter tool.
- **[Subscription Teardown](subscription-teardown.md)** — one-shot is *self*-teardown
  for the single-fire case; the teardown pattern covers the general lifecycle
  (including the handle-vs-key choice for the `WithCancel` cancellers). A one-shot
  needs no manual cleanup precisely because it removes itself.
- **[Shared Event Registry](../architectural/shared-event-registry.md)** — a common
  home for one-shot listeners: many packages arm a single "first ready / first connect"
  reaction on a shared signal.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — relevant when the
  one-shot's single fire is itself expensive and dispatched async; the bound governs how
  that work runs, while the guard governs that it runs only once.
