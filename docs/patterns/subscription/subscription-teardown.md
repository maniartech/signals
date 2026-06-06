# Subscription Teardown

**Family:** Subscription Lifecycle
· **Also Known As:** Cleanup, Lifecycle Management, Leak Avoidance
· **Status:** ✅ shipped (`Reset`, `RemoveListener`); relies on the v1.4
  semaphore-based async design for the "no `Close()` needed" guarantee

## Intent

Cleanly **remove listeners and reclaim resources** for dynamically-created signals,
so that per-session, per-request, or per-widget event wiring does not accumulate into
a **memory or goroutine leak** over the life of the process.

## Motivation

Consider a chat server that creates one `signals.AsyncSignal[Message]` **per active
session** to fan a user's messages out to their open widgets — a typing indicator, an
unread badge, a notification toaster. Sessions come and go all day. The wiring looks
innocent:

```go
type Session struct {
    Messages *signals.AsyncSignal[Message]
}

func newSession(u *User) *Session {
    s := &Session{Messages: signals.New[Message]()}
    s.Messages.AddListener(func(ctx context.Context, m Message) {
        u.Inbox.Append(m) // closure captures *User — a large object graph
    }, "inbox")
    s.Messages.AddListener(typingIndicator(u), "typing")
    return s
}
```

The naive worry, coming from channel-and-goroutine event systems, is: *"each signal
must own a background goroutine to dispatch; if I don't `Close()` it on logout, that
goroutine leaks forever."* Developers carry that fear over and start hunting for a
`Close()` method to call on `logout`. There **isn't one** — and the fear is misplaced.

Here is the actual v1.4 design fact that resolves it: **async dispatch uses a counting
semaphore and spawns work only while an emit is in flight — there are no persistent
background goroutines.** An **idle** signal (one not currently emitting) holds **zero**
running goroutines. So a session signal that no one is emitting on is just an ordinary
heap object: once nothing references it, the **garbage collector reclaims it**. There
is no `Close()` to forget and no goroutine leak from idle signals. That entire class of
bug does not exist here.

The leak that **does** remain is subtler and is about **listener closures**, not
goroutines. Look again at the `"inbox"` listener: its closure **captures `*User`** — a
large object graph. As long as that listener is registered on a signal that is still
reachable, the closure keeps `*User` alive. If the session signal is itself reachable
from some long-lived registry, **the user can't be collected** even after logout. The
fix is to **remove the listeners you no longer need**:

```go
func (s *Session) Close() {
    s.Messages.Reset() // drop every listener; releases all captured closures at once
}
```

`Reset()` removes all listeners in one call, dropping their captured references so the
`*User` graph becomes collectable. If you only want to detach one widget, remove it by
key. Either way, teardown is about **letting the listeners (and what they capture) go**,
not about closing a signal that was never holding a goroutine in the first place.

## Applicability

**Use this pattern when:**

- You create signals **dynamically** — per session, per request, per connection, per
  widget — and need to release them when their scope ends.
- A listener's **closure captures a large or sensitive object** (`*User`, a buffer, a
  DB handle) that must become collectable when the subscriber goes away.
- You want to **reuse** a signal after clearing its current subscribers (`Reset` then
  re-subscribe).
- A long-lived signal accumulates **transient subscribers** (widgets, plugins) that
  must be removed individually as they come and go.

**Avoid it (or prefer another pattern) when:**

- The signal and all its listeners live for the **whole process lifetime** — there is
  nothing to tear down; teardown adds no value.
- A listener should remove itself **after one fire** → use
  [One-Shot Subscription](one-shot-subscription.md), which is self-teardown.
- You only need to remove **one named** listener and the rest stay → that is a targeted
  `RemoveListener` (see [Keyed Subscription](keyed-subscription.md)), the lighter half
  of this pattern.

## Structure

```
  Dynamic scope (session / request / widget)
  ┌─────────────────────────────────────────────────────────┐
  │  signal := signals.New[Message]()                        │
  │     ├─ AddListener(inboxHandler,  "inbox")  ── captures ─▶ *User (large)
  │     └─ AddListener(typingHandler, "typing") ── captures ─▶ widget
  │                                                           │
  │   ...emits happen while the scope is live...             │
  │   (each emit borrows a semaphore slot; NO standing       │
  │    background goroutine — idle signal = 0 goroutines)     │
  │                                                           │
  │  scope ends ─▶ teardown:                                 │
  │     ├─ RemoveListener("typing")   ── drops one closure   │
  │     └─ Reset()                    ── drops ALL closures   │
  └─────────────────────────────────────────────────────────┘
                              │
                              ▼
   no listeners hold captured refs  ▶  *User collectable
   nothing references the signal     ▶  signal itself GC'd  (no Close() needed)
```

## Participants

| Participant | Responsibility |
|-------------|----------------|
| **Owner / Scope** | The session/request/widget that creates the signal and is responsible for its teardown |
| **Dynamic Signal** | The per-scope `AsyncSignal`/`SyncSignal`; holds the listener table |
| **Listener closure** | The handler; may capture large objects that stay alive while it's registered |
| **`RemoveListener(key)`** | Detaches **one** listener, releasing its captured references |
| **`Reset()`** | Detaches **all** listeners at once; makes the signal safe to reuse |
| **Counting semaphore** (v1.4) | Bounds in-flight async work; exists only during emits — no standing goroutine |
| **Garbage collector** | Reclaims the idle signal and freed closures once unreferenced |

## Collaborations

1. The owner creates a signal within a dynamic scope and registers listeners,
   **keying** any it intends to remove individually.
2. During the scope's life, emits run listeners. Async dispatch borrows a semaphore
   slot **per in-flight emit** and releases it on completion — **no goroutine persists
   between emits.**
3. When the scope ends, the owner performs teardown:
   - `RemoveListener(key)` to detach a single subscriber (e.g. one closed widget), or
   - `Reset()` to detach **all** subscribers at once.
4. Removing a listener drops the only reference the signal held to that closure; the
   closure — and anything it captured — becomes eligible for collection.
5. Once nothing references the signal itself, the **idle** signal (holding no
   goroutines) is garbage-collected like any other object. No `Close()` is required.
6. If the owner intends to **reuse** the signal, `Reset()` leaves it in a clean,
   listener-free state ready for fresh subscriptions.

## Consequences

**Benefits**

- ✓ **No goroutine leak from idle signals.** The semaphore design means an idle signal
  owns zero goroutines, so forgetting a (non-existent) `Close()` cannot leak one.
- ✓ **Closure memory is reclaimable.** Removing listeners releases their captured
  objects, so per-scope `*User`/buffer/handle graphs don't pile up.
- ✓ **Reusable signals.** `Reset()` returns a signal to a pristine state for a new set
  of subscribers without reallocating it.
- ✓ **Simple mental model.** "Remove listeners you no longer need; the signal itself
  takes care of itself." No lifecycle ceremony.

**Liabilities**

- ✗ **The closure-capture leak is real and silent.** A never-removed listener keeps its
  captured memory alive indefinitely. The library prevents the *goroutine* leak; you
  must still prevent the *closure* leak by removing listeners.
- ✗ **Teardown is your responsibility at the right moment.** `Reset`/`RemoveListener`
  must be wired to the scope's end (logout, request done, widget close). Miss the hook
  and the closures leak.
- ✗ **`Reset()` is all-or-nothing.** It drops *every* listener; if some should survive,
  use targeted `RemoveListener` instead.
- ✗ **Teardown vs in-flight emits.** Removing a listener stops *future* dispatch to it;
  an emit already running it will finish. Don't assume removal instantly silences work
  already in progress.

## Implementation

1. **There is no `Close()` — and that's by design, not an omission.** The v1.4 async
   engine uses a **counting semaphore** to bound concurrent listeners and spawns
   worker goroutines **only for the duration of in-flight emits**. Between emits an
   idle signal has **no running goroutines**. Consequently there is nothing to "close":
   an unreferenced idle signal is collected by the GC like any struct. Do not go looking
   for a teardown method on the signal itself; teardown is about its *listeners*.

2. **The remaining leak is closure capture — guard it deliberately.** The one thing the
   runtime cannot clean up for you is a listener you never removed. While registered, it
   pins everything its closure captured (`*User`, buffers, connections). For any listener
   whose lifetime is shorter than the signal's, **remove it when its owner goes away.**
   This is the single discipline that makes the pattern matter.

3. **Key anything you'll remove individually.** `RemoveListener` is by key; an anonymous
   listener can't be detached without `Reset()`. If a subscriber has its own lifecycle,
   give it a key at registration — see [Keyed Subscription](keyed-subscription.md).

4. **`Reset()` for whole-scope teardown; `RemoveListener` for surgical teardown.** When
   a session/request ends, `Reset()` clears everything in one call. When a single widget
   closes but the session lives on, remove just that widget's key. Pick the granularity
   that matches what's ending.

5. **`Reset()` makes a signal safe to reuse.** After `Reset()` the signal is empty and
   valid; you can re-subscribe and keep emitting. Reuse avoids re-allocating a signal
   for a recurring scope, and (since the zero value is usable) keeps construction
   cheap either way.

6. **Mind in-flight emits during teardown.** Removal affects *future* dispatch. A
   listener currently executing as part of an in-flight async emit will run to
   completion even after `RemoveListener`/`Reset`. If you must ensure no listener is
   running before releasing a captured resource, drain/quiesce emits first (e.g. stop
   producing and let outstanding `EmitAndWait` calls return) — see
   [Bounded Concurrency](../flow-control/bounded-concurrency.md) for how in-flight work
   is bounded.

7. **Tie teardown to the scope's lifecycle hook.** Put `Reset()`/`RemoveListener` in the
   exact place the scope ends — `defer session.Close()`, an HTTP middleware's cleanup,
   a widget's `OnUnmount`. A teardown that isn't wired to a hook is a teardown that
   doesn't happen.

## Sample Code

### Structural Example (how the pieces fit)

A minimal skeleton that maps one-to-one onto the **Participants** and the
**Structure** diagram above — the *Owner/Scope* that creates a dynamic *Signal*, the
*listener closures* that capture large objects, and the two teardown calls
(`RemoveListener` for one, `Reset` for all). Note what is **not** here: there is no
`Close()` on the signal, because an idle signal holds no goroutines. Read this first;
the practical examples then apply it to real problems.

```go
// 1. OWNER/SCOPE creates a per-scope SIGNAL (session/request/widget).
sig := signals.New[Event]() // dynamic: lives only as long as this scope

// 2. LISTENER CLOSURES — these CAPTURE the real leak risk. While registered, the
//    closure pins everything it closes over (here, a large *Subject).
sig.AddListener(func(ctx context.Context, e Event) {
    subject.Handle(e) // captures *subject — stays alive while this listener lives
}, "handler-a")
sig.AddListener(func(ctx context.Context, e Event) {
    subject.Track(e)
}, "handler-b")

// ...emits run while the scope is live. Async borrows a semaphore slot PER emit
//    and releases it on completion — NO standing background goroutine exists.

// 3. TEARDOWN — surgical: drop ONE closure when its owner (e.g. a widget) goes away.
sig.RemoveListener("handler-b") // releases only handler-b's captured refs

// 4. TEARDOWN — whole-scope: drop ALL closures at once on scope end (logout/request).
sig.Reset() // releases every captured ref; signal is now empty AND reusable

//   after teardown:
//     no listener pins *subject  → *subject becomes collectable
//     nothing references sig     → idle signal is GC'd  (NO Close() needed)
```

The teardown calls (steps 3–4) are the heart of the pattern: the runtime reclaims the
*goroutines* and the idle *signal* for you, but only **you** can release the *closure
captures* by removing the listeners that hold them.

### Practical Example 1 — Per-request signal cleaned up at request end

A handler builds a short-lived signal to fan a request's progress out to a few
in-request observers (a tracing span, a metrics recorder). Each observer's closure
captures the request-scoped context and buffers; a `defer Reset()` guarantees they are
released when the request ends, even on the error path.

```go
package api

import (
    "context"
    "net/http"

    "github.com/maniartech/signals"
)

type Progress struct {
    Stage string
    Bytes int64
}

func Handler(w http.ResponseWriter, r *http.Request) {
    // Per-request signal, created dynamically. No Close() — it's just a heap object.
    progress := signals.New[Progress]()

    // These closures capture request-scoped state (span, buffers). They must be
    // released when the request ends, or they outlive the request they belong to.
    span := tracer.StartSpan(r.Context(), "request")
    progress.AddListener(func(ctx context.Context, p Progress) {
        span.Annotate(p.Stage) // captures *span
    }, "trace")
    progress.AddListener(func(ctx context.Context, p Progress) {
        metrics.RecordStage(p.Stage, p.Bytes)
    }, "metrics")

    // defer guarantees teardown on every exit path, including panics/errors.
    defer progress.Reset() // drops both closures at once; *span etc. now collectable

    serve(r.Context(), progress, w, r) // emits Progress as the work advances
}
```

Because teardown is wired to the request's lifecycle via `defer`, no per-request
closure survives the request — heap usage stays flat across millions of requests
instead of growing with the number ever served.

### Practical Example 2 — Long-lived service that rotates a set of listeners

A long-running feature-flag service keeps a single durable signal but periodically
**rotates** the set of subscribers when its rule-set reloads. The old closures capture
the previous rule snapshot; if they are not removed on each rotation, every reload
leaks a full snapshot. `Reset()` then re-subscribe is the rotation primitive.

```go
package flags

import (
    "context"
    "sync"

    "github.com/maniartech/signals"
)

type Evaluated struct {
    Flag    string
    Subject string
}

// Durable: lives for the whole process. But its SUBSCRIBERS rotate on each reload.
var Decisions = signals.New[Evaluated]()

var mu sync.Mutex // serialize rotation against itself

// Rotate swaps in a fresh set of listeners bound to the new rule snapshot. Reset()
// drops the previous closures (and the old snapshot they captured) before the new
// ones are attached — without it, every reload would leak a snapshot.
func Rotate(snapshot *RuleSnapshot) {
    mu.Lock()
    defer mu.Unlock()

    Decisions.Reset() // release ALL prior closures → old *RuleSnapshot collectable

    // Re-subscribe against the new snapshot; keyed so they remain individually
    // addressable if a single sink needs detaching between rotations.
    Decisions.AddListener(func(ctx context.Context, e Evaluated) {
        snapshot.Audit(e) // captures the NEW snapshot only
    }, "flags/audit")
    Decisions.AddListener(func(ctx context.Context, e Evaluated) {
        snapshot.Sample(e)
    }, "flags/sampler")
}
```

The signal itself is never torn down — only its listener *set* rotates. Each `Reset()`
is the moment the previous snapshot's closures are released, so memory tracks the
*current* rule-set size rather than accumulating one snapshot per reload over the
service's uptime.

**Contrast — the closure leak that the runtime will NOT save you from:**

```go
// ❌ Per-session listeners that are never removed.
func startSession(u *User) {
    sig := registry.SignalFor(u.ID) // long-lived, shared registry keeps sig reachable
    sig.AddListener(func(ctx context.Context, m Message) {
        u.Inbox.Append(m) // captures *User
    }) // anonymous AND never removed
    // On logout: nothing detaches it. The signal stays reachable via the registry,
    // so the listener — and the whole *User graph — never gets collected.
}
```

The symptom in production: memory grows roughly linearly with the number of sessions
ever created (not currently active). It is **not** a goroutine leak — goroutine count
stays flat — which is exactly why it's often misdiagnosed. A heap profile shows a
mounting population of retained `*User` objects pinned by live listener closures.

## Variations

- **Whole-scope `Reset()`.** The session/request ends; drop everything in one call.
- **Surgical `RemoveListener`.** One subscriber's lifecycle ends inside a longer-lived
  scope; remove just its key (see [Keyed Subscription](keyed-subscription.md)).
- **`defer`-based teardown.** For request-scoped signals, `defer sig.Reset()` (or a
  scope `Close`) guarantees cleanup even on the error path.
- **Reset-and-reuse.** Recurring scopes reuse one signal object via `Reset()` between
  uses, avoiding repeated allocation.
- **Self-teardown.** Where a listener should clean *itself* up after a single fire,
  [One-Shot Subscription](one-shot-subscription.md) removes the teardown burden entirely.

## Known Uses

- **`context.Context` cancellation** — the canonical Go idiom for releasing scope-bound
  resources at the right moment; teardown wired to a lifecycle hook is the same shape.
- **`defer file.Close()` / `defer conn.Close()`** — scope-bound cleanup tied to the
  end of a function; per-request `Reset()` is the event-system analogue.
- **React `useEffect` cleanup** — effects return a teardown that removes subscriptions
  on unmount; the same subscribe-on-mount / remove-on-unmount discipline.
- **Kubernetes informer shutdown / client-go** — handler registrations removed on
  controller stop so dynamically-created watches don't leak.
- **RxJS `Subscription.unsubscribe()`** — releasing a subscription frees the operator
  chain and whatever it retained; the closure-capture concern is identical.

## Related Patterns

- **[Keyed Subscription](keyed-subscription.md)** — the prerequisite for *targeted*
  teardown: `RemoveListener` works by key. Keys are what make surgical removal possible.
- **[One-Shot Subscription](one-shot-subscription.md)** — self-teardown for the
  single-fire case; removes the listener for you so there's nothing to clean up.
- **[Shared Event Registry](../architectural/shared-event-registry.md)** — the place
  where teardown discipline matters most: long-lived shared signals are exactly where a
  forgotten transient listener pins memory indefinitely.
- **[Bounded Concurrency](../flow-control/bounded-concurrency.md)** — explains the
  semaphore-based async engine that gives idle signals zero goroutines (the reason no
  `Close()` is needed) and bounds the in-flight work you may need to drain before
  releasing a captured resource.
