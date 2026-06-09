package signals_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase R / G5: usability/scenario tests. These exercise the library the way the
// pattern catalog's *practical examples* do — realistic end-to-end flows — proving
// the API is ergonomic and correct in real usage, not just unit-correct. Each maps
// to a documented pattern in docs/patterns/.

// Pattern: Synchronous Sequential Dispatch — an ordered processing pipeline that
// runs validate → normalize → save in registration order and completes before Emit
// returns.
func TestUsability_OrderedPipeline(t *testing.T) {
	type Form struct {
		raw        string
		normalized string
		saved      bool
	}
	var steps []string
	pipeline := signals.NewSync[*Form]()
	pipeline.AddListener(func(_ context.Context, f *Form) { steps = append(steps, "validate") }, "1-validate")
	pipeline.AddListener(func(_ context.Context, f *Form) {
		f.normalized = "norm:" + f.raw
		steps = append(steps, "normalize")
	}, "2-normalize")
	pipeline.AddListener(func(_ context.Context, f *Form) {
		f.saved = true
		steps = append(steps, "save")
	}, "3-save")

	f := &Form{raw: "hello"}
	pipeline.Emit(context.Background(), f)

	want := []string{"validate", "normalize", "save"}
	if len(steps) != len(want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("step %d = %q, want %q (order not preserved)", i, steps[i], want[i])
		}
	}
	if f.normalized != "norm:hello" || !f.saved {
		t.Fatalf("pipeline did not complete: %+v", *f)
	}
}

// Pattern: Transactional Emission — a payment pipeline that aborts on the first
// failing step and surfaces the error; later steps must not run.
func TestUsability_TransactionalAbortsOnError(t *testing.T) {
	var ran []string
	declined := errors.New("card declined")
	pay := signals.NewSync[int]()
	pay.AddListenerWithErr(func(context.Context, int) error { ran = append(ran, "validate"); return nil }, "1")
	pay.AddListenerWithErr(func(context.Context, int) error { ran = append(ran, "authorize"); return declined }, "2")
	pay.AddListenerWithErr(func(context.Context, int) error { ran = append(ran, "capture"); return nil }, "3")

	err := pay.TryEmit(context.Background(), 100)
	if !errors.Is(err, declined) {
		t.Fatalf("TryEmit err = %v, want %v", err, declined)
	}
	if contains(ran, "capture") {
		t.Fatalf("capture ran after abort: %v", ran)
	}
}

// Pattern: Keyed Subscription — a UI component subscribes on mount and removes
// exactly its own listener on unmount.
func TestUsability_ComponentMountUnmount(t *testing.T) {
	dashboard := signals.New[int]()
	var refreshed int32
	const key = "widget-42"

	dashboard.AddListener(func(context.Context, int) { atomic.AddInt32(&refreshed, 1) }, key)
	dashboard.TryEmit(context.Background(), 1) // mounted → refreshes
	dashboard.RemoveListener(key)              // unmount
	dashboard.TryEmit(context.Background(), 2) // gone → no refresh

	if got := atomic.LoadInt32(&refreshed); got != 1 {
		t.Fatalf("component refreshed %d times; want 1 (removed on unmount)", got)
	}
}

// Pattern: Keyed Subscription — hot-swap a handler under the same key, and guard
// against accidental double-registration.
func TestUsability_HotSwapHandlerUnderSameKey(t *testing.T) {
	cfg := signals.NewSync[int]()
	var which string
	cfg.AddListener(func(context.Context, int) { which = "old" }, "logger")
	cfg.RemoveListener("logger")
	cfg.AddListener(func(context.Context, int) { which = "new" }, "logger")

	cfg.Emit(context.Background(), 1)
	if which != "new" {
		t.Fatalf("handler = %q after swap; want new", which)
	}
	if got := cfg.AddListener(func(context.Context, int) {}, "logger"); got != -1 {
		t.Fatalf("double-registration returned %d; want -1", got)
	}
}

// Pattern: One-Shot Subscription — a one-time migration that runs on the first
// connection only, no matter how many connections occur.
func TestUsability_OneTimeMigrationOnFirstConnect(t *testing.T) {
	connected := signals.New[int]()
	var migrations int32
	connected.AddOnce(func(context.Context, int) { atomic.AddInt32(&migrations, 1) })

	for i := 0; i < 5; i++ {
		connected.TryEmit(context.Background(), i)
	}
	if got := atomic.LoadInt32(&migrations); got != 1 {
		t.Fatalf("migration ran %d times; want exactly 1", got)
	}
	if !connected.IsEmpty() {
		t.Fatal("one-shot listener should auto-remove, leaving the signal empty")
	}
}

// Pattern: Subscription Teardown — a per-session signal cleaned up on logout via
// Reset; afterward no listener fires.
func TestUsability_SessionTeardown(t *testing.T) {
	session := signals.New[int]()
	var events int32
	session.AddListener(func(context.Context, int) { atomic.AddInt32(&events, 1) }, "audit")
	session.AddListener(func(context.Context, int) { atomic.AddInt32(&events, 1) }, "cache")

	session.TryEmit(context.Background(), 1) // 2 listeners fire
	session.Reset()                          // logout
	session.TryEmit(context.Background(), 2) // nothing fires

	if got := atomic.LoadInt32(&events); got != 2 {
		t.Fatalf("got %d events; want 2 (Reset must drop all listeners)", got)
	}
	if !session.IsEmpty() {
		t.Fatal("session should be empty after Reset")
	}
}

// Pattern: Shared Event Registry — a package-level signal coordinating multiple
// independent reactors (audit + cache) that don't know about each other.
func TestUsability_SharedEventRegistryFanOut(t *testing.T) {
	type User struct{ ID int }
	userLoggedIn := signals.New[User]()

	var audited, cacheWarmed int32
	userLoggedIn.AddListener(func(_ context.Context, u User) { atomic.AddInt32(&audited, 1) }, "audit/login")
	userLoggedIn.AddListener(func(_ context.Context, u User) { atomic.AddInt32(&cacheWarmed, 1) }, "cache/warm")

	userLoggedIn.TryEmit(context.Background(), User{ID: 7})

	if atomic.LoadInt32(&audited) != 1 || atomic.LoadInt32(&cacheWarmed) != 1 {
		t.Fatalf("fan-out incomplete: audited=%d cacheWarmed=%d", audited, cacheWarmed)
	}
}

// Pattern: Async Error Routing — fire-and-forget delivery whose failures must be
// surfaced (alerted) rather than lost, even though Emit returns immediately.
func TestUsability_AsyncErrorRoutingAlertsOnFailure(t *testing.T) {
	type Webhook struct{ URL string }
	deliveries := signals.New[Webhook]()
	failed := make(chan error, 1)
	deliveries.OnError(func(_ context.Context, err error) { failed <- err })

	wantErr := errors.New("502 from endpoint")
	deliveries.AddListenerWithErr(func(context.Context, Webhook) error { return wantErr }, "deliver")

	deliveries.Emit(context.Background(), Webhook{URL: "https://hooks.example/x"}) // returns at once

	select {
	case err := <-failed:
		if !errors.Is(err, wantErr) {
			t.Fatalf("OnError received %v; want %v", err, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery failure was not routed to OnError")
	}
}

// Pattern: Result Aggregation — fan an alert out to several channels concurrently and
// report exactly which one(s) failed.
func TestUsability_ResultAggregationReportsFailedChannel(t *testing.T) {
	type Alert struct{ Msg string }
	notify := signals.New[Alert]()
	pagerDown := errors.New("pagerduty unreachable")
	notify.AddListenerWithErr(func(context.Context, Alert) error { return nil }, "slack")
	notify.AddListenerWithErr(func(context.Context, Alert) error { return pagerDown }, "pagerduty")
	notify.AddListenerWithErr(func(context.Context, Alert) error { return nil }, "email")

	err := notify.TryEmit(context.Background(), Alert{Msg: "disk full"})
	if !errors.Is(err, pagerDown) {
		t.Fatalf("aggregated error = %v; want it to identify the PagerDuty failure", err)
	}
}

// Pattern: Bounded Concurrency — size MaxConcurrent to a constrained downstream (e.g.
// a database connection pool) so handlers never exceed it, and no work is dropped.
func TestUsability_BoundedConcurrencyProtectsDownstream(t *testing.T) {
	const dbConns = 5
	const records = 40
	writes := signals.NewWithOptions[int](&signals.SignalOptions{MaxConcurrent: dbConns})

	var inFlight, peak, done int32
	gate := make(chan struct{})
	for i := 0; i < records; i++ {
		writes.AddListener(func(context.Context, int) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
					break
				}
			}
			<-gate
			atomic.AddInt32(&inFlight, -1)
			atomic.AddInt32(&done, 1)
		})
	}

	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for atomic.LoadInt32(&peak) < dbConns && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		close(gate)
	}()
	writes.TryEmit(context.Background(), 1)

	if got := atomic.LoadInt32(&peak); got > dbConns {
		t.Fatalf("peak concurrency %d exceeded the pool limit %d", got, dbConns)
	}
	if got := atomic.LoadInt32(&done); got != records {
		t.Fatalf("only %d/%d writes completed — work was dropped", got, records)
	}
}

// Pattern: Backpressure — TryEmit makes the producer wait for completion, so a
// loss-intolerant producer self-throttles and never outruns its consumer (no loss).
func TestUsability_BackpressureViaTryEmit(t *testing.T) {
	ledger := signals.New[int]()
	var written int32
	ledger.AddListener(func(context.Context, int) { atomic.AddInt32(&written, 1) })

	const trades = 100
	for i := 0; i < trades; i++ {
		ledger.TryEmit(context.Background(), i) // blocks until written — no trade is lost
	}
	if got := atomic.LoadInt32(&written); got != trades {
		t.Fatalf("wrote %d/%d trades; backpressure must lose none", got, trades)
	}
}
