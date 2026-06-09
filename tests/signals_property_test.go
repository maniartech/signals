package signals_test

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"

	"github.com/maniartech/signals"
)

// Phase R / FR-8: property-based proofs of the lock-free core invariants over long,
// seeded (reproducible) operation streams checked against an independent model.
//   I1 count integrity · I2 introspection consistency · I3 no-call-after-remove
//   I4 all-live-receive · I15 ordering · I17 happens-before (sequential)

// I1 + I2: after any Add/Remove stream, Len()/HasKey()/Keys() agree exactly with an
// independent model of which keys are live.
func TestProperty_KeyedStateConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sig := signals.NewSync[int]()
	model := map[string]bool{}

	for step := 0; step < 5000; step++ {
		key := fmt.Sprintf("k%d", rng.Intn(30))
		switch rng.Intn(2) {
		case 0:
			r := sig.AddListener(noop, key)
			if model[key] {
				if r != -1 {
					t.Fatalf("step %d: dup add of %q returned %d, want -1", step, key, r)
				}
			} else {
				model[key] = true
			}
		case 1:
			r := sig.RemoveListener(key)
			if model[key] {
				delete(model, key)
			} else if r != -1 {
				t.Fatalf("step %d: removing absent %q returned %d, want -1", step, key, r)
			}
		}
		if got := sig.Len(); got != len(model) {
			t.Fatalf("step %d: Len()=%d, model=%d", step, got, len(model))
		}
		if got := sig.HasKey(key); got != model[key] {
			t.Fatalf("step %d: HasKey(%q)=%v, model=%v", step, key, got, model[key])
		}
		keys := sig.Keys()
		if len(keys) != len(model) {
			t.Fatalf("step %d: Keys() len=%d, model=%d", step, len(keys), len(model))
		}
		for _, k := range keys {
			if !model[k] {
				t.Fatalf("step %d: Keys() returned non-live %q", step, k)
			}
		}
	}
}

// I3 + I4: every emission invokes exactly the currently-live set — proving both
// "all live receive" and "no call after remove". Unique keys ⇒ each listener has one
// lifecycle, so its observed call count must equal the emits while it was live.
func TestProperty_LiveListenersReceiveExactlyEmissions(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	sig := signals.NewSync[int]()
	ctx := context.Background()

	type listener struct {
		calls    int64
		expected int64
		key      string
	}
	var all, live []*listener
	next := 0

	for step := 0; step < 4000; step++ {
		switch rng.Intn(3) {
		case 0:
			l := &listener{key: fmt.Sprintf("k%d", next)}
			next++
			sig.AddListener(func(context.Context, int) { atomic.AddInt64(&l.calls, 1) }, l.key)
			all = append(all, l)
			live = append(live, l)
		case 1:
			if len(live) > 0 {
				i := rng.Intn(len(live))
				sig.RemoveListener(live[i].key)
				live[i] = live[len(live)-1]
				live = live[:len(live)-1]
			}
		case 2:
			sig.Emit(ctx, step)
			for _, l := range live {
				l.expected++
			}
		}
	}
	for _, l := range all {
		if got := atomic.LoadInt64(&l.calls); got != l.expected {
			t.Fatalf("listener %q: %d calls, expected %d", l.key, got, l.expected)
		}
	}
}

// I15 + I17: sync emits in registration order (= registration order until the first
// removal); after a removal, set membership is preserved (each remaining listener
// fires exactly once, the removed one not at all). The happens-before of sequential
// AddListener→Emit is implicit in observing all registrations.
func TestProperty_SyncOrderingAndMembership(t *testing.T) {
	sig := signals.NewSync[int]()
	var order []int
	const n = 20
	for i := 0; i < n; i++ {
		i := i
		sig.AddListener(func(context.Context, int) { order = append(order, i) }, fmt.Sprintf("k%d", i))
	}

	sig.Emit(context.Background(), 0)
	if len(order) != n {
		t.Fatalf("got %d fires, want %d", len(order), n)
	}
	for i := 0; i < n; i++ {
		if order[i] != i {
			t.Fatalf("position %d = %d, want %d (registration order before any removal)", i, order[i], i)
		}
	}

	// After a removal, ordering is no longer guaranteed, but membership is.
	order = nil
	sig.RemoveListener("k5")
	sig.Emit(context.Background(), 0)
	if len(order) != n-1 {
		t.Fatalf("after removal got %d fires, want %d", len(order), n-1)
	}
	seen := map[int]bool{}
	for _, v := range order {
		if seen[v] {
			t.Fatalf("listener %d fired twice", v)
		}
		seen[v] = true
	}
	if seen[5] {
		t.Fatal("removed listener 5 still fired")
	}
}
