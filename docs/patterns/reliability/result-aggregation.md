# Result Aggregation

**Family:** Reliability
· **Also Known As:** Error Join, Gather-Errors
· **Status:** ✅ shipped — async `TryEmit` and error-returning listeners
  (`AddListenerWithErr`) ship in v1.4

## Intent

Run a signal's listeners **concurrently**, **wait for all of them to finish**, and
return a **single error that combines every listener's failure** (via `errors.Join`) —
giving synchronous-style, all-errors-reported error handling at concurrent speed.

## Motivation

An incident-management system must page on-call when a service goes down. "Page
on-call" means fanning a single alert out to three independent channels:

- post to a **Slack** channel,
- trigger a **PagerDuty** incident,
- send a backup **email** to the team alias.

These deliveries are independent and slow (three separate network calls), so you want
them to run **in parallel** — but you also critically need to know **which ones
failed**, because a partially delivered page is dangerous: if PagerDuty silently failed
and only Slack worked, on-call may never wake up. The naive approach registers plain
listeners that return nothing observable, so the outcomes are thrown away:

```go
var Alert = signals.New[Incident]() // AsyncSignal
Alert.AddListener(postSlack)        // returns nothing observable
Alert.AddListener(triggerPagerDuty)
Alert.AddListener(sendEmail)

Alert.TryEmit(ctx, incident) // ✅ runs all 3 concurrently, waits — but plain
                             //   listeners report no error, so nothing surfaces
```

With plain `AddListener` listeners, the three run concurrently and the call blocks until
all finish, which is exactly the timing you want. But the listeners return **nothing**:
if PagerDuty's API was down, you have no idea. The page looks "sent." This is the worst
of both worlds for a critical alert — you *waited* (so you could have learned the truth)
but you threw the truth away.

You could give each listener its own goroutine, error channel, and `sync.WaitGroup` by
hand — but that is exactly the concurrency boilerplate (and the bugs that come with it)
that a signal exists to remove. Result Aggregation packages it: make the listeners
error-returning (`AddListenerWithErr`, ✅) and emit with **`TryEmit`** (✅ on
async), which fans out, waits for all, and returns the `errors.Join` of every failure:

```go
var Alert = signals.New[Incident]()
Alert.AddListenerWithErr(postSlack, "slack")
Alert.AddListenerWithErr(triggerPagerDuty, "pagerduty")
Alert.AddListenerWithErr(sendEmail, "email")

if err := Alert.TryEmit(ctx, incident); err != nil { // ✅
    // err is the errors.Join of EVERY channel that failed — all of them, not just the first.
    log.Error("alert partially failed", "err", err)
    // Inspect and retry only the channels that failed:
    if errors.Is(err, pager.ErrPagerDutyDown) {
        fallbackPhoneCall(ctx, incident)
    }
}
```

All three deliveries still run concurrently and you still wait for all of them — but
now you get back **one error that contains every failure**. Slack and email succeeding
while PagerDuty fails yields an error reporting exactly PagerDuty, and you can fall back
or retry **just that channel**.

## Applicability

**Use this pattern when:**

- Listeners are **independent** and can run **concurrently** for speed (fan-out to
  several targets, multi-write).
- You **need to know every failure**, not just the first — partial success must be
  detectable per target so you can retry/compensate selectively.
- You **have a caller waiting** for the outcome (this is a blocking, return-an-error
  verb).

**Avoid it (or prefer another pattern) when:**

- The steps are an **ordered pipeline** where a later step must not run after an earlier
  failure → [Transactional Emission](transactional-emission.md) (*sync* `TryEmit`,
  stop-on-first-error).
- You **fire-and-forget** and have no caller to return to → route failures via
  [Async Error Routing](async-error-routing.md) (`OnError`).
- You want concurrency and waiting but **don't care about errors** →
  [Await-All Dispatch](../dispatch/await-all-dispatch.md) (async `TryEmit`, ignoring the
  returned error).
- The work is **loss-tolerant** and you must not block the producer →
  [Load Shedding](../flow-control/load-shedding.md).

## Structure

```
  Caller ──TryEmit(ctx, payload)──▶ AsyncSignal
                                              │  (fan-out: all listeners start concurrently)
            ┌──────────────────┬──────────────┴───────────────┐
            ▼                  ▼                               ▼
        Listener₁          Listener₂                       Listener₃
        ├ nil              ├ err₂                           ├ err₃
        │                  │                                │
        └──────────────────┴───────────────┬───────────────┘
                                            ▼
                              [ wait for ALL to finish ]
                                            ▼
                          errors.Join(nil, err₂, err₃)  ──▶ caller
                          (nils dropped; one joined error or nil if all succeeded)
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Calls async `TryEmit`; **blocks** until all listeners finish; receives the joined error |
| **AsyncSignal** | Fans listeners out concurrently, waits for all, joins their errors with `errors.Join` |
| **Error-returning listeners** | `SignalListenerErr[T]` via `AddListenerWithErr` (✅); each runs independently, returns `nil` or an error |
| **Joined error** | The single `error` returned — `errors.Join` of all non-nil results; inspectable with `errors.Is`/`errors.As` |
| **Context** | Cancellation/deadline bound on the whole fan-out |

## Collaborations

1. The caller invokes async `TryEmit(ctx, payload)` and **blocks**.
2. The signal starts **all** error-returning listeners concurrently — there is no
   ordering guarantee between them.
3. Each listener runs to completion independently and returns `nil` or an error.
   Crucially, **one listener's failure does not stop the others** — every listener gets
   its chance. Across goroutines it *cannot* stop at the first error, so async `TryEmit`
   waits for all and joins them (unlike the stop-on-first-error of *sync* `TryEmit`).
4. The signal **waits for all** listeners to finish.
5. The signal collects the results and returns `errors.Join(results...)`: `nil` if all
   succeeded, otherwise a single error wrapping every non-nil failure (nils are
   dropped).
6. The caller inspects the joined error with `errors.Is`/`errors.As` to determine
   *which* targets failed and reacts (retry just those, fall back, alert).

## Consequences

**Benefits**

- ✓ **Concurrent speed.** Total latency is roughly the *slowest* listener, not the sum
  — three 200ms calls finish in ~200ms, not 600ms.
- ✓ **Complete failure picture.** Every failure is reported, so partial success is
  fully visible and you can act per target.
- ✓ **Synchronous ergonomics.** You get a single `error` return value and standard
  `errors.Is`/`errors.As` inspection — concurrency without writing goroutine/WaitGroup
  plumbing.
- ✓ **All listeners always run.** No step is starved because an unrelated step failed.

**Liabilities**

- ✗ **The producer waits.** This is a blocking verb — not for the latency-critical hot
  path. If you cannot wait, you wanted fire-and-forget plus `OnError`.
- ✗ **No partial-failure rollback.** Like a multi-write, some targets succeed and some
  fail; there is no automatic undo of the successes. You handle compensation or
  idempotent retry yourself.
- ✗ **Concurrency on shared state.** Because listeners run in parallel, any shared
  mutable state they touch needs its own synchronization.
- ✗ **Slowest-listener tail latency.** You wait for the slowest one; a single hung
  target stretches the whole call (mitigate with a context deadline).

> **Trilemma note:** This is a blocking, wait-for-all path — it makes the producer wait
> and loses nothing. It trades hot-path latency for completeness and a clean,
> all-errors return contract.

## Implementation

1. **Listeners must be error-returning.** Use `AddListenerWithErr` (✅).
   A plain `AddListener` listener returns nothing and cannot contribute to the joined
   error — its failures are invisible to `TryEmit`.

2. **Understand `errors.Join` semantics.** `errors.Join(errs...)` returns `nil` if
   every argument is `nil`; otherwise it returns a single error whose `Error()` string
   is each non-nil error on its own line, and which **unwraps to all of them**. So
   `errors.Is(joined, ErrPagerDutyDown)` is true if *any* joined error matches, and
   `errors.As` finds the first matching type. You inspect the aggregate the same way you
   inspect any wrapped error.

3. **Make per-target errors identifiable.** For `errors.Is`/`errors.As` to be useful,
   each listener should return a sentinel or typed error you can match on, and ideally
   wrap it with the target's identity (`fmt.Errorf("slack: %w", err)`). Otherwise the
   joined error tells you *that* something failed but not *which* channel.

4. **Handle partial success explicitly — retry just the failures.** The defining value
   of this pattern is selective recovery. After inspecting the joined error, re-emit or
   directly re-invoke only the failed targets. Do **not** blindly retry the whole fan-
   out, or you will double-deliver to the targets that already succeeded — make those
   deliveries idempotent if a full retry is unavoidable.

5. **Bound the wait with a context deadline.** Because you wait for the slowest
   listener, a hung target can stall the caller indefinitely. Pass a `ctx` with a
   timeout: async `TryEmit`'s waiter returns at the context deadline **even if a handler
   is still hung**, so the caller is never pinned to a stuck listener. A handler that
   ignores `ctx` keeps running detached in the background, so listeners should still
   honor `ctx` internally for the deadline to take effect on their in-flight work.

6. **Synchronize shared state.** Listeners run concurrently. If two of them write the
   same map, counter, or buffer, guard it (mutex/atomic) or give each its own state and
   merge afterward. The signal parallelizes them; it does not make their bodies
   thread-safe.

7. **Concurrency may be bounded.** When the signal is configured with a
   `MaxConcurrent` (✅, see [Bounded Concurrency](../flow-control/bounded-concurrency.md)),
   listeners still all run and are still all awaited — the semaphore just limits how many
   run at the same instant. Aggregation semantics are unchanged; only the scheduling is.

8. **Errors vs. panics.** Async `TryEmit` aggregates *returned errors*. A listener that
   *panics* is recovered and routed to the global panic handler
   ([Panic Isolation](panic-isolation.md)); it does not (necessarily) appear in the
   joined error. Treat an expected failure (return an error) and a bug (panic)
   differently — return errors for things you anticipate.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Caller* that blocks, the *AsyncSignal* fanning out, the independent
*error-returning listeners*, and the *joined error*. Read this first to see the
mechanics; the practical examples then apply it to real problems.

```go
// 1. ASYNC SIGNAL — listeners will run CONCURRENTLY (no ordering guarantee).
sig := signals.New[Task]()

// 2. ERROR-RETURNING LISTENERS (✅) — independent; each returns nil or
//    an error. One listener's failure does NOT stop the others. Wrap with identity
//    so the joined error tells you WHICH target failed.
sig.AddListenerWithErr(func(ctx context.Context, t Task) error {
    return fmt.Errorf("target-a: %w", targetA(ctx, t)) // identifiable per target
}, "target-a")
sig.AddListenerWithErr(func(ctx context.Context, t Task) error {
    return fmt.Errorf("target-b: %w", targetB(ctx, t))
}, "target-b")

// 3. CALLER — async TryEmit fans out, WAITS for all, returns errors.Join of failures.
err := sig.TryEmit(ctx, t) // ✅
//   ├─ all listeners start concurrently
//   ├─ EVERY listener runs to completion (no stop-on-first-error)
//   ├─ wait for all to finish
//   └─ return errors.Join(...) — nil if all succeeded, else one error wrapping each failure
if err != nil {
    // Inspect WHICH targets failed and recover only those.
    if errors.Is(err, ErrTargetBDown) {
        retryTargetB(ctx, t) // selective recovery — don't double-deliver to target-a
    }
}
```

The wait-then-join (step 3) is the heart of the pattern: it is where concurrent speed
meets a complete, all-errors-reported failure picture you can act on per target.

### Practical Example 1 — Critical-alert fan-out

A critical-alert fan-out that delivers to three channels concurrently and reports
exactly which ones failed, retrying only those:

```go
package alerting

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/maniartech/signals"
)

type Incident struct {
    ID       string
    Service  string
    Severity string
    Summary  string
}

var (
    ErrSlackDown     = errors.New("slack delivery failed")
    ErrPagerDutyDown = errors.New("pagerduty delivery failed")
    ErrEmailDown     = errors.New("email delivery failed")
)

var alert *signals.AsyncSignal[Incident]

func Init(slack SlackClient, pager PagerDutyClient, mail Mailer) {
    alert = signals.New[Incident]()

    alert.AddListenerWithErr(func(ctx context.Context, in Incident) error { // ✅
        if err := slack.Post(ctx, in.Service, in.Summary); err != nil {
            return fmt.Errorf("%w: %v", ErrSlackDown, err) // identifiable per target
        }
        return nil
    }, "slack")

    alert.AddListenerWithErr(func(ctx context.Context, in Incident) error {
        if err := pager.Trigger(ctx, in.ID, in.Severity); err != nil {
            return fmt.Errorf("%w: %v", ErrPagerDutyDown, err)
        }
        return nil
    }, "pagerduty")

    alert.AddListenerWithErr(func(ctx context.Context, in Incident) error {
        if err := mail.Send(ctx, "oncall@example.com", in.Summary); err != nil {
            return fmt.Errorf("%w: %v", ErrEmailDown, err)
        }
        return nil
    }, "email")
}

// Page fans out to all channels concurrently, waits, and returns the joined failures.
func Page(ctx context.Context, in Incident) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second) // bound the slowest listener
    defer cancel()

    err := alert.TryEmit(ctx, in) // ✅ — errors.Join of all failures
    if err == nil {
        return nil // every channel delivered
    }

    // Partial success: inspect and recover ONLY the channels that failed.
    if errors.Is(err, ErrPagerDutyDown) {
        // PagerDuty is the loud one — fall back so on-call is not missed.
        fallbackPhoneTree(ctx, in)
    }
    return fmt.Errorf("alert %s partially failed: %w", in.ID, err)
}
```

**Contrast — concurrent but blind:**

```go
// ❌ Plain listeners wait for all three but report no outcome.
alert.AddListener(postSlack)
alert.AddListener(triggerPagerDuty)
alert.AddListener(sendEmail)
alert.TryEmit(ctx, incident) // page "sent" even if PagerDuty was down → on-call never woke;
                             //   plain listeners return nothing, so the join is always nil
```

The symptom in production: an incident fires, the page is logged as delivered, and yet
no human responds — because the one channel that mattered failed silently and nothing
returned that fact.

### Practical Example 2 — Multi-region replica write

A write must land in three regional replicas. They are independent and slow, so issue
all three concurrently — but you must know each replica's pass/fail to decide whether a
quorum was met and to retry **only** the replicas that failed (retrying a succeeded one
would double-write).

```go
package storage

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/maniartech/signals"
)

type Record struct {
    Key   string
    Value []byte
}

type Replica struct {
    Region string
    Client RegionClient
}

// One sentinel per region so the joined error names exactly which replicas failed.
var regionErr = map[string]error{
    "us-east": errors.New("us-east replica write failed"),
    "eu-west": errors.New("eu-west replica write failed"),
    "ap-south": errors.New("ap-south replica write failed"),
}

var replicate *signals.AsyncSignal[Record]

func Init(replicas []Replica) {
    replicate = signals.New[Record]()

    for _, r := range replicas {
        r := r // capture per iteration
        sentinel := regionErr[r.Region]
        replicate.AddListenerWithErr(func(ctx context.Context, rec Record) error { // ✅
            if err := r.Client.Put(ctx, rec.Key, rec.Value); err != nil {
                return fmt.Errorf("%w: %v", sentinel, err) // identifiable per replica
            }
            return nil
        }, r.Region)
    }
}

// Write fans out to all replicas concurrently and reports per-replica outcomes.
func Write(ctx context.Context, rec Record) error {
    ctx, cancel := context.WithTimeout(ctx, 3*time.Second) // bound the slowest replica
    defer cancel()

    err := replicate.TryEmit(ctx, rec) // ✅ — errors.Join of all failures
    if err == nil {
        return nil // every replica acknowledged
    }

    // Count failures to check a quorum, and retry ONLY the regions that failed.
    failed := 0
    for region, sentinel := range regionErr {
        if errors.Is(err, sentinel) {
            failed++
            go retryReplica(ctx, region, rec) // selective, idempotent retry
        }
    }
    if failed > len(regionErr)/2 {
        return fmt.Errorf("write %s lost quorum (%d failed): %w", rec.Key, failed, err)
    }
    return nil // quorum held; failed replicas are being retried in the background
}
```

Because each replica reports its own sentinel, the joined error is enough to count
failures against a quorum threshold and to re-issue the write to exactly the regions
that missed — never to the ones that already succeeded.

## Variations

- **All-or-nothing on the aggregate.** Treat any non-nil joined error as a total
  failure and retry the whole fan-out (only safe if every listener is idempotent).
- **Quorum / best-effort.** Accept success if *enough* targets succeeded (e.g. 2 of 3
  channels). Inspect the joined error to count failures and decide against a threshold
  rather than requiring zero.
- **Categorize before reacting.** Use `errors.As` to split transient vs. permanent
  failures in the joined error: retry the transient targets, alert a human on the
  permanent ones.
- **Bounded fan-out.** Combine with `MaxConcurrent`
  ([Bounded Concurrency](../flow-control/bounded-concurrency.md)) when there are many
  listeners, so the concurrent fan-out cannot itself overwhelm the machine.

## Known Uses

- **Go's `errors.Join`** (Go 1.20+) and `golang.org/x/sync/errgroup` — the stdlib idiom
  for running concurrent work and combining or short-circuiting on errors; this pattern
  is that idiom wired into a signal.
- **Promise.allSettled** (JavaScript) — runs all promises concurrently, waits for all,
  and reports each one's fulfilled/rejected status rather than rejecting on the first.
- **Scatter-gather / fan-out-fan-in** messaging — dispatch to multiple workers, gather
  every response, report partial failures (enterprise integration patterns).
- **Quorum writes** in distributed databases (Dynamo, Cassandra) — write to N replicas
  concurrently and report per-replica success/failure against a quorum threshold.
- **Multi-region / multi-CDN purges** — issue all purges concurrently and surface which
  regions failed so only those are retried.

## Related Patterns

- **[Transactional Emission](transactional-emission.md)** — the opposite reliability
  choice: *sequential* *sync* `TryEmit` that *stops on the first error*. Choose it when
  steps are ordered and dependent; choose Result Aggregation (async `TryEmit`) when they
  are independent and you want every failure.
- **[Await-All Dispatch](../dispatch/await-all-dispatch.md)** — the same concurrent
  wait-for-all timing via async `TryEmit` with plain listeners, where no error surfaces.
  Result Aggregation is that timing made error-aware by using error-returning listeners
  so `TryEmit` returns the joined failures.
- **[Async Error Routing](async-error-routing.md)** — the choice when you do **not**
  wait: fire-and-forget with `OnError`. Result Aggregation is for when a caller *is*
  waiting and wants a return value.
- **[Panic Isolation](panic-isolation.md)** — handles a listener that *panics* (a bug),
  routed globally, as distinct from a listener that *returns an error* (expected),
  joined into the result.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — caps how many of
  the aggregated listeners run at once without changing the aggregation semantics.
