# Short-Circuit Dispatch

**Family:** Dispatch
· **Also Known As:** Stop Propagation, Halt Chain, Early-Exit Dispatch, First-Responder Dispatch
· **Status:** ✅ shipped (v1.4 — `signals.StopPropagation` on `SyncSignal`)

## Intent

Let a single listener **end a sequential emission early** — so that once one listener
declares the event *handled*, the **remaining listeners do not run** — without that
early exit being mistaken for a failure. A listener returns the `signals.StopPropagation`
sentinel; the chain halts cleanly and the emitter still sees success. This is the
`event.stopPropagation()` / middleware short-circuit idea, expressed as a control value
rather than an error.

## Motivation

Picture an HTTP service whose cross-cutting concerns are modeled as a `SyncSignal`
middleware chain: authentication, then a feature gate, then the business handlers, run
in order before the request is served. The first stage is an **auth interceptor** that
must *veto* the request when the caller is not allowed — and when it vetoes, **none of
the later stages should run at all**. Authorizing nothing, billing nothing, touching no
database.

A developer reaches for the obvious tool and tries to signal "stop" by returning an
error from the auth stage:

```go
var BeforeHandler = signals.NewSync[*ReqCtx]()
BeforeHandler.AddListenerWithErr(authenticate, "auth")   // returns an error to "stop"
BeforeHandler.AddListenerWithErr(featureGate, "gate")
BeforeHandler.AddListenerWithErr(serve, "serve")

if err := BeforeHandler.TryEmit(ctx, rc); err != nil {
    // auth denial arrives here as an "error" — indistinguishable from a real fault
    http.Error(w, err.Error(), http.StatusInternalServerError) // WRONG status, WRONG log level
}
```

This *does* stop the chain — `TryEmit` aborts on the first non-nil error — but it
conflates two completely different things. A request that was **correctly denied by
policy** now looks identical to a request where the auth backend **threw a 500**. It
gets logged as an error, it pages on-call, it inflates the error-rate dashboards, and
the caller cannot tell "you are not allowed" from "we are broken." The stop you wanted
was a *success* — the system did exactly the right thing — but the only vocabulary you
had was *failure*.

Short-Circuit Dispatch gives the early exit its own word. The stopping listener returns
the `StopPropagation` sentinel, which halts the remaining listeners **and reports
success**:

```go
var BeforeHandler = signals.NewSync[*ReqCtx]()
BeforeHandler.AddListenerWithErr(authenticate, "auth")
BeforeHandler.AddListenerWithErr(featureGate, "gate")
BeforeHandler.AddListenerWithErr(serve, "serve")

func authenticate(ctx context.Context, rc *ReqCtx) error {
    if !rc.Authorized() {
        rc.Status = http.StatusForbidden
        return signals.StopPropagation // stop the chain — and this is NOT a failure
    }
    return nil
}

// gate and serve are skipped when auth stops; TryEmit returns nil, not the sentinel.
if err := BeforeHandler.TryEmit(ctx, rc); err != nil {
    // reached ONLY for a real fault — a genuine backend error, not a policy denial
    return fmt.Errorf("request pipeline failed: %w", err)
}
respond(w, rc) // rc.Status carries 403; no later stage ran, nothing was billed
```

`featureGate` and `serve` never run, `TryEmit` returns `nil` (the deny was *handled*,
not *failed*), and a genuine backend error from any stage still surfaces normally. The
pattern restores the distinction the naive version threw away: **stop-with-success** is
now spelled differently from **stop-with-failure**.

## Applicability

**Use this pattern when:**

- **One listener can decide the event is fully handled** and later listeners would be
  redundant or wrong — first-responder / first-match dispatch, where the first capable
  handler consumes the event and the rest must not see it.
- **A veto / gate / interceptor must short-circuit a pipeline** the way
  `event.stopPropagation()` halts DOM bubbling or a middleware `return` skips the rest
  of the stack — and the short-circuit is a *normal*, expected outcome, not an error.
- **You need the early exit to be distinguishable from a failure** at the emitter: a
  clean stop returns `nil` from `TryEmit` (or is silent on `Emit`), while a real error
  still propagates.
- **The chain is sequential and ordered** — the pattern only has meaning where there is
  a deterministic "rest of the chain" to skip (see the SyncSignal-only limitation below).

**Avoid it (or prefer another pattern) when:**

- **You want to stop *and* surface a failure** (the step genuinely failed) → return a
  real error and use [Transactional Emission](../reliability/transactional-emission.md);
  that is stop-with-failure, this is stop-with-success.
- **Every listener must always run** regardless of what earlier ones decide → plain
  [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md) with `Emit`.
- **The signal is asynchronous.** `AsyncSignal` has no sequential chain to halt and
  **ignores** `StopPropagation` entirely — there is nothing to short-circuit. This
  pattern is SyncSignal-only.
- **Only plain listeners are registered.** A `SignalListener[T]` returns nothing and so
  can never stop the chain — only error-returning listeners can.

## Structure

```
  Caller ──Emit / TryEmit(ctx, payload)──▶ SyncSignal
                                             │
                        ┌────────────────────┘
                        ▼
                  [ ctx.Err()? ] ──canceled──▶ stop  (Emit: return; TryEmit: return ctx.Err())
                        │ ok
                        ▼
                  Listener₁ ──returns──┐
                        │              ├─ StopPropagation? ─yes─▶ STOP CHAIN, success
                        │              │      (Emit: return; TryEmit: return nil)
                        │              ├─ other non-nil err? ─yes─▶ Emit: route to OnError, CONTINUE
                        │              │                            TryEmit: return err (stop+fail)
                        │ nil          └─ nil ─────────────────────▶ continue
                        ▼
                  [ ctx.Err()? ] ── between every listener
                        │ ok
                        ▼
                  Listener₂ … Listenerₙ   ◀── skipped entirely once a listener short-circuits
                        │
                        ▼
                  done (all ran, or one stopped early)
```

The same diagram holds under `SignalOptions.Order = signals.LIFO` — the walk is
most-recently-added first, and `StopPropagation` halts that reverse walk just the same.

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Calls `Emit` or `TryEmit`; treats a clean stop as success (no error / `nil`) and a real error as failure |
| **SyncSignal** | Iterates listeners in registration order; on `StopPropagation` skips the remaining listeners and reports success |
| **Stopping listener** | A `SignalListenerErr[T]` that returns `signals.StopPropagation` (or an error wrapping it) to declare the event handled and end the chain |
| **Downstream listeners** | Later listeners in the chain that are **not invoked** once a stop occurs |
| **Context** | Carries cancellation/deadline; checked between listeners (a separate stop reason from `StopPropagation`) |

## Collaborations

1. The caller invokes `Emit` or `TryEmit(ctx, payload)` and **blocks** while the
   SyncSignal walks the chain in its configured order (FIFO by default, LIFO if
   `SignalOptions.Order = signals.LIFO`).
2. Before each listener the signal checks `ctx`. A canceled context is a *different*
   stop reason: `Emit` returns, `TryEmit` returns `ctx.Err()`.
3. The signal calls the next listener. If it is an error-returning listener, the signal
   inspects its return value with `errors.Is(err, StopPropagation)`.
4. **If it is (or wraps) `StopPropagation`,** the signal **stops immediately** — the
   remaining listeners do not run — and reports **success**: `TryEmit` returns `nil`,
   and `Emit` does **not** route the sentinel to the `OnError` sinks.
5. **If it is any other non-nil error,** the two verbs diverge: `TryEmit` stops and
   returns that error (stop-with-failure); `Emit` is best-effort — it routes the error
   to `OnError` and **keeps going**.
6. **If it returns `nil`,** the signal proceeds to the next listener (step 2), until the
   chain is exhausted or a listener short-circuits.

## Consequences

**Benefits**

- ✓ **Early exit without false failures.** A handled event ends the chain *and* reads as
  success — `TryEmit` returns `nil`, `Emit` does not page `OnError`. Policy denials no
  longer masquerade as 500s.
- ✓ **First-responder semantics for free.** The first capable listener consumes the
  event and the rest are skipped — no shared "handled" flag threaded through every
  listener.
- ✓ **Composes with order.** Works identically under FIFO and LIFO; under LIFO it halts
  the reverse (newest-first) walk, making "the newest handler can veto the rest" trivial.
- ✓ **Standard-library idiom.** Mirrors `fs.SkipAll` / `filepath.SkipDir` — a sentinel
  that *controls iteration* rather than *reporting an error* — so Go developers already
  know the shape.
- ✓ **Detection is `errors.Is`.** Wrapping the sentinel (`fmt.Errorf("...: %w", signals.StopPropagation)`)
  to add context still stops the chain, because the signal unwraps it.

**Liabilities**

- ✗ **SyncSignal-only.** `AsyncSignal` invokes listeners concurrently — there is no
  ordered "rest of the chain" — so it **ignores** `StopPropagation` outright. A listener
  shared between a sync and an async signal stops the former and is a no-op on the latter.
- ✗ **Error-returning listeners only.** A plain `SignalListener[T]` cannot return the
  sentinel, so it can never stop the chain. The stopping stages must be registered with
  `AddListenerWithErr` / `AddOnceWithErr`.
- ✗ **Order-dependent behavior.** *Which* listener gets to short-circuit depends on
  registration order. A stop early in the chain hides every later listener; reordering
  changes who wins. This is intended, but it makes order load-bearing.
- ✗ **Asymmetry between `Emit` and `TryEmit` for *real* errors.** The sentinel behaves
  the same on both verbs (stop + success), but a *non-sentinel* error does not: `Emit`
  continues past it, `TryEmit` stops on it. Know which verb you are using.

> **Trilemma note:** Short-Circuit Dispatch is a control-flow refinement of the
> synchronous, blocking path — it changes *how far* the chain runs, not the
> bounded-memory / never-wait / never-lose trade-off. It keeps zero loss and bounded
> memory and makes the caller wait, exactly like its parent sequential dispatch.

## Implementation

1. **Return `signals.StopPropagation` from an error-returning listener to stop.** Only a
   `SignalListenerErr[T]` (registered via `AddListenerWithErr` or `AddOnceWithErr`) can
   return a value; a plain listener cannot short-circuit. The sentinel is a package-level
   `var signals.StopPropagation = errors.New(...)` — return it directly, or wrap it.

2. **Wrap to add context; it still stops.** `fmt.Errorf("auth denied: %w", signals.StopPropagation)`
   is detected via `errors.Is` and halts the chain just like the bare sentinel — the
   wrapping is for *your* logs, not for the signal. Do **not** wrap with `%v` (that
   breaks the chain and the sentinel is lost).

3. **A clean stop is success, not error.** On `TryEmit` the return is `nil` — the
   sentinel is **never** what `TryEmit` hands back. On `Emit` the sentinel is **not**
   routed to the `OnError` sinks. Encode the *outcome* (allowed/denied, who handled it)
   on the payload, since the stop itself carries no error to the caller.

4. **Contrast a stop with a real failure deliberately.** A non-sentinel error means
   *stop and it failed*: on `TryEmit` it is returned to the caller; on `Emit` it is
   routed to `OnError` (and, on `Emit` only, the chain *continues*). Reserve real errors
   for genuine faults and the sentinel for "handled, we're done." Mixing them up is the
   exact bug the pattern exists to prevent.

5. **It composes with `SignalOptions.Order`.** Under `LIFO` the chain walks
   most-recently-added first; `StopPropagation` halts that reverse walk too, so the
   *newest* handler can veto the older ones. See
   [Reverse (LIFO) Dispatch](reverse-dispatch.md).

6. **AsyncSignal ignores it — by design.** On an `AsyncSignal`, a listener returning
   `StopPropagation` is neither joined by `TryEmit` nor routed to `OnError` nor given any
   other effect: there is no sequential propagation to stop. Keep stop-logic on the sync
   signal. This also keeps a listener that may run on either signal type well-defined —
   the sentinel is never mistaken for an error anywhere.

7. **Context cancellation is a separate stop reason.** A canceled `ctx` also ends the
   chain, but it is *not* a short-circuit: `Emit` returns silently, `TryEmit` returns
   `ctx.Err()`. Don't conflate "a listener handled it" with "the caller gave up."

8. **Performance.** Short-circuiting is strictly *cheaper* than running the whole chain —
   it is the same tight sequential loop that exits early. There is no extra allocation or
   synchronization; the sentinel check is one `errors.Is` per error-returning listener.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton mapping onto the **Participants** and **Structure** above — the
*Caller*, the *SyncSignal*, the ordered listeners, and the one that short-circuits.

```go
// 1. SYNC SIGNAL — sequential, registration-ordered dispatch.
sig := signals.NewSync[Event]()

// 2. LISTENERS — error-returning, because only those can stop the chain.
sig.AddListenerWithErr(func(ctx context.Context, e Event) error {
    if handled(e) {
        return signals.StopPropagation // STOP: later listeners are skipped, this is success
    }
    return nil // not handled — let the next listener try
}, "first-responder")

sig.AddListenerWithErr(func(ctx context.Context, e Event) error {
    return fallback(ctx, e) // runs ONLY if the first listener did not stop the chain
}, "fallback")

// 3. CALLER — TryEmit returns nil on a clean stop, the error on a real failure.
err := sig.TryEmit(ctx, e)
//   ├─ a listener returns StopPropagation → chain stops, TryEmit returns nil (success)
//   ├─ a listener returns a real error    → chain stops, TryEmit returns that error (failure)
//   ├─ ctx canceled                        → returns ctx.Err()
//   └─ all listeners return nil            → returns nil
if err != nil {
    return fmt.Errorf("pipeline failed: %w", err) // only real faults land here
}
```

The clean stop reading as `nil` is the heart of the pattern: "handled, stop the rest"
and "broken, stop the rest" are spelled differently, so the caller can tell them apart.

### Practical Example 1 — Middleware chain with an auth short-circuit (first-responder)

A request middleware chain where any stage may *handle and stop*: the auth stage denies
unauthorized requests, the cache stage serves a hit, and only un-handled requests reach
the business handler. A handled request stops the chain cleanly — it is not a failure.

```go
package httpmw

import (
    "context"
    "fmt"
    "net/http"

    "github.com/maniartech/signals"
)

// Carries the request through the chain; a stage that handles it sets Status/Body.
type ReqCtx struct {
    R      *http.Request
    UserID string
    Status int
    Body   []byte
}

var BeforeHandler = signals.NewSync[*ReqCtx]()

func Init(auth AuthService, cache Cache) {
    // Stage 1: authenticate. A denial is a HANDLED outcome — stop, but do not fail.
    BeforeHandler.AddListenerWithErr(func(ctx context.Context, c *ReqCtx) error {
        uid, ok := auth.Verify(ctx, c.R.Header.Get("Authorization"))
        if !ok {
            c.Status = http.StatusForbidden
            return signals.StopPropagation // deny: cache + handler never run; NOT an error
        }
        c.UserID = uid
        return nil // authorized — fall through to the next stage
    }, "auth")

    // Stage 2: cache. A hit is also a HANDLED outcome — serve it and stop the rest.
    BeforeHandler.AddListenerWithErr(func(ctx context.Context, c *ReqCtx) error {
        if body, ok := cache.Get(ctx, c.R.URL.Path); ok {
            c.Status, c.Body = http.StatusOK, body
            return signals.StopPropagation // first responder wins: handler never runs
        }
        return nil // miss — let the real handler produce the response
    }, "cache")

    // Stage 3: the business handler. Reached only when nothing above short-circuited.
    BeforeHandler.AddListenerWithErr(func(ctx context.Context, c *ReqCtx) error {
        body, err := render(ctx, c.UserID, c.R.URL.Path)
        if err != nil {
            return fmt.Errorf("render: %w", err) // REAL error — stop AND fail
        }
        c.Status, c.Body = http.StatusOK, body
        return nil
    }, "handler")
}

func Handle(w http.ResponseWriter, r *http.Request) {
    c := &ReqCtx{R: r}

    // A clean stop (deny or cache hit) returns nil; only a real fault returns an error.
    if err := BeforeHandler.TryEmit(r.Context(), c); err != nil {
        http.Error(w, "internal error", http.StatusInternalServerError) // genuine 500
        return
    }

    w.WriteHeader(c.Status) // 403 on deny, 200 on cache hit or rendered response
    w.Write(c.Body)
}
```

A denied request and a cache hit both **stop the chain and return `nil`** — the later
stages never run, and the emitter correctly treats them as success. Only a *real* render
failure surfaces as an error and a 500. The naive "return an error to stop" approach
would have logged every 403 and every cache hit as a server error.

### Practical Example 2 — Stop-with-success vs. stop-with-failure, side by side

The single most important distinction in this pattern. Both listeners *stop* the chain;
they differ only in whether the stop is a **success** (sentinel) or a **failure** (real
error). The contrast is what `StopPropagation` exists to make expressible.

```go
package gate

import (
    "context"
    "errors"
    "fmt"

    "github.com/maniartech/signals"
)

type Submission struct {
    ID    string
    Body  string
    Vote  string // set by a handling stage
}

var ErrBackendDown = errors.New("policy backend unreachable")

var Review = signals.NewSync[*Submission]()

func Init(policy PolicyBackend) {
    // STOP-WITH-SUCCESS: an auto-approve rule HANDLES the submission and ends the chain.
    Review.AddListenerWithErr(func(ctx context.Context, s *Submission) error {
        if isTrusted(s) {
            s.Vote = "auto-approved"
            // Wrapping the sentinel keeps the stop AND adds a breadcrumb for logs.
            return fmt.Errorf("trusted author %s: %w", s.ID, signals.StopPropagation)
        }
        return nil // not auto-approved — let the next stage evaluate
    }, "auto-approve")

    // STOP-WITH-FAILURE: a backend outage is a genuine fault — stop AND report it.
    Review.AddListenerWithErr(func(ctx context.Context, s *Submission) error {
        verdict, err := policy.Evaluate(ctx, s.Body)
        if err != nil {
            return fmt.Errorf("%w: %v", ErrBackendDown, err) // real error: TryEmit returns it
        }
        s.Vote = verdict
        return nil
    }, "policy")
}

func Run(ctx context.Context, s *Submission) error {
    err := Review.TryEmit(ctx, s)
    switch {
    case err == nil:
        // EITHER every stage passed, OR a stage short-circuited with StopPropagation.
        // Both are success: s.Vote holds the outcome ("auto-approved" or the verdict).
        return nil
    case errors.Is(err, ErrBackendDown):
        return fmt.Errorf("review %s could not complete: %w", s.ID, err) // genuine failure
    default:
        return err
    }
}
```

Note the asymmetry the pattern hinges on:

- `auto-approve` returns the **wrapped sentinel** → the chain stops, `policy` never
  runs, and `TryEmit` returns `nil`. The submission was *handled*.
- `policy` returns a **real error** → the chain stops and `TryEmit` returns that error.
  The submission *failed*.

`errors.Is(err, signals.StopPropagation)` is **never** true at the call site, because
`TryEmit` translates a clean stop to `nil` before returning. The sentinel is consumed
inside the signal; the caller only ever sees `nil` (handled or fully-passed) or a real
error.

## Variations

- **First-responder / first-match.** A chain of candidate handlers; the first one that
  can handle the event sets the result and returns `StopPropagation`. Classic for
  command routers, key-binding tables, and content negotiators.
- **Veto / gate.** A single early stage may reject the operation (auth, feature flag,
  quota) and stop the rest with `StopPropagation`. The decision rides on the payload.
- **LIFO veto.** Under `SignalOptions.Order = signals.LIFO`, the *newest* handler runs
  first and can short-circuit the older ones — "the latest override wins." See
  [Reverse (LIFO) Dispatch](reverse-dispatch.md).
- **Wrapped sentinel for tracing.** Return `fmt.Errorf("handled by %s: %w", name, signals.StopPropagation)`
  so the stop carries a breadcrumb in logs while still halting the chain via `errors.Is`.
- **Best-effort short-circuit on `Emit`.** On `Emit` (not `TryEmit`), `StopPropagation`
  still stops the chain and is not routed to `OnError`, while *real* errors are routed
  and the chain continues — useful when stopping is meaningful but ordinary listener
  errors should not abort notification.

## Known Uses

- **DOM `Event.stopPropagation()` / `stopImmediatePropagation()`.** A handler halts the
  event's journey through the listener list / bubbling phase — the direct browser
  analogue of this pattern.
- **HTTP middleware short-circuit.** Express (`return` without calling `next()`),
  ASP.NET, Rack, and Go `net/http` middleware all let a layer end the request early
  (auth deny, cache hit) without invoking the inner handlers.
- **Go stdlib `fs.SkipAll` / `filepath.SkipDir`.** Sentinel errors returned from a walk
  callback that *control iteration* — stop the walk — without being treated as a failure.
  `StopPropagation` is the same convention applied to signal dispatch.
- **Chain-of-Responsibility (GoF).** The first handler that can service the request
  consumes it and the rest of the chain is skipped — first-responder dispatch is exactly
  this.
- **Servlet `Filter` chains / interceptor stacks (gRPC).** A filter that does not call
  `chain.doFilter` short-circuits the remaining filters and the servlet.

## Related Patterns

- **[Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md)** — the parent
  pattern: ordered, in-caller, blocking dispatch. Short-Circuit Dispatch is that walk
  given an early-exit verb. Without a stop, the chain runs to completion exactly as
  described there.
- **[Reverse (LIFO) Dispatch](reverse-dispatch.md)** — short-circuiting composes with
  LIFO order: under `SignalOptions.Order = signals.LIFO` the newest handler runs first
  and can veto the rest.
- **[Transactional Emission](../reliability/transactional-emission.md)** — the
  stop-with-*failure* sibling. There, a *real* error stops the chain and is returned;
  here, the *sentinel* stops the chain and is **not** an error. Same "halt on first,"
  opposite meaning: failure vs. handled-success.
- **[Async Error Routing](../reliability/async-error-routing.md)** — contrast for the
  async case: `AsyncSignal` has no chain to short-circuit, ignores `StopPropagation`,
  and routes genuine listener errors to the per-signal sink instead.
