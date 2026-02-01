package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

func TestRemoveListenerEmptyKeyOnlyRemovesKeyed(t *testing.T) {
	sig := signals.NewSync[int]()

	var unkeyedCalled int
	var keyedCalled int

	sig.AddListener(func(ctx context.Context, v int) {
		unkeyedCalled++
	})
	sig.AddListener(func(ctx context.Context, v int) {
		keyedCalled++
	}, "")

	if got := sig.RemoveListener(""); got != 1 {
		t.Fatalf("Expected 1 listener remaining, got %d", got)
	}

	sig.Emit(context.Background(), 1)

	if unkeyedCalled != 1 {
		t.Fatalf("Expected unkeyed listener to remain, got %d calls", unkeyedCalled)
	}
	if keyedCalled != 0 {
		t.Fatalf("Expected keyed listener to be removed, got %d calls", keyedCalled)
	}
}
