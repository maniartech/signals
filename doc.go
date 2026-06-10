// Package signals is an in-process, type-safe event system for Go: a generic
// publish/subscribe primitive for decoupled communication between components of
// the same process. It is not a network message bus — there are no brokers,
// sockets, or serialization.
//
// # Signal types
//
// Two implementations share one interface, Signal[T]:
//
//   - SyncSignal[T] (signals.NewSync) invokes listeners sequentially in the
//     caller's goroutine. Emit is best-effort (an error-returning listener's
//     error is routed to OnError sinks and the chain continues); TryEmit stops
//     at the first error or canceled context and returns it — transactional.
//   - AsyncSignal[T] (signals.New) is fire-and-forget: Emit snapshots the
//     listeners, spawns a dispatcher, and returns immediately, running each
//     listener in its own goroutine. TryEmit runs all listeners concurrently,
//     waits for every one, and returns errors.Join of all failures. An optional
//     SignalOptions.MaxConcurrent installs a counting-semaphore bound (a safety
//     valve that parks excess handlers — nothing is dropped — not a worker pool
//     and not a throughput optimization); dispatch is unbounded by default.
//
// The public surface is symmetric across both types and the Signal[T] interface,
// and that symmetry is enforced at compile time (see the assertions in new.go).
//
// # Internals
//
// The listener set is stored as an immutable slice behind an atomic.Pointer.
// Reads (every Emit/TryEmit) are a single atomic load with no lock, no per-emit
// snapshot copy, and no allocation; writes (AddListener/RemoveListener/Reset)
// serialize on a write mutex, build a fresh slice, and atomically swap it in.
// This is "lock-free reads, locked writes" copy-on-write. There is no sync.Pool,
// no RWMutex, and no background worker pool.
//
// # Concurrency and memory-model contract
//
// These are the normative public guarantees; each is backed by a committed
// concurrency test. A mission-critical caller may rely on them directly.
//
//   - Happens-before: a completed AddListener/AddListenerWithErr/AddOnce/
//     AddOnceWithErr/RemoveListener/Reset happens-before any emit that begins
//     afterward and observes it. The library synchronizes only its listener
//     set — payload contents and listener-captured state are the caller's
//     responsibility.
//   - Readers never block: Emit/TryEmit reads are lock-free (a single atomic
//     load) and never block on a concurrent writer — a liveness guarantee, not
//     merely a performance note. Writers are strictly serialized; the slice swap
//     and keyed-map update are jointly consistent (no lost updates).
//   - Ordering: a SyncSignal invokes listeners in the current slice order, which
//     equals registration order only until the first RemoveListener (removal uses
//     swap-remove and may reorder). AsyncSignal listener execution order is
//     unspecified; TryEmit's joined error, however, is in deterministic
//     registration order.
//   - Reentrancy and visibility: a listener may safely Add/Remove/Reset/Emit on
//     the same signal during its own invocation. The in-flight emission iterates
//     the previously published immutable snapshot, so such mutations affect only
//     subsequent emissions, never the current one. (Exception: a reentrant
//     TryEmit on a bounded AsyncSignal can self-deadlock if it exhausts the
//     semaphore — size MaxConcurrent accordingly.)
//   - Nil inputs: a nil listener passed to any Add* method panics at the call
//     site (fail-fast, never a deferred background nil-deref). A nil context is
//     permitted and means "no cancellation / no deadline"; context.Background()
//     is preferred. SetPanicHandler(nil) and a nil OnError sink discard. A
//     negative MaxConcurrent is treated as unset (unbounded).
//   - Payload opacity: payloads of type T are never inspected; the zero value
//     (including a nil pointer or interface) is valid and delivered unmodified.
//   - Key contract: an absent or empty-string key means unkeyed. Keys() never
//     contains the empty string or internally generated keys; HasKey("") is
//     always false; unkeyed listeners are invisible to all key-based APIs. The
//     int returned by the Add* methods is the subscriber count (or -1 on a
//     duplicate key) — it is not a positional handle, and removal is by key only.
//
// # Panics
//
// A panic in an AsyncSignal listener is recovered on its handler goroutine and
// reported to the process-global handler configured via SetPanicHandler (the
// default logs via the standard library log package); it cannot crash the
// process or stop other listeners. A panic in a SyncSignal listener propagates
// to the Emit/TryEmit caller, like any ordinary sequential call.
package signals
