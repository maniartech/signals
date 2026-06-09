package signals_test

import (
	"context"
	"testing"

	"github.com/maniartech/signals"
)

func TestSyncSignal_EmitInvokesErrorListeners(t *testing.T) {
	sig := signals.NewSync[int]()

	called := 0
	sig.AddListenerWithErr(func(ctx context.Context, v int) error {
		called++
		return nil
	})

	sig.Emit(context.Background(), 1)

	if called != 1 {
		t.Fatalf("Expected error listener to be called by Emit, got %d", called)
	}
}

func TestSyncSignal_EmitStopsOnCanceledContextBeforeStart(t *testing.T) {
	sig := signals.NewSync[int]()

	called := 0
	sig.AddListener(func(ctx context.Context, v int) {
		called++
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sig.Emit(ctx, 1)

	if called != 0 {
		t.Fatalf("Expected Emit to skip listeners on canceled context, got %d", called)
	}
}
