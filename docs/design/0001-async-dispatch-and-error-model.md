# ADR 0001 — Async dispatch model & async error handling (v1.4 Phase 4)

**Status:** Accepted / **Locked** · Supersedes the FR-7 "performance lever" framing
in `v1.4-requirements.md` (reframed below). **Drives:** Phase 4 implementation.

This record captures the ratified design for the async worker pool (FR-7) and the
async error model (FR-5 follow-on). It is the authoritative build spec; where it
disagrees with the older prose in `v1.4-requirements.md`, **this document wins**.

---

## Context

`AsyncSignal.Emit` is fire-and-forget. Today it spawns **one goroutine per listener
per emit**, with no limit. Under sustained overload (a fast emitter with slow
listeners) goroutines accumulate without bound — the classic event-system meltdown.
FR-7 originally proposed a worker pool as a *performance lever*. During design we
concluded the real value is **bounded, predictable resource use** (a safety valve),
not raw throughput, and we reframe it accordingly (honesty rule: do not claim a
number the design does not produce).

---

## The locked design

### Dispatch shape

```go
// Emit — fire-and-forget. The caller spawns ONE goroutine (the dispatcher) and
// returns immediately. Moving the dispatch loop (and any slot parking) into the
// background is what keeps Emit non-blocking even when a pool is configured.
func (s *AsyncSignal[T]) Emit(ctx context.Context, payload T) {
	go s.dispatch(ctx, payload, nil)
}

// EmitAndWait — same dispatcher, run on the caller's goroutine, then wait for all
// handlers. Obeys the same concurrency bound.
func (s *AsyncSignal[T]) EmitAndWait(ctx context.Context, payload T) {
	var wg sync.WaitGroup
	s.dispatch(ctx, payload, &wg)
	wg.Wait()
}

// EmitAndWaitErr — like EmitAndWait, but collects every handler error via
// errors.Join and returns it.
func (s *AsyncSignal[T]) EmitAndWaitErr(ctx context.Context, payload T) error { /* … */ }

func (s *AsyncSignal[T]) dispatch(ctx context.Context, payload T, wg *sync.WaitGroup) {
	if ctx != nil && ctx.Err() != nil {
		return // canceled context ⇒ skip all listeners (existing semantics)
	}
	for _, kl := range s.load() { // lock-free read of the immutable slice
		if s.slots != nil {
			s.slots <- struct{}{} // acquire a slot; parks ONLY when all are in use
		}
		if wg != nil {
			wg.Add(1)
		}
		go func(kl keyedListener[T]) { // EACH handler runs independently / concurrently
			if wg != nil {
				defer wg.Done()
			}
			if s.slots != nil {
				defer func() { <-s.slots }()
			}
			defer recoverToPanicHandler() // one panic can't kill siblings
			invoke(kl, ctx, payload)      // routes errors via OnError (see below)
		}(kl)
	}
}
```

### Struct & configuration

```go
type AsyncSignal[T any] struct {
	baseSignal *BaseSignal[T]
	baseOnce   sync.Once

	// slots is a counting semaphore bounding how many handler goroutines run at
	// once. nil ⇒ unbounded (a goroutine per handler, like earlier releases).
	slots chan struct{}

	onErr atomic.Pointer[[]func(context.Context, error)] // per-signal error sinks
}

func New[T any]() *AsyncSignal[T] { // unbounded — safe correctness default
	return &AsyncSignal[T]{baseSignal: NewBaseSignal[T](nil)}
}

func NewWithOptions[T any](opts *SignalOptions) *AsyncSignal[T] {
	s := &AsyncSignal[T]{baseSignal: NewBaseSignal[T](opts)}
	if opts != nil && opts.WorkerPoolSize > 0 {
		s.slots = make(chan struct{}, opts.WorkerPoolSize)
	}
	return s
}

// DefaultWorkerPoolSize is the RECOMMENDED bound (2×NumCPU) for callers who want
// bounding without choosing a number. NOT auto-applied — unset stays unbounded.
func DefaultWorkerPoolSize() int { return 2 * runtime.NumCPU() }

func recoverToPanicHandler() {
	if r := recover(); r != nil {
		handleListenerPanic(r) // → the SetPanicHandler callback (global)
	}
}
```

---

## Decisions

### A — Worker pool

- **A1. Mechanism: counting semaphore** (`slots chan struct{}`), not a persistent
  worker pool. No background workers ⇒ **no `Close()`, no lifecycle, GC-friendly.**
- **A2. No drop by default.** When the bound is hit, excess dispatch **parks**
  (cheaply) until a slot frees; nothing is dropped and the caller is never blocked
  (the parking is in the background `go dispatch()` goroutine). Explicit drop/error
  overflow policies may be added later as an opt-in, not in v1.4.
- **A3. `EmitAndWait`/`EmitAndWaitErr` obey the same bound** (they share `dispatch`).
- **A4. Default is UNBOUNDED** (`New()` ⇒ `slots == nil`). Bounding is an informed
  opt-in via `WorkerPoolSize > 0`; `DefaultWorkerPoolSize()` (2×NumCPU) is the
  recommended value but is **not** applied automatically.
- **A5. Educate, don't silently protect.** Two risks are documented loudly next to
  `WorkerPoolSize` rather than defaulted away:
  1. **Starvation:** a bound can starve *long-running* listeners — only N run, the
     rest never start. (This is the decisive reason the default is unbounded.)
  2. **Reentrancy/self-deadlock:** a handler that `EmitAndWait`s on its *own*
     bounded signal can deadlock by holding a slot while waiting for one.

### B — Async error model

- **B1. `OnError(func(ctx, error))` per signal, multiple allowed** (additive sinks).
  Errors returned by async listeners on the fire-and-forget `Emit` path are routed
  to every registered `OnError` callback — the way panics route to `SetPanicHandler`.
- **B2. `EmitAndWaitErr(ctx, payload) error`** — concurrent handlers, waits, returns
  all failures combined via `errors.Join`.
- **B3. Fix the silent gap + promote `AddListenerWithErr`.** Async `dispatch`
  currently ignores error-returning listeners entirely. v1.4 wires them in (routed
  per B1/B2) and promotes `AddListenerWithErr` to the `Signal` interface, implemented
  on async. The error-returning listener is *how a handler reports failure*; without
  it `OnError`/`EmitAndWaitErr` would have no input.
- **B4. Panics stay global** (`SetPanicHandler`), **errors are per-signal**
  (`OnError`). Expected failures → return an `error`; unexpected bugs → `panic`.

---

## Goroutine accounting (honest)

- **Caller's `Emit`:** spawns **one** goroutine (the dispatcher) and returns — O(1)
  on the hot path (today the caller itself loops and spawns N).
- **Per emit total:** 1 dispatcher + 1 goroutine per handler. Handlers are
  **independent and concurrent** (this is real async, not sequential).
- **With a pool:** at most `WorkerPoolSize` handler goroutines run concurrently;
  the rest park.

## Consequences & caveats (to document, not hide)

- ✓ `Emit` never blocks the caller; the contract has no asterisk.
- ✓ Each handler runs independently — matches async expectations.
- ✓ No persistent goroutines, no `Close()`.
- ✗ **Not a throughput optimization.** It adds a dispatcher goroutine + semaphore
  ops; it will not "beat goroutine-per-listener" on a raw-speed benchmark. We
  benchmark/justify it as *bounded, predictable resource use under load*, not ns/op.
- ✗ **Bounds execution, not the backlog.** Under *sustained* overload, parked
  dispatch goroutines accumulate without a hard limit (cheap — ~2 KB, idle — but
  unbounded). A hard ceiling would require drop or caller-blocking, both declined by
  default. Documented as a known edge.

## Impact on existing docs

The pattern catalog (`docs/patterns/`) currently assumes **drop-and-count** as the
`Emit` overflow default. Reconcile to this ADR:
- `flow-control/load-shedding.md` — reframe to an **opt-in** policy (not the default).
- `flow-control/bounded-concurrency.md` — semaphore design, unbounded default, the
  starvation caveat, pool-size ≠ subscriber-count reasoning with examples.
- `flow-control/backpressure.md` — `EmitAndWait` as the lossless path.
- `reliability/async-error-routing.md`, `result-aggregation.md` — `OnError` (multiple),
  `EmitAndWaitErr`, `AddListenerWithErr` on async.
- `dispatch/fire-and-forget-dispatch.md` — one-dispatcher-goroutine model, parks
  (does not drop) under a bound by default.
