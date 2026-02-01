package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

func TestSyncSignal_ZeroValueUsable(t *testing.T) {
	var sig signals.SyncSignal[int]

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Expected zero value SyncSignal to be usable, got panic: %v", r)
		}
	}()

	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
}

func TestAsyncSignal_ZeroValueUsable(t *testing.T) {
	var sig signals.AsyncSignal[int]

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Expected zero value AsyncSignal to be usable, got panic: %v", r)
		}
	}()

	sig.AddListener(func(ctx context.Context, v int) {})
	sig.Emit(context.Background(), 1)
}
