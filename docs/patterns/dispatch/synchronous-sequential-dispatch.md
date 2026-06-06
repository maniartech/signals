# Synchronous Sequential Dispatch

**Family:** Dispatch
· **Also Known As:** Ordered Dispatch, In-Caller Dispatch, Blocking Pipeline
· **Status:** ✅ shipped

## Intent

Deliver an event to its listeners **one at a time, in registration order, in the
caller's own goroutine**, blocking until the last listener returns — so that ordered,
completion-guaranteed side effects run before the line after `Emit` executes.

## Motivation

Picture the checkout path of an e-commerce service. When an order is placed, three
things must happen *before* you can write the order row and tell the customer
"confirmed": the order must be **validated**, then its fields **normalized**
(trim, lowercase email, canonicalize currency), then a **derived total**
recomputed. Each step reads what the previous step wrote.

A developer reaches for an event signal to decouple these steps, and naively wires
them on an async, fire-and-forget signal:

```go
var OrderPlaced = signals.New[*Order]() // async!
OrderPlaced.AddListener(validate)
OrderPlaced.AddListener(normalize)
OrderPlaced.AddListener(recomputeTotal)

func PlaceOrder(ctx context.Context, o *Order) error {
    OrderPlaced.Emit(ctx, o) // returns IMMEDIATELY
    return repo.Save(ctx, o)  // saves a half-validated, un-normalized order
}
```

This is silently, dangerously wrong. `Emit` on an async signal returns *before any
listener has run* — the three listeners are scheduled on separate goroutines in **no
guaranteed order**, and `repo.Save` races them. You persist an order whose email is
still mixed-case, whose total is still zero, and whose validation may not have
happened at all. Worse, two of the listeners mutate the same `*Order` concurrently —
a data race. The bug is invisible in light testing and corrupts data under real
traffic.

The mismatch is between the *delivery semantics you need* (ordered, completed-before-I-
continue, single-threaded) and the ones you chose (concurrent, fire-and-forget).
Synchronous Sequential Dispatch is the delivery mode that matches:

```go
var OrderPlaced = signals.NewSync[*Order]() // sync!
OrderPlaced.AddListener(validate)
OrderPlaced.AddListener(normalize)
OrderPlaced.AddListener(recomputeTotal)

func PlaceOrder(ctx context.Context, o *Order) error {
    OrderPlaced.Emit(ctx, o) // runs validate → normalize → recomputeTotal, then returns
    return repo.Save(ctx, o)  // sees a fully validated, normalized order
}
```

`Emit` now runs the listeners **in your goroutine**, **in the order you added them**,
and does not return until `recomputeTotal` finishes. The line after `Emit` is
guaranteed to see all their effects, and because everything runs single-threaded
there is no race on the shared `*Order`.

## Applicability

**Use this pattern when:**

- **The next line depends on the listeners having run.** You read, after `Emit`, the
  state that listeners produced (a validated/normalized payload, a populated cache, a
  flushed buffer).
- **Order matters.** Listeners form a pipeline where each stage builds on the previous
  (validate → normalize → enrich → persist) or behave like ordered middleware.
- **You want simple, race-free reasoning.** Single-threaded execution means listeners
  can mutate shared state without locks.
- **Latency of the side effects is acceptable on the caller's path** — the work is
  short, or the caller is *meant* to wait for it.

**Avoid it (or prefer another pattern) when:**

- **Listeners are slow and independent and you don't need ordering** → run them
  concurrently with [Await-All Dispatch](await-all-dispatch.md), or detach entirely
  with [Fire-and-Forget Dispatch](fire-and-forget-dispatch.md).
- **You must stop the chain on the first error and surface it** → use `TryEmit`,
  documented under [Transactional Emission](../reliability/transactional-emission.md).
  Plain sync `Emit` **discards** listener errors.
- **The caller is on a latency-critical hot path** and the side effects are not needed
  for correctness → [Fire-and-Forget Dispatch](fire-and-forget-dispatch.md).

## Structure

```
  Caller's goroutine (blocks for the whole duration)
  ┌──────────────────────────────────────────────────────────────┐
  │                                                                │
  │  Emit(ctx, payload)                                            │
  │      │                                                         │
  │      ├─ ctx canceled at entry? ──yes──▶ run nothing, return    │
  │      │                                                         │
  │      ├─▶ Listener₁(ctx, payload)   ── completes ──┐            │
  │      │                                            │            │
  │      ├─ ctx canceled? ──yes──▶ stop, return       │ (in order) │
  │      │                                            ▼            │
  │      ├─▶ Listener₂(ctx, payload)   ── completes ──┐            │
  │      │                                            │            │
  │      ├─ ctx canceled? ──yes──▶ stop, return       ▼            │
  │      │                                                         │
  │      └─▶ Listenerₙ(ctx, payload)   ── completes ──▶ return     │
  │                                                                │
  └──────────────────────────────────────────────────────────────┘
   No goroutines spawned. Strict registration order. Blocks until done.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Calls `Emit`; intentionally **waits** for all listeners; reads their effects on the next line |
| **SyncSignal** | Iterates listeners in registration order, in the caller's goroutine; checks `ctx` between them |
| **Context** | Carries cancellation/deadline; checked at entry and between listeners to short-circuit |
| **Listener** | A `SignalListener[T]` (or `SignalListenerErr[T]`) run to completion before the next begins; may mutate shared state without locks |

## Collaborations

1. The caller invokes `Emit(ctx, payload)` and **gives up its goroutine to the
   dispatch** — it does not continue until dispatch finishes.
2. The signal first checks `ctx`. If `ctx.Err() != nil` at entry, **no listener runs**
   and `Emit` returns immediately (the canceled-context-skips-all rule).
3. Otherwise the signal calls `Listener₁`, waits for it to return, then `Listener₂`,
   and so on, in the exact order the listeners were added.
4. **Between listeners** the signal re-checks `ctx`. If it has become canceled or
   deadline-exceeded, the chain **stops mid-way** — remaining listeners do not run —
   and `Emit` returns.
5. When the last listener returns (or the context cancels), control returns to the
   caller, which now safely reads everything the listeners produced.

## Consequences

**Benefits**

- ✓ **Strict ordering.** Listeners run in registration order, every time — the basis
  for pipelines and ordered middleware.
- ✓ **Completion guarantee.** When `Emit` returns, every listener has finished; the
  next line sees all their effects.
- ✓ **Race-free by construction.** Single-threaded execution lets listeners share and
  mutate state without locks or channels.
- ✓ **Cheap and predictable.** No goroutines, no scheduling overhead, no goroutine
  pile-up under load — the cost is exactly the sum of the listeners' work.
- ✓ **Cooperative cancellation.** A canceled context stops the chain promptly between
  listeners.

**Liabilities**

- ✗ **The caller pays the full latency.** Total time is the *sum* of all listeners; a
  slow listener stalls the caller and everything behind it.
- ✗ **Head-of-line blocking.** One slow or hung listener blocks every later listener
  and the caller indefinitely (within a listener, cancellation is cooperative — the
  signal cannot interrupt a listener that ignores `ctx`).
- ✗ **Errors are discarded.** Plain `Emit` runs every listener and **throws away any
  error** they return. If you need first-error-wins semantics, use `TryEmit`
  ([Transactional Emission](../reliability/transactional-emission.md)).
- ✗ **No parallelism.** Independent listeners that *could* overlap do not — you leave
  throughput on the table when ordering isn't actually required.

> **Trilemma corner sacrificed:** Synchronous dispatch is *not* a streaming/overload
> pattern — it keeps zero loss and bounded memory but makes the **producer wait** by
> definition. It is the natural home for loss-intolerant, must-complete work, at the
> cost of caller latency.

## Implementation

1. **Construct with `NewSync[T]()`** (✅) — or `NewSyncWithOptions[T]` (✅) if you need
   a custom `InitialCapacity`/`GrowthFunc`. The zero value `var s signals.SyncSignal[T]`
   is also usable directly (lazy init). The concurrency-bounding options
   (`WorkerPoolSize`, `Overflow`) are meaningless for a sync signal — there is no
   concurrency to bound.

2. **Order is registration order — but mind removals.** Listeners fire in the order
   added. Per the API reference, removal uses *swap-remove*, so calling
   `RemoveListener` can reorder the *remaining* listeners. If exact order must survive
   churn, prefer a stable design: register the full pipeline once at startup and avoid
   mid-life removals, or model the pipeline as a single composite listener.

3. **Cancellation is checked between listeners, not inside them.** The signal inspects
   `ctx` before each listener and stops the chain if it is done. It **cannot** preempt
   a listener already running — long listeners should check `ctx` themselves at natural
   boundaries (`select { case <-ctx.Done(): ... }`) so cancellation is responsive.

4. **`Emit` discards errors — choose deliberately.** A `SignalListenerErr[T]` added via
   `AddListenerWithErr` (✅ on sync) still has its error *swallowed* by plain `Emit`.
   This is correct only when listener failures are advisory (best-effort logging,
   metrics). When a failure must abort the operation, call `TryEmit`, which stops at the
   first non-nil error and returns it — see
   [Transactional Emission](../reliability/transactional-emission.md).

5. **Keep listeners fast and bounded.** Because the caller waits for the *sum*, this
   pattern is for short, deterministic steps. Pushing network calls or unbounded I/O
   into a sync chain turns every `Emit` into a latency cliff. If a step is slow but
   order-independent, lift it out to an async path.

6. **No goroutine leaks, no panic recovery.** Sync dispatch spawns nothing, so there is
   nothing to leak. But unlike async, a **panic in a listener propagates to the
   caller** — it is *not* routed to the global panic handler (that mechanism is for
   recovered *async* panics). If a sync listener can panic and you want to isolate it,
   recover inside the listener — see [Panic Isolation](../reliability/panic-isolation.md).

7. **Shared mutable state is safe — within one emit.** Single-threaded dispatch means
   listeners on the *same* emit see each other's writes without synchronization. That
   guarantee does **not** extend across *concurrent emits* from different goroutines;
   if two goroutines emit the same signal at once, their listener runs interleave at
   the goroutine-scheduler level and shared state needs its own protection.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Caller* that blocks, the *SyncSignal* that iterates in order, the
*Context* checked between listeners, and the ordered *Listeners*. Read this first to see
the mechanics; the practical examples then apply it to real problems.

```go
// 1. SIGNAL — a SyncSignal. Dispatch runs in the caller's goroutine, in order.
sig := signals.NewSync[State]()

// 2. LISTENERS — added in the exact order they must run. Registration order == run order.
sig.AddListener(func(ctx context.Context, s State) {
    stageOne(s) // runs first; the next listener sees whatever it wrote
}, "stage-1")
sig.AddListener(func(ctx context.Context, s State) {
    stageTwo(s) // runs only after stage-1 has fully returned
}, "stage-2")

// 3. CALLER — Emit blocks until the LAST listener returns. No goroutines spawned.
sig.Emit(ctx, state)
//   ├─ ctx canceled at entry?      → run nothing, return
//   ├─▶ stage-1(ctx, state)        → completes
//   ├─ ctx canceled between?       → stop the chain, return
//   └─▶ stage-2(ctx, state)        → completes, THEN Emit returns

// 4. The next line is guaranteed to see every listener's effects — no race, no wait race.
use(state)
```

The blocking, in-order iteration (step 3) is the heart of the pattern: it is where
"ordered" meets "completed-before-I-continue," letting the line after `Emit` safely read
whatever the listeners produced. Plain sync `Emit` **discards** listener errors; reach for
`TryEmit` ([Transactional Emission](../reliability/transactional-emission.md)) when a
failure must abort the chain.

### Practical Example 1 — Ordered data-transform pipeline (parse → validate → enrich)

A day-to-day case in any service that ingests external records: each row must be
**parsed**, then **validated**, then **enriched**, then have its **derived total**
recomputed — strictly in that order, because each stage reads what the previous one
wrote — before the persistence layer is allowed to touch it.

```go
package checkout

import (
    "context"
    "errors"
    "strings"

    "github.com/maniartech/signals"
)

type Order struct {
    ID       string
    Email    string
    Currency string
    Lines    []Line
    Total    int64 // cents; derived
    valid    bool
}

// One sync signal, wired once at startup. Order of AddListener calls == run order.
var OrderPlaced = signals.NewSync[*Order]()

func init() {
    OrderPlaced.AddListener(validate, "validate")
    OrderPlaced.AddListener(normalize, "normalize")
    OrderPlaced.AddListener(recomputeTotal, "total")
}

func validate(ctx context.Context, o *Order) {
    o.valid = o.ID != "" && len(o.Lines) > 0 && o.Email != ""
}

func normalize(ctx context.Context, o *Order) {
    o.Email = strings.ToLower(strings.TrimSpace(o.Email))
    o.Currency = strings.ToUpper(o.Currency)
}

func recomputeTotal(ctx context.Context, o *Order) {
    var sum int64
    for _, l := range o.Lines {
        sum += l.UnitPrice * int64(l.Qty)
    }
    o.Total = sum
}

// PlaceOrder relies on the listeners having fully run before it saves.
func PlaceOrder(ctx context.Context, repo Repo, o *Order) error {
    OrderPlaced.Emit(ctx, o) // sequential, in-order, blocks until recomputeTotal returns

    if !o.valid {
        return errors.New("order failed validation")
    }
    // Safe: email is lowercased, currency uppercased, total computed.
    return repo.Save(ctx, o)
}
```

Because dispatch is single-threaded, `normalize` safely reads the fields `validate` set
and `recomputeTotal` reads the normalized lines — no locks, no race — and `PlaceOrder`
sees a fully prepared `*Order` on the line after `Emit`.

**Contrast — the wrong dispatch mode for this job:**

```go
// ❌ Async fire-and-forget: Emit returns before any listener runs.
var OrderPlaced = signals.New[*Order]()
// ...
OrderPlaced.Emit(ctx, o) // returns immediately
return repo.Save(ctx, o)  // races the listeners; saves a half-baked order + data race
```

The symptom in production: intermittently persisted orders with un-normalized emails
and zero totals, plus the Go race detector flagging concurrent writes to `*Order`.

### Practical Example 2 — HTTP request middleware chain

Every backend hits this: cross-cutting concerns must run **in a fixed order before the
handler** — authenticate, then rate-limit (which needs the identity auth resolved), then
record an access log — and any stage may short-circuit the request. Order is the whole
point, and the handler must see everything the chain populated.

```go
package httpmw

import (
    "context"
    "net/http"

    "github.com/maniartech/signals"
)

// Carries the request through the chain; stages read and write its fields in order.
type ReqCtx struct {
    R        *http.Request
    W        http.ResponseWriter
    UserID   string // set by auth, read by rate-limit and logging
    Aborted  bool   // any stage may short-circuit by setting this
    Status   int
}

// One sync chain, wired once. AddListener order is the middleware order.
var BeforeHandler = signals.NewSync[*ReqCtx]()

func init() {
    BeforeHandler.AddListener(authenticate, "auth")     // 1st: resolve identity
    BeforeHandler.AddListener(rateLimit, "rate-limit")  // 2nd: needs UserID from auth
    BeforeHandler.AddListener(accessLog, "log")         // 3rd: logs the resolved identity
}

func authenticate(ctx context.Context, c *ReqCtx) {
    if c.Aborted {
        return
    }
    uid, ok := verifyToken(c.R.Header.Get("Authorization"))
    if !ok {
        c.Aborted, c.Status = true, http.StatusUnauthorized
        return
    }
    c.UserID = uid // later stages depend on this being set first
}

func rateLimit(ctx context.Context, c *ReqCtx) {
    if c.Aborted {
        return
    }
    if !limiter.Allow(c.UserID) { // reads what authenticate wrote
        c.Aborted, c.Status = true, http.StatusTooManyRequests
    }
}

func accessLog(ctx context.Context, c *ReqCtx) {
    if c.Aborted {
        return
    }
    logger.Info("request", "user", c.UserID, "path", c.R.URL.Path)
}

// Middleware runs the ordered chain, then dispatches to the real handler only if no
// stage short-circuited.
func Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        c := &ReqCtx{R: r, W: w}
        BeforeHandler.Emit(r.Context(), c) // blocks: auth → rate-limit → log, in order

        if c.Aborted {
            http.Error(w, http.StatusText(c.Status), c.Status)
            return
        }
        next.ServeHTTP(w, r) // safe: identity resolved, limit checked, request logged
    })
}
```

Running these concurrently would be a bug: `rateLimit` would race `authenticate` and see
an empty `UserID`. Sequential, in-caller dispatch is exactly the ordered-middleware
semantics this needs.

## Variations

- **`TryEmit` (transactional).** Same sequential, in-caller execution, but stops at the
  first listener that returns a non-nil error and returns it — turning the pipeline
  into a fail-fast transaction. See
  [Transactional Emission](../reliability/transactional-emission.md).
- **Ordered middleware chain.** Model cross-cutting concerns (auth → rate-limit →
  audit → handle) as sequential listeners; each can short-circuit by checking and
  populating fields on the payload. Order is the whole point.
- **Composite listener.** When the pipeline is fixed, register one listener that calls
  the stages internally. This makes ordering immune to subscription churn (see
  Implementation note 2) at the cost of dynamic subscribe/unsubscribe.
- **Sync with explicit deadline.** Pass a `context.WithTimeout` so the between-listener
  cancellation check bounds the total chain time, provided listeners themselves respect
  `ctx`.

## Known Uses

- **HTTP/middleware chains** (Go's `net/http` middleware, Express, ASP.NET) — ordered,
  in-request, each layer runs to completion before the next; the canonical sequential
  dispatch.
- **Database triggers** — `BEFORE INSERT`/`BEFORE UPDATE` triggers fire synchronously,
  in a defined order, and the row write sees their effects.
- **The Go stdlib** — `sort.Slice`'s `less` callbacks, `template` funcs, and
  `http.HandlerFunc` composition all run synchronously in the caller's goroutine.
- **Domain-event handlers in DDD** — in-process handlers that must complete within the
  same unit of work / transaction before commit.
- **Build pipelines / interceptor stacks** (gRPC interceptors, middleware) — ordered,
  blocking, each stage layering behavior on the next.

## Related Patterns

- **[Transactional Emission](../reliability/transactional-emission.md)** — the
  error-aware sibling: same sequential semantics, but `TryEmit` stops on the first
  error and returns it instead of discarding it. Reach for it whenever a listener
  failure must abort the operation.
- **[Await-All Dispatch](await-all-dispatch.md)** — when the listeners are *independent*
  and slow, run them concurrently and join, instead of summing their latency
  sequentially. Trades ordering for parallelism.
- **[Fire-and-Forget Dispatch](fire-and-forget-dispatch.md)** — the opposite choice:
  detach the side effects entirely when the caller must not wait and need not learn the
  outcome.
- **[Panic Isolation](../reliability/panic-isolation.md)** — note that, unlike async, a
  panic in a *sync* listener propagates to the caller; isolate inside the listener if
  needed.
- **[Context-Scoped Emission](../architectural/context-scoped-emission.md)** — how the
  `ctx` you pass to `Emit` drives the between-listener cancellation that stops the chain.
