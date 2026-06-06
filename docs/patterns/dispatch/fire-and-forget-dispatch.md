# Fire-and-Forget Dispatch

**Family:** Dispatch
· **Also Known As:** Detached Dispatch, Best-Effort Notify, Async Emit
· **Status:** ✅ shipped

## Intent

Emit an event and **immediately regain control of the caller's goroutine**. Each
listener runs concurrently in its own goroutine; the caller never waits for them and
never learns their outcome — the right delivery mode for decoupled side effects on a
latency-critical hot path.

## Motivation

Consider the product-page handler of a high-traffic storefront. Every page view should
fan out a handful of side effects: record an **analytics** event, bump a
**recently-viewed** cache, warm a **recommendations** cache, and update a
**presence/heartbeat** counter. None of these affect the response the shopper sees —
but all of them, done synchronously, would pile latency onto the hot path.

The naive approach runs them inline:

```go
func ProductPage(ctx context.Context, w http.ResponseWriter, id string) {
    p := catalog.Get(ctx, id)

    analytics.Record(ctx, view(id))      // 8ms  network
    cache.MarkViewed(ctx, userID, id)    // 3ms
    recs.Warm(ctx, userID, id)           // 40ms network — the killer
    presence.Beat(ctx, userID)           // 2ms

    render(w, p) // the user has been waiting ~53ms for things they never see
}
```

Every shopper now pays ~53ms of side-effect latency *before the page renders*, even
though none of that work belongs on the response path. Under load it is worse: the
recommendations service has a slow afternoon, `recs.Warm` climbs to 400ms, and your
p99 page-load time tracks it straight off a cliff. The primary workload — serving the
page — is held hostage by secondary, decoupled work.

Fire-and-Forget Dispatch decouples them. Emit the event, return instantly, and let the
listeners run concurrently on their own goroutines:

```go
var PageViewed = signals.New[PageView]() // async signal

func init() {
    PageViewed.AddListener(func(ctx context.Context, v PageView) { analytics.Record(ctx, v) })
    PageViewed.AddListener(func(ctx context.Context, v PageView) { cache.MarkViewed(ctx, v.UserID, v.ProductID) })
    PageViewed.AddListener(func(ctx context.Context, v PageView) { recs.Warm(ctx, v.UserID, v.ProductID) })
    PageViewed.AddListener(func(ctx context.Context, v PageView) { presence.Beat(ctx, v.UserID) })
}

func ProductPage(ctx context.Context, w http.ResponseWriter, id string) {
    p := catalog.Get(ctx, id)
    PageViewed.Emit(ctx, PageView{UserID: userID, ProductID: id}) // returns immediately
    render(w, p) // the response no longer waits on any side effect
}
```

`Emit` returns the instant it has scheduled the listeners — the four side effects run
concurrently in the background while the page renders. The hot path is protected, and a
slow recommendations service no longer drags down the p99 of the page load.

That decoupling is not free, and the cost is the whole point of the next section.

## Applicability

**Use this pattern when:**

- The side effects are **decoupled** from the caller's result — analytics, cache-warm
  hints, notifications, presence updates, audit *fan-out* where the caller need not
  confirm completion.
- The caller is on a **latency-critical hot path** and must not pay for secondary work.
- The data is **loss-tolerant** — losing one event under extreme overload is survivable
  (see the critical note in Consequences).
- Listeners are **independent** — they don't depend on each other's effects or ordering.

**Avoid it (or prefer another pattern) when:**

- **The next line depends on listeners having run** → use
  [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md) (ordered, in-caller).
- **You must wait for all listeners but still want concurrency** → use
  [Await-All Dispatch](await-all-dispatch.md) (`EmitAndWait`).
- **The data is loss-intolerant** (orders, trades, payments, audit *of record*) → fire-
  and-forget will drop under overload by design; use a path that can slow you down —
  [Backpressure](../flow-control/backpressure.md).
- **You need to know whether listeners failed** → fire-and-forget hides outcomes; route
  failures with [Async Error Routing](../reliability/async-error-routing.md).

## Structure

```
  Caller's goroutine
  ┌───────────────────────────────┐
  │  Emit(ctx, payload)           │
  │     │                         │
  │     ├─ ctx canceled? ─yes─▶ run nothing, return
  │     │                         │
  │     └─ schedule listeners ─────┐  (returns immediately) ──▶ caller continues
  └───────────────────────────────┘                            (never waits, never
                                  │                              learns the outcome)
          ┌───────────────────────┼───────────────────────┐
          ▼                       ▼                       ▼
   goroutine: Listener₁    goroutine: Listener₂    goroutine: Listenerₙ
     (concurrent,            (concurrent,            (concurrent,
      no ordering)            no ordering)            no ordering)
          │                       │                       │
     panic? ─▶ recovered ──▶ global panic handler   (process never crashes)
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Calls `Emit`; on the hot path; **never blocks**, **never observes** listener results |
| **AsyncSignal** | Schedules each listener on its own goroutine and returns control immediately |
| **Context** | Checked once at emit time; if already canceled, **no** listener runs |
| **Listener** | Runs concurrently, in arbitrary order; its return value/error is discarded by this path |
| **Panic handler** | Global `SetPanicHandler`; receives any recovered panic so one listener can't crash the process |

## Collaborations

1. The caller invokes `Emit(ctx, payload)`. The signal checks `ctx` once: if
   `ctx.Err() != nil`, **no listener runs** and `Emit` returns (canceled-context-skips-all).
2. Otherwise the signal **schedules each listener on its own goroutine** and returns
   control to the caller **immediately** — the caller does not wait for any listener to
   start, run, or finish.
3. The listeners execute **concurrently** with each other and with the caller, in **no
   guaranteed order**.
4. If a listener **panics**, the runtime recovers it (the process never crashes) and
   routes the recovered value to the global panic handler set via `SetPanicHandler`.
5. Listener return values and errors are **not delivered back** to the caller. To learn
   about async failures, register an out-of-band sink
   ([Async Error Routing](../reliability/async-error-routing.md)).

## Consequences

**Benefits**

- ✓ **Minimal caller latency.** `Emit` returns almost instantly; the hot path is never
  held hostage by side effects.
- ✓ **Concurrency for free.** Listeners overlap; total wall-clock is roughly the slowest
  listener, paid in the *background*, not on the caller.
- ✓ **Strong decoupling.** Producers know nothing about consumers' speed, success, or
  even existence.
- ✓ **Crash isolation.** A panicking listener is recovered and routed, never taking down
  the process or the caller — see [Panic Isolation](../reliability/panic-isolation.md).

**Liabilities**

- ✗ **No completion guarantee.** When `Emit` returns, listeners may not have started; the
  next line cannot rely on their effects.
- ✗ **No outcome visibility.** Errors and return values are discarded by this path; a
  silently failing listener is invisible unless you wire
  [Async Error Routing](../reliability/async-error-routing.md).
- ✗ **No ordering.** Listeners run in arbitrary order; never build logic that assumes one
  ran before another.
- ✗ **Unbounded by default → must shed under sustained overload.** A fresh goroutine per
  listener per emit means that if listeners slow down while the producer does not, live
  goroutines and memory grow without limit — the classic meltdown. This is not a flaw to
  be fixed by waiting; it is the *consequence of the promise this pattern makes*.

> **Trilemma corner sacrificed: zero-loss.** Fire-and-forget has promised that the
> **producer never waits**. By the catalog's central law, a path that never makes the
> producer wait *cannot* also guarantee bounded memory *and* zero loss under sustained
> overload. Fire-and-forget keeps the never-wait promise and keeps memory bounded by
> **dropping (and counting) the excess** — see
> [Load Shedding](../flow-control/load-shedding.md). Choosing this dispatch mode is
> therefore a structural commitment to a **loss-tolerant** stream. If the data cannot
> tolerate loss, this is the wrong dispatch mode — use a path that is allowed to slow you
> down ([Backpressure](../flow-control/backpressure.md)).

## Implementation

1. **Construct with `New[T]()`** (✅) — or `NewWithOptions[T]` (✅) to set
   `InitialCapacity`/`GrowthFunc`, and (🔜 v1.4) `WorkerPoolSize`/`Overflow` to bound the
   concurrency and choose an overflow policy. The zero value
   `var s signals.AsyncSignal[T]` is usable directly (lazy `sync.Once` init).

2. **Bound the concurrency in production.** The unbounded default is fine for low-rate or
   bursty-then-idle signals, but any high-rate stream with slow listeners *must* set
   `WorkerPoolSize` (🔜 v1.4) so the goroutine count has a ceiling — see
   [Bounded Concurrency](../flow-control/bounded-concurrency.md). Bounding is the
   prerequisite for graceful behavior under load.

3. **Decide the overflow policy on purpose.** Once bounded, what happens when the bound is
   saturated is a *policy* choice: `OverflowDropNewest` (🔜 v1.4, default) sheds and counts
   the excess — see [Load Shedding](../flow-control/load-shedding.md); `OverflowBlock`
   (🔜 v1.4) turns `Emit` into a blocking, backpressuring call (no longer fire-and-forget
   — see [Backpressure](../flow-control/backpressure.md)); `OverflowError` (🔜 v1.4)
   reports via the hook without running.

4. **Context is checked once, at emit.** If `ctx` is already canceled, no listener runs.
   But once a listener has been scheduled, fire-and-forget does **not** wait on `ctx` to
   tear it down — long-lived listeners should observe `ctx` themselves. Also beware
   passing a *request-scoped* `ctx`: it may be canceled the instant the handler returns,
   which (depending on timing) can cancel work you wanted to outlive the request. For
   detached work that must survive the request, derive a fresh context — see
   [Context-Scoped Emission](../architectural/context-scoped-emission.md).

5. **Panics are recovered, not surfaced.** A listener panic is caught and routed to the
   handler registered with `signals.SetPanicHandler` (✅). Set it once at startup so
   panics are logged/counted, not lost — see
   [Panic Isolation](../reliability/panic-isolation.md). The caller never sees the panic.

6. **Errors need an explicit sink.** `SignalListenerErr[T]` may be added (🔜 v1.4 on
   async via `AddListenerWithErr`), but plain `Emit` discards the returned error. To
   observe async failures, register `OnError` (🔜 v1.4) — see
   [Async Error Routing](../reliability/async-error-routing.md). Keep shed counts (never
   ran) separate from error counts (ran, failed) and panic counts (ran, panicked).

7. **Don't pass mutable state by shared pointer across listeners.** Because listeners run
   concurrently and unordered, two listeners mutating the same `*T` race. Pass values or
   immutable snapshots; if listeners must coordinate, you probably wanted
   [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md) instead.

8. **Graceful shutdown needs care.** Because the caller doesn't wait, in-flight listeners
   can be cut off when the process exits. If you must drain them before shutdown, use
   [Await-All Dispatch](await-all-dispatch.md) for that final emission, or coordinate a
   drain — fire-and-forget alone offers no join point.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Caller* that never blocks, the *AsyncSignal* that schedules
listeners on their own goroutines, the *Context* checked once at emit, the *Panic
handler*, and the concurrent *Listeners*. Read this first to see the mechanics; the
practical examples then apply it to real problems.

```go
// 1. SIGNAL — an AsyncSignal. Emit schedules listeners and returns immediately.
sig := signals.New[Event]()

// 2. PANIC HANDLER — global, set once. A buggy listener must not crash the process.
signals.SetPanicHandler(func(recovered any) { // ✅
    log.Println("listener panicked:", recovered)
})

// 3. LISTENERS — run concurrently, in NO guaranteed order. Independent of each other.
sig.AddListener(func(ctx context.Context, e Event) {
    sideEffectA(e) // own goroutine
}, "a")
sig.AddListener(func(ctx context.Context, e Event) {
    sideEffectB(e) // own goroutine, may overlap A
}, "b")

// 4. CALLER — fire-and-forget; Emit returns at once. Never waits, never learns outcomes.
sig.Emit(ctx, e)
//   ├─ ctx canceled at emit?  → run nothing, return
//   └─ otherwise              → schedule A and B on goroutines, return IMMEDIATELY
nextLine() // runs now, in parallel with A and B; must NOT depend on their effects
```

The immediate return (step 4) is the heart of the pattern: it is where "the producer
never waits" is promised — which, by the catalog's central law, makes this a structurally
**loss-tolerant** stream. Listener errors and return values are discarded here; wire
`OnError` ([Async Error Routing](../reliability/async-error-routing.md)) if you must learn
about failures.

### Practical Example 1 — Page-view fan-out off the response path

A high-traffic storefront fans out several decoupled side effects per page view —
analytics, recommendation warming, recently-viewed cache, presence — none of which the
shopper waits on. They stay off the response hot path, with panics routed and the stream
treated as loss-tolerant:

```go
package web

import (
    "context"
    "log/slog"
    "runtime"

    "github.com/maniartech/signals"
)

type PageView struct {
    UserID    string
    ProductID string
}

// Bounded async signal: high-rate, slow-ish listeners, loss-tolerant data.
var PageViewed = signals.NewWithOptions[PageView](&signals.SignalOptions{
    WorkerPoolSize: 8 * runtime.NumCPU(),       // 🔜 v1.4 — ceiling on concurrent listeners
    Overflow:       signals.OverflowDropNewest,  // 🔜 v1.4 — shed + count under overload
})

func init() {
    // Route recovered panics once, globally — a buggy listener must not crash the server.
    signals.SetPanicHandler(func(recovered any) { // ✅
        slog.Error("listener panicked", "recovered", recovered)
    })

    // Observe drops so loss is visible, not silent.
    PageViewed.OnOverflow(func(dropped PageView) { // 🔜 v1.4
        metrics.Inc("pageview.shed")
    })

    PageViewed.AddListener(recordAnalytics, "analytics")
    PageViewed.AddListener(warmRecommendations, "recs")
    PageViewed.AddListener(markRecentlyViewed, "recent")
    PageViewed.AddListener(beatPresence, "presence")
}

func recordAnalytics(ctx context.Context, v PageView)      { analytics.Record(ctx, v) }
func warmRecommendations(ctx context.Context, v PageView)  { recs.Warm(ctx, v.UserID, v.ProductID) }
func markRecentlyViewed(ctx context.Context, v PageView)   { cache.MarkViewed(ctx, v.UserID, v.ProductID) }
func beatPresence(ctx context.Context, v PageView)         { presence.Beat(ctx, v.UserID) }

// ProductPage returns to the shopper without waiting on any side effect.
func ProductPage(ctx context.Context, w http.ResponseWriter, userID, id string) {
    p := catalog.Get(ctx, id)
    PageViewed.Emit(ctx, PageView{UserID: userID, ProductID: id}) // fire-and-forget, returns now
    render(w, p)
}
```

**Contrast — fire-and-forget for loss-intolerant data (a bug, not a style choice):**

```go
// ❌ Orders are loss-intolerant. Fire-and-forget will silently drop them under overload.
var OrderPlaced = signals.New[Order]()
OrderPlaced.AddListener(persistToLedger)
OrderPlaced.Emit(ctx, order) // returns immediately; under load this order may never persist
```

The symptom in production: during a traffic spike, a fraction of orders never reach the
ledger and there is no error anywhere to explain it — because the stream was *promised*
never to make the producer wait, so it shed. Loss-intolerant data belongs on
[Backpressure](../flow-control/backpressure.md) or
[Await-All Dispatch](await-all-dispatch.md).

### Practical Example 2 — Cache invalidation broadcast on the write path

A common multi-node service problem: when a record is written, every node's local cache
entry must be busted. The write itself is the source of truth and must return fast — the
invalidation broadcast is best-effort fan-out (a stale entry self-heals on TTL or the next
write), so it belongs off the write path on a fire-and-forget signal.

```go
package store

import (
    "context"
    "log/slog"
    "runtime"

    "github.com/maniartech/signals"
)

// What changed — broadcast to every cache-busting listener.
type Invalidation struct {
    Key     string
    Version int64
}

// Bounded async signal: busts are loss-tolerant (TTL backstops a missed one).
var Invalidated = signals.NewWithOptions[Invalidation](&signals.SignalOptions{
    WorkerPoolSize: 4 * runtime.NumCPU(),       // 🔜 v1.4 — ceiling on concurrent busts
    Overflow:       signals.OverflowDropNewest, // 🔜 v1.4 — shed + count under a burst
})

func init() {
    signals.SetPanicHandler(func(recovered any) { // ✅ — a bad listener must not crash writes
        slog.Error("invalidation listener panicked", "recovered", recovered)
    })
    Invalidated.OnOverflow(func(dropped Invalidation) { // 🔜 v1.4
        metrics.Inc("cache.invalidation.shed")
    })

    Invalidated.AddListener(evictLocal, "local")       // drop the in-process entry
    Invalidated.AddListener(notifyPeers, "peers")      // gossip to other nodes
    Invalidated.AddListener(bumpCDN, "cdn")            // purge the edge, best-effort
}

func evictLocal(ctx context.Context, inv Invalidation) { localCache.Delete(inv.Key) }
func notifyPeers(ctx context.Context, inv Invalidation) { peers.Publish(ctx, inv) }
func bumpCDN(ctx context.Context, inv Invalidation)     { cdn.Purge(ctx, inv.Key) }

// Write persists the record (the slow, must-succeed part), then fires the broadcast and
// returns without waiting on any cache to settle.
func Write(ctx context.Context, repo Repo, key string, val []byte) (int64, error) {
    ver, err := repo.Put(ctx, key, val) // source of truth; this must succeed
    if err != nil {
        return 0, err
    }
    Invalidated.Emit(ctx, Invalidation{Key: key, Version: ver}) // fire-and-forget, returns now
    return ver, nil
}
```

The write commits and returns at storage speed; the three cache layers are busted
concurrently in the background. A purge that's shed under a write burst is harmless — the
entry's TTL or the next write covers it — which is exactly what makes this a fire-and-forget
fit rather than an `EmitAndWait` one.

## Variations

- **Bounded fire-and-forget.** The production-grade default: same non-blocking `Emit`,
  but with `WorkerPoolSize` (🔜 v1.4) capping concurrency — see
  [Bounded Concurrency](../flow-control/bounded-concurrency.md).
- **Fire-and-forget with observed loss.** Add `OnOverflow` (🔜 v1.4) so every shed event
  is counted — [Load Shedding](../flow-control/load-shedding.md).
- **Fire-and-forget with error routing.** Add `OnError` (🔜 v1.4) to learn about
  failures without blocking the caller —
  [Async Error Routing](../reliability/async-error-routing.md).
- **Detached-context fire-and-forget.** Derive a context that outlives the request when
  the side effects must complete after the handler returns —
  [Context-Scoped Emission](../architectural/context-scoped-emission.md).

## Known Uses

- **Web analytics / telemetry beacons** — page views, clicks, and impressions are fired
  off the response path; a lost beacon under load is acceptable.
- **Cache warming / read-through hints** — best-effort population that must never slow the
  request that triggered it.
- **Push notifications & webhooks fan-out** — notify many subscribers without the producer
  waiting on any of them.
- **UDP and syslog** — protocols that send without waiting for acknowledgement, accepting
  loss as the price of never blocking the sender.
- **The actor model / `go` statement** — `go f()` is fire-and-forget at the language
  level; this pattern is the structured, bounded, observable version of it.

## Related Patterns

- **[Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md)** — the
  opposite delivery mode: ordered, in-caller, blocking. Choose it when the next line
  depends on the listeners.
- **[Await-All Dispatch](await-all-dispatch.md)** — concurrent like fire-and-forget, but
  *waits* for all listeners (a completion barrier). The middle ground when you need both
  parallelism and completion.
- **[Load Shedding](../flow-control/load-shedding.md)** — the inevitable consequence of
  the never-wait promise: under overload this dispatch mode drops and counts the excess.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — bound the goroutine
  count so fire-and-forget degrades gracefully instead of melting down.
- **[Backpressure](../flow-control/backpressure.md)** — the alternative for
  loss-intolerant data: slow the producer rather than drop. The right choice when
  fire-and-forget's loss-tolerance is unacceptable.
- **[Async Error Routing](../reliability/async-error-routing.md)** — recover the outcome
  visibility that fire-and-forget discards.
- **[Panic Isolation](../reliability/panic-isolation.md)** — how a panicking listener is
  recovered and routed instead of crashing the process.
