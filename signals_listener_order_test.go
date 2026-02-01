package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

func TestSyncSignal_ListenerOrderPreserved(t *testing.T) {
	sig := signals.NewSync[int]()

	order := make([]int, 0, 3)
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 1) })
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 2) })
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 3) })

	sig.Emit(context.Background(), 1)

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("Expected listener order [1 2 3], got %v", order)
	}
}

func TestSyncSignal_OrderPreservedAfterRemoval(t *testing.T) {
	sig := signals.NewSync[int]()

	order := make([]int, 0, 3)
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 1) }, "a")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 2) }, "b")
	sig.AddListener(func(ctx context.Context, v int) { order = append(order, 3) }, "c")

	sig.RemoveListener("b")
	sig.Emit(context.Background(), 1)

	if len(order) != 2 || order[0] != 1 || order[1] != 3 {
		t.Fatalf("Expected listener order [1 3] after removal, got %v", order)
	}
}
