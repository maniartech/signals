# Panic Isolation

**Family:** Reliability
· **Also Known As:** Bulkhead, Fault Containment
· **Status:** ✅ shipped

## Intent

Stop **one listener's panic** from crashing the process or aborting its sibling
listeners: recover the panic at the dispatch boundary and **route it to a configurable
handler**, so a bug in one subscriber stays contained to that subscriber.

## Motivation

A growing application has many teams, and each registers listeners on a shared
`UserSignedUp` signal: the billing team starts a trial, the analytics team records a
funnel event, the email team queues a welcome message, a third-party plugin syncs the
user to a CRM. None of these teams reviews the others' code.

One day the CRM plugin ships a bug: on users whose `Company` field is `nil`, it
dereferences it and **panics**. Without isolation, here is the blast radius on an async
signal where listeners share goroutine machinery:

```go
var UserSignedUp = signals.New[User]()
UserSignedUp.AddListener(startTrial)     // billing
UserSignedUp.AddListener(recordFunnel)   // analytics
UserSignedUp.AddListener(syncToCRM)      // plugin — panics on nil Company
UserSignedUp.AddListener(queueWelcome)   // email

UserSignedUp.Emit(ctx, user) // one listener's panic...
```

In plain Go, an unrecovered panic in a goroutine **crashes the entire process**. So a
single team's `nil`-dereference — in an *optional* CRM integration — takes down the
whole application: billing, analytics, and email all die with it, and every in-flight
request is dropped. A non-critical plugin just caused a full outage. This is the
failure mode bulkheads exist to prevent: a leak in one compartment must not sink the
ship.

Panic Isolation contains it. The library **automatically recovers** panics from async
listeners so they never crash the process, and routes the recovered value to a handler
you configure once with `SetPanicHandler`:

```go
// At startup, wire panics to your monitoring so they are visible, not just swallowed.
signals.SetPanicHandler(func(recovered any) { // ✅ global, set once
    monitoring.ReportPanic(recovered)
    log.Printf("recovered listener panic: %v", recovered)
})

var UserSignedUp = signals.New[User]()
UserSignedUp.AddListener(startTrial)
UserSignedUp.AddListener(recordFunnel)
UserSignedUp.AddListener(syncToCRM)   // still panics on nil Company...
UserSignedUp.AddListener(queueWelcome)

UserSignedUp.Emit(ctx, user) // ...but the panic is recovered and reported; siblings run
```

Now the CRM plugin's bug is contained to the CRM listener. Billing, analytics, and
email keep working, the process stays up, and the panic shows up in monitoring as an
alert to fix — a bug report instead of an outage.

## Applicability

**Use this pattern when:**

- Listeners come from **different teams, packages, or plugins** and you cannot vouch for
  every one — a bug in any of them must not take down the others or the process.
- You are running **async** dispatch, where an unrecovered panic would otherwise crash
  the process (an unrecovered goroutine panic is fatal in Go).
- You want panics **observable** — reported to monitoring/logging — rather than silently
  swallowed.

**Avoid it (or rely on it differently) when:**

- The failure is an **expected error**, not a bug → return an `error` and handle it via
  [Async Error Routing](async-error-routing.md) or [Result Aggregation](result-aggregation.md).
  Do not `panic` for control flow.
- You need **per-signal** failure routing → panics are *global* by design; expected
  failures are per-signal via `OnError`. Use the right channel for each (see
  Implementation).
- A panic indicates **unrecoverable, process-wide corruption** (e.g. you intentionally
  want to fail fast on an invariant violation). Recovering and continuing can hide a
  genuinely dangerous state — be deliberate about which invariants you are willing to
  swallow.

## Structure

```
  Emitter ──Emit / TryEmit──▶ AsyncSignal
                                      │
              ┌───────────────────────┼───────────────────────┐
              ▼                        ▼                        ▼
        Listener₁ (ok)          Listener₂ (PANICS)        Listener₃ (ok)
              │                        │                        │
              │                  recover() at the              │
              │                  dispatch boundary             │
              │                        │                        │
              │                        ▼                        │
              │             SetPanicHandler(recovered)          │
              │             (global; default logs; nil discards)│
              │                        │                        │
              └──────────── siblings unaffected ────────────────┘
                         process stays alive
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Emitter** | Emits the payload; never sees the panic and is never crashed by it |
| **AsyncSignal** | Wraps each listener call in `recover()`; contains the panic at the dispatch boundary |
| **Listener (faulty)** | The subscriber whose bug panics; its panic is caught, its work abandoned |
| **Sibling listeners** | Run unaffected — one listener's panic does not stop the others |
| **Panic handler** | `SetPanicHandler(func(recovered any))` — *global*, receives the recovered value; default logs via stdlib `log`; `nil` discards |

## Collaborations

1. The emitter calls `Emit` (or `TryEmit`). The signal dispatches each listener.
2. A faulty listener panics partway through its work.
3. The signal's recovery boundary catches the panic with `recover()` — it **never
   propagates** out of the dispatch machinery, so the process is not crashed.
4. The signal invokes the **global** panic handler set via `SetPanicHandler`, passing
   the recovered value. If no handler was set, the **default** handler logs it via the
   standard library `log`; if the handler was explicitly set to `nil`, the panic is
   **discarded** silently.
5. The faulty listener's remaining work is abandoned (it panicked, so it produced no
   result), but **sibling listeners continue to run** normally.
6. The emitter and the rest of the application proceed as if that one listener had
   simply done nothing.

## Consequences

**Benefits**

- ✓ **Fault containment.** A bug in one listener cannot crash the process or stop
  sibling listeners — the bulkhead holds.
- ✓ **Process survival under bad plugins.** Third-party or cross-team code is sandboxed
  against the most catastrophic failure mode.
- ✓ **Observable panics.** Routed to a handler you control, so a panic becomes an alert
  to fix rather than a silent or fatal event.
- ✓ **Zero boilerplate.** Recovery is automatic for async listeners; you do not sprinkle
  `defer recover()` into every subscriber.

**Liabilities**

- ✗ **The faulty listener's work is lost.** Recovery contains the damage; it does not
  complete the abandoned work. That listener simply did nothing for that emission.
- ✗ **Global, not per-signal.** One panic handler serves the whole program. You cannot
  route panics from different signals to different handlers (errors *can* be routed per
  signal — that asymmetry is deliberate; see Implementation).
- ✗ **Can mask real bugs if misused.** If you `panic` for ordinary control flow and rely
  on recovery, you hide failures that should have been returned errors. Recovery is for
  *unexpected* bugs, not a substitute for error handling.
- ✗ **No automatic retry.** A recovered panic is not re-attempted.

> **Trilemma note:** Panic Isolation is orthogonal to the bounded/never-wait/never-lose
> trilemma — it is about *fault containment*, not flow control. It applies on every
> dispatch path.

## Implementation

1. **Set the handler once, at startup, before any emit.** `SetPanicHandler` is global
   and atomic; register your monitoring-reporting handler in `main`/init so no early
   panic is missed. Setting it after panics have already occurred means those were only
   logged by the default handler (or discarded if you nil'd it).

2. **The default logs; `nil` discards — choose deliberately.** With no handler set,
   recovered panics are logged via the stdlib `log` package, so they are at least
   visible. Passing `nil` to `SetPanicHandler` **silences** them entirely — only do that
   if you truly want panics swallowed, which is rarely what you want in production.

3. **Always report to monitoring in production.** The single most valuable thing the
   handler can do is surface the panic to your error-tracking/alerting system (Sentry,
   Datadog, etc.) plus a structured log. A contained-but-invisible panic is a silently
   broken listener; make it loud.

4. **Keep the handler cheap and panic-free.** It runs on the dispatch path. Do not do
   heavy I/O or acquire contended locks in it, and make sure the handler itself cannot
   panic (guard it), or you create a failure inside the failure handler.

5. **Panics are *global*; errors are *per-signal* — by design.** This is the central
   distinction of the Reliability family. A *panic* is an **unexpected bug** with no
   meaningful per-signal semantics, so it routes to one global `SetPanicHandler`. An
   *expected failure* is a returned `error`, routed **per signal** via `OnError`
   ([Async Error Routing](async-error-routing.md)) or returned via `TryEmit`. Use a
   returned error for things you anticipate; reserve panics for genuine bugs.

6. **Recovery is automatic for async; sync surfaces to the caller.** Async listener
   panics are recovered and routed because there is no caller to unwind into (the same
   "no caller" logic as async error routing). On a synchronous `Emit`/`TryEmit`, the
   caller is right there on the stack — handle a panicking sync listener with the
   caller's own `recover` if you need to, in addition to the global handler.

7. **Do not rely on isolation to fix the bug.** Containment keeps you up; it does not
   make the listener correct. Treat every routed panic as a defect to fix at the source,
   not a steady-state condition to live with.

8. **Distinguish a panic from a shed event and from a returned error.** Three different
   things land in three places: a *shed* event never ran a listener
   ([Load Shedding](../flow-control/load-shedding.md) → `OnOverflow`, 🔭 post-v1.4 —
   nothing is shed in v1.4); a *returned error*
   ran and failed expectedly (→ `OnError` / joined result); a *panic* ran and hit a bug
   (→ `SetPanicHandler`). Keep their metrics separate so dashboards stay legible.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Emitter*, the *AsyncSignal* with its recovery boundary, a *faulty
listener*, its *siblings*, and the global *panic handler*. Read this first to see the
mechanics; the practical examples then apply it to real problems.

```go
// 1. PANIC HANDLER — global, set ONCE at startup, before any Emit. Make panics an
//    alert, not a silent or fatal event. Keep it cheap and panic-free.
signals.SetPanicHandler(func(recovered any) { // ✅ global
    monitoring.ReportPanic(recovered) // route to Sentry/Datadog, etc.
})

// 2. ASYNC SIGNAL — wraps each listener call in recover() at the dispatch boundary.
sig := signals.New[Event]()

// 3. LISTENERS — siblings that must not be taken down by each other.
sig.AddListener(func(ctx context.Context, e Event) { handleOK(ctx, e) }, "safe")
sig.AddListener(func(ctx context.Context, e Event) {
    risky(ctx, e) // may panic — a bug, not an expected error
}, "risky")

// 4. EMITTER — fire-and-forget (or TryEmit); never sees a listener's panic.
sig.Emit(ctx, e)
//   ├─ "safe"  runs normally
//   ├─ "risky" panics → recover() at the boundary → SetPanicHandler(recovered)
//   └─ the process stays alive and "safe" is UNAFFECTED — the bulkhead holds
```

The recovery boundary (step 4) is the heart of the pattern: it is where one listener's
bug is contained to that listener instead of crashing the process or its siblings.

### Practical Example 1 — Plugin listeners from different teams

A shared signal with listeners from multiple teams, hardened so any one of them can
panic without taking the others (or the process) down:

```go
package onboarding

import (
    "context"
    "log"

    "github.com/maniartech/signals"
)

type User struct {
    ID      string
    Email   string
    Company *Company // optional — may be nil
}

var userSignedUp = signals.New[User]()

// InstallPanicReporting wires recovered panics to monitoring. Call ONCE at startup,
// before the first Emit, so no early panic is missed.
func InstallPanicReporting(mon Monitoring) {
    signals.SetPanicHandler(func(recovered any) { // ✅ global, atomic
        mon.ReportPanic(recovered)                 // make it an alert, not a silent event
        log.Printf("onboarding: recovered listener panic: %v", recovered)
    })
}

func Init(billing Billing, analytics Analytics, crm CRM, mail Mailer) {
    // Billing — critical, well-reviewed.
    userSignedUp.AddListener(func(ctx context.Context, u User) {
        billing.StartTrial(ctx, u.ID)
    }, "billing")

    // Analytics.
    userSignedUp.AddListener(func(ctx context.Context, u User) {
        analytics.RecordFunnel(ctx, "signup", u.ID)
    }, "analytics")

    // Third-party CRM plugin — NOT reviewed by us. If it dereferences a nil
    // Company and panics, isolation keeps billing/analytics/email alive and the
    // process up; the panic is reported to monitoring as a bug to fix.
    userSignedUp.AddListener(func(ctx context.Context, u User) {
        crm.Sync(ctx, u.Company.Name, u.Email) // panics when u.Company == nil
    }, "crm")

    // Email.
    userSignedUp.AddListener(func(ctx context.Context, u User) {
        mail.QueueWelcome(ctx, u.Email)
    }, "email")
}

// Register fires the signal. A panic in any one listener is contained automatically.
func Register(ctx context.Context, u User) {
    userSignedUp.Emit(ctx, u) // a buggy CRM sync no longer crashes onboarding
}
```

**Contrast — no handler wired, and the wrong use of panic:**

```go
// ❌ panic used for expected control flow, with panics silenced.
signals.SetPanicHandler(nil) // discards panics → bugs vanish without a trace
userSignedUp.AddListener(func(ctx context.Context, u User) {
    if u.Company == nil {
        panic("missing company") // this is an EXPECTED case — should be a returned error
    }
})
```

The symptom: a foreseeable condition is modeled as a panic and then discarded, so the
"missing company" case fails silently with no error to inspect and no alert — the
opposite of what reliability tooling is for. Model expected cases as errors; reserve
panics (and this pattern) for genuine bugs.

### Practical Example 2 — One bad listener in a notification fan-out

A single `OrderShipped` event fans out to several independent notifiers — SMS, push,
in-app feed, a partner webhook. A bug in any one (a malformed template, a nil pointer)
must not stop the siblings from notifying the customer or crash the worker process.

```go
package shipping

import (
    "context"
    "log"

    "github.com/maniartech/signals"
)

type Shipment struct {
    OrderID  string
    UserID   string
    Carrier  string
    Tracking string
}

var orderShipped = signals.New[Shipment]()

// InstallPanicReporting wires recovered panics to monitoring. Call ONCE at startup,
// before the first Emit, so no early panic is missed.
func InstallPanicReporting(mon Monitoring) {
    signals.SetPanicHandler(func(recovered any) { // ✅ global, atomic
        mon.ReportPanic(recovered) // a contained panic is still a defect to fix
        log.Printf("shipping: recovered notifier panic: %v", recovered)
    })
}

func Init(sms SMSClient, push PushClient, feed ActivityFeed, partner WebhookClient) {
    // SMS — well-reviewed.
    orderShipped.AddListener(func(ctx context.Context, s Shipment) {
        sms.Send(ctx, s.UserID, "Your order "+s.OrderID+" has shipped")
    }, "sms")

    // Push notification.
    orderShipped.AddListener(func(ctx context.Context, s Shipment) {
        push.Send(ctx, s.UserID, s.Tracking)
    }, "push")

    // In-app activity feed — renders from a template that can panic on bad data.
    // If it does, isolation keeps SMS, push, and the partner webhook running.
    orderShipped.AddListener(func(ctx context.Context, s Shipment) {
        feed.Append(ctx, s.UserID, renderTemplate(s)) // may panic on a malformed field
    }, "feed")

    // Third-party partner webhook — not our code.
    orderShipped.AddListener(func(ctx context.Context, s Shipment) {
        partner.Post(ctx, s.OrderID, s.Carrier, s.Tracking)
    }, "partner")
}

// Notify fans out the shipment event. A panic in any one notifier is contained
// automatically; the customer still gets every other notification.
func Notify(ctx context.Context, s Shipment) {
    orderShipped.Emit(ctx, s) // a buggy feed render no longer silences SMS or push
}
```

If the activity-feed template panics on one malformed shipment, that customer simply
misses their feed entry — but their SMS and push still arrive, the partner is still
notified, the worker stays up, and the panic surfaces in monitoring as a bug to fix.

## Variations

- **Report-and-alert handler.** The recommended production form: forward the recovered
  value to Sentry/Datadog/PagerDuty *and* log it, so every contained panic is triaged.
- **Count-only handler.** Increment a `panics_recovered` metric for a low-overhead
  health signal; pair with alerting on any non-zero rate.
- **Re-panic on fatal invariants.** For a small set of invariants where continuing is
  genuinely unsafe, have the handler decide to escalate (log, flush, then crash
  deliberately) rather than swallow — isolation as a *policy choice*, not a blanket
  rule.
- **Per-listener defensive guard.** For a known-risky listener, add its own
  `defer/recover` inside the listener to attach listener-specific context before the
  global handler ever sees it.

## Known Uses

- **The Bulkhead pattern** (Hystrix, Resilience4j, Polly) — isolate failures into
  compartments so one failing dependency cannot exhaust the whole system; the naval
  metaphor this pattern shares its alias with.
- **Erlang/OTP "let it crash" + supervisors** — a process may crash in isolation; its
  supervisor contains and reacts to the failure without bringing down siblings.
- **Web server per-request recovery** (Go's `net/http`, Gin's `Recovery` middleware) —
  a panic in one handler is recovered so it returns a 500 instead of crashing the
  server for all connections.
- **Browser event listeners** — a throwing handler on an event does not stop other
  handlers for the same event from running.
- **OS process isolation** — a fault in one process does not crash the kernel or other
  processes; the same containment principle at the OS level.

## Related Patterns

- **[Async Error Routing](async-error-routing.md)** — the sibling for *expected*
  failures: returned errors routed *per signal* via `OnError`. Panic Isolation is its
  *global* counterpart for *unexpected* bugs (panics). The error/panic and per-signal/
  global split is deliberate.
- **[Result Aggregation](result-aggregation.md)** — aggregates returned *errors* from
  concurrent listeners; a *panic* is recovered and routed globally instead of joined
  into the result.
- **[Transactional Emission](transactional-emission.md)** — distinguishes a returned
  error (expected, aborts the sync chain) from a panic (a bug, contained globally).
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** and
  **[Await-All Dispatch](../dispatch/await-all-dispatch.md)** — the async delivery modes
  whose listener panics are recovered automatically by this pattern.
- **[Load Shedding](../flow-control/load-shedding.md)** — keep a *shed* event (dropped
  before running) distinct from a *recovered panic* (ran and hit a bug); separate
  counters.
