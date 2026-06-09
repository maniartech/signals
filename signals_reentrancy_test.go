package signals_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

// Phase 5 / FR-8: reentrancy. A handler that mutates or re-emits the signal during
// its own invocation must be safe — no panic, no deadlock, no corruption — and the
// in-flight emission must observe the immutable snapshot taken when the emit began.

// Sync: a handler that adds and removes listeners during emit. Because the emission
// iterates the snapshot captured at Emit time, a sibling removed mid-emit still runs
// this round and a listener added mid-emit does not; the *next* emit reflects the
// mutations.
func TestReentrancy_SyncMutateDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	var aRan, bRan, addedRan int32

	sig.AddListener(func(context.Context, int) {
		atomic.AddInt32(&aRan, 1)
		sig.AddListener(func(context.Context, int) { atomic.AddInt32(&addedRan, 1) }, "added")
		sig.RemoveListener("b")
	}, "a")
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&bRan, 1) }, "b")

	sig.Emit(context.Background(), 1) // snapshot = [a, b]

	if aRan != 1 || bRan != 1 {
		t.Fatalf("in-flight snapshot not honored: aRan=%d bRan=%d (want 1,1)", aRan, bRan)
	}
	if addedRan != 0 {
		t.Fatalf("a listener added mid-emit ran this round (%d); want 0", addedRan)
	}
	if sig.HasKey("b") || !sig.HasKey("added") {
		t.Fatalf("post-emit state wrong: hasB=%v hasAdded=%v (want false, true)", sig.HasKey("b"), sig.HasKey("added"))
	}

	// The next emit uses the mutated state: b is gone, added now fires.
	atomic.StoreInt32(&bRan, 0)
	atomic.StoreInt32(&addedRan, 0)
	sig.Emit(context.Background(), 2)
	if bRan != 0 || addedRan != 1 {
		t.Fatalf("second emit: bRan=%d addedRan=%d (want 0,1)", bRan, addedRan)
	}
}

// Sync: a handler that re-emits on the same signal must recurse and terminate, not
// deadlock (sync emit holds no lock).
func TestReentrancy_SyncReentrantEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	var calls int32
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
		if v > 0 {
			sig.Emit(ctx, v-1) // reentrant; bounded by v
		}
	})
	sig.Emit(context.Background(), 3) // 3→2→1→0
	if got := atomic.LoadInt32(&calls); got != 4 {
		t.Fatalf("reentrant sync emit ran %d times; want 4 (v=3,2,1,0)", got)
	}
}

// Sync: a handler that Resets the signal during emit. The in-flight snapshot still
// completes; afterward the signal is empty.
func TestReentrancy_SyncResetDuringEmit(t *testing.T) {
	sig := signals.NewSync[int]()
	var ran int32
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1); sig.Reset() }, "a")
	sig.AddListener(func(context.Context, int) { atomic.AddInt32(&ran, 1) }, "b")

	sig.Emit(context.Background(), 1) // both run from the snapshot despite the Reset
	if got := atomic.LoadInt32(&ran); got != 2 {
		t.Fatalf("snapshot not honored across reset: ran=%d, want 2", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("signal should be empty after the reentrant Reset")
	}
}

// Async: a handler that fire-and-forget re-emits on the same (unbounded) signal must
// not deadlock and must terminate.
func TestReentrancy_AsyncReentrantEmit(t *testing.T) {
	sig := signals.New[int]()
	var calls int32
	done := make(chan struct{})
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&calls, 1)
		if v > 0 {
			sig.Emit(context.Background(), v-1)
		} else {
			close(done)
		}
	})
	sig.Emit(context.Background(), 4)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant async emit deadlocked or did not terminate")
	}
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Fatalf("reentrant async emit ran %d times; want 5 (v=4..0)", got)
	}
}

// Async: a handler that removes itself during emit (the manual form of AddOnce) is
// safe and the removal takes effect for subsequent emissions.
func TestReentrancy_AsyncHandlerRemovesSelf(t *testing.T) {
	sig := signals.New[int]()
	var calls int32
	sig.AddListener(func(context.Context, int) {
		atomic.AddInt32(&calls, 1)
		sig.RemoveListener("self")
	}, "self")

	sig.EmitAndWait(context.Background(), 1) // runs once, removes itself
	sig.EmitAndWait(context.Background(), 2) // gone
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("self-removing handler ran %d times; want 1", got)
	}
	if !sig.IsEmpty() {
		t.Fatal("signal should be empty after the handler removed itself")
	}
}
