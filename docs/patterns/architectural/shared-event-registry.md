# Shared Event Registry

**Family:** Architectural
· **Also Known As:** Event Bus, Package-Level Signal Registry, Application Event Hub
· **Status:** ✅ shipped

## Intent

Declare signals as package-level variables in one shared `events` package so that
unrelated packages can coordinate through events **without importing each other**.
The publisher and every subscriber depend only on the shared registry, never on each
other's internals — making event flow the seam that decouples a monolith into
independently evolvable modules.

## Motivation

Consider a typical web application's `auth` package. When a user logs in, several
other parts of the system need to react: the `audit` package records the event, the
`cache` package warms the user's session data, and the `billing` package updates a
"last active" timestamp for seat-counting. The naive approach wires these directly:

```go
package auth

import (
    "myapp/audit"   // auth now depends on audit
    "myapp/billing" // ...and billing
    "myapp/cache"   // ...and cache
)

func Login(ctx context.Context, u User) error {
    // ...authenticate...
    audit.RecordLogin(ctx, u)        // direct call
    cache.WarmSession(ctx, u)        // direct call
    billing.TouchActivity(ctx, u)    // direct call
    return nil
}
```

This looks harmless on day one. By month six it has rotted into a problem you can
*feel* every time you touch `auth`:

- `auth` imports `audit`, `billing`, and `cache`. Every new reaction to login means
  **editing `auth`** and adding **another import**. The "login" concept now knows
  about billing seat-counting — a concern it should never have heard of.
- The import graph fans out. When `billing` later wants to read something from
  `auth`, you get an **import cycle**, and Go refuses to compile. The usual "fix" is
  a junk-drawer `internal/shared` package that everything depends on — coupling by
  another name.
- Testing `auth.Login` now drags in the real `audit`, `cache`, and `billing`
  packages (or a thicket of interface mocks). The unit under test isn't a unit
  anymore.

The root problem is **dependency direction**: the package that *detects* an event is
forced to depend on every package that *reacts* to it. A Shared Event Registry
inverts that. Both sides depend on a small, dependency-free `events` package that
owns the signal variables:

```go
package events // imports nothing from the app — only the signals library

import "github.com/maniartech/signals"

type User struct {
    ID    string
    Email string
}

// The login event, owned by no business package.
var UserLoggedIn = signals.New[User]()
```

```go
package auth

import "myapp/events" // the ONLY app package auth imports for this

func Login(ctx context.Context, u User) error {
    // ...authenticate...
    events.UserLoggedIn.Emit(ctx, u) // tell the world; don't name the listeners
    return nil
}
```

```go
package audit

import "myapp/events"

func init() {
    events.UserLoggedIn.AddListener(recordLogin, "audit/login-logger")
}
```

Now `auth` knows nothing about `audit`, `billing`, or `cache` — and never will.
Adding a fourth reaction (say, sending a security-notification email) is a *new
package* that subscribes to `events.UserLoggedIn`; **`auth` is not touched**. The
import graph is a star, not a web: every package points at `events`, and `events`
points at nobody.

## Applicability

**Use this pattern when:**

- Multiple packages must **react to the same domain event** (`auth` → `audit`,
  `cache`, `billing`) and you want to add reactions without editing the emitter.
- You are building a **modular monolith** or **plugin architecture** where modules
  should be independently buildable and testable, coordinating only through a
  published event contract.
- An **HTTP handler emits a domain event** (`OrderPlaced`, `PaymentCaptured`) that an
  open-ended set of packages consume — you want the handler decoupled from the
  consumer list.
- You want the event *contract* (payload types) to live in one reviewable place that
  both producers and consumers import.

**Avoid it (or prefer another approach) when:**

- The coordination is **cross-process or cross-machine.** This pattern is
  **in-process only** — see the boundary note below. For distributed delivery use a
  real message broker (Kafka, NATS, RabbitMQ, a cloud pub/sub).
- There is exactly **one** caller and **one** callee with a stable relationship — a
  direct function call is simpler and more navigable than an event.
- The flow is **order-sensitive across packages** and you cannot register listeners
  explicitly at startup → see Implementation note 4 and prefer a
  [Synchronous Sequential Dispatch](../dispatch/synchronous-sequential-dispatch.md)
  signal registered in a known order.

> **Boundary — this is an in-process event hub, not a distributed bus.** A Shared
> Event Registry lives entirely inside one running process. There is no network hop,
> no serialization, no durability, no delivery-across-restart, and no fan-out to
> other services. If a subscriber is in a *different process*, this pattern does not
> reach it — put a message broker between the processes and let each process keep its
> own in-process registry on its side of the wire.

## Structure

```
                         events package (depends on nobody)
                         ┌──────────────────────────────────┐
                         │  var UserLoggedIn = signals.New() │
                         │  var OrderPlaced  = signals.New() │
                         └──────────────────────────────────┘
                              ▲                 ▲       ▲
              import "events" │                 │ import "events"
                              │                 │       │
   ┌───────────────┐         │      ┌───────────┴──┐  ┌─┴────────────┐
   │  auth (pub)   │─Emit───▶│◀─Add─│ audit (sub)  │  │ cache (sub)  │
   │               │  Listener key: │ "audit/      │  │ "cache/      │
   │ events.User-  │   the registry │  login-logger"│  │  warm"       │
   │ LoggedIn.Emit │   dispatches   └──────────────┘  └──────────────┘
   └───────────────┘   to all subs

   Dependency direction: pub → events ← sub.  pub and sub NEVER import each other.
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Registry package** (`events`) | Owns the package-level signal vars and the payload types; imports no business package |
| **Payload type** | The event's data contract (`User`, `Order`), shared by both sides |
| **Signal variable** | `var X = signals.New[T]()` (async) or `signals.NewSync[T]()` (ordered) — the named coordination point |
| **Publisher package** | Imports `events`; calls `X.Emit(ctx, payload)` when the event occurs; names no subscriber |
| **Subscriber package** | Imports `events`; calls `X.AddListener(handler, "namespace/name")`; names no publisher |
| **Namespace key** | A per-package unique listener key (`"audit/login-logger"`) preventing collisions and enabling targeted removal |

## Collaborations

1. At program startup, each subscriber package registers its handler against the
   shared signal with a **namespaced key**:
   `events.UserLoggedIn.AddListener(recordLogin, "audit/login-logger")`.
2. The publisher, when the event occurs, calls
   `events.UserLoggedIn.Emit(ctx, user)`. It has no list of subscribers and no
   compile-time knowledge of who will react.
3. The registry's signal dispatches the payload to **every** registered listener,
   per the chosen signal type's semantics (async fan-out for `New`, ordered
   sequential for `NewSync`).
4. New reactions arrive as **new subscriber packages** that call `AddListener` on the
   same signal. The publisher is never modified. Removing a reaction is
   `RemoveListener("audit/login-logger")` — again without touching the publisher.

## Consequences

**Benefits**

- ✓ **Acyclic, inverted dependencies.** Publisher and subscribers both depend on
  `events`; neither depends on the other. Import cycles become structurally
  impossible for event-mediated relationships.
- ✓ **Open for extension.** Adding the Nth reaction is a new package that subscribes;
  the emitter is closed for modification (the Open/Closed Principle, enforced by the
  import graph).
- ✓ **Independent testability.** A subscriber is tested by emitting on the signal; a
  publisher is tested by asserting it emitted — neither needs the other's code.
- ✓ **One reviewable contract.** Payload types and event names live in one package,
  so the system's event vocabulary is discoverable in a single file.

**Liabilities**

- ✗ **Indirection costs navigability.** "Who handles `UserLoggedIn`?" is no longer a
  single click — it is "who calls `AddListener` on it?" Mitigate with the key
  namespace convention and a grep-friendly naming scheme.
- ✗ **In-process only.** Offers none of a broker's guarantees (durability, retries,
  cross-process delivery). Reaching for it where you need those is a category error.
- ✗ **`init()` ordering is non-deterministic across packages** (see Implementation
  note 4) — a real hazard for order-sensitive flows.
- ✗ **Global mutable state.** Package-level vars are process-wide singletons; tests
  that add listeners must `Reset()` or use distinct keys to avoid cross-test bleed.

> **Trilemma note:** this is a *structural* pattern, not a flow-control one — it does
> not by itself pick a corner of the bounded-memory / never-wait / never-lose
> trilemma. That choice is made by *which signal type* you put in the registry: an
> async `New` signal inherits fire-and-forget loss-tolerance; a `NewSync` signal
> inherits blocking ordered delivery. Choose per event (see Variations).

## Implementation

1. **Give the registry package zero business imports.** The whole value of the
   pattern is that `events` sits at the bottom of the import graph. It should import
   only `github.com/maniartech/signals` and the standard library. The moment `events`
   imports `billing`, you have reintroduced coupling and risk a cycle. Payload structs
   live *in* `events` (or in an even lower leaf package) precisely so both sides can
   share them without depending on a business package.

2. **Namespace every listener key by owning package.** Use `"<package>/<purpose>"`,
   e.g. `"audit/login-logger"`, `"cache/session-warm"`, `"billing/activity-touch"`.
   Keys are global to the signal; two packages that both register the unnamespaced
   key `"logger"` will collide — the second `AddListener` returns `-1` and **silently
   does nothing** (keyed dedup). Namespacing makes collisions impossible and makes
   `RemoveListener("audit/login-logger")` target exactly one handler.

3. **Pick `New` vs `NewSync` per event, deliberately.** Use `signals.New[T]()`
   (async, unordered, fire-and-forget) for *notifications* where reactions are
   independent and the publisher must not block — the common case. Use
   `signals.NewSync[T]()` when listeners must run in a **deterministic order** or the
   publisher must observe completion/errors via `TryEmit`. The registry can mix both:
   `UserLoggedIn` async, `OrderValidating` sync.

4. **Beware `init()`-time registration for order-sensitive flows.** Registering in a
   package's `init()` is convenient, but Go only guarantees init order *within* a
   package's import tree — the relative order in which sibling subscriber packages run
   their `init()` is **not something you should rely on**. For an async signal this is
   harmless (async makes no ordering guarantee anyway). But if you need listener A to
   run before listener B *across packages*, do **not** depend on `init()` order.
   Instead, register explicitly from a single startup function in `main` (or a
   composition-root package) in the order you want, and use a **`SyncSignal`** so that
   registration order is the execution order:

   ```go
   // package main — the composition root decides order explicitly
   func wireEvents() {
       events.OrderPlaced.AddListener(inventory.Reserve, "inventory/reserve")
       events.OrderPlaced.AddListener(billing.Charge,    "billing/charge")
       events.OrderPlaced.AddListener(shipping.Schedule, "shipping/schedule")
   }
   ```

5. **Propagate context through every emit.** Pass the request/operation `ctx` into
   `Emit`/`TryEmit` so cancellation and deadlines flow to listeners. The registry is
   the natural place where a request-scoped context fans out — see
   [Context-Scoped Emission](context-scoped-emission.md).

6. **Reset between tests.** Because the registry is global mutable state, a test that
   adds a listener must remove it (`RemoveListener(key)`) or clear the signal
   (`Reset()`) in cleanup, or later tests will see stray handlers. Prefer per-test
   unique keys plus `t.Cleanup(func(){ events.X.RemoveListener(key) })`.

7. **The zero-value works, but name your signals explicitly.** `var X
   signals.AsyncSignal[T]` is usable without a constructor (lazy init), but in a
   registry you almost always want `signals.New[T]()` / `signals.NewSync[T]()` so the
   choice of async vs sync is visible at the declaration site.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the dependency-free *Registry package*, the *Payload
type*, the *Signal variable*, a *Publisher* that names no subscriber, and a
*Subscriber* that names no publisher, joined only by a *namespaced key*. Read this
first to see the dependency direction (`pub → events ← sub`); the practical examples
then apply it to real problems.

```go
// ─── package events ── the REGISTRY: depends on nobody in the app ──────────────
package events

import "github.com/maniartech/signals"

// PAYLOAD TYPE — the contract both sides share, owned by no business package.
type Thing struct{ ID string }

// SIGNAL VARIABLE — the named coordination point. async New ⇒ fire-and-forget
// fan-out; reactions are independent and must not block the publisher.
var ThingHappened = signals.New[Thing]() // ✅

// ─── package producer ── the PUBLISHER: imports events, never a subscriber ─────
package producer

import (
    "context"

    "myapp/events"
)

func DoWork(ctx context.Context) {
    // ... the event occurs ...
    events.ThingHappened.Emit(ctx, events.Thing{ID: "42"}) // announce; name no listener
    //   └─ the registry dispatches to EVERY registered listener (async, unordered)
}

// ─── package reactor ── a SUBSCRIBER: imports events, never the publisher ──────
package reactor

import (
    "context"

    "myapp/events"
)

func init() {
    // NAMESPACED KEY "<package>/<purpose>" — globally unique, removable by name.
    // A duplicate key would return -1 and silently do nothing (keyed dedup).
    events.ThingHappened.AddListener(react, "reactor/handle-thing") // ✅
}

func react(ctx context.Context, t events.Thing) {
    handle(ctx, t.ID) // oblivious to the publisher's identity
}
```

The seam is the key idea: `producer` and `reactor` never import each other, so a new
reaction is a *new subscriber package* and the publisher is never reopened.

### Practical Example 1 — Monolith user-login fan-out

**Problem:** logging in must trigger audit logging, session cache-warming, and a
billing "last active" touch — three concerns that should not bloat the `auth`
package, must not import each other, and must not block the login response.

```go
// ─── package events ───────────────────────────────────────────────────────────
// Depends on nothing in the app. Owns the contract.
package events

import "github.com/maniartech/signals"

type User struct {
    ID    string
    Email string
}

// Async fan-out: the three reactions are independent and must not block login.
var UserLoggedIn = signals.New[User]() // ✅
```

```go
// ─── package auth (publisher) ─────────────────────────────────────────────────
package auth

import (
    "context"

    "myapp/events"
)

// Login authenticates, then announces. It imports no reacting package.
func Login(ctx context.Context, email, password string) (events.User, error) {
    u, err := verify(email, password)
    if err != nil {
        return events.User{}, err
    }
    events.UserLoggedIn.Emit(ctx, u) // fire-and-forget; auth keeps moving
    return u, nil
}
```

```go
// ─── package audit (subscriber) ───────────────────────────────────────────────
package audit

import (
    "context"

    "myapp/events"
)

func init() {
    // Namespaced key: unique across the whole app, removable by name.
    events.UserLoggedIn.AddListener(recordLogin, "audit/login-logger")
}

func recordLogin(ctx context.Context, u events.User) {
    writeAuditRow(ctx, "login", u.ID)
}
```

```go
// ─── package cache (subscriber) ───────────────────────────────────────────────
package cache

import (
    "context"

    "myapp/events"
)

func init() {
    events.UserLoggedIn.AddListener(warmSession, "cache/session-warm")
}

func warmSession(ctx context.Context, u events.User) {
    preloadProfile(ctx, u.ID)
}
```

```go
// ─── package billing (subscriber) ─────────────────────────────────────────────
package billing

import (
    "context"

    "myapp/events"
)

func init() {
    events.UserLoggedIn.AddListener(touchActivity, "billing/activity-touch")
}

func touchActivity(ctx context.Context, u events.User) {
    markSeatActive(ctx, u.ID)
}
```

Adding a fourth reaction — say, a `security` package that emails on login from a new
device — is a **new file in a new package** that calls
`events.UserLoggedIn.AddListener(notifyNewDevice, "security/new-device")`. The `auth`
package is never reopened.

**Contrast — the coupling this replaces:**

```go
// ❌ auth importing every reacting package: edits to auth on every new reaction,
//    and an import cycle the moment billing needs to read from auth.
package auth

import (
    "myapp/audit"
    "myapp/billing"
    "myapp/cache"
)

func Login(ctx context.Context, u User) error {
    audit.RecordLogin(ctx, u)
    cache.WarmSession(ctx, u)
    billing.TouchActivity(ctx, u) // ← add a 4th? edit auth again.
    return nil
}
```

### Practical Example 2 — Plugin/feature modules self-registering on a domain hub

**Problem:** a modular monolith ships optional feature modules (analytics,
recommendations, fraud screening) that each want to react to `OrderPlaced`. Each
module must be addable or removable by toggling a single blank import — without
touching the checkout package or any other module.

The registry owns the hub; each module registers itself in its own `init()`, so the
set of active reactions is exactly the set of imported modules. `main` decides which
modules exist by which it imports; the publisher stays oblivious.

```go
// ─── package events ── the shared domain-event hub ────────────────────────────
package events

import "github.com/maniartech/signals"

type Order struct {
    ID     string
    UserID string
    Total  int64 // cents
}

var OrderPlaced = signals.New[Order]() // ✅ async: independent module reactions
```

```go
// ─── package checkout (publisher) ─────────────────────────────────────────────
package checkout

import (
    "context"

    "myapp/events"
)

// PlaceOrder names no feature module. It announces and returns.
func PlaceOrder(ctx context.Context, o events.Order) error {
    if err := persist(ctx, o); err != nil {
        return err
    }
    events.OrderPlaced.Emit(ctx, o) // every imported module reacts; checkout moves on
    return nil
}
```

```go
// ─── package analytics (feature module) ───────────────────────────────────────
package analytics

import (
    "context"

    "myapp/events"
)

// Self-registration: importing this package activates the reaction.
func init() {
    events.OrderPlaced.AddListener(track, "analytics/order-funnel")
}

func track(ctx context.Context, o events.Order) {
    recordRevenue(ctx, o.UserID, o.Total)
}
```

```go
// ─── package fraud (feature module) ───────────────────────────────────────────
package fraud

import (
    "context"

    "myapp/events"
)

func init() {
    events.OrderPlaced.AddListener(screen, "fraud/order-screen")
}

func screen(ctx context.Context, o events.Order) {
    if o.Total > 100_000 {
        flagForReview(ctx, o.ID)
    }
}
```

```go
// ─── package main ── the composition root chooses which modules exist ─────────
package main

import (
    _ "myapp/analytics" // blank import: its init() self-registers on the hub
    _ "myapp/fraud"     // drop this line to ship without fraud screening
    // _ "myapp/recommend" // not imported ⇒ not active; no other file changes
)

func main() { /* ... start the server ... */ }
```

> **`init()` ordering caveat.** Module `init()` self-registration is ideal here only
> because `OrderPlaced` is **async** and unordered — the relative order in which
> sibling modules run their `init()` is not guaranteed across packages and must not
> be relied on. If these reactions had to run in a fixed order, you would switch to a
> `signals.NewSync[Order]()` signal and register explicitly from a single
> `wireEvents()` in `main` (Implementation note 4), not via scattered `init()`s. And
> as always, this hub is **in-process only**: a module living in a separate service
> is not reachable here — put a broker between the processes.

## Variations

- **Sync registry for ordered, fail-fast flows.** Declare the signal with
  `signals.NewSync[T]()` and emit with `TryEmit` when subscribers must run in a fixed
  order and the publisher must stop on the first failure (validation → reservation →
  charge). See [Transactional Emission](../reliability/transactional-emission.md).
- **Per-module sub-registries.** Instead of one giant `events` package, give each
  bounded context its own registry (`events/orders`, `events/accounts`). Lower
  contention on review and clearer ownership, at the cost of more import targets.
- **Interface-typed payloads.** Make the payload an interface when several concrete
  event shapes share one signal — though prefer one signal per concrete event type
  for type safety.
- **Explicit wiring instead of `init()`.** A `wireEvents()` composition root called
  from `main` makes registration order deterministic and visible, trading a touch of
  boilerplate for control (strongly preferred for sync/ordered registries — see
  Implementation note 4).

## Known Uses

- **Domain events in DDD / hexagonal architecture** — aggregates publish events that
  application services subscribe to, decoupling the domain from side effects.
- **The "event bus" in modular monoliths** (e.g. Shopify-style, .NET MediatR notifications, Laravel's event facade) — modules coordinate through a published event contract rather than direct calls.
- **GUI / desktop frameworks** — Qt signals/slots, .NET events, and the Observer
  pattern generally: emitters name events, not handlers.
- **The Go standard library `signal.Notify`** — a process-level registry where
  producers (the OS) and consumers (your handlers) meet at a named channel without
  knowing each other.
- **VS Code / browser extension event APIs** — a host process exposes named events
  (`onDidChangeActiveEditor`); plugins subscribe without the host knowing they exist.

## Related Patterns

- **[Context-Scoped Emission](context-scoped-emission.md)** — the companion
  architectural pattern; a registry is where a request-scoped context fans out, so
  cancellation and deadlines reach every subscriber.
- **[Synchronous Sequential Dispatch](../dispatch/synchronous-sequential-dispatch.md)**
  — what a `NewSync` registry signal delivers: ordered, blocking fan-out when
  cross-package order matters.
- **[Fire-and-Forget Dispatch](../dispatch/fire-and-forget-dispatch.md)** — what a
  `New` registry signal delivers: non-blocking notification fan-out, the default for
  independent reactions.
- **[Transactional Emission](../reliability/transactional-emission.md)** — pair with
  a sync registry signal and `TryEmit` when the fanned-out steps form a sequence that
  must stop on the first failure.
- **[Keyed Subscription](../subscription/keyed-subscription.md)** — the namespaced-key
  discipline that keeps registry subscribers collision-free and individually
  removable.
