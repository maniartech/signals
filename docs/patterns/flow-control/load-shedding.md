# Load Shedding

**Family:** Flow-Control
· **Also Known As:** Drop-and-Count, Overflow Dropping, Bounded-Lossy Dispatch
· **Status:** Builds on ✅ Fire-and-Forget `Emit`; the explicit overflow hooks
  (`OnOverflow`, `SignalOptions.Overflow`) are 🔜 v1.4

## Intent

Keep an event system alive and bounded under sustained overload by **deliberately
discarding** the events it cannot process, while **counting and reporting** every
discard so the loss is visible and measurable rather than silent.

## Motivation

Picture a real-time telemetry pipeline. A fleet of services emits a metric on a
`signals.AsyncSignal[Metric]`, and one listener ships each metric to a collector
over the network. On a normal day the collector keeps up and everything is fine.

Now the collector has a bad afternoon: network latency spikes, and each ship takes
500ms instead of 2ms. But the services don't slow down — they emit metrics at the
same 50,000/second they always do. With the naive default (a fresh goroutine per
listener per emit), here's what unfolds minute by minute:

```go
var Telemetry = signals.New[Metric]()
Telemetry.AddListener(shipToCollector) // now takes 500ms each

for m := range firehose { // 50,000/sec, never slows
    Telemetry.Emit(ctx, m) // spawns a goroutine that lives 500ms
}
```

Each emit spawns a goroutine that lives for 500ms. At 50,000 emits/sec × 0.5s, you
accumulate **25,000 live goroutines almost instantly**, then 50,000, then more —
each holding a metric and a network buffer in memory. Goroutine stacks and buffers
climb, the garbage collector thrashes, the scheduler spends its time context-
switching instead of working, and within a couple of minutes the **process is
OOM-killed**. The telemetry system — the very thing you'd look at to diagnose the
incident — is the first thing to die. This is the classic event-system meltdown.

The root cause is an *unbounded* response to overload. Load Shedding fixes it by
making the system bounded and *intentionally lossy*: cap the concurrent work, and
when more arrives than the cap can absorb, **throw the excess away on purpose and
increment a counter** so operators can see exactly how much was shed.

```go
var Telemetry = signals.NewWithOptions[Metric](&signals.SignalOptions{
    WorkerPoolSize: 64,                  // at most 64 ships in flight
    Overflow:       signals.OverflowDropNewest, // 🔜 v1.4 — shed the excess
})
Telemetry.AddListener(shipToCollector)
Telemetry.OnOverflow(func(dropped Metric) { metrics.Inc("telemetry.shed") }) // 🔜 v1.4

for m := range firehose {
    Telemetry.Emit(ctx, m) // bounded; under overload some metrics are shed + counted
}
```

The collector's bad afternoon now costs you *some dropped metrics* (acceptable for
telemetry) instead of a *dead process* (not acceptable). And because every drop is
counted, you can alert on the shed rate and see the incident in your dashboards.

## Applicability

**Use this pattern when:**

- The data is **loss-tolerant** — losing some under extreme load is survivable
  (metrics, analytics events, cache-warm hints, presence/heartbeat updates,
  non-critical UI refreshes).
- The producer **cannot or must not be slowed down** — e.g. it's on a latency-
  critical hot path where blocking would harm the primary workload.
- You need **bounded, predictable resource use** under bursty or adversarial load
  (real-time, embedded, or any system that must not OOM).

**Avoid it (or prefer another pattern) when:**

- The data is **loss-intolerant** (trades, orders, payments, audit entries). Dropping
  one is a bug or a financial/legal incident → use [Backpressure](backpressure.md).
- You actually *can* afford to wait for completion → [Await-All Dispatch](../dispatch/await-all-dispatch.md).
- You haven't yet bounded concurrency at all → start with
  [Bounded Concurrency](bounded-concurrency.md); Load Shedding is the *policy* applied
  once a bound exists.

## Structure

```
                         WorkerPoolSize = N (the bound)
                         ┌───────────────────────────┐
  Producer ──Emit──▶  [ admission check ]            │
   (fast)                │      │                     │
                         │      ├─ slot free ─▶  run listener (1 of ≤ N concurrent)
                         │      │
                         │      └─ saturated ─▶  OverflowPolicy
                         │                          ├─ DropNewest ─▶ discard payload
                         │                          │                 └─▶ OnOverflow(dropped)  [count]
                         │                          ├─ Block ───────▶ (see Backpressure)
                         │                          └─ Error ───────▶ OnOverflow(dropped) [no run]
                         └───────────────────────────┘
   Emit ALWAYS returns immediately (never blocks) under DropNewest/Error.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Producer (Emitter)** | Calls `Emit`; on the hot path; must not block |
| **Signal** | Performs the admission check against the concurrency bound |
| **Concurrency bound** | `WorkerPoolSize` — the maximum number of listeners running at once |
| **Overflow policy** | Decides the fate of an emission that can't be admitted (`OverflowDropNewest` for this pattern) |
| **Overflow hook** | `OnOverflow(func(dropped T))` — observes/counts every shed event |
| **Listener** | Processes admitted events; oblivious to shedding |

## Collaborations

1. The producer calls `Emit(ctx, payload)` and **immediately regains control** — the
   contract of fire-and-forget is preserved no matter what the admission check
   decides.
2. The signal checks whether an admission slot is available within the
   `WorkerPoolSize` bound.
3. **If a slot is free:** the listener work is admitted and runs (concurrently with
   up to `N-1` others).
4. **If saturated:** the `Overflow` policy is consulted. Under `OverflowDropNewest`
   (this pattern), the incoming payload is discarded and the registered `OnOverflow`
   hook is invoked with the dropped payload so it can be counted/logged.
5. As in-flight listeners complete, slots free up and subsequent emissions are
   admitted again. The system self-balances at the throughput the listeners can
   sustain, shedding only the true excess.

## Consequences

**Benefits**

- ✓ **Bounded resource use.** Memory and goroutine count have a hard ceiling
  regardless of producer rate — no meltdown.
- ✓ **Non-blocking producer.** The hot path never stalls; latency for the primary
  workload is protected.
- ✓ **Observable loss.** Every drop is counted, turning an invisible failure into a
  monitorable signal you can alert on.
- ✓ **Graceful degradation.** Under overload the system delivers *most* events and
  degrades smoothly, instead of failing catastrophically.

**Liabilities**

- ✗ **Events are lost** — by design. Only acceptable for loss-tolerant data.
- ✗ **No delivery guarantee** for any individual event under overload.
- ✗ **Tuning required.** `WorkerPoolSize` that is too small sheds too eagerly; too
  large weakens the protection. Sizing needs thought (see Implementation).

> **Trilemma corner sacrificed:** Load Shedding keeps *bounded memory* and a
> *non-blocking producer*, and gives up *zero loss*. That sacrifice is the whole
> point — and it is only legitimate when the data can tolerate it.

## Implementation

1. **Shedding presupposes a bound.** Load Shedding is the *policy* that runs when the
   [Bounded Concurrency](bounded-concurrency.md) limit is hit. Without a bound there
   is nothing to shed — and nothing to stop the meltdown. Always set
   `WorkerPoolSize`.

2. **Choose the drop direction deliberately.** `OverflowDropNewest` discards the
   *incoming* event (simplest, lowest latency, keeps already-queued work). Other
   systems offer drop-oldest (favor fresh data — useful for "latest value wins"
   gauges) or drop-random. v1.4 ships `OverflowDropNewest` as the default; document
   clearly which one you rely on.

3. **Never shed silently.** The defining discipline of this pattern is that *every*
   drop is counted. Wire `OnOverflow` to a metric counter at minimum. A shed rate
   climbing from zero is one of the highest-signal alerts you can have — it means
   real load or a stalled consumer.

4. **Keep the `OnOverflow` hook cheap and non-blocking.** It runs on the emit path.
   Increment an atomic counter or push to a buffered metrics client; never do I/O or
   acquire contended locks inside it, or you reintroduce the very stall you avoided.

5. **Size `WorkerPoolSize` to the *downstream*, not the producer.** For IO-bound
   listeners, match the capacity of what they talk to (e.g. the collector's max
   concurrent connections). For CPU-bound listeners, `runtime.NumCPU()` to
   `2*runtime.NumCPU()`. The bound is "how much concurrency my dependencies can
   absorb," never "how many events arrive."

6. **Shedding interacts with ordering.** Async dispatch is already unordered;
   shedding makes the *delivered set* non-contiguous too. Do not build logic that
   assumes every Nth event arrives.

7. **Distinguish from the panic/error paths.** A *shed* event never ran a listener at
   all (it was dropped before admission). That is different from a listener that ran
   and returned an error ([Async Error Routing](../reliability/async-error-routing.md))
   or panicked ([Panic Isolation](../reliability/panic-isolation.md)). Keep their
   counters separate so dashboards stay legible.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Producer*, the *Signal* with its *bound*, the
*Overflow hook*, and the *Listener*. Read this first to see the mechanics; the
practical examples then apply it to real problems.

```go
// 1. SIGNAL with a concurrency BOUND and an overflow POLICY.
sig := signals.NewWithOptions[Event](&signals.SignalOptions{
    WorkerPoolSize: 4,                          // the bound: ≤ 4 listeners at once
    Overflow:       signals.OverflowDropNewest, // 🔜 v1.4 — the policy: shed the excess
})

// 2. LISTENER — the admitted work. Oblivious to shedding.
sig.AddListener(func(ctx context.Context, e Event) {
    process(e) // runs only when a slot was free
}, "worker")

// 3. OVERFLOW HOOK — observes every shed event. Must be cheap & non-blocking.
sig.OnOverflow(func(dropped Event) { // 🔜 v1.4
    droppedCount.Add(1) // count, never silent
})

// 4. PRODUCER — fire-and-forget; ALWAYS returns immediately.
sig.Emit(ctx, e)
//   ├─ slot free  → listener runs
//   └─ saturated  → e is dropped, OnOverflow(e) fires, Emit still returns at once
```

The admission check (step 4) is the heart of the pattern: it is where "bounded +
non-blocking" meets "lossy," and where the overflow hook makes the loss visible.

### Practical Example 1 — Metrics pipeline that protects itself

A metrics-shipping pipeline that survives a slow collector and exposes its shed rate:

```go
package telemetry

import (
    "context"
    "runtime"
    "sync/atomic"

    "github.com/maniartech/signals"
)

type Metric struct {
    Name  string
    Value float64
}

var (
    shed   atomic.Uint64 // count of dropped metrics (exported to your dashboard)
    stream *signals.AsyncSignal[Metric]
)

func Init(collector Collector) {
    // Bound concurrency to what the collector can absorb, and shed the rest.
    stream = signals.NewWithOptions[Metric](&signals.SignalOptions{
        WorkerPoolSize: 4 * runtime.NumCPU(), // IO-bound: a few per core
        Overflow:       signals.OverflowDropNewest, // 🔜 v1.4
    })

    // Every drop is counted — never silent.
    stream.OnOverflow(func(dropped Metric) { // 🔜 v1.4
        shed.Add(1)
    })

    stream.AddListener(func(ctx context.Context, m Metric) {
        _ = collector.Ship(ctx, m) // slow under incident; bounded by the pool
    }, "collector")
}

// Record is on the hot path. It must never block the caller, even mid-incident.
func Record(ctx context.Context, m Metric) {
    stream.Emit(ctx, m) // fire-and-forget; excess is shed + counted, never queued unbounded
}

// ShedCount lets a /metrics endpoint or health check expose the loss.
func ShedCount() uint64 { return shed.Load() }
```

**Contrast — the anti-pattern that looks fine until it doesn't:**

```go
// ❌ No bound, no policy: works in load tests, melts down in the real incident.
var Telemetry = signals.New[Metric]()
Telemetry.AddListener(shipToCollector)
for m := range firehose {
    Telemetry.Emit(ctx, m) // unbounded goroutines when the collector slows → OOM
}
```

The symptom in production: goroutine count and memory climb monotonically while the
downstream is slow, and the process is eventually OOM-killed — taking your
observability down with it.

### Practical Example 2 — Request logging behind a slow log sink

A day-to-day case every backend team hits: request/access logs are shipped to a
sink (Elasticsearch, Loki, a cloud logging API). When the sink slows down, you must
**never** let logging back up your request handlers — a slow log pipeline must not
turn into slow (or dead) API responses. Debug/access logs are loss-tolerant, so shed
them and keep serving traffic.

```go
package reqlog

import (
    "context"
    "runtime"
    "sync/atomic"

    "github.com/maniartech/signals"
)

type Entry struct {
    Method, Path string
    Status       int
    LatencyMS    int64
}

var (
    dropped atomic.Uint64
    logs    = signals.NewWithOptions[Entry](&signals.SignalOptions{
        WorkerPoolSize: 2 * runtime.NumCPU(),
        Overflow:       signals.OverflowDropNewest, // 🔜 v1.4
    })
)

func init() {
    logs.OnOverflow(func(Entry) { dropped.Add(1) }) // 🔜 v1.4 — exposed at /healthz
    logs.AddListener(func(ctx context.Context, e Entry) {
        _ = sink.Write(ctx, e) // slow when the log backend is degraded
    }, "sink")
}

// Middleware records on the hot request path — must not block the handler.
func Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        rec := newRecorder(w)
        next.ServeHTTP(rec, r)
        logs.Emit(r.Context(), Entry{ // fire-and-forget; sheds under sink outage
            Method: r.Method, Path: r.URL.Path, Status: rec.status, LatencyMS: rec.ms(),
        })
    })
}
```

When the log backend has an outage, API latency is unaffected and the
`dropped` counter rises — exactly the trade you want: **serve users, lose some logs,
see how many.**

## Variations

- **Drop-oldest (latest-wins).** For gauges where only the freshest value matters
  (e.g. "current temperature"), discarding the *queued* value in favor of the new one
  keeps data fresh. Choose the overflow direction accordingly.
- **Sampling under load.** Combine with a sampler that, once the shed rate crosses a
  threshold, deterministically keeps 1-in-K events so the *delivered* stream stays
  statistically representative rather than arbitrarily gappy.
- **Tiered shedding.** Tag events by priority and shed low-priority first, admitting
  high-priority events preferentially within the same bound.
- **`OverflowError` instead of silent drop.** Route every overflow through the hook
  with richer context (not just a count) when you need per-drop forensics.

## Known Uses

- **Network routers** — tail-drop and RED/WRED deliberately drop packets under
  congestion; TCP treats the drop as the congestion *signal*.
- **StatsD / metrics agents** — drop metrics over budget rather than stall the app
  being measured, exposing a "dropped" counter.
- **Envoy / service meshes** — the overload manager sheds load (rejects/drops) to
  protect the proxy from collapse.
- **Reactive Streams** (Akka Streams, Project Reactor, RxJava) — `OverflowStrategy`
  / `onBackpressureDrop` make exactly this choice explicit and configurable.
- **Operating-system audio/video pipelines** — drop frames to preserve real-time
  cadence rather than accumulate latency.

## Related Patterns

- **[Bounded Concurrency](bounded-concurrency.md)** — the prerequisite; Load Shedding
  is the policy applied when its limit is reached.
- **[Backpressure](backpressure.md)** — the opposite trade-off for loss-intolerant
  data: slow the producer instead of dropping. The two patterns are the two answers
  to the same trilemma.
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** — the
  delivery mode that makes a stream loss-tolerant in the first place, and thus the
  natural home for shedding.
- **[Async Error Routing](../reliability/async-error-routing.md)** — distinguishes a
  *shed* event (never ran) from a *failed* one (ran, errored); keep their metrics
  separate.
