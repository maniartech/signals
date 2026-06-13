# Reverse (LIFO) Dispatch

**Family:** Dispatch
· **Also Known As:** Handler Stack, Reverse-Teardown, LIFO Dispatch, Newest-First Dispatch
· **Status:** ✅ shipped (v1.4 — `SignalOptions{Order: signals.LIFO}` on `SyncSignal`)

## Intent

Invoke a `SyncSignal`'s listeners in **reverse registration order** — most-recently-added
first — so the **newest** handler runs **first** and a set of handlers **unwinds in the
reverse of the order it was set up**, exactly like nested `defer` statements or a stack
popping. This is the "handler stack" discipline made into a delivery mode.

## Motivation

Consider a terminal UI (or a modal-stack in a desktop/mobile app) that handles the
**Escape key**. Screens are pushed onto a stack: the app opens, then a settings panel is
pushed, then a confirmation dialog on top of that. Each screen registers an Escape
handler when it appears. When the user presses Escape, exactly one screen should react —
**the topmost one** (the confirmation dialog), dismissing itself — and the screens below
should *not* see the event.

A developer wires each screen's Escape handler onto a single shared sync signal:

```go
var OnEscape = signals.NewSync[KeyEvent]() // default FIFO

func (s *Screen) Show() {
    s.cancel = OnEscape.AddListenerWithCancel(func(ctx context.Context, e KeyEvent) {
        if !e.handled {
            s.dismiss()
            e.handled = true
        }
    })
}
func (s *Screen) Hide() { s.cancel() } // pop: remove my handler
```

With the **default FIFO** order this is subtly wrong. The handlers fire **oldest first**:
the root app's handler runs *before* the confirmation dialog's. The root screen sees an
unhandled event, reacts (or worse, quits the app), and the topmost dialog — the one the
user actually wanted to close — never gets its turn, or gets it too late. The mental model
is a stack ("the thing on top handles it"), but FIFO delivers bottom-up.

You could hack around it by re-sorting, juggling priorities, or removing-and-re-adding
listeners on every push — all fragile, all noise. The real fix is to change the **delivery
order** to match the stack discipline you already have:

```go
var OnEscape = signals.NewSyncWithOptions[KeyEvent](&signals.SignalOptions{
    Order: signals.LIFO, // newest handler runs first — the topmost screen wins
})
```

Now the most-recently-shown screen's handler runs first, marks the event handled, and the
screens beneath it skip it. Push registers; pop (`cancel()`) removes; and crucially the
order is **stable across those pushes and pops** — registration order is a genuine
guarantee, so "newest first" stays correct no matter how the stack churns.

## Applicability

**Use this pattern when:**

- **The newest handler should take precedence** — override / interceptor stacks where a
  later registration *shadows* earlier ones (middleware-style "innermost wins").
- **Resources must tear down in reverse of setup** — listeners that acquire/configure in
  order should release in the opposite order, the way `defer` unwinds and a stack pops
  (open A → open B → … close B → close A).
- **You are modeling a focus / back-button / Escape / modal stack** — the topmost (most
  recently pushed) handler reacts first and may consume the event before lower layers see
  it. (Android's `OnBackPressedDispatcher` is LIFO for exactly this reason.)
- **Order must survive subscription churn** — handlers are pushed and popped at runtime
  and you need "newest first" to remain a real invariant, not a coincidence.

**Avoid it (or prefer another pattern) when:**

- **You have an async / fan-out signal.** LIFO is `SyncSignal`-only. `AsyncSignal`
  invocation order is unspecified and `Order` is ignored — see
  [Fire-and-Forget Dispatch](fire-and-forget-dispatch.md) and
  [Await-All Dispatch](await-all-dispatch.md).
- **The natural flow is a forward pipeline** (validate → normalize → enrich) where each
  stage builds on the previous → keep the default FIFO of
  [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md).
- **Order does not matter at all** — don't reach for LIFO to signal intent you don't
  actually depend on; default FIFO is the simpler contract.

## Structure

```
  Registration order (push):  H₁  then  H₂  …  then  Hₙ   (Hₙ is newest / "top of stack")

  Caller's goroutine (blocks for the whole duration)
  ┌──────────────────────────────────────────────────────────────┐
  │  Emit(ctx, payload)   — LIFO: iterate the slice in REVERSE     │
  │      │                                                         │
  │      ├─ ctx canceled at entry? ──yes──▶ run nothing, return    │
  │      │                                                         │
  │      ├─▶ Hₙ (newest)   ── completes ──┐                        │
  │      │                                │  (reverse order)       │
  │      ├─ ctx canceled? ──yes──▶ stop    ▼                        │
  │      ├─▶ Hₙ₋₁          ── completes ──┐                        │
  │      │                                ▼                        │
  │      └─▶ H₁ (oldest)   ── completes ──▶ return                 │
  │                                                                │
  └──────────────────────────────────────────────────────────────┘
   Same slice, same cost — just walked from the tail. Order is STABLE
   across add/remove because removal is order-preserving (not swap-remove).
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Caller (Emitter)** | Calls `Emit`/`TryEmit`; blocks until the reverse walk finishes; reads effects on the next line |
| **SyncSignal (LIFO)** | Constructed with `Order: signals.LIFO`; iterates the published listener slice from the tail (newest) to the head (oldest), checking `ctx` between listeners |
| **`SignalOptions.Order`** | The `EmitOrder` field (`FIFO` default, `LIFO`) set once at construction; fixes the walk direction |
| **Handler stack** | The listeners, conceptually pushed (`AddListener`/`AddListenerWithCancel`) and popped (`cancel()`/`RemoveListener`); newest is "top" |
| **Canceller `func()`** | The keyless teardown handle from `AddListenerWithCancel`; calling it pops that handler off the stack |
| **Context** | Carries cancellation/deadline; checked at entry and between listeners to short-circuit the reverse walk |

## Collaborations

1. The signal is constructed once with `NewSyncWithOptions[T](&SignalOptions{Order: LIFO})`.
   The chosen order is fixed for the signal's lifetime.
2. Handlers are **pushed** over time via `AddListener` (or, ideally,
   `AddListenerWithCancel` for a keyless pop handle). Each push appends to the tail of the
   listener slice — it becomes the new "top of stack."
3. On `Emit(ctx, payload)`, the signal checks `ctx`; if already canceled, **no listener
   runs**. Otherwise it walks the slice **from the tail backward**, invoking the newest
   handler first, then the next-newest, down to the oldest.
4. **Between handlers** the signal re-checks `ctx`; a canceled/expired context stops the
   reverse walk mid-way (remaining, older handlers do not run).
5. A handler may **consume** the event (set a "handled" flag on the payload) so older
   handlers beneath it skip their work — the stack-precedence idiom.
6. A handler is **popped** by calling its canceller (or `RemoveListener(key)`). Because
   removal is **order-preserving**, popping any handler leaves the remaining handlers in
   the same relative order, so "newest first" stays correct across the churn.

## Consequences

**Benefits**

- ✓ **Newest-wins precedence.** The most recently registered handler runs first — the
  natural semantics for override/interceptor stacks and topmost-modal handling.
- ✓ **Reverse-teardown for free.** Handlers registered in setup order unwind in the exact
  reverse, mirroring `defer` — no manual ordering or priority bookkeeping.
- ✓ **Stable order across churn.** Removal is order-preserving (a fresh slice omitting the
  match, *not* swap-remove), so FIFO/LIFO is a genuine, durable guarantee even as handlers
  are pushed and popped. **LIFO is only meaningful because order is stable.**
- ✓ **No extra cost.** Reverse iteration is free — the same single atomic load and the
  same O(n) walk as forward, just from the tail. Order-preserving removal copies n−1
  elements: the same O(n) the copy-on-write write already pays, with no regression.
- ✓ **All sync semantics intact.** Single-threaded, completion-guaranteed, ctx-checked
  between listeners, and it composes with `TryEmit` (stop-on-first-error in reverse) and
  the cancellable subscription API.

**Liabilities**

- ✗ **SyncSignal-only.** `AsyncSignal` ignores `Order`; its invocation order is
  unspecified. LIFO is **not** a general fan-out feature — it is a *sequential* dispatch
  discipline.
- ✗ **Best for handler-stack / teardown shapes.** For a straight forward pipeline LIFO is
  the wrong default and will surprise readers; reach for it only when newest-first or
  reverse-unwind is the actual requirement.
- ✗ **Same head-of-line blocking as any sync dispatch.** A slow or hung handler stalls the
  caller and the rest of the (reverse) chain; cancellation between handlers is cooperative.
- ✗ **Plain `Emit` discards errors.** As with FIFO, `Emit` routes listener errors to
  `OnError` and continues; use `TryEmit` for stop-on-first-error (which, under LIFO, stops
  at the first error in *reverse* order).

> **Trilemma corner sacrificed:** identical to FIFO sync dispatch — zero loss and bounded
> memory are kept, the **producer waits** by definition. LIFO changes *order*, not the
> blocking/loss profile.

## Implementation

1. **Construct with the option; it is immutable afterward.** Use
   `signals.NewSyncWithOptions[T](&signals.SignalOptions{Order: signals.LIFO})`. `Order`
   is read once at construction into the signal's `order` field and consulted on the
   lock-free emit path; it **cannot** be changed on a live signal. The zero value is
   `FIFO`, so a default `NewSync`/zero-value `SyncSignal` stays registration-order.

2. **`Order` applies to `SyncSignal` only.** `AsyncSignal` ignores it entirely —
   async handlers run concurrently with no ordering guarantee, so "reverse order" is
   meaningless there. Do not expect LIFO from `signals.New`/`NewWithOptions`.

3. **Stability is the whole point — and it is real now.** Removal builds a fresh slice
   that omits the matched listener while preserving the relative order of the rest (it is
   **not** swap-remove). That is what makes LIFO (and FIFO) a *stable* guarantee across
   add/remove: push and pop handlers freely and "newest first" still holds. Without
   order-preserving removal, LIFO would be a coin-flip after the first pop.

4. **Cancellation is checked between handlers, not inside them.** The reverse walk
   inspects `ctx` before each handler and stops if it is done; it cannot preempt a handler
   already running. Long handlers should check `ctx` themselves at natural boundaries.

5. **`TryEmit` gives stop-on-first-error in reverse.** Under LIFO, `TryEmit` invokes
   newest-first and returns the **first** error it hits walking backward, stopping the
   chain there. This is the fail-fast form of an override stack: the topmost handler that
   fails aborts the emission. Plain `Emit` instead routes errors to `OnError` and runs the
   whole reverse chain best-effort.

6. **Use `AddListenerWithCancel` for the push/pop handle.** A handler stack maps cleanly
   onto keyless teardown: `cancel := sig.AddListenerWithCancel(h)` pushes, `cancel()` pops.
   The canceller is idempotent (`sync.Once`-guarded), so a double-pop is a safe no-op and
   a stale canceller never removes a later re-push. Reach for a **key** instead when
   another module must pop a specific handler by name — see
   [Subscription Teardown](../subscription/subscription-teardown.md).

7. **Consuming the event is your idiom, not the library's.** LIFO chooses *who runs
   first*; it does not stop the chain once a handler "handles" the event. To get
   topmost-wins-and-others-skip, carry a `handled` flag on the payload and have each
   handler early-return when it is already set (or use `TryEmit` and return a sentinel to
   stop). The signal will still visit lower handlers — they simply no-op.

8. **No goroutines, panics propagate to the caller.** Like all sync dispatch, LIFO spawns
   nothing, so there is nothing to leak; but a panic in a handler propagates to the `Emit`
   caller (it is *not* routed to the async panic handler). Recover inside the handler if a
   stack layer can panic — see [Panic Isolation](../reliability/panic-isolation.md).

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton mapping onto the **Participants** and **Structure** above — the
LIFO-configured *SyncSignal*, handlers *pushed* onto the stack, and `Emit` walking them
*newest-first*. Read this first; the practical examples then apply it.

```go
// 1. SIGNAL — a SyncSignal in LIFO order. Newest listener runs first.
sig := signals.NewSyncWithOptions[Event](&signals.SignalOptions{
    Order: signals.LIFO,
})

// 2. PUSH handlers over time. Each push becomes the new "top of stack".
sig.AddListener(func(ctx context.Context, e Event) { base(e) })   // H₁ (oldest)
popMid := sig.AddListenerWithCancel(func(ctx context.Context, e Event) {
    middle(e)                                                      // H₂
})
sig.AddListener(func(ctx context.Context, e Event) { top(e) })    // H₃ (newest / top)

// 3. CALLER — Emit blocks and walks the stack from the TOP down.
sig.Emit(ctx, e)
//   ├─ ctx canceled at entry?  → run nothing, return
//   ├─▶ H₃ top(e)              → completes  (newest first)
//   ├─ ctx canceled between?   → stop, older handlers skipped
//   ├─▶ H₂ middle(e)           → completes
//   └─▶ H₁ base(e)             → completes, THEN Emit returns

// 4. POP a handler. Removal is ORDER-PRESERVING, so the remaining
//    handlers keep their relative order — "newest first" stays correct.
popMid() // H₂ gone; next Emit walks H₃ then H₁, still newest-first.
```

The reverse walk (step 3) plus order-preserving pop (step 4) are the heart of the pattern:
LIFO is *useful* precisely because the order it picks is *stable* as the stack churns.

### Practical Example 1 — Reverse-order resource teardown (defer-style unwind)

A service boots subsystems in dependency order: open the metrics exporter, then the DB
pool, then the cache that depends on the DB. On shutdown they must close in **reverse** —
cache first, then DB, then metrics — or the cache flushes into an already-closed DB. A
LIFO `Shutdown` signal makes registration order *be* the teardown order, automatically
reversed, just like stacked `defer`s.

```go
package app

import (
    "context"

    "github.com/maniartech/signals"
)

// Fired once during graceful shutdown. LIFO: subsystems tear down in the
// reverse of the order they registered their closers — newest setup unwinds first.
var Shutdown = signals.NewSyncWithOptions[context.Context](&signals.SignalOptions{
    Order: signals.LIFO,
})

// onClose registers a teardown step. Call these in *startup* order; LIFO guarantees
// they run in reverse on shutdown (cache → db → metrics), like nested defer.
func onClose(name string, fn func(context.Context)) {
    Shutdown.AddListener(func(ctx context.Context, c context.Context) {
        fn(c) // each step releases what the corresponding setup acquired
    }, name)
}

func Boot(ctx context.Context) {
    startMetrics(ctx)
    onClose("metrics", stopMetrics) // registered 1st → torn down LAST

    openDB(ctx)
    onClose("db", closeDB)          // registered 2nd → torn down 2nd

    openCache(ctx)                  // depends on DB
    onClose("cache", flushCache)    // registered 3rd (newest) → torn down FIRST
}

// GracefulShutdown unwinds the stack: flushCache → closeDB → stopMetrics.
func GracefulShutdown(ctx context.Context) {
    Shutdown.Emit(ctx, ctx) // LIFO: newest closer runs first
}
```

`flushCache` runs while the DB is still open (correct), and `stopMetrics` runs last. Had
this used the default FIFO, the cache would have flushed *after* the DB closed — a
shutdown-time data-loss bug that only bites in production.

### Practical Example 2 — Escape / back-button modal stack (topmost handler wins)

A TUI (or modal app) pushes screens onto a stack and shares one `OnEscape` sync signal.
Pressing Escape must dismiss only the **topmost** screen. LIFO makes the newest screen's
handler run first; a `handled` flag lets it consume the event so lower screens skip it.
Each screen pushes on show and pops on hide via a keyless canceller.

```go
package ui

import (
    "context"

    "github.com/maniartech/signals"
)

type KeyEvent struct{ handled bool }

// LIFO: the most-recently-shown screen's Escape handler runs first.
var OnEscape = signals.NewSyncWithOptions[*KeyEvent](&signals.SignalOptions{
    Order: signals.LIFO,
})

type Screen struct {
    name   string
    pop    func() // keyless teardown handle; calling it removes this screen's handler
}

// Show pushes the screen's Escape handler onto the stack (top of LIFO order).
func (s *Screen) Show() {
    s.pop = OnEscape.AddListenerWithCancel(func(ctx context.Context, e *KeyEvent) {
        if e.handled {
            return // a screen above me already consumed this Escape
        }
        s.dismiss()
        e.handled = true // consume it; screens below me will skip
    })
}

// Hide pops this screen's handler. Order-preserving removal keeps the rest of the
// stack in newest-first order, so the next-topmost screen now wins Escape.
func (s *Screen) Hide() {
    if s.pop != nil {
        s.pop()
        s.pop = nil
    }
}

func (s *Screen) dismiss() { /* close this screen, return focus below */ }

// DispatchEscape feeds an Escape keypress to the stack; the topmost screen handles it.
func DispatchEscape(ctx context.Context) {
    OnEscape.Emit(ctx, &KeyEvent{}) // newest screen first, then down the stack
}
```

Push three screens; press Escape; only the top one dismisses. Pop it; press Escape again;
the new top dismisses. The "topmost handles it" mental model and the delivery order finally
agree — and they stay agreeing as screens come and go, because removal preserves order.

### Practical Example 3 — Fail-fast override stack with `TryEmit`

An interceptor stack where the **newest** interceptor may veto an operation, and the
*first* veto (from the top down) should abort. `TryEmit` on a LIFO signal walks
newest-first and returns the first non-nil error, stopping the chain there.

```go
package guard

import (
    "context"
    "errors"

    "github.com/maniartech/signals"
)

type Request struct{ User, Action string }

// LIFO override stack: the most-recently-installed guard checks first and can veto.
var BeforeAction = signals.NewSyncWithOptions[Request](&signals.SignalOptions{
    Order: signals.LIFO,
})

func init() {
    // Base policy installed first → checked LAST.
    BeforeAction.AddListenerWithErr(func(ctx context.Context, r Request) error {
        if r.User == "" {
            return errors.New("base: anonymous denied")
        }
        return nil
    }, "base-policy")
}

// InstallOverride pushes a higher-precedence guard; under LIFO it runs BEFORE the
// base policy, so a temporary override can short-circuit the older rule.
func InstallOverride(g signals.SignalListenerErr[Request]) func() {
    return BeforeAction.AddListenerWithCancel(toPlain(g)) // see note below
}

// Authorize runs the stack newest-first and stops at the first veto.
func Authorize(ctx context.Context, r Request) error {
    // TryEmit walks LIFO: newest guard → … → base-policy, returning the first error.
    return BeforeAction.TryEmit(ctx, r)
}
```

> Note: `AddListenerWithCancel` takes a plain `SignalListener`; for error-returning guards
> that need a keyless handle, prefer a **keyed** `AddListenerWithErr` + `RemoveListener`
> (there are deliberately no `WithCancel` variants of the error column — keys serve the
> addressable case). The `toPlain` shim above is illustrative; in real code register the
> error listener with a key and pop it by that key.

Under LIFO `TryEmit`, the freshest override gets the first say and a single veto aborts —
a fail-fast override stack in a few lines.

## Variations

- **LIFO `Emit` (best-effort stack).** Run every handler newest-first, routing any errors
  to `OnError` and never stopping — for notification/teardown stacks where each layer must
  run regardless of others.
- **LIFO `TryEmit` (fail-fast stack).** Stop at the first error encountered in reverse
  order — the override/veto stack of Practical Example 3.
- **Keyed pop vs. handle pop.** Use `AddListenerWithCancel` for same-scope push/pop with no
  key to invent; use a **key** + `RemoveListener` when another module must pop a named
  layer — see [Subscription Teardown](../subscription/subscription-teardown.md).
- **Consume-and-skip.** Carry a `handled` flag on the payload so the topmost handler can
  suppress lower ones (modal Escape, Example 2), since the library does not stop the chain
  on "handled" by itself.
- **FIFO sibling.** Drop the `Order` option (or set `FIFO`) for the forward-pipeline
  default — [Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md).

## Known Uses

- **`defer` (Go)** — deferred calls run in LIFO order; reverse-teardown is the language's
  built-in unwind discipline, and this pattern brings it to a shared signal.
- **Android `OnBackPressedDispatcher`** — back-press callbacks are invoked
  most-recently-added first; the topmost component consumes the back gesture. LIFO modal
  handling, exactly.
- **Middleware / interceptor stacks** — many frameworks let an inner (later-registered)
  layer wrap and take precedence over outer ones; "innermost wins" is LIFO precedence.
- **Undo stacks and exception unwinding** — the most recent action is undone first; stack
  frames unwind newest-first. The same last-in-first-out shape.
- **UI focus / Escape / dialog stacks** — the front-most (newest) modal handles input and
  Escape before anything beneath it.

## Related Patterns

- **[Synchronous Sequential Dispatch](synchronous-sequential-dispatch.md)** — the FIFO
  sibling and default. Same single-threaded, completion-guaranteed, ctx-checked semantics;
  LIFO differs only in walking the listener slice from the tail. Choose FIFO for forward
  pipelines, LIFO for handler stacks and reverse-teardown.
- **[Subscription Teardown](../subscription/subscription-teardown.md)** — the push/pop
  mechanics behind a handler stack: `AddListenerWithCancel`'s keyless canceller, the
  handle-vs-key choice, and why order-preserving removal keeps the stack order stable.
- **[One-Shot Subscription](../subscription/one-shot-subscription.md)** — pair with LIFO
  when a stack layer should react *once* then pop itself; `AddOnce`/`AddOnceWithCancel`
  give self-removing handlers that still respect the configured reverse order.
- **[Transactional Emission](../reliability/transactional-emission.md)** — `TryEmit`'s
  stop-on-first-error semantics, here applied newest-first to build a fail-fast override
  stack.
- **[Panic Isolation](../reliability/panic-isolation.md)** — as with all sync dispatch, a
  panic in a stack handler propagates to the caller; isolate inside the handler if a layer
  can panic.
