# Keyed Subscription

**Family:** Subscription Lifecycle
· **Also Known As:** Named Listener, Identified Subscription
· **Status:** ✅ shipped (`AddListener` with a key, `RemoveListener`, keyed dedup,
  and the introspection helpers `HasKey` / `Keys`)

## Intent

Give a listener a stable **string key** when you register it, so it can later be
individually **removed, replaced, or de-duplicated** by name. Without a key an
anonymous listener has no handle: you can add it, but you can never single it out
again.

## Motivation

Consider a dashboard service that subscribes to a `signals.AsyncSignal[Account]`
to keep a per-account usage tracker warm. Each account view, when it mounts,
registers a listener; when it unmounts, it wants that listener gone. The naive
approach registers anonymously:

```go
var AccountChanged = signals.New[Account]()

func (v *UsageView) Mount() {
    AccountChanged.AddListener(v.refresh) // anonymous — no handle returned that identifies it
}

func (v *UsageView) Unmount() {
    // ...how do we remove *this* view's listener?  We have no key. We can't.
}
```

`RemoveListener` takes a key, not a function value (Go cannot compare functions for
equality anyway). With nothing to name the subscription, `Unmount` is stuck: it
either leaves the listener registered forever — a slow leak where every mounted-then-
unmounted view keeps firing and keeps its captured `*UsageView` alive — or it calls
`Reset()` and nukes *every other* subscriber along with it. Both are wrong.

A second failure shows up under retries. Say the view's `Mount` runs twice (a
re-render, a reconnect, a careless caller). Anonymously, you now have **two** copies
of the same listener, and every emit does the work twice.

Keyed Subscription fixes both. Hand the listener a name:

```go
func (v *UsageView) Mount() {
    // Keyed: identified, removable, and de-duplicated in one call.
    AccountChanged.AddListener(v.refresh, "usage-view/"+v.AccountID)
}

func (v *UsageView) Unmount() {
    AccountChanged.RemoveListener("usage-view/" + v.AccountID) // removes exactly this one
}
```

Now the subscription has a handle. `Unmount` removes precisely this view's listener
and no other. And because the library **de-duplicates by key**, a double `Mount`
registers once: the second `AddListener` with an existing key is a no-op that returns
`-1`, so the work never doubles.

## Applicability

**Use this pattern when:**

- A component **subscribes on start and must unsubscribe on stop** (views, sessions,
  background workers, plugins) — anything with a lifecycle shorter than the signal's.
- You need to **replace** a listener's implementation in place — swap the handler
  behind a stable name without disturbing other subscribers.
- You must **guard against double-registration** — the same logical subscriber may be
  wired up more than once and you want idempotent registration.
- You want a **namespaced registry** of subscribers you can reason about
  (`"billing/usage-tracker"`, `"audit/login-logger"`).

**Avoid it (or prefer another pattern) when:**

- The listener lives for the **entire lifetime** of the process and is never removed
  or replaced — an anonymous `AddListener(handler)` is fine and simpler.
- You want a listener that **removes itself after one fire** → use
  [One-Shot Subscription](one-shot-subscription.md), which manages the lifecycle for
  you (though a keyed `AddOnce` (`AddOnce(h, key)`) lets you combine both).
- The only reason you reached for a key is **removal by the same code that
  registered** → see the note below; `AddListenerWithCancel` gives you a teardown
  handle with no key at all.

> **When a key is NOT needed.** If a listener is anonymous or scoped — a closure with
> no natural name, registered and torn down by the same code — you don't have to
> invent a key just to remove it. `AddListenerWithCancel(h)` (and its one-shot
> sibling `AddOnceWithCancel`) returns an idempotent canceller func that removes
> exactly that listener: `cancel := sig.AddListenerWithCancel(h); defer cancel()`.
> Keys remain the right tool when the subscription must be **addressable by name
> across modules** (registered here, removed there), needs **duplicate detection**
> (the `-1` dedup), or should appear in **`Keys()`/`HasKey` introspection** —
> `WithCancel` registrations without a caller key are deliberately hidden from
> `Keys()`. See [Subscription Teardown](subscription-teardown.md) for the full
> handle-vs-key decision guidance.

## Structure

```
  Register ─ AddListener(handler, "billing/usage-tracker")
                 │
                 ▼
        ┌──────────────────────────────────────────────┐
        │  Signal: keyed listener table                 │
        │  ┌──────────────────────┬─────────────────┐   │
        │  │ "billing/usage-track"│ handler ───────▶ │   │   ◀─ key is the handle
        │  │ "audit/login-logger" │ handler          │   │
        │  │ "usage-view/acct-42" │ handler          │   │
        │  └──────────────────────┴─────────────────┘   │
        └──────────────────────────────────────────────┘
                 ▲                         │
                 │                         ▼
   AddListener(h, "audit/login-logger")   RemoveListener("usage-view/acct-42")
        │                                  └─▶ returns new count, or -1 if absent
        └─▶ key already present? ─▶ no-op, returns -1   (de-dup)
            key new?              ─▶ insert,  returns new count
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Subscriber** | Owns a lifecycle; registers with a key on start, removes by key on stop |
| **Key** | A stable, unique `string` that names the subscription (the handle) |
| **Signal** | Maintains the keyed listener table; enforces key uniqueness (dedup) |
| **`AddListener(h, key)`** | Inserts under `key`, or no-ops and returns `-1` if the key already exists |
| **`RemoveListener(key)`** | Drops the listener under `key`; returns `-1` if no such key |
| **`HasKey` / `Keys`** | Introspect the table: existence check / snapshot of all keys |

## Collaborations

1. A subscriber registers with `AddListener(handler, key)`. If the key is **new**, the
   listener is stored under it and the call returns the resulting listener **count**.
2. If the key is **already present**, the library performs **keyed dedup**: it does
   nothing and returns **`-1`**. The caller can treat `-1` as "already registered."
3. To replace the implementation, the subscriber calls `RemoveListener(key)` then
   `AddListener(newHandler, key)` — the name stays constant while the behavior changes.
4. On stop, the subscriber calls `RemoveListener(key)`. If the key was present its
   listener is removed and the new count is returned; if it was absent, `-1` signals
   "nothing to remove."
5. Callers can ask `HasKey(key)` for an O(1) existence check, or `Keys()`
   for a snapshot of every registered key — useful for diagnostics and for building a
   replace-only-if-present flow without touching the count.

## Consequences

**Benefits**

- ✓ **Individually addressable listeners.** Each keyed subscription has a handle, so
  it can be removed or replaced without disturbing any other.
- ✓ **Idempotent registration.** Keyed dedup makes double-`Mount` / double-wire-up
  safe: the second add is a no-op (`-1`), never a duplicate.
- ✓ **Leak-resistant lifecycles.** Components that subscribe on start can reliably
  unsubscribe on stop, releasing the listener and everything its closure captured.
- ✓ **Legible registry.** Namespaced keys turn an opaque list of functions into a
  self-documenting table of who is listening and why.

**Liabilities**

- ✗ **Key collisions are silent-ish.** A second add under an existing key returns `-1`
  and does nothing — if you ignore the return value, you may believe you registered a
  handler that was actually dropped. Check the return, or use `HasKey` first.
- ✗ **You must invent and manage a namespace.** Keys are strings; uniqueness is your
  responsibility. Ad-hoc keys collide; disciplined ones (`"domain/role"`) do not.
- ✗ **No automatic cleanup.** A keyed listener still lives until you remove it.
  The key makes removal *possible*, not automatic (see
  [Subscription Teardown](subscription-teardown.md)).

## Implementation

1. **Always key a listener you intend to remove or replace *by name*.** This is the
   core rule. `RemoveListener` works by key; a plain `AddListener` with no key cannot
   be targeted. If a subscription has a lifecycle, give it a key at birth — or, when
   the same code registers and removes it, hold the canceller from
   `AddListenerWithCancel` instead (see the note under Applicability).

2. **v1.4 behavior change — `RemoveListener("")` no longer removes unkeyed listeners.**
   In earlier behavior, calling `RemoveListener` with an empty string could match
   listeners that were added without a key (they were treated as having key `""`).
   v1.4 fixes this: unkeyed listeners carry an explicit *keyed = false* flag and are
   **not** addressable by `""`. The practical consequence: do not rely on `""` as a
   wildcard or as a way to reach anonymous listeners — it never reliably did what it
   appeared to. If you want to remove a listener, **give it a real key.** To remove
   everything, use `Reset()`.

3. **Treat `-1` as a real signal, not noise.** Both `AddListener` and `RemoveListener`
   return `-1` for the "didn't do what you might expect" case (key already exists /
   key not found). A return ≥ 0 is the new listener count. Branch on it when
   correctness depends on whether the add/remove actually happened.

4. **Replace = remove-then-add under the same key.** There is no atomic "swap handler"
   call. The idiom is `RemoveListener(key)` followed by `AddListener(newHandler, key)`.
   Between those two calls there is a brief window with no listener under that key; if
   an emit can race with the swap, perform it where concurrent emits are quiesced, or
   accept that one emit may miss the listener. The keyed *dedup* (returning `-1` on a
   duplicate add) means you **cannot** skip the remove and just add again — the second
   add would be rejected.

5. **Namespace your keys.** Use a `"domain/role"` or `"package/purpose"` convention:
   `"billing/usage-tracker"`, `"audit/login-logger"`, `"cache/invalidator"`. Encode
   the instance when there are many (`"usage-view/acct-42"`). A disciplined namespace
   prevents accidental collisions between unrelated subscribers sharing one signal —
   which matters most on a [Shared Event Registry](../architectural/shared-event-registry.md).

6. **Guard registration with `HasKey` when you need a decision, not a count.**
   `if !sig.HasKey(k) { sig.AddListener(h, k) }` reads more clearly than inspecting a
   `-1`, and is O(1). For diagnostics, `Keys()` gives a snapshot you can log or expose
   on a health endpoint to see exactly who is subscribed.

7. **Keys are independent of dispatch mode.** Keyed subscription works identically on
   `SyncSignal` and `AsyncSignal`; it governs the *registry*, not delivery. It composes
   freely with any dispatch or flow-control pattern.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Subscriber* that owns a lifecycle, the *Key* that
names the subscription, the *Signal* that enforces dedup, and the
`AddListener`/`RemoveListener` calls keyed by that name. Read this first to see the
mechanics; the practical examples then apply it to real problems.

```go
// 1. SIGNAL — maintains the keyed listener table.
sig := signals.New[Event]()

// 2. KEY — a stable, unique string that names this subscription (the handle).
const key = "domain/role" // namespaced; uniqueness is your responsibility

// 3. REGISTER — insert under the key.
n := sig.AddListener(func(ctx context.Context, e Event) {
    handle(e)
}, key)
//   ├─ key new        → inserted, n = new listener count (≥ 0)
//   └─ key duplicate  → no-op, n == -1  (keyed DEDUP: the add was rejected)

if n == -1 {
    // already registered under this key — treat as "ensure exactly one"
}

// 4. INTROSPECT — decide without inspecting a count.
if sig.HasKey(key) { /* exactly one subscriber lives under key */ }
_ = sig.Keys() // snapshot of every caller-supplied key, for diagnostics

// 5. REPLACE — there is no atomic swap: remove first, then re-add under the
//    SAME key (dedup would reject a re-add if the old one were still present).
sig.RemoveListener(key)
sig.AddListener(newHandler, key)

// 6. REMOVE — detach exactly this one on the subscriber's stop.
m := sig.RemoveListener(key)
//   ├─ key present → removed, m = new count
//   └─ key absent  → m == -1  ("nothing to remove")
```

The key (step 2) is the heart of the pattern: it is the handle that turns an
otherwise-anonymous listener into something you can de-duplicate, replace, and remove
by name — `RemoveListener` takes a key, never a function value.

### Practical Example 1 — Dashboard view: subscribe on mount, remove exactly itself on unmount

A dashboard renders many account views; each view subscribes when it mounts and must
detach **only its own** listener when it unmounts. Keying by the component's account
id makes removal surgical and makes an accidental double-mount idempotent.

```go
package dashboard

import (
    "context"

    "github.com/maniartech/signals"
)

type Account struct {
    ID      string
    Balance int64
}

// One shared signal; every account view subscribes to it, each under its own key.
var AccountChanged = signals.New[Account]()

type UsageView struct {
    AccountID string
    redraw    func(Account)
}

// key derives deterministically from the view's identity, so Mount and Unmount
// never need to share state beyond the AccountID itself.
func (v *UsageView) key() string { return "usage-view/" + v.AccountID }

// Mount subscribes. The keyed dedup makes a double-mount (re-render, reconnect)
// safe: the second AddListener returns -1 and does nothing.
func (v *UsageView) Mount() {
    if n := AccountChanged.AddListener(func(ctx context.Context, a Account) {
        if a.ID == v.AccountID {
            v.redraw(a) // refresh only this view's account
        }
    }, v.key()); n == -1 {
        // Already mounted under this key — re-render, not a new subscription.
        return
    }
}

// Unmount removes exactly this view's listener and no other subscriber.
func (v *UsageView) Unmount() {
    AccountChanged.RemoveListener(v.key()) // surgical: leaves every other view intact
}
```

When a view is mounted, unmounted, and re-mounted across a reconnect, the listener
count stays at exactly one for its key — no duplicates, no orphaned closure pinning a
disposed view component.

### Practical Example 2 — Hot-swapping a config-reload handler atomically

A service reloads configuration on a SIGHUP and wants the *new* config baked into the
handler. Because there is no atomic swap call, you `RemoveListener` then `AddListener`
under the same key; `HasKey` guards against wiring the handler up twice at startup.

```go
package config

import (
    "context"

    "github.com/maniartech/signals"
)

type Reloaded struct {
    Version int
    Values  map[string]string
}

// Fires every time configuration is reloaded from disk.
var ConfigReloaded = signals.New[Reloaded]()

const applyKey = "config/apply-to-router"

// Install wires the apply handler once. HasKey makes the guard explicit:
// a second Install (double-init, test re-run) is a clean no-op, not a duplicate.
func Install(router *Router) {
    if ConfigReloaded.HasKey(applyKey) { // clearer than inspecting -1
        return // already installed
    }
    ConfigReloaded.AddListener(func(ctx context.Context, r Reloaded) {
        router.Apply(r.Values)
    }, applyKey)
}

// SwapApplier hot-replaces the apply handler in place — e.g. to point it at a new
// router instance — without disturbing any other ConfigReloaded subscriber.
func SwapApplier(newRouter *Router) {
    // Remove first: the keyed dedup would reject a re-add while the old one lives.
    ConfigReloaded.RemoveListener(applyKey)
    ConfigReloaded.AddListener(func(ctx context.Context, r Reloaded) {
        newRouter.Apply(r.Values)
    }, applyKey)
}
```

The same name (`applyKey`) stays constant across the lifetime of the service while
the behavior behind it is replaced atomically from the subscribers' point of view —
every other reload subscriber (metrics, audit, feature-flag cache) is untouched.

**Contrast — the anonymous registration that cannot be undone:**

```go
// ❌ No key, no handle: this listener can never be individually removed or replaced.
AccountChanged.AddListener(func(ctx context.Context, a Account) { /* ... */ })
// Unmount has nothing to target. The choice is: leak it forever, or Reset() everyone.
```

The symptom in production: a component that re-subscribes on every reconnect slowly
accumulates duplicate anonymous listeners; emit work multiplies, and captured objects
never get collected — a leak with no clean fix short of `Reset()`. (If the listener
truly needs no name, `AddListenerWithCancel` would at least have returned a removal
handle; a plain anonymous `AddListener` returns only a count.)

## Variations

- **Register-if-absent.** Use `HasKey(key)` to decide before adding, instead
  of relying on the `-1` return — clearer intent for "ensure exactly one."
- **Replace-only-if-present.** Check `HasKey` before a swap so you don't accidentally
  *create* a subscriber when you meant to update an existing one.
- **Instance-scoped keys.** Embed an entity id in the key (`"usage-view/acct-42"`) so
  many short-lived instances coexist on one signal, each removable on its own.
- **Self-keying subscribers.** Have each component derive its key deterministically
  from its identity, so registration and teardown never need to share state beyond the
  identity itself.

## Known Uses

- **DOM `addEventListener` / `removeEventListener`** — removal requires the *same*
  handle; keyed subscription is the named-handle equivalent that sidesteps Go's
  inability to compare functions.
- **Node.js `EventEmitter`** — listeners are tracked so individual ones can be removed;
  named handlers are the idiomatic way to make that possible.
- **Kubernetes informers / client-go** — event handlers are registered with
  registration handles so they can be removed deterministically on shutdown.
- **Message-broker consumer groups / subscription IDs** — a named subscription is the
  unit you pause, resume, or cancel; the name is the handle.

## Related Patterns

- **[One-Shot Subscription](one-shot-subscription.md)** — for a listener that removes
  itself after a single fire; a keyed `AddOnce` (`AddOnce(h, key)`) combines
  one-shot semantics with a key.
- **[Subscription Teardown](subscription-teardown.md)** — the lifecycle discipline that
  *uses* keys (and `Reset`, and the `WithCancel` cancellers) to remove listeners and
  avoid leaks. Keys make *named* targeted teardown possible; the cancellers cover the
  anonymous case.
- **[Shared Event Registry](../architectural/shared-event-registry.md)** — where keyed,
  namespaced subscriptions matter most: many independent packages subscribe to one
  signal and must not step on each other.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — orthogonal but
  often combined: keys manage *who* listens; bounded concurrency manages *how many run
  at once*.
