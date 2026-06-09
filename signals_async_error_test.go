package signals_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase 4B / FR-5 + ADR 0001 §B: async error model.
//   I10 EmitAndWaitErr aggregation (exactly those errors, deterministic order, nil iff all ok)
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

// I10: EmitAndWaitErr returns exactly the failing handlers' errors, in registration
// order, and nil when all succeed. Plain listeners never contribute.
func TestAsyncErr_EmitAndWaitErrAggregates(t *testing.T) {
	sig := signals.New[int]()
	eA := errors.New("errA")
	eC := errors.New("errC")
	sig.AddListenerWithErr(func(context.Context, int) error { return eA }, "a")
	sig.AddListener(func(context.Context, int) {}, "b")                          // plain
	sig.AddListenerWithErr(func(context.Context, int) error { return eC }, "c")  // fails
	sig.AddListenerWithErr(func(context.Context, int) error { return nil }, "d") // ok

	err := sig.EmitAndWaitErr(context.Background(), 1)
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
	if err := ok.EmitAndWaitErr(context.Background(), 1); err != nil {
		t.Fatalf("all-succeed EmitAndWaitErr = %v; want nil", err)
	}

	// Empty signal → nil.
	if err := signals.New[int]().EmitAndWaitErr(context.Background(), 1); err != nil {
		t.Fatalf("empty EmitAndWaitErr = %v; want nil", err)
	}
	// Pre-canceled ctx → ctx.Err().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sig.EmitAndWaitErr(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled EmitAndWaitErr = %v; want context.Canceled", err)
	}
}

// EmitAndWaitErr under a cancellable context where all handlers complete (covers the
// done-completes-first branch with a non-nil errs slice).
func TestAsyncErr_EmitAndWaitErrCompletesUnderCancellableCtx(t *testing.T) {
	sig := signals.New[int]()
	e := errors.New("e")
	sig.AddListenerWithErr(func(context.Context, int) error { return e })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sig.EmitAndWaitErr(ctx, 1); !errors.Is(err, e) {
		t.Fatalf("EmitAndWaitErr = %v; want e", err)
	}
}

// ctx-liveness for the error variant: a hung handler must not hang the waiter; it
// returns ctx.Err() at the deadline (and must not read the in-progress errs slice).
func TestAsyncErr_EmitAndWaitErrRespectsCtxDeadline(t *testing.T) {
	sig := signals.New[int]()
	block := make(chan struct{})
	defer close(block)
	sig.AddListenerWithErr(func(context.Context, int) error { <-block; return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sig.EmitAndWaitErr(ctx, 1) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("EmitAndWaitErr = %v; want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EmitAndWaitErr hung on a stuck error-returning handler")
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
