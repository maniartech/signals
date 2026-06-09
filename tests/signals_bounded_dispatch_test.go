package signals_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase 4A / FR-7 + FR-8: proofs for bounded async dispatch (MaxConcurrent).
//   I8  bound holds (≤ N concurrent, per-signal across overlapping emits)
//   I9  Emit is non-blocking even with a saturated bound
//   I13 a panicking handler still releases its slot (no bounded-pool deadlock)
//   I16 no silent drop — every handler eventually runs (M ≫ N)
//   ctx-liveness — EmitAndWait returns at the ctx deadline despite a hung handler

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
		go func() { defer wg.Done(); sig.EmitAndWait(context.Background(), 1) }()
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

	sig.EmitAndWait(context.Background(), 1)
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
		sig.EmitAndWait(context.Background(), i)
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
	sig.EmitAndWait(context.Background(), 1)
	if got := atomic.LoadInt32(&ran); got != listeners {
		t.Fatalf("ran %d/%d handlers — bound dropped work", got, listeners)
	}
}

// Covers EmitAndWait under a CANCELLABLE context where handlers complete normally
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
	sig.EmitAndWait(ctx, 1)
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
	sig.EmitAndWait(nil, 1) //nolint:staticcheck // exercise nil-ctx bounded acquire
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
	sig.EmitAndWait(doneNotErrCtx{done: closedDone()}, 2)

	if got := atomic.LoadInt32(&ran); got != 1 {
		t.Fatalf("ran=%d; the parked EmitAndWait acquire should cancel and run 0 (only the first emit ran)", got)
	}
	close(block)
}

// ctx-liveness: EmitAndWait must return at the context deadline even if a handler
// is hung, rather than blocking forever on it.
func TestBounded_EmitAndWaitRespectsCtxDeadline(t *testing.T) {
	sig := signals.New[int]()
	block := make(chan struct{})
	defer close(block)
	sig.AddListener(func(context.Context, int) { <-block }) // never returns during the test

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { sig.EmitAndWait(ctx, 1); close(done) }()
	select {
	case <-done: // returned at/after the deadline — good
	case <-time.After(2 * time.Second):
		t.Fatal("EmitAndWait hung on a stuck handler instead of returning at the ctx deadline")
	}
}
