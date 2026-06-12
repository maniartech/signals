# Async Error Routing

**Family:** Reliability
· **Also Known As:** Error Sink, Out-of-Band Error Handler
· **Status:** ✅ shipped — `OnError` (on **both** sync and async signals) and
  error-returning listeners (`AddListenerWithErr`) ship in v1.4

## Intent

Surface failures from **fire-and-forget** async listeners — which have no caller left
to return an error to — by **routing the returned error to a registered per-signal
handler**, the same out-of-band way that panics are routed. *Fire-and-forget must not
mean fail-silently.*

## Motivation

A SaaS app sends a welcome email when a user signs up. Email delivery is slow and
flaky (third-party API, network, rate limits), and the signup request must not wait on
it — so the email goes out on a fire-and-forget async signal:

```go
var UserRegistered = signals.New[User]() // AsyncSignal
UserRegistered.AddListener(func(ctx context.Context, u User) {
    _ = mailer.SendWelcome(ctx, u.Email) // ❌ error has nowhere to go
})

// In the signup handler:
UserRegistered.Emit(ctx, user) // returns immediately; the HTTP response is sent
```

Here is the structural problem that makes this *worse* than a normal error. With
`Emit` on an `AsyncSignal`, the call **returns immediately** — by the time the mailer
actually fails 800ms later, the `Emit` call site is **long gone**: the HTTP handler
has already written `201 Created` and the goroutine that called `Emit` has moved on.
**There is no return value to put an error into, and no caller standing there to
receive it.** So the listener does the only thing it can: it swallows the error with
`_ =`. The welcome email silently never arrives. Multiply that across an outage and you
have thousands of users who never got onboarded, **and not a single log line or metric
to tell you it happened**. The first you hear of it is a support ticket.

Notice this is *exactly* the situation the library already solves for **panics**: an
async listener that panics has no caller to unwind into, so the panic is recovered and
routed to a global handler instead of crashing the process. A **returned error from an
async listener is the same shape of problem** — a failure with no caller to catch it —
and it deserves the same solution: route it out-of-band to a registered sink.

`OnError` (✅) provides that sink. Make the listener error-returning
(`AddListenerWithErr`, ✅), and register a handler:

```go
var UserRegistered = signals.New[User]()

UserRegistered.AddListenerWithErr(func(ctx context.Context, u User) error {
    return mailer.SendWelcome(ctx, u.Email) // ✅ — error now goes somewhere
}, "welcome-email")

// Per-signal error sink. Called out-of-band whenever a listener returns non-nil.
UserRegistered.OnError(func(ctx context.Context, err error) { // ✅
    log.Error("welcome email failed", "err", err)
    metrics.Inc("welcome_email.failures")
})

UserRegistered.Emit(ctx, user) // still fire-and-forget; failures are now visible
```

The signup path is still non-blocking, but a failed delivery is now **counted and
logged** the instant it happens. The failure became *visible* without the producer
ever having to wait for it.

## Applicability

**Use this pattern when:**

- You dispatch **fire-and-forget** (`AsyncSignal.Emit`) and the producer has already
  moved on by the time a listener might fail — there is no return value to use.
- The listeners **can fail** in expected ways (network, third-party APIs, I/O) and you
  need those failures **observable** (logged, metered, alerted) even though you did not
  wait for them.
- You want failure handling **per signal** — different signals route their errors to
  different sinks (email failures vs. webhook failures) with their own context.

**Avoid it (or prefer another pattern) when:**

- You **waited** for the listeners and want a return value → you have a caller to
  return to, so use [Result Aggregation](result-aggregation.md) (`TryEmit`).
- The dispatch is **synchronous** and you want the error **returned** → the caller is
  right there; use [Transactional Emission](transactional-emission.md) (`TryEmit`).
  (On the sync best-effort `Emit` path, errors route to `OnError` instead — `OnError`
  is on sync signals too.)
- The failure is a **panic** (an unexpected bug), not a returned error → that is routed
  globally via [Panic Isolation](panic-isolation.md) and `SetPanicHandler`.
- The event is **loss-tolerant** and you care about *overload* behavior rather than a
  listener that *ran and failed* → that is the flow-control concern of
  [Load Shedding](../flow-control/load-shedding.md) (an explicit drop policy is
  🔭 post-v1.4), distinct from this pattern.

## Structure

```
  Producer ──Emit(ctx, payload)──▶ AsyncSignal ──▶ returns IMMEDIATELY
   (gone)                              │            (no value to carry an error)
                                       │
                         spawns ───────┼───────── ... (fire-and-forget)
                                       ▼
                         ┌─────────────────────────────┐
                         │ async listener runs later    │
                         │   ├─ returns nil  ─▶ done     │
                         │   ├─ returns err  ─▶ OnError(ctx, err)  ◀── routed sink
                         │   └─ panics       ─▶ SetPanicHandler    ◀── separate path
                         └─────────────────────────────┘
                                       ▲
                                       │
                 The Emit caller is long gone — the error has
                 no return path, so it goes out-of-band to OnError.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Producer (Emitter)** | Calls `Emit`; returns immediately; is *gone* before any failure occurs |
| **AsyncSignal** | Dispatches listeners detached; routes a listener's returned error to `OnError` |
| **Error-returning listener** | `SignalListenerErr[T]` added via `AddListenerWithErr` (✅); returns the failure |
| **Error sink(s) (`OnError`)** | Per-signal handler(s) `func(ctx, err)` on **both** sync and async signals; **multiple may be registered** (additive), each logs/meters/alerts out-of-band (✅) |
| **Panic handler** | Separate, *global* sink for panics — the same out-of-band idea for bugs (see [Panic Isolation](panic-isolation.md)) |

## Collaborations

1. The producer calls `Emit(ctx, payload)` and **immediately regains control** — the
   fire-and-forget contract. The producer typically completes its own work (e.g. writes
   the HTTP response) right after.
2. Some time later, the signal runs each error-returning listener in a detached
   goroutine.
3. A listener returns a non-nil error. Because the original `Emit` caller is gone,
   there is **nowhere to return it** — so the signal invokes **every** registered
   `OnError(ctx, err)` handler with that error (sinks are additive).
4. The `OnError` handler does its lightweight, non-blocking job: log, increment a
   counter, push to an alerting pipeline.
5. If no `OnError` handler is registered, the error has no sink and is effectively
   dropped — which is why registering at least one is the whole point of the pattern.
   When several sinks are registered, each is invoked with the same error.
6. A *panic* (as opposed to a returned error) takes the separate, global panic-handler
   path; the two failure channels are deliberately distinct.

## Consequences

**Benefits**

- ✓ **Failures become visible.** A fire-and-forget failure is logged/metered/alerted
  instead of silently swallowed — the core win.
- ✓ **Producer stays non-blocking.** The hot path keeps its fire-and-forget latency;
  error handling happens entirely out-of-band.
- ✓ **Per-signal routing.** Each signal can send its errors to a sink with the right
  context (which channel failed, which payload type), instead of one global mush.
- ✓ **Symmetry with panics.** The same mental model — "no caller, so route it out-of-
  band" — covers both errors (`OnError`) and panics (`SetPanicHandler`).

**Liabilities**

- ✗ **No return value, ever.** The producer fundamentally cannot know the outcome
  inline; if you *need* the result, you needed `TryEmit`, not `Emit`.
- ✗ **No automatic retry.** `OnError` *observes* the failure; it does not re-deliver.
  Retry/DLQ logic must be built on top (see Implementation).
- ✗ **Ordering and timing are loose.** `OnError` fires whenever a listener finishes
  failing, in no guaranteed order relative to other listeners — it is a notification,
  not a sequence point.
- ✗ **Silent if unregistered.** Forget to call `OnError` and you are back to fail-
  silently. The pattern only helps if the sink exists.

> **Trilemma note:** This sits on the fire-and-forget (never-wait) path, so it inherits
> that path's properties. Async Error Routing does not change *what* is delivered; it
> makes the *failures of what was delivered* observable.

## Implementation

1. **The listener must be error-returning.** Use `AddListenerWithErr` (✅)
   with a `SignalListenerErr[T]`. A plain `AddListener` listener returns
   nothing, so `OnError` can never fire for it — there is no error to route.

2. **Register `OnError` once, at wiring time.** Set it where you construct/initialize
   the signal, before the first `Emit`, so no early failure slips through unrouted.
   Treat a missing `OnError` as a configuration bug for any signal whose listeners can
   fail.

3. **Keep the `OnError` handler cheap and non-blocking.** It runs on the listener's
   goroutine, off the hot path, but a slow handler still ties up that goroutine (and,
   under a `MaxConcurrent` bound, its semaphore slot) and can mask throughput. Increment an atomic counter, log structured fields, or push to a
   buffered alerting client — never do unbounded I/O or acquire a contended lock
   inside it.

4. **Errors and panics are different channels — by design.** A returned error is an
   *expected* failure (the email API said no); a panic is an *unexpected* bug (nil
   dereference). `OnError` is **per-signal** and carries the `ctx`; the panic handler is
   **global** (`SetPanicHandler`) and carries the recovered value. Wire both, and keep
   their metrics separate so a spike in one is not hidden by the other.

5. **`OnError` does not retry — build retry on top if you need it.** Inside the handler
   (or inside the listener before returning) you can enqueue the failed payload onto a
   retry queue or dead-letter topic. Keep that enqueue itself non-blocking and bounded;
   an unbounded retry buffer reintroduces the meltdown that fire-and-forget was avoiding.

6. **Carry enough context to act on the error.** The handler receives `ctx` and `err`,
   but not the payload. If you need the payload for retry/forensics, wrap it into the
   error before returning it (`fmt.Errorf("send welcome to %s: %w", u.Email, err)`), or
   embed an identifying field, so the sink can reconstruct what failed.

7. **Register one sink or several — they are additive.** `OnError` supports **multiple
   registrations** on the same signal; every registered sink is invoked with each error
   (✅). Register a dedicated sink per concern (logging, metrics, alerting), or fan
   out inside a single handler — both are valid. Keep each sink cheap and non-blocking,
   since they all run per failure.

8. **`OnError` is strictly for listeners that *ran and returned an error*.** It is not an
   overload signal: in v1.4 a `MaxConcurrent` bound makes excess handlers **park** until
   a slot frees (no drop, no caller-block), so nothing is shed before running. Explicit
   drop/overflow policies are **🔭 post-v1.4** (see
   [Load Shedding](../flow-control/load-shedding.md)) and out of scope here.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Producer* that is gone before any failure, the *AsyncSignal*, the
*error-returning listener*, and the out-of-band *error sink*. Read this first to see the
mechanics; the practical examples then apply it to real problems.

```go
// 1. ASYNC SIGNAL — fire-and-forget; Emit returns before listeners finish.
sig := signals.New[Job]()

// 2. ERROR-RETURNING LISTENER (✅) — may fail with NO caller waiting.
sig.AddListenerWithErr(func(ctx context.Context, j Job) error {
    return doWork(ctx, j) // returns later, when the Emit caller is long gone
}, "worker")

// 3. ERROR SINK — the out-of-band route for a failure that has no return path.
//    Cheap & non-blocking. Register ONCE at wiring time, before the first Emit.
sig.OnError(func(ctx context.Context, err error) { // ✅
    failures.Add(1)                  // count — never silent
    log.Error("job failed", "err", err)
})

// 4. PRODUCER — fire-and-forget; ALWAYS returns immediately.
sig.Emit(ctx, j)
//   ├─ listener returns nil → done
//   ├─ listener returns err → OnError(ctx, err) fires out-of-band (no caller to catch it)
//   └─ listener panics       → SetPanicHandler (a SEPARATE, global channel)
```

The out-of-band routing (step 3) is the heart of the pattern: it is where a
fire-and-forget failure — which has no return value and no caller — becomes visible
instead of silently swallowed.

### Practical Example 1 — Notification fan-out delivery failures

A notification fan-out where each detached delivery failure is routed, counted, and
optionally retried — without ever blocking the producer:

```go
package notify

import (
    "context"
    "fmt"
    "log/slog"
    "sync/atomic"

    "github.com/maniartech/signals"
)

type Event struct {
    UserID  string
    Kind    string
    Payload []byte
}

var (
    deliverFailures atomic.Uint64
    notifications   *signals.AsyncSignal[Event]
)

func Init(mailer Mailer, hooks WebhookClient, dlq RetryQueue) {
    notifications = signals.New[Event]()

    // Error-returning listeners (✅). Each may fail with no caller waiting.
    notifications.AddListenerWithErr(func(ctx context.Context, e Event) error {
        if err := mailer.Send(ctx, e.UserID, e.Payload); err != nil {
            return fmt.Errorf("email to %s: %w", e.UserID, err) // wrap with identity
        }
        return nil
    }, "email")

    notifications.AddListenerWithErr(func(ctx context.Context, e Event) error {
        if err := hooks.Post(ctx, e.UserID, e.Payload); err != nil {
            return fmt.Errorf("webhook to %s: %w", e.UserID, err)
        }
        return nil
    }, "webhook")

    // The out-of-band error sink (✅): cheap, non-blocking, visible.
    notifications.OnError(func(ctx context.Context, err error) {
        deliverFailures.Add(1)
        slog.ErrorContext(ctx, "notification delivery failed", "err", err)
        dlq.Enqueue(err) // bounded retry queue; build retry ON TOP of OnError
    })

    // Panics are a SEPARATE, global channel — unexpected bugs, not expected failures.
    signals.SetPanicHandler(func(recovered any) {
        slog.Error("notification listener panicked", "recovered", recovered)
    })
}

// Notify is fire-and-forget: returns immediately, failures surface via OnError.
func Notify(ctx context.Context, e Event) {
    notifications.Emit(ctx, e) // producer never waits; nothing fails silently anymore
}

// FailureCount exposes the routed-error total to a /metrics endpoint.
func FailureCount() uint64 { return deliverFailures.Load() }
```

**Contrast — the fail-silently anti-pattern:**

```go
// ❌ Fire-and-forget with a swallowed error: failures are invisible.
notifications.AddListener(func(ctx context.Context, e Event) {
    _ = mailer.Send(ctx, e.UserID, e.Payload) // error discarded; no OnError to catch it
})
notifications.Emit(ctx, e)
```

The symptom in production: an upstream provider has an outage, deliveries fail en
masse, and your dashboards stay green — because nothing ever recorded that the
fire-and-forget work failed. You learn about it from users, not from your own system.

### Practical Example 2 — Background search-index updates

When a product is edited, its searchable document must be re-indexed. The save request
must not wait on the search cluster, so re-indexing fires async — but an indexing
failure means stale search results, so it must be logged and retried, never lost.

```go
package catalog

import (
    "context"
    "fmt"
    "log/slog"
    "sync/atomic"

    "github.com/maniartech/signals"
)

type ProductChanged struct {
    ProductID string
    Revision  int64
}

var (
    indexFailures atomic.Uint64
    productEdited *signals.AsyncSignal[ProductChanged]
)

func Init(index SearchIndex, retries RetryQueue) {
    productEdited = signals.New[ProductChanged]()

    // Error-returning listener (✅): re-index off the request path.
    productEdited.AddListenerWithErr(func(ctx context.Context, c ProductChanged) error {
        if err := index.Upsert(ctx, c.ProductID, c.Revision); err != nil {
            // Wrap with identity so the sink can reconstruct what to retry.
            return fmt.Errorf("reindex product %s rev %d: %w", c.ProductID, c.Revision, err)
        }
        return nil
    }, "search-index")

    // Out-of-band sink (✅): log, count, and schedule a retry — bounded.
    productEdited.OnError(func(ctx context.Context, err error) {
        indexFailures.Add(1)
        slog.ErrorContext(ctx, "search reindex failed", "err", err)
        retries.Schedule(err) // build retry ON TOP of OnError; keep the queue bounded
    })
}

// OnProductSaved returns immediately; the save handler never blocks on the index.
func OnProductSaved(ctx context.Context, c ProductChanged) {
    productEdited.Emit(ctx, c) // fire-and-forget; failures surface via OnError
}

// IndexFailureCount exposes the routed-error total to a /metrics endpoint.
func IndexFailureCount() uint64 { return indexFailures.Load() }
```

When the search cluster is degraded, product saves stay fast and the `indexFailures`
counter climbs while failed documents land on the retry queue — a stale index that
self-heals instead of one that silently drifts out of sync.

## Variations

- **Route to a dead-letter queue.** `OnError` enqueues the failed work for later retry
  or manual inspection rather than only counting it — the durable form of the pattern.
- **Severity-aware routing.** Inspect the error inside `OnError` (`errors.Is` /
  `errors.As`) and split: transient errors → retry queue, permanent errors → alert a
  human.
- **Per-signal vs. shared sink.** Give each signal its own `OnError` for precise
  attribution, or have several signals call into one shared handler function for a
  single failure pipeline — your choice of granularity. Because `OnError` is additive,
  one signal can also feed *both* a per-signal sink and a shared pipeline at once.
- **Errors *and* panics to one pipeline.** Have both `OnError` and the global
  `SetPanicHandler` feed the same incident-reporting client (with distinct tags) so all
  detached failures land in one place while staying classifiable.

## Known Uses

- **Message-queue dead-letter queues** (RabbitMQ, SQS, Kafka). Messages that fail
  processing are routed to a DLQ instead of vanishing — the canonical out-of-band
  failure sink for detached work.
- **JavaScript `unhandledRejection` / `window.onerror`.** Async failures with no
  awaiter are routed to a global handler — exactly this "no caller, route out-of-band"
  idea.
- **Erlang/OTP supervisors.** A detached process's failure is reported to its
  supervisor rather than lost; the supervisor decides how to react.
- **Sidekiq / Celery error callbacks (`on_failure`)** — background-job frameworks
  route a failed job to a configured handler since the enqueuer is long gone.
- **Go's `http.Server.ErrorLog`.** Errors from connections the caller can't see are
  routed to a configured logger rather than returned.

## Related Patterns

- **[Result Aggregation](result-aggregation.md)** — the alternative when you *did*
  wait: async `TryEmit` returns a joined error to a caller that is still there. Use it
  when you have a return path; use Async Error Routing when you do not.
- **[Panic Isolation](panic-isolation.md)** — the sibling for *unexpected* failures.
  Same out-of-band philosophy, but global (`SetPanicHandler`) and for panics, not
  returned errors.
- **[Transactional Emission](transactional-emission.md)** — the synchronous analogue
  where the caller receives the first error directly via `TryEmit`.
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** — the
  delivery mode that creates the "no caller to return to" situation this pattern
  resolves.
- **[Load Shedding](../flow-control/load-shedding.md)** — the flow-control concern of an
  event *shed under overload before running* (an explicit drop policy is 🔭 post-v1.4),
  as distinct from a listener that *ran and returned an error* (→ `OnError`). In v1.4 a
  `MaxConcurrent` bound parks excess handlers rather than dropping them.
