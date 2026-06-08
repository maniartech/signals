package signals_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/maniartech/signals"
)

// Phase R / G4: an explicit, consolidated edge-case matrix for the sync core and
// the Phase-3 APIs. Each case asserts a documented contract (FR-1, FR-4, FR-5, FR-9).

// --- nil listener ⇒ fail-fast panic at the registration call site (FR-9) ---

func TestEdge_NilListenerPanicsAtCallSite(t *testing.T) {
	s := signals.NewSync[int]()
	a := signals.New[int]()
	cases := []struct {
		name string
		fn   func()
	}{
		{"sync.AddListener(nil)", func() { s.AddListener(nil) }},
		{"sync.AddListenerWithErr(nil)", func() { s.AddListenerWithErr(nil) }},
		{"sync.AddOnce(nil)", func() { s.AddOnce(nil) }},
		{"sync.AddOnceWithKey(nil)", func() { s.AddOnceWithKey(nil, "k") }},
		{"async.AddListener(nil)", func() { a.AddListener(nil) }},
		{"async.AddOnce(nil)", func() { a.AddOnce(nil) }},
	}
	for _, c := range cases {
		retroMustPanic(t, c.name, c.fn)
	}
}

// --- already-canceled context ⇒ skip every listener (FR-1) ---

func TestEdge_PreCanceledContextSkipsAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := signals.NewSync[int]()
	var n int32
	s.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) })

	s.Emit(ctx, 1)
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("Emit ran %d listeners under canceled ctx; want 0", got)
	}
	if err := s.TryEmit(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("TryEmit returned %v; want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&n); got != 0 {
		t.Fatalf("TryEmit ran %d listeners under canceled ctx; want 0", got)
	}

	a := signals.New[int]()
	var m int32
	a.AddListener(func(context.Context, int) { atomic.AddInt32(&m, 1) })
	a.EmitAndWait(ctx, 1)
	if got := atomic.LoadInt32(&m); got != 0 {
		t.Fatalf("EmitAndWait ran %d listeners under canceled ctx; want 0", got)
	}
}

// --- empty signal (0 listeners) ⇒ no-op, no panic, TryEmit returns nil ---

func TestEdge_EmptySignalIsNoOp(t *testing.T) {
	s := signals.NewSync[int]()
	s.Emit(context.Background(), 1) // must not panic
	if err := s.TryEmit(context.Background(), 1); err != nil {
		t.Fatalf("TryEmit on empty signal returned %v; want nil", err)
	}
	a := signals.New[int]()
	a.Emit(context.Background(), 1)
	a.EmitAndWait(context.Background(), 1) // must not panic / must return
}

// --- duplicate keyed add ⇒ -1, no-op ---

func TestEdge_DuplicateKeyReturnsMinusOne(t *testing.T) {
	s := signals.NewSync[int]()
	if got := s.AddListener(func(context.Context, int) {}, "k"); got != 1 {
		t.Fatalf("first add = %d, want 1", got)
	}
	if got := s.AddListener(func(context.Context, int) {}, "k"); got != -1 {
		t.Fatalf("dup AddListener = %d, want -1", got)
	}
	if got := s.AddListenerWithErr(func(context.Context, int) error { return nil }, "k"); got != -1 {
		t.Fatalf("dup AddListenerWithErr = %d, want -1", got)
	}
	if got := s.AddOnceWithKey(func(context.Context, int) {}, "k"); got != -1 {
		t.Fatalf("dup AddOnceWithKey = %d, want -1", got)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d after duplicate adds; want 1", s.Len())
	}
}

// --- removing an absent key ⇒ -1 ---

func TestEdge_RemoveAbsentReturnsMinusOne(t *testing.T) {
	s := signals.NewSync[int]()
	s.AddListener(func(context.Context, int) {}) // unkeyed
	if got := s.RemoveListener("nope"); got != -1 {
		t.Fatalf("RemoveListener(absent) = %d, want -1", got)
	}
	// RemoveListener("") must not remove the unkeyed listener (FR-4) and returns -1.
	if got := s.RemoveListener(""); got != -1 {
		t.Fatalf("RemoveListener(\"\") = %d, want -1 (must not touch unkeyed)", got)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d; unkeyed listener must survive", s.Len())
	}
}

// --- empty-string key is treated as UNKEYED and invisible to key APIs (FR-9) ---

func TestEdge_EmptyStringKeyIsUnkeyed(t *testing.T) {
	s := signals.NewSync[int]()
	var n int32
	// Added with an explicit "" key — must behave as unkeyed.
	s.AddListener(func(context.Context, int) { atomic.AddInt32(&n, 1) }, "")

	if s.HasKey("") {
		t.Fatal(`HasKey("") must be false`)
	}
	if keys := s.Keys(); len(keys) != 0 {
		t.Fatalf("Keys() = %v; empty-string key must be omitted (treated unkeyed)", keys)
	}
	// A second "" listener is NOT a duplicate (both unkeyed) — both registered.
	if got := s.AddListener(func(context.Context, int) {}, ""); got != 2 {
		t.Fatalf("second empty-key add = %d; want 2 (not deduped)", got)
	}
	// AddOnceWithKey("") behaves as an unkeyed one-shot (auto-removing, hidden key).
	a := signals.New[int]()
	var once int32
	a.AddOnceWithKey(func(context.Context, int) { atomic.AddInt32(&once, 1) }, "")
	if a.HasKey("") {
		t.Fatal(`AddOnceWithKey("") must not register key ""`)
	}
	a.EmitAndWait(context.Background(), 1)
	a.EmitAndWait(context.Background(), 1)
	if got := atomic.LoadInt32(&once); got != 1 {
		t.Fatalf("empty-key one-shot fired %d times; want 1", got)
	}

	// The original "" listener still fires on emit.
	s.Emit(context.Background(), 1)
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("empty-key listener fired %d times; want 1", got)
	}
}

// --- zero-value payload is delivered unmodified (FR-1 payload opacity) ---

func TestEdge_ZeroValuePayloadDelivered(t *testing.T) {
	type Msg struct{ V int }
	s := signals.NewSync[Msg]()
	got := Msg{V: -1}
	s.AddListener(func(_ context.Context, m Msg) { got = m })
	s.Emit(context.Background(), Msg{}) // zero value
	if got != (Msg{}) {
		t.Fatalf("zero payload delivered as %+v; want zero value", got)
	}
}
