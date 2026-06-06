# Context-Scoped Emission

**Family:** Architectural
· **Also Known As:** Cancellation Propagation, Deadline-Aware Dispatch
· **Status:** ✅ shipped

## Intent

Carry cancellation, deadlines, and request-scoped values through an emission so that
listeners **stop doing pointless work the moment the operation that triggered them is
abandoned** — the client disconnected, the deadline passed, or the process is shutting
down. The `context.Context` threaded into every `Emit` is the in-band signal that ties
listener lifetime to the originating request.

## Motivation

Picture an HTTP endpoint that exports a user's data. The handler emits an
`ExportRequested` event; listeners gather records, render a CSV, and stream it to
storage. A single export can fan out into seconds of downstream work across several
listeners. Now the user closes the browser tab two hundred milliseconds in.

With a context-free emission, nobody downstream knows:

```go
func handleExport(w http.ResponseWriter, r *http.Request) {
    // ❌ context.Background() severs the listeners from the request's fate.
    ExportRequested.EmitAndWait(context.Background(), Export{UserID: id})
    // The user left long ago, but every listener runs to completion anyway:
    // it queries the database, renders megabytes of CSV, and uploads it —
    // all for a response that will never be read.
}
```

Multiply that by a burst of impatient users hammering reload during a slow spell and
the system does a **mountain of work whose result is already in the trash**. Database
connections stay checked out, CPU renders CSVs nobody wants, the upload bandwidth is
spent on abandoned exports — and the *next* user's legitimate request waits behind
this waste. The handler had the one piece of information that could have stopped it —
`r.Context()`, which Go cancels automatically when the client disconnects — and threw
it away.

Context-Scoped Emission threads that context through:

```go
func handleExport(w http.ResponseWriter, r *http.Request) {
    // Bound the work to the request's lifetime AND a hard deadline.
    ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
    defer cancel()

    if err := ExportRequested.EmitAndWaitErr(ctx, Export{UserID: id}); err != nil {
        http.Error(w, "export failed or canceled", http.StatusGatewayTimeout)
        return
    }
}
```

Now the moment the client disconnects (or 30 seconds elapse), `ctx` is canceled.
Listeners that check `ctx` — by passing it to their database calls, HTTP requests, and
loops — unwind promptly instead of grinding on. The work stops when the *reason* for
the work stops. The context is the leash that ties every downstream listener to the
originating request.

## Applicability

**Use this pattern when:**

- An emission triggers **non-trivial downstream work** (I/O, queries, rendering) whose
  value evaporates if the originating request is canceled or times out.
- The work has a **natural deadline** — an HTTP request budget, an SLA, a batch
  window — that listeners should respect.
- You need **request-scoped values** (trace IDs, request IDs, tenant, auth principal)
  to reach listeners for correlation and logging.
- You are emitting during **graceful shutdown** and want in-flight listener work to
  observe the shutdown context and wind down.

**Avoid relying on it for:**

- **Interrupting a listener that ignores `ctx`.** The library cannot preempt a running
  goroutine; a listener that never checks `ctx.Err()` runs to completion regardless
  (see Implementation note 3). Context bounds *cooperative* work only.
- **Carrying business data.** Context values are for request-scoped, cross-cutting
  metadata (IDs, deadlines), not for the event payload — that is what the typed
  payload `T` is for. Smuggling domain data through `ctx.Value` is an anti-pattern.
- **Fire-and-forget where you never observe the outcome** — async `Emit` still honors
  the canceled-at-emit-time skip, but if you need to *act* on cancellation use a
  waiting variant (`EmitAndWait`, `TryEmit`).

## Structure

```
  HTTP request (r.Context, auto-canceled on client disconnect)
        │
        ▼  ctx, cancel := context.WithTimeout(r.Context(), 30s)
  ┌───────────┐
  │  Handler  │── Emit(ctx, payload) ──▶ ┌──────────────┐
  └───────────┘                          │    Signal    │
        │                                └──────┬───────┘
        │  (a) ctx already canceled?            │
        │      └─▶ NO listener runs ────────────┤  ← all emit variants
        │                                       │
        │  (b) SYNC Emit/TryEmit: check ctx ───▶│  between listeners
        │      ┌── ctx live ──▶ run L₁          │     │
        │      ├── ctx live ──▶ run L₂          │     │  canceled mid-chain?
        │      └── canceled ──▶ STOP  ──────────┘     │  └▶ TryEmit returns ctx.Err()
        │                                             │
        └── (c) INSIDE a running listener: ───────────┘
               only the listener's own `ctx.Err()` /
               ctx-aware calls can stop it early.
```

Three distinct checkpoints — (a) at emit time, (b) between sync listeners, (c) inside
each listener — and the library owns only the first two.

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Context source** | The origin of cancellation/deadline — `r.Context()`, a shutdown context, a `WithTimeout`/`WithCancel` derivation |
| **Emitter** | Threads `ctx` into `Emit`/`EmitAndWait`/`TryEmit`; never substitutes `context.Background()` to "make the warning go away" |
| **Signal** | Skips all listeners if `ctx` is already canceled; for sync emission, checks `ctx` *between* listeners and stops |
| **Cooperative listener** | Honors the context: passes it to I/O calls and checks `ctx.Err()` inside loops so it can abandon early |
| **Context value carrier** | Optional: request/trace IDs placed in `ctx` upstream and read by listeners for correlation |

## Collaborations

1. An upstream boundary (HTTP handler, job runner, shutdown coordinator) obtains or
   derives a context: `ctx := r.Context()`, optionally narrowed with
   `context.WithTimeout` / `context.WithCancel`.
2. The emitter calls `Signal.Emit(ctx, payload)` (or `EmitAndWait` / `TryEmit`),
   passing that exact context.
3. **At emit time:** if `ctx.Err() != nil` already, the signal runs **no listener at
   all** — every emit variant honors this short-circuit.
4. **For sync `Emit` / `TryEmit`:** the signal checks `ctx` **between** listeners. If
   the context is canceled partway through the chain, remaining listeners are skipped;
   `TryEmit` returns `ctx.Err()` so the caller learns the chain was cut short.
5. **Inside each listener:** the listener is responsible for its own promptness — it
   passes `ctx` to database/HTTP calls and checks `ctx.Err()` in long loops. The
   library cannot interrupt a listener that ignores the context.
6. Listeners may also read **request-scoped values** from `ctx` (trace ID, tenant) to
   correlate their work with the originating request.

## Consequences

**Benefits**

- ✓ **No wasted work.** Abandoned requests stop fanning out into useless downstream
  computation, freeing connections, CPU, and bandwidth for live work.
- ✓ **Deadlines flow for free.** A single `context.WithTimeout` at the boundary bounds
  the entire fan-out; you don't thread timeouts by hand into each listener.
- ✓ **Built-in skip on cancel.** The canceled-at-emit-time short-circuit means a
  late-arriving emit on a dead request costs nothing — no listener runs.
- ✓ **Correlation for free.** Trace/request IDs in the context reach every listener,
  so logs and spans across the fan-out stitch back to one request.

**Liabilities**

- ✗ **Cooperative, not preemptive.** Context bounds only listeners that *check* it. A
  CPU-bound loop with no `ctx.Err()` check runs to completion regardless — the
  guarantee is only as good as the listeners' discipline.
- ✗ **Async fire-and-forget gives weak feedback.** Plain async `Emit` returns
  immediately; mid-flight cancellation of an already-dispatched async listener is the
  listener's job, and the emitter learns nothing. Use a waiting variant when the
  outcome matters.
- ✗ **`context.Background()` defeats it silently.** A single careless substitution
  severs the whole subtree from cancellation with no error — a subtle, easy-to-miss
  regression.

> **Trilemma note:** Context-Scoped Emission is orthogonal to the
> bounded-memory / never-wait / never-lose trilemma — it does not pick a corner. It is
> a *correctness and resource-hygiene* mechanism that layers onto whichever dispatch
> mode you chose, making that mode abandon doomed work early.

## Implementation

1. **Always derive from the real upstream context.** In an HTTP handler that is
   `r.Context()`; in a worker it is the job's context; in shutdown it is the
   shutdown context. Reserve `context.Background()` for true program roots (and tests),
   never to silence a "context required" parameter on a request path.

2. **Know exactly what the library guarantees — and what it doesn't.** Three
   precise rules:
   - **Canceled at emit time ⇒ nothing runs.** If `ctx.Err() != nil` when you call any
     emit variant (`Emit`, `EmitAndWait`, `TryEmit`), **no listener runs**. A late emit
     on a dead request is a no-op.
   - **Sync checks *between* listeners.** `SyncSignal.Emit` and `TryEmit` re-check
     `ctx` before each listener in the chain. On cancellation mid-chain, the remaining
     listeners are skipped; `TryEmit` returns `ctx.Err()`. `Emit` (which discards
     errors) simply stops.
   - **Nothing interrupts a *running* listener.** Once a listener has started, only the
     listener itself can end early. The library does not — and cannot — preempt a
     goroutine mid-execution.

3. **Make listeners context-aware — that is where the real savings live.** A listener
   that ignores `ctx` is the weak link. Pass `ctx` to every blocking call
   (`db.QueryContext(ctx, …)`, `http.NewRequestWithContext(ctx, …)`) and check
   `ctx.Err()` inside long loops:

   ```go
   func renderExport(ctx context.Context, e Export) error {
       rows, err := db.QueryContext(ctx, exportSQL, e.UserID) // canceled ⇒ query aborts
       if err != nil {
           return err
       }
       defer rows.Close()
       for rows.Next() {
           if ctx.Err() != nil { // bail out of a long render promptly
               return ctx.Err()
           }
           writeRow(rows)
       }
       return rows.Err()
   }
   ```

4. **Choose the emit variant for the feedback you need.** Use `TryEmit` (sync) when you
   want the chain to stop on cancellation *and* to learn it did via the returned
   `ctx.Err()`. Use `EmitAndWaitErr` (🔜 v1.4) for concurrent listeners when you need
   the joined outcome including cancellation. Plain async `Emit` is correct for
   loss-tolerant notifications but tells the caller nothing about cancellation.

5. **Set deadlines at the boundary, once.** `context.WithTimeout(r.Context(), budget)`
   at the handler bounds the entire fan-out. Always `defer cancel()` to release the
   timer even on the success path, or you leak it.

6. **Use context values only for request-scoped metadata.** Trace IDs, request IDs,
   tenant, and auth principal belong in `ctx`; the business event belongs in the typed
   payload `T`. Read values in listeners for correlated logging — never to pass the
   event's actual data.

7. **Wire the shutdown context into emission for graceful drain.** During shutdown,
   emit with the shutdown context so in-flight listeners observe cancellation and wind
   down, instead of starting fresh long-running work as the process exits.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the three
checkpoints in the **Structure** diagram — the *Context source*, the *Emitter* that
threads `ctx` (never `context.Background()`), the *Signal* that skips on a
canceled-at-emit-time context, and the *Cooperative listener* that honors `ctx`
inside its own work. Read this first to see the mechanics; the practical examples
then apply it to real problems.

```go
// CONTEXT SOURCE — the origin of cancellation/deadline (request, shutdown, budget).
ctx, cancel := context.WithTimeout(upstreamCtx, 30*time.Second)
defer cancel() // release the timer on EVERY path, or leak it

// Optionally carry request-scoped metadata (trace IDs) — NOT business data.
ctx = context.WithValue(ctx, traceIDKey, traceID)

// EMITTER — threads the real ctx. checkpoint (a): if ctx is already canceled,
// the signal runs NO listener at all (true for every emit variant).
//   sync  TryEmit ── checkpoint (b): re-checks ctx BETWEEN listeners, stops the
//                    chain mid-flight, and returns ctx.Err() so the caller learns.
if err := sig.TryEmit(ctx, payload); err != nil { // ✅ sync, stop-on-cancel
    return err // ctx.Err() (or a listener error) — the chain was cut short
}

// COOPERATIVE LISTENER — checkpoint (c): the library cannot preempt a running
// goroutine, so the listener must honor ctx itself.
func listen(ctx context.Context, p Payload) error { // SignalListenerErr[Payload] ✅
    rows, err := db.QueryContext(ctx, sql, p.ID) // pass ctx to I/O ⇒ canceled aborts
    if err != nil {
        return err
    }
    defer rows.Close()
    for rows.Next() {
        if ctx.Err() != nil { // check inside long loops ⇒ abandon doomed work early
            return ctx.Err()
        }
        process(rows)
    }
    return rows.Err()
}
```

The leverage is checkpoint (c): the library owns (a) and (b), but the real savings
come from listeners that pass `ctx` to their calls and poll `ctx.Err()` in loops.

### Practical Example 1 — HTTP export bounded by client liveness and a deadline

**Problem:** a data-export endpoint fans out into seconds of querying, rendering, and
uploading. If the client disconnects or a 30s budget elapses, every listener should
stop instead of finishing work for a response nobody will read.

The handler derives `ctx` from `r.Context()` (auto-canceled on disconnect), narrows
it with a hard deadline, and threads it into the emission; a trace ID rides along for
correlation.

```go
package export

import (
    "context"
    "net/http"
    "time"

    "github.com/maniartech/signals"
)

type Export struct {
    UserID string
    Format string
}

type ctxKey string

const traceIDKey ctxKey = "trace-id"

// Concurrent listeners (render, upload), waited on.
var ExportRequested = signals.New[Export]() // ✅

func init() {
    ExportRequested.AddListener(renderAndUpload, "export/render-upload")
    ExportRequested.AddListener(recordMetrics, "export/metrics")
}

// Handler: ties the whole fan-out to the request's lifetime + a 30s budget.
func Handler(w http.ResponseWriter, r *http.Request) {
    // r.Context() is canceled automatically when the client disconnects.
    ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
    defer cancel() // release the timer on every path

    // Propagate a request-scoped trace ID to every listener for correlation.
    ctx = context.WithValue(ctx, traceIDKey, r.Header.Get("X-Trace-Id"))

    // If the client already left, ctx is canceled and NO listener runs.
    ExportRequested.EmitAndWait(ctx, Export{ // ✅ concurrent, blocks until all done
        UserID: r.URL.Query().Get("user"),
        Format: "csv",
    })

    if err := ctx.Err(); err != nil {
        http.Error(w, "export canceled or timed out", http.StatusGatewayTimeout)
        return
    }
    w.WriteHeader(http.StatusOK)
}

// A cooperative listener: honors ctx so it abandons doomed work promptly.
func renderAndUpload(ctx context.Context, e Export) {
    trace, _ := ctx.Value(traceIDKey).(string) // correlate logs to the request

    rows, err := db.QueryContext(ctx, exportSQL, e.UserID) // canceled ⇒ aborts
    if err != nil {
        logWith(trace, "query failed or canceled", err)
        return
    }
    defer rows.Close()

    for rows.Next() {
        if ctx.Err() != nil { // client gone / deadline passed: stop rendering now
            logWith(trace, "render abandoned", ctx.Err())
            return
        }
        renderRow(rows)
    }
    upload(ctx, e) // ctx-aware: the upload is abandoned too if ctx is done
}

func recordMetrics(ctx context.Context, e Export) { /* fast, ctx-oblivious is fine */ }
```

**Contrast — the leak this prevents:**

```go
// ❌ context.Background() cuts the fan-out loose from the request.
func Handler(w http.ResponseWriter, r *http.Request) {
    // The user can disconnect or the deadline pass; listeners never find out.
    ExportRequested.EmitAndWait(context.Background(), Export{UserID: id})
    // Database, render, and upload all run to completion for a discarded response.
}
```

The production symptom: under a burst of impatient reloads, query-pool exhaustion and
CPU spent rendering exports for responses nobody reads — work that the request's own
context would have canceled.

### Practical Example 2 — Cancelable long-running report that polls ctx.Err()

**Problem:** a user kicks off a heavy analytics report (millions of rows aggregated
into a workbook) and may cancel it from the UI, or navigate away. The report listener
runs a long page-by-page loop; it must notice the cancellation and abort mid-stream
rather than burning minutes of CPU and a database connection on a discarded result.

Here the cancellation source is an explicit `context.WithCancel` whose `cancel` is
wired to the UI's stop button. The emitter uses the sync `TryEmit` variant so it
learns *via the returned `ctx.Err()`* that the chain was cut short; the listener does
the real work of polling `ctx.Err()` between pages.

```go
package reports

import (
    "context"

    "github.com/maniartech/signals"
)

type ReportJob struct {
    JobID   string
    TenantID string
    Query   string
}

// Sync signal: TryEmit stops the chain on cancel and returns ctx.Err().
var ReportRequested = signals.NewSync[ReportJob]() // ✅

func init() {
    // Error-returning listener so a canceled/failed build surfaces to the emitter.
    ReportRequested.AddListenerWithErr(buildWorkbook, "reports/workbook") // ✅ sync
}

// Run is invoked by the job runner; cancelFn is hooked to the UI "Cancel" button.
func Run(parent context.Context, job ReportJob) (context.CancelFunc, error) {
    ctx, cancel := context.WithCancel(parent)

    // TryEmit returns ctx.Err() if canceled at emit time or mid-chain, or the
    // listener's error. The caller acts on cancellation instead of ignoring it.
    if err := ReportRequested.TryEmit(ctx, job); err != nil {
        cancel()
        return nil, err // e.g. context.Canceled — report aborted, nothing leaked
    }
    return cancel, nil
}

// The long listener: polls ctx.Err() so a user cancel aborts it promptly.
func buildWorkbook(ctx context.Context, job ReportJob) error {
    sheet := newWorkbook()
    for page := 0; ; page++ {
        if err := ctx.Err(); err != nil { // user canceled / navigated away
            return err // stop now: no more queries, free the DB connection
        }
        rows, err := db.QueryContext(ctx, job.Query, job.TenantID, page) // ctx-aware
        if err != nil {
            return err
        }
        if rows == nil { // no more pages
            break
        }
        appendPage(sheet, rows)
    }
    return store(ctx, job.JobID, sheet) // also abandoned if ctx is done
}
```

When the user hits Cancel, the wired `cancel()` fires, the next `ctx.Err()` check in
the loop returns `context.Canceled`, `buildWorkbook` unwinds, and `TryEmit` hands
that error back to `Run` — the work stops the instant its reason does, with no leaked
query or half-built workbook left grinding in the background.

## Variations

- **Sync stop-on-cancel with feedback.** Use a `signals.NewSync[T]()` signal and
  `TryEmit(ctx, payload)`: the chain stops between listeners on cancellation and
  returns `ctx.Err()`, so the caller knows the sequence was cut short. Ideal for
  ordered pipelines that must abandon together.
- **Pure deadline propagation.** No cancellation source, just a budget:
  `context.WithTimeout(context.Background(), budget)` to cap a background batch's
  fan-out even when there is no client to disconnect.
- **Shutdown-scoped emission.** Emit with the application's shutdown context so
  in-flight listeners drain cooperatively during graceful termination.
- **Trace-only context.** Carry just correlation IDs (no deadline) to stitch a
  fan-out's logs and spans back to one request, when cancellation isn't a concern.

## Known Uses

- **Go's `net/http`** — `Request.Context()` is canceled when the client disconnects or
  the server times out; the canonical source of an emission-bounding context.
- **`database/sql` and gRPC** — `QueryContext` / per-RPC contexts abort in-flight work
  on cancellation; the cooperative-listener model this pattern relies on.
- **OpenTelemetry / distributed tracing** — trace and span context propagated through
  `context.Context` so downstream work correlates to the originating request.
- **Structured concurrency** (`errgroup`, Trio/asyncio nurseries) — cancellation of a
  scope tears down all child work; context here plays the scope's role for a fan-out.
- **Kubernetes / server graceful shutdown** — a shutdown context signals in-flight
  handlers to wind down rather than start new long-running work.

## Related Patterns

- **[Shared Event Registry](shared-event-registry.md)** — the companion architectural
  pattern; a registry is the fan-out point where one request-scoped context reaches
  many decoupled subscribers.
- **[Synchronous Sequential Dispatch](../dispatch/synchronous-sequential-dispatch.md)**
  — the dispatch mode that gives context its strongest grip: sync `Emit`/`TryEmit`
  check `ctx` *between* listeners and stop a chain mid-flight.
- **[Await-All Dispatch](../dispatch/await-all-dispatch.md)** — pair context with
  `EmitAndWait`/`EmitAndWaitErr` when you must wait for the bounded fan-out and observe
  cancellation in the result.
- **[Transactional Emission](../reliability/transactional-emission.md)** — combines
  naturally: a `TryEmit` pipeline that stops both on the first listener *error* and on
  context cancellation, returning whichever happened first.
