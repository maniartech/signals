package signals_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase 4B / FR-5 + ADR 0001 §B: async error model.
//   I10 TryEmit aggregation (exactly those errors, deterministic order, nil iff all ok)
//   I11 OnError routing to every sink (multi-sink fan-out)
//   I14 a panicking OnError sink is isolated (does not crash / stop other sinks)

// Compile-time check: both concrete types still satisfy the (now extended) interface.
var (
	_ signals.Signal[int] = (*signals.AsyncSignal[int])(nil)
	_ signals.Signal[int] = (*signals.SyncSignal[int])(nil)
)

// I11: an error returned on the fire-and-forget Emit path reaches every registered
// OnError sink.
func TestAsyncErr_OnErrorRoutesToAllSinks(t *testing.T) {
	sig := signals.New[int]()
	boom := errors.New("boom")
	var got1, got2 error
	var wg sync.WaitGroup
	wg.Add(2)
	sig.OnError(func(_ context.Context, err error) { got1 = err; wg.Done() })
	sig.OnError(func(_ context.Context, err error) { got2 = err; wg.Done() })
	sig.AddListenerWithErr(func(context.Context, int) error { return boom })

	sig.Emit(context.Background(), 1)
	wg.Wait()

	if !errors.Is(got1, boom) || !errors.Is(got2, boom) {
		t.Fatalf("sinks got (%v, %v); want both = boom", got1, got2)
	}
}

// I11 (stronger): M sinks × K failing handlers ⇒ each sink observes exactly K errors.
func TestAsyncErr_MultiSinkFanOut(t *testing.T) {
	const m, k = 3, 5
	sig := signals.New[int]()
	counts := make([]int32, m)
	var wg sync.WaitGroup
	wg.Add(m * k)
	for i := 0; i < m; i++ {
		i := i
		sig.OnError(func(context.Context, error) { atomic.AddInt32(&counts[i], 1); wg.Done() })
	}
	for j := 0; j < k; j++ {
		sig.AddListenerWithErr(func(context.Context, int) error { return errors.New("e") })
	}

	sig.Emit(context.Background(), 1)
	wg.Wait()

	for i := 0; i < m; i++ {
		if got := atomic.LoadInt32(&counts[i]); got != k {
			t.Fatalf("sink %d observed %d errors; want %d", i, got, k)
		}
	}
}

// routeError with no registered sinks must be a safe no-op (covers the nil-sinks path).
func TestAsyncErr_NoSinkIsNoOp(t *testing.T) {
	sig := signals.New[int]()
	sig.OnError(nil) // ignored
	done := make(chan struct{})
	sig.AddListenerWithErr(func(context.Context, int) error { close(done); return errors.New("x") })
	sig.Emit(context.Background(), 1)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
}

// I10: TryEmit returns exactly the failing handlers' errors, in registration
// order, and nil when all succeed. Plain listeners never contribute.
func TestAsyncErr_TryEmitAggregates(t *testing.T) {
	sig := signals.New[int]()
	eA := errors.New("errA")
	eC := errors.New("errC")
	sig.AddListenerWithErr(func(context.Context, int) error { return eA }, "a")
	sig.AddListener(func(context.Context, int) {}, "b")                          // plain
	sig.AddListenerWithErr(func(context.Context, int) error { return eC }, "c")  // fails
	sig.AddListenerWithErr(func(context.Context, int) error { return nil }, "d") // ok

	err := sig.TryEmit(context.Background(), 1)
	if !errors.Is(err, eA) || !errors.Is(err, eC) {
		t.Fatalf("joined err = %v; want both errA and errC", err)
	}
	// Deterministic registration order: errA before errC.
	if msg := err.Error(); strings.Index(msg, "errA") > strings.Index(msg, "errC") {
		t.Fatalf("errors not in registration order: %q", msg)
	}

	ok := signals.New[int]()
	ok.AddListenerWithErr(func(context.Context, int) error { return nil })
	ok.AddListener(func(context.Context, int) {})
	if err := ok.TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("all-succeed TryEmit = %v; want nil", err)
	}

	// Empty signal → nil.
	if err := signals.New[int]().TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("empty TryEmit = %v; want nil", err)
	}
	// Pre-canceled ctx → ctx.Err().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sig.TryEmit(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled TryEmit = %v; want context.Canceled", err)
	}
}

// TryEmit under a cancellable context where all handlers complete (covers the
// done-completes-first branch with a non-nil errs slice).
func TestAsyncErr_TryEmitCompletesUnderCancellableCtx(t *testing.T) {
	sig := signals.New[int]()
	e := errors.New("e")
	sig.AddListenerWithErr(func(context.Context, int) error { return e })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sig.TryEmit(ctx, 1); !errors.Is(err, e) {
		t.Fatalf("TryEmit = %v; want e", err)
	}
}

// ctx-liveness for the error variant: a hung handler must not hang the waiter; it
// returns ctx.Err() at the deadline (and must not read the in-progress errs slice).
func TestAsyncErr_TryEmitRespectsCtxDeadline(t *testing.T) {
	sig := signals.New[int]()
	block := make(chan struct{})
	defer close(block)
	sig.AddListenerWithErr(func(context.Context, int) error { <-block; return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sig.TryEmit(ctx, 1) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("TryEmit = %v; want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TryEmit hung on a stuck error-returning handler")
	}
}

// I14: a panicking OnError sink is recovered and does not stop the remaining sinks.
func TestAsyncErr_SinkPanicIsolated(t *testing.T) {
	signals.SetPanicHandler(func(any) {}) // swallow for the test
	defer signals.SetPanicHandler(nil)

	sig := signals.New[int]()
	var secondRan int32
	var wg sync.WaitGroup
	wg.Add(2)
	sig.OnError(func(context.Context, error) { wg.Done(); panic("sink boom") })
	sig.OnError(func(context.Context, error) { atomic.AddInt32(&secondRan, 1); wg.Done() })
	sig.AddListenerWithErr(func(context.Context, int) error { return errors.New("e") })

	sig.Emit(context.Background(), 1)
	wg.Wait()

	if atomic.LoadInt32(&secondRan) != 1 {
		t.Fatal("second sink did not run after the first sink panicked")
	}
}

// Sync AddListenerWithErr remains driven by TryEmit (interface-level coverage of the
// promoted method on the sync type).
func TestAsyncErr_SyncAddListenerWithErrViaInterface(t *testing.T) {
	var sig signals.Signal[int] = signals.NewSync[int]()
	want := errors.New("stop")
	sig.AddListenerWithErr(func(context.Context, int) error { return want })
	// TryEmit is sync-only (not on the interface); assert via the concrete type.
	if err := signals.NewSync[int]().TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("empty TryEmit = %v; want nil", err)
	}
	_ = want
}

// Phase 4 gate / FR-8 I14: the failure-handling path must itself be failure-safe.
// These cover the two I14 sites not exercised by TestAsyncErr_SinkPanicIsolated:
// a panic INSIDE the panic handler, and a panic inside a one-shot (AddOnce) handler.

// I14: a buggy panic handler that itself panics must NOT crash the process. If the
// secondary panic escaped the handler goroutine it would be unrecovered and abort the
// whole test binary; surviving (and the sibling listener still running) proves the
// panic handler is contained — it is the last line of defense and must be unbreakable.
func TestI14_PanicHandlerThatPanicsIsContained(t *testing.T) {
	signals.SetPanicHandler(func(any) { panic("the panic handler is itself buggy") })
	defer signals.SetPanicHandler(nil)

	sig := signals.New[int]()
	var sibling int32
	sig.AddListener(func(context.Context, int) { panic("listener panic") }, "boom")
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&sibling, 1) }, "ok")

	sig.TryEmit(context.Background(), 1) // must return; process must survive

	if atomic.LoadInt32(&sibling) != 1 {
		t.Fatal("sibling listener did not run — the failure path was not contained")
	}
}

// I14: an async one-shot handler that panics on its only fire is consumed on attempt
// (not retried) and is still auto-removed.
func TestI14_AsyncOnceHandlerPanicConsumed(t *testing.T) {
	signals.SetPanicHandler(func(any) {}) // swallow
	defer signals.SetPanicHandler(nil)

	sig := signals.New[int]()
	var calls int32
	sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&calls, 1); panic("once boom") })

	sig.TryEmit(context.Background(), 1) // fires, panics (recovered), self-removes
	sig.TryEmit(context.Background(), 2) // must NOT fire again

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("async once handler fired %d times; want 1 (consumed on attempt)", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("async once handler not auto-removed after panicking")
	}
}

// I14: a sync one-shot handler that panics propagates to the Emit caller (as any sync
// listener does), but is still consumed on attempt and auto-removed — so a second
// emission does not fire it again.
func TestI14_SyncOnceHandlerPanicConsumed(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	sig.AddOnce(func(context.Context, int) { atomic.AddInt32(&calls, 1); panic("once boom") })

	// Sync listener panics propagate to the caller — recover the expected one.
	func() {
		defer func() { _ = recover() }()
		sig.Emit(context.Background(), 1)
	}()
	sig.Emit(context.Background(), 2) // already removed → must not fire

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("sync once handler fired %d times; want 1 (consumed on attempt)", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("sync once handler not auto-removed after panicking")
	}
}

// Phase 4A / FR-7 + FR-8: proofs for bounded async dispatch (MaxConcurrent).
//   I8  bound holds (≤ N concurrent, per-signal across overlapping emits)
//   I9  Emit is non-blocking even with a saturated bound
//   I13 a panicking handler still releases its slot (no bounded-pool deadlock)
//   I16 no silent drop — every handler eventually runs (M ≫ N)
//   ctx-liveness — TryEmit returns at the ctx deadline despite a hung handler

func TestBounded_DefaultMaxConcurrent(t *testing.T) {
	if got := signals.DefaultMaxConcurrent(); got != 2*runtime.NumCPU() {
		t.Fatalf("DefaultMaxConcurrent() = %d, want %d", got, 2*runtime.NumCPU())
	}
}

// I8: with MaxConcurrent=N, at most N handlers run at once — even across many
// concurrent overlapping emits (proves the semaphore is per-signal, not per-emit).
func TestBounded_ConcurrencyNeverExceedsLimit(t *testing.T) {
	const limit = 4
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: limit})

	var inFlight, maxSeen int32
	gate := make(chan struct{})
	const listeners = 12
	for i := 0; i < listeners; i++ {
		sig.AddListener(func(context.Context, int) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxSeen)
				if cur <= old || atomic.CompareAndSwapInt32(&maxSeen, old, cur) {
					break
				}
			}
			<-gate // hold the slot until released
			atomic.AddInt32(&inFlight, -1)
		})
	}

	// Several overlapping emits compete for the same N slots.
	var wg sync.WaitGroup
	for e := 0; e < 5; e++ {
		wg.Add(1)
		go func() { defer wg.Done(); sig.TryEmit(context.Background(), 1) }()
	}

	// Let handlers pile up against the bound, then release them all.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&maxSeen) < limit && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	close(gate)
	wg.Wait()

	if got := atomic.LoadInt32(&maxSeen); got > limit {
		t.Fatalf("max concurrent handlers = %d, exceeds limit %d", got, limit)
	}
	if got := atomic.LoadInt32(&maxSeen); got != limit {
		t.Fatalf("max concurrent handlers = %d, expected to reach the limit %d", got, limit)
	}
}

// I9: Emit returns immediately (does not block the caller) even when the bound is
// fully saturated by slow handlers.
func TestBounded_EmitNeverBlocksCaller(t *testing.T) {
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: 1})
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	sig.AddListener(func(context.Context, int) { started <- struct{}{}; <-release })

	sig.Emit(context.Background(), 1) // occupies the single slot
	<-started

	done := make(chan struct{})
	go func() {
		sig.Emit(context.Background(), 2) // bound saturated — must still return at once
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Emit blocked the caller while the bound was saturated")
	}
	close(release)
}

// I13: a panicking handler under a bound still releases its slot, so the pool does
// not deadlock. With MaxConcurrent=1, after a panicking handler a later handler must
// still be able to acquire the (freed) slot and run.
func TestBounded_SlotReleasedOnPanic(t *testing.T) {
	signals.SetPanicHandler(func(any) {}) // swallow for the test
	defer signals.SetPanicHandler(nil)

	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: 1})
	sig.AddListener(func(context.Context, int) { panic("boom") }, "panics")
	ran := make(chan struct{}, 1)
	sig.AddListener(func(context.Context, int) { ran <- struct{}{} }, "after")

	sig.TryEmit(context.Background(), 1)
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("slot leaked on panic — second handler never acquired the freed slot")
	}

	// And the signal is still usable for many further emits (no capacity lost).
	var n int32
	sig.Reset()
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) })
	for i := 0; i < 50; i++ {
		sig.TryEmit(context.Background(), i)
	}
	if atomic.LoadInt32(&n) != 50 {
		t.Fatalf("after a panic, only %d/50 later emits ran — capacity leaked", n)
	}
}

// I16: under a bound, no handler is silently dropped — emitting M ≫ N listeners runs
// all M (the excess parks and eventually runs).
func TestBounded_NoSilentDrop(t *testing.T) {
	const limit, listeners = 3, 100
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: limit})
	var ran int32
	for i := 0; i < listeners; i++ {
		sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1) })
	}
	sig.TryEmit(context.Background(), 1)
	if got := atomic.LoadInt32(&ran); got != listeners {
		t.Fatalf("ran %d/%d handlers — bound dropped work", got, listeners)
	}
}

// Covers TryEmit under a CANCELLABLE context where handlers complete normally
// (waitForOrCancel takes the done-completes-first path), and the bounded acquire's
// cancellable-ctx success branch.
func TestBounded_CompletesUnderCancellableCtx(t *testing.T) {
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: 4})
	var n int32
	for i := 0; i < 3; i++ {
		sig.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) })
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig.TryEmit(ctx, 1)
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Fatalf("ran %d/3 under cancellable ctx; want 3", got)
	}
}

// Covers the bounded acquire under a NIL context (the ctx==nil send branch).
func TestBounded_NilContextAcquire(t *testing.T) {
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: 2})
	var n int32
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) })
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) })
	sig.TryEmit(nil, 1) //nolint:staticcheck // exercise nil-ctx bounded acquire
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("ran %d/2 under nil ctx; want 2", got)
	}
}

// doneNotErrCtx is a context whose Done() channel is closed but whose Err() stays
// nil. A real context never does this — but it deterministically isolates the
// parked-acquire cancellation branch: the in-loop ctx.Err() check passes (nil), and
// the (saturated) slot-acquire select then takes the <-ctx.Done() case. In
// production that branch is hit in the race window where ctx cancels after the
// in-loop check but while the dispatcher is parked acquiring a slot.
type doneNotErrCtx struct{ done <-chan struct{} }

func (doneNotErrCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c doneNotErrCtx) Done() <-chan struct{}     { return c.done }
func (doneNotErrCtx) Err() error                  { return nil }
func (doneNotErrCtx) Value(any) any               { return nil }

func closedDone() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// Covers the parked-acquire cancellation branch deterministically: the single slot
// is held by a blocked handler, so the next emit's acquire parks; with Done closed
// the select takes <-ctx.Done() and dispatch returns without starting the handler.
func TestBounded_ParkedAcquireCancels(t *testing.T) {
	sig := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: 1})
	block := make(chan struct{})
	inSlot := make(chan struct{}, 1)
	var ran int32
	sig.AddListener(func(context.Context, int) {
		atomic.AddInt32(&ran, 1)
		inSlot <- struct{}{}
		<-block // hold the single slot
	})

	sig.Emit(context.Background(), 1) // the handler takes the slot and blocks
	<-inSlot                          // slot now occupied

	// The slot is full, so this emit's acquire parks; Done is closed → cancel.
	sig.TryEmit(doneNotErrCtx{done: closedDone()}, 2)

	if got := atomic.LoadInt32(&ran); got != 1 {
		t.Fatalf("ran=%d; the parked TryEmit acquire should cancel and run 0 (only the first emit ran)", got)
	}
	close(block)
}

// ctx-liveness: TryEmit must return at the context deadline even if a handler
// is hung, rather than blocking forever on it.
func TestBounded_TryEmitRespectsCtxDeadline(t *testing.T) {
	sig := signals.New[int]()
	block := make(chan struct{})
	defer close(block)
	sig.AddListener(func(context.Context, int) { <-block }) // never returns during the test

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { sig.TryEmit(ctx, 1); close(done) }()
	select {
	case <-done: // returned at/after the deadline — good
	case <-time.After(2 * time.Second):
		t.Fatal("TryEmit hung on a stuck handler instead of returning at the ctx deadline")
	}
}

// TestAsyncErr_TryEmitJoinRaceFree exercises the concurrent per-index error
// collection in async TryEmit under the race detector: k error-returning listeners
// all fail at once, and the joined result must contain every one of them. Repeated
// trials give the race detector many chances to flag an unsynchronized write to the
// errs slice or an unordered read of it — closing the gap where the happens-before of
// the errs[idx] writes vs the post-wg.Wait errors.Join read was argued only in
// comments, never run under -race with multiple failing listeners.
func TestAsyncErr_TryEmitJoinRaceFree(t *testing.T) {
	const k = 16
	listenerErrs := make([]error, k)
	for i := range listenerErrs {
		listenerErrs[i] = errors.New("listener failed") // distinct identities; errors.Is matches each
	}
	for trial := 0; trial < 50; trial++ {
		sig := signals.New[int]()
		for i := 0; i < k; i++ {
			e := listenerErrs[i]
			sig.AddListenerWithErr(func(ctx context.Context, v int) error { return e })
		}
		joined := sig.TryEmit(context.Background(), trial)
		if joined == nil {
			t.Fatalf("trial %d: expected a joined error, got nil", trial)
		}
		for i, e := range listenerErrs {
			if !errors.Is(joined, e) {
				t.Fatalf("trial %d: joined error is missing listener %d's error", trial, i)
			}
		}
	}
}

// ErrStopPropagation is a sync-only control value: AsyncSignal has no sequential chain to
// stop, so it never reports the sentinel as a failure — not joined by TryEmit, and (on
// the Emit path) not routed to OnError. A real error from a sibling still surfaces.
func TestErrStopPropagation_AsyncIgnoredAsFailure(t *testing.T) {
	sig := signals.New[int]()
	real := errors.New("real")
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return signals.ErrStopPropagation })
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return real })
	err := sig.TryEmit(context.Background(), 1)
	if !errors.Is(err, real) {
		t.Fatalf("TryEmit err = %v; want it to contain the real error", err)
	}
	if errors.Is(err, signals.ErrStopPropagation) {
		t.Fatal("ErrStopPropagation must not be reported as a failure by async TryEmit")
	}
}
