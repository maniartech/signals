package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

// RemoveListener must never remove unkeyed listeners (FR-4), and an empty-string key
// is treated as unkeyed (FR-9) — so RemoveListener("") is a no-op returning -1, and
// only a real key removes its listener.
func TestRemoveListenerEmptyKeyOnlyRemovesKeyed(t *testing.T) {
	sig := signals.NewSync[int]()

	var unkeyedCalled int
	var keyedCalled int

	sig.AddListener(func(ctx context.Context, v int) {
		unkeyedCalled++
	})
	sig.AddListener(func(ctx context.Context, v int) {
		keyedCalled++
	}, "k")

	// Empty-string key is unkeyed (FR-9): RemoveListener("") removes nothing and must
	// not touch the unkeyed listener (FR-4).
	if got := sig.RemoveListener(""); got != -1 {
		t.Fatalf("RemoveListener(\"\") = %d; want -1 (no-op, must not remove unkeyed)", got)
	}
	// Removing the real key leaves the unkeyed listener in place.
	if got := sig.RemoveListener("k"); got != 1 {
		t.Fatalf("RemoveListener(\"k\") left %d listeners, want 1 (the unkeyed one)", got)
	}

	sig.Emit(context.Background(), 1)

	if unkeyedCalled != 1 {
		t.Fatalf("Expected unkeyed listener to remain, got %d calls", unkeyedCalled)
	}
	if keyedCalled != 0 {
		t.Fatalf("Expected keyed listener to be removed, got %d calls", keyedCalled)
	}
}
