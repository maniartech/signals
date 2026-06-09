package signals_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/maniartech/signals"
)

func TestSignalOptionsGrowthFuncIsUsed(t *testing.T) {
	var called int32
	opts := &signals.SignalOptions{
		InitialCapacity: 1,
		GrowthFunc: func(currentCap int) int {
			atomic.AddInt32(&called, 1)
			return currentCap + 1
		},
	}

	sig := signals.NewSyncWithOptions[int](opts)
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.AddListener(func(ctx context.Context, v int) {})

	if atomic.LoadInt32(&called) == 0 {
		t.Fatalf("Expected GrowthFunc to be called when capacity grows")
	}
}

func TestSignalOptionsGrowthFuncIsUsedForErrorListeners(t *testing.T) {
	var called int32
	opts := &signals.SignalOptions{
		InitialCapacity: 1,
		GrowthFunc: func(currentCap int) int {
			atomic.AddInt32(&called, 1)
			return currentCap + 1
		},
	}

	sig := signals.NewSyncWithOptions[int](opts)
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })

	if atomic.LoadInt32(&called) == 0 {
		t.Fatalf("Expected GrowthFunc to be called when capacity grows for error listeners")
	}
}
