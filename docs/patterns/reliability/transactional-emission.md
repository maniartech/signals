# Transactional Emission

**Family:** Reliability
· **Also Known As:** Stop-on-First-Error, All-or-Nothing Dispatch
· **Status:** ✅ shipped

## Intent

Run a signal's listeners **sequentially**, and **abort the entire chain the moment
one of them fails** — returning that first error to the caller — so a multi-step
process behaves like a transaction: either every step gets its chance in order, or
the run halts at the first failure with the reason in hand.

## Motivation

Consider an e-commerce checkout. Placing an order is not one action; it is an ordered
pipeline of steps that **must** run in sequence, and where **each step depends on the
previous one having succeeded**:

1. **Validate** the cart (items in stock, price unchanged, address valid).
2. **Authorize** the customer's card for the total.
3. **Capture** the authorized funds.

These steps have a strict dependency: you must never authorize a card for a cart that
failed validation, and you must never capture funds you never authorized. The naive
approach is a fire-and-forget or a plain sync emit that ignores what each listener
reports:

```go
var Checkout = signals.NewSync[Order]()
Checkout.AddListener(validateCart)   // returns nothing the emitter can see
Checkout.AddListener(authorizeCard)  // runs even if validation should have stopped us
Checkout.AddListener(captureFunds)   // runs even if authorization failed

Checkout.Emit(ctx, order) // ✅ Emit DISCARDS every listener's error
```

Plain `Emit` on a `SyncSignal` runs all three listeners in order, but it **throws away
every error they return**. So when `validateCart` discovers the item is out of stock,
`Emit` keeps going: it authorizes the card and captures the funds anyway. You have now
**charged a customer for an order you cannot fulfil** — a refund, a support ticket, and
a trust hit, all because the emitter never looked at the first step's verdict. Worse,
the failure is silent: nothing in the call site reveals that anything went wrong.

Transactional Emission fixes this by switching from `Emit` to **`TryEmit`**, which runs
the same listeners in the same order but **stops at the first non-nil error and returns
it**:

```go
var Checkout = signals.NewSync[Order]()
Checkout.AddListenerWithErr(validateCart, "validate")
Checkout.AddListenerWithErr(authorizeCard, "authorize")
Checkout.AddListenerWithErr(captureFunds, "capture")

if err := Checkout.TryEmit(ctx, order); err != nil {
    return fmt.Errorf("checkout aborted: %w", err) // capture never ran
}
```

Now if `validateCart` returns `ErrOutOfStock`, `authorizeCard` and `captureFunds`
**never run**, and the caller gets the error immediately. The pipeline is all-or-
nothing at the boundary of the first failure, exactly like a database transaction that
rolls forward only while each statement succeeds.

## Applicability

**Use this pattern when:**

- The listeners form an **ordered pipeline** where a later step is meaningless — or
  dangerous — if an earlier step failed (validate → authorize → capture).
- You need the **first failure's error returned to the caller** so the caller can
  decide what to do (retry, surface to the user, roll back).
- **Determinism matters**: you want exactly one listener running at a time, in
  registration order, with no concurrency to reason about.
- You want **cancellation to short-circuit** the chain — if the context is canceled
  partway through, remaining steps must not run.

**Avoid it (or prefer another pattern) when:**

- Listeners are **independent** and you want them all to run regardless of individual
  failures, then collect every error → [Result Aggregation](result-aggregation.md).
- The work is **fire-and-forget** with no caller to return to → handle failures via
  [Async Error Routing](async-error-routing.md).
- You want **concurrent** execution for speed and order does not matter →
  [Await-All Dispatch](../dispatch/await-all-dispatch.md).
- You only care that listeners ran and never inspect their result → plain
  [Synchronous Sequential Dispatch](../dispatch/synchronous-sequential-dispatch.md)
  with `Emit`.

## Structure

```
  Caller ──TryEmit(ctx, payload)──▶ SyncSignal
                                       │
                  ┌────────────────────┘
                  ▼
            [ ctx.Err()? ] ──canceled──▶ return ctx.Err()  (no listener runs)
                  │ ok
                  ▼
            Listener₁ ──err?──▶ yes ──▶ return err₁   ◀── chain ABORTS here
                  │ nil
                  ▼
            [ ctx.Err()? ] ──canceled──▶ return ctx.Err()
                  │ ok
                  ▼
            Listener₂ ──err?──▶ yes ──▶ return err₂   ◀── Listener₃ never runs
                  │ nil
                  ▼
            [ ctx.Err()? ] ─ ... between every listener
                  │ ok
                  ▼
            Listener₃ ──err?──▶ yes ──▶ return err₃
                  │ nil
                  ▼
            return nil   (all listeners succeeded)
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Invokes `TryEmit`; receives the first error or `nil`; decides recovery |
| **SyncSignal** | Runs listeners sequentially in registration order; checks context between them; stops on first error |
| **Error-returning listeners** | `SignalListenerErr[T]` — each step; returns `nil` to continue or an error to abort the chain |
| **Context** | Carries cancellation/deadline; checked between listeners to short-circuit |

## Collaborations

1. The caller invokes `TryEmit(ctx, payload)` and **blocks** until the chain finishes
   or aborts.
2. Before running any listener, the signal checks `ctx.Err()`. If the context is
   already canceled, **no listener runs** and `ctx.Err()` is returned.
3. The signal runs listener 1. If it returns a non-nil error, `TryEmit` **returns that
   error immediately** — later listeners do **not** run.
4. If listener 1 returns `nil`, the signal re-checks `ctx.Err()` (cancellation can
   arrive mid-chain). If canceled, it returns `ctx.Err()`; otherwise it proceeds to
   listener 2.
5. Steps 3–4 repeat for each listener in registration order.
6. If every listener returns `nil` and the context is never canceled, `TryEmit`
   returns `nil` — the whole "transaction" committed.

## Consequences

**Benefits**

- ✓ **Fail-fast with the reason.** The caller gets the *first* error and can react,
  instead of discovering damage later.
- ✓ **No work after failure.** Expensive or irreversible later steps (charging a card,
  sending an email) never run once an earlier step fails.
- ✓ **Deterministic order.** Exactly one listener runs at a time in registration
  order — trivial to reason about, no data races to consider.
- ✓ **Cancellation-aware.** A canceled context short-circuits the remaining chain
  promptly, between listeners.

**Liabilities**

- ✗ **No isolation/rollback.** Steps that already succeeded are **not** undone when a
  later step fails — `TryEmit` stops, it does not roll back. If steps have side
  effects, you must provide compensation yourself (see Implementation).
- ✗ **Serial latency.** Total time is the *sum* of every listener up to the failure; no
  concurrency. For independent, slow steps this is wasteful → consider
  [Result Aggregation](result-aggregation.md).
- ✗ **Only the first error is seen.** Subsequent listeners never run, so you learn
  nothing about whether *they* would also have failed.

> **Trilemma note:** This is a synchronous, blocking path — it makes the caller wait
> and loses nothing. It trades throughput (serial, fail-fast) for correctness and a
> clean error contract.

## Implementation

1. **Use `AddListenerWithErr`, not `AddListener`.** Only `SignalListenerErr[T]`
   (`func(context.Context, T) error`) listeners can report a failure. A plain
   `SignalListener[T]` added with `AddListener` returns nothing, so it can never abort
   the chain — it always "succeeds" from `TryEmit`'s point of view.

2. **Sync `TryEmit` returns the *first* error and stops; `Emit` discards all errors.**
   These are the two sync emission verbs and they are deliberately different. Reach for
   `TryEmit` whenever the outcome of a step matters; reserve `Emit` for genuinely
   fire-and-continue notifications where no listener can fail meaningfully. (On an
   *async* signal the same `TryEmit` verb instead runs handlers concurrently and joins
   *all* their errors — it cannot stop-on-first across goroutines; see
   [Result Aggregation](result-aggregation.md). This pattern is the *sync* path.)

3. **Registration order is the execution order.** `SyncSignal` preserves registration
   order, so add your steps in the order they must run: validate first, capture last.
   Note that a `RemoveListener` uses swap-remove, which can reorder the *remaining*
   listeners — do not remove and re-add steps mid-pipeline and expect the original
   order to survive.

4. **Context is checked between listeners, not inside them.** The signal short-circuits
   *between* steps on cancellation, but a listener already running to completion is not
   interrupted. Long-running listeners should still honor `ctx` internally (e.g. pass
   it to the HTTP/DB call) so a deadline mid-step is respected.

5. **There is no automatic rollback — design compensation explicitly.** `TryEmit` gives
   you *stop-on-first-error*, not *all-or-nothing side effects*. If `authorize`
   succeeds but `capture` fails, the authorization still happened. Either (a) order the
   steps so the irreversible one is last and only runs after everything else passed, or
   (b) have the failing step (or the caller, on seeing the error) issue a compensating
   action (void the authorization). Pattern (a) is the simpler and preferred design.

6. **Wrap the returned error for context.** `TryEmit` returns the listener's raw error.
   Wrap it at the call site with `fmt.Errorf("checkout: %w", err)` so the caller can
   still use `errors.Is`/`errors.As` to identify the underlying cause while gaining a
   breadcrumb about *where* it failed.

7. **Keep listeners side-effect-honest about partial progress.** Because earlier steps
   have already run when a later one fails, make each step's side effects either
   idempotent or easily compensated. A validation step (read-only) is ideal early; a
   capture step (irreversible) belongs last.

8. **Performance.** `TryEmit` is a tight sequential loop with no goroutine spawning and
   no synchronization overhead beyond the per-step context check — the cheapest and
   most predictable emission verb. Its cost is purely the sum of the listeners it runs.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the **Structure**
diagram above — the *Caller*, the *SyncSignal*, the ordered *error-returning listeners*,
and the *context* checked between them. Read this first to see the mechanics; the
practical examples then apply it to real problems.

```go
// 1. SYNC SIGNAL — sequential, deterministic, registration-ordered dispatch.
sig := signals.NewSync[Request]()

// 2. ERROR-RETURNING LISTENERS — each is a step in the transaction. Registration
//    order IS execution order. A non-nil return ABORTS the chain here.
sig.AddListenerWithErr(func(ctx context.Context, r Request) error {
    return step1(ctx, r) // step 1 — runs first
}, "step-1")
sig.AddListenerWithErr(func(ctx context.Context, r Request) error {
    return step2(ctx, r) // step 2 — runs ONLY if step 1 returned nil
}, "step-2")
sig.AddListenerWithErr(func(ctx context.Context, r Request) error {
    return step3(ctx, r) // step 3 — the irreversible step goes LAST
}, "step-3")

// 3. CALLER — TryEmit blocks, runs the chain, returns the FIRST error (or nil).
err := sig.TryEmit(ctx, r)
//   ├─ ctx already canceled → no listener runs, returns ctx.Err()
//   ├─ a listener returns err → chain ABORTS, that err is returned, later steps skipped
//   ├─ ctx canceled mid-chain → returns ctx.Err() between listeners
//   └─ all listeners return nil → returns nil (the "transaction" committed)
if err != nil {
    return fmt.Errorf("aborted: %w", err) // first failure, with its reason in hand
}
```

The abort-on-first-error (step 3) is the heart of the pattern: it is where a multi-step
process gains its all-or-nothing-forward, fail-fast-with-the-reason contract.

### Practical Example 1 — Checkout payment pipeline

An e-commerce checkout is an ordered pipeline — validate → authorize → capture — where
each step is meaningless or dangerous if an earlier one failed. The irreversible capture
runs last, so it only happens once everything else has passed:

```go
package checkout

import (
    "context"
    "errors"
    "fmt"

    "github.com/maniartech/signals"
)

type Order struct {
    ID       string
    Items    []LineItem
    Total    Money
    CardTok  string
}

var (
    ErrOutOfStock   = errors.New("item out of stock")
    ErrPriceChanged = errors.New("price changed since cart")
    ErrCardDeclined = errors.New("card declined")
)

// One signal whose listeners form the ordered, fail-fast pipeline.
var checkout = signals.NewSync[Order]()

func Init(inv Inventory, pay PaymentGateway) {
    // Step 1: read-only validation. Safe to run first; no side effects to undo.
    checkout.AddListenerWithErr(func(ctx context.Context, o Order) error {
        for _, li := range o.Items {
            if !inv.InStock(ctx, li.SKU, li.Qty) {
                return fmt.Errorf("%w: %s", ErrOutOfStock, li.SKU)
            }
        }
        return nil
    }, "validate")

    // Step 2: authorize (reversible — can be voided). Runs only if validation passed.
    checkout.AddListenerWithErr(func(ctx context.Context, o Order) error {
        if err := pay.Authorize(ctx, o.CardTok, o.Total); err != nil {
            return fmt.Errorf("%w: %v", ErrCardDeclined, err)
        }
        return nil
    }, "authorize")

    // Step 3: capture (irreversible) — LAST, so it runs only when everything else
    // has already succeeded. This is the "design compensation by ordering" rule.
    checkout.AddListenerWithErr(func(ctx context.Context, o Order) error {
        return pay.Capture(ctx, o.CardTok, o.Total)
    }, "capture")
}

// Place runs the transaction. On the first failure the chain aborts and the error
// is returned; no later step runs.
func Place(ctx context.Context, o Order) error {
    if err := checkout.TryEmit(ctx, o); err != nil {
        return fmt.Errorf("checkout %s aborted: %w", o.ID, err)
    }
    return nil // every step committed in order
}
```

Callers can inspect the cause precisely because the error chain is preserved:

```go
err := checkout.Place(ctx, order)
switch {
case errors.Is(err, checkout.ErrOutOfStock):
    return reofferAlternatives(order)      // validation vetoed; nothing was charged
case errors.Is(err, checkout.ErrCardDeclined):
    return promptForNewCard(order)         // authorize failed; capture never ran
case err != nil:
    return retryLater(order)
}
```

**Contrast — the silent-damage anti-pattern:**

```go
// ❌ Emit discards errors: an out-of-stock cart still gets the card charged.
checkout.AddListener(validateCart) // error ignored
checkout.AddListener(authorizeCard)
checkout.AddListener(captureFunds)
checkout.Emit(ctx, order) // runs ALL three regardless of failures → customer charged
```

The symptom in production: refunds and chargebacks for orders that should never have
been placed, with nothing in the logs to explain why the validation "didn't work" —
because the emitter never looked at it.

### Practical Example 2 — Pre-publish content gate

A CMS must not publish an article until it clears an ordered set of policy checks:
the body must be non-empty, then it must contain no banned words, then the author must
be a verified contributor. The first veto blocks publication and names itself — and an
expensive check never runs if a cheaper earlier one already failed.

```go
package publishing

import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/maniartech/signals"
)

type Draft struct {
    ID       string
    AuthorID string
    Title    string
    Body     string
}

var (
    ErrEmptyBody      = errors.New("article body is empty")
    ErrBannedWord     = errors.New("article contains a banned word")
    ErrAuthorUnverified = errors.New("author is not a verified contributor")
)

// Cheapest, most-likely-to-fail checks first; the remote verification call last.
var prepublish = signals.NewSync[Draft]()

func Init(banlist Banlist, authors AuthorDirectory) {
    // Check 1: local, free — reject an empty body before doing anything else.
    prepublish.AddListenerWithErr(func(ctx context.Context, d Draft) error {
        if strings.TrimSpace(d.Body) == "" {
            return ErrEmptyBody
        }
        return nil
    }, "not-empty")

    // Check 2: local scan — no banned words. Runs only if the body is non-empty.
    prepublish.AddListenerWithErr(func(ctx context.Context, d Draft) error {
        if word, ok := banlist.FirstMatch(d.Body); ok {
            return fmt.Errorf("%w: %q", ErrBannedWord, word)
        }
        return nil
    }, "no-banned-words")

    // Check 3: remote lookup (the costly one) — verify the author. LAST, so the
    // network call is skipped entirely when a local check already vetoed.
    prepublish.AddListenerWithErr(func(ctx context.Context, d Draft) error {
        if !authors.IsVerified(ctx, d.AuthorID) {
            return fmt.Errorf("%w: %s", ErrAuthorUnverified, d.AuthorID)
        }
        return nil
    }, "author-verified")
}

// Publish gates the action: the first failing rule aborts the chain and explains why.
func Publish(ctx context.Context, d Draft) error {
    if err := prepublish.TryEmit(ctx, d); err != nil {
        return fmt.Errorf("cannot publish %s: %w", d.ID, err)
    }
    return store.MarkPublished(ctx, d.ID) // reached only when every gate passed
}
```

Because the checks are ordered cheapest-first, a draft with an empty body never reaches
the banned-word scan or the remote author lookup — and the returned error names the
exact rule that blocked publication.

## Variations

- **Validation-only chain.** Every listener is a read-only check (no side effects).
  `TryEmit` becomes a composable, ordered validator — the first failing rule wins and
  names itself. Reordering checks puts the cheapest/most-likely-to-fail first.
- **Pre-publish / pre-commit gate.** Run a chain of policy checks before a deploy,
  publish, or merge; the first veto blocks the action and explains why.
- **Saga with compensation.** When later steps have side effects, pair this pattern
  with explicit compensating actions keyed off the returned error — `TryEmit`
  identifies *where* the saga stopped so you know which compensations to run.
- **Short-circuit short list.** A two-step `authorize → confirm` chain is the smallest
  useful form: confirm never runs on a declined authorization.

## Known Uses

- **Database transactions.** SQL runs statements in order and aborts the transaction on
  the first error — the canonical all-or-nothing-forward model this pattern mirrors.
- **HTTP middleware chains** (Go's `net/http`, Express, Rack). A middleware that
  short-circuits (returns/throws) stops the rest of the chain — the same stop-on-first-
  failure structure.
- **CI/CD pipeline stages.** Build → test → deploy; a failed stage halts the pipeline
  and surfaces the failing stage's output. Later stages never run.
- **Validation frameworks** (e.g. fail-fast validators) that stop at the first broken
  rule and report it.
- **Unix shell `&&` chaining.** `validate && authorize && capture` — each command runs
  only if the previous one exited zero.

## Related Patterns

- **[Result Aggregation](result-aggregation.md)** — the opposite reliability choice:
  `TryEmit` on an *async* signal runs listeners *concurrently*, lets them *all* run, and
  returns the `errors.Join` of *every* failure instead of stopping at the first. (Across
  goroutines it *cannot* stop-on-first; only the sequential *sync* `TryEmit` described
  here aborts the chain on the first error.) Choose it when steps are independent.
- **[Async Error Routing](async-error-routing.md)** — for fire-and-forget async work
  where there is no caller to return an error to; failures go to a per-signal sink.
- **[Synchronous Sequential Dispatch](../dispatch/synchronous-sequential-dispatch.md)**
  — the same sequential delivery mode, but with `Emit`, which *discards* errors.
  Transactional Emission is that pattern made error-aware via `TryEmit`.
- **[Panic Isolation](panic-isolation.md)** — distinguishes a returned *error*
  (expected failure that aborts the chain) from a *panic* (an unexpected bug). A
  panicking listener is a different failure mode than one that returns an error.
- **[Context-Scoped Emission](../architectural/context-scoped-emission.md)** — supplies
  the context whose cancellation short-circuits the chain between steps.
