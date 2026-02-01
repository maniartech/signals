package signals_test

import (
	"context"
	"sync"
	"testing"

	"github.com/maniartech/signals"
)

func TestSyncSignal_EmitZeroAllocations(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.AddListener(func(ctx context.Context, v int) {})

	allocs := testing.AllocsPerRun(1000, func() {
		sig.Emit(context.Background(), 1)
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations, got %f", allocs)
	}
}

func TestAsyncSignal_EmitZeroAllocations(t *testing.T) {
	sig := signals.New[int]()
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.AddListener(func(ctx context.Context, v int) {})

	allocs := testing.AllocsPerRun(1000, func() {
		sig.Emit(context.Background(), 1)
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations, got %f", allocs)
	}
}

func TestSyncSignal_ConcurrentEmitZeroAllocations(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddListener(func(ctx context.Context, v int) {})

	allocs := testing.AllocsPerRun(100, func() {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sig.Emit(context.Background(), 1)
			}()
		}
		wg.Wait()
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations for concurrent emit, got %f", allocs)
	}
}

func TestAsyncSignal_ConcurrentEmitZeroAllocations(t *testing.T) {
	sig := signals.New[int]()
	sig.AddListener(func(ctx context.Context, v int) {})

	allocs := testing.AllocsPerRun(100, func() {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sig.Emit(context.Background(), 1)
			}()
		}
		wg.Wait()
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations for concurrent emit, got %f", allocs)
	}
}

func TestSyncSignal_TryEmitZeroAllocations(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddListener(func(ctx context.Context, v int) {})
	sig.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })

	allocs := testing.AllocsPerRun(1000, func() {
		_ = sig.TryEmit(context.Background(), 1)
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations for TryEmit, got %f", allocs)
	}
}

func TestSyncSignal_SingleKeyedListenerZeroAllocations(t *testing.T) {
	sig := signals.NewSync[int]()
	sig.AddListener(func(ctx context.Context, v int) {}, "k1")

	allocs := testing.AllocsPerRun(1000, func() {
		sig.Emit(context.Background(), 1)
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations for single keyed listener, got %f", allocs)
	}
}

func TestAsyncSignal_SingleKeyedListenerZeroAllocations(t *testing.T) {
	sig := signals.New[int]()
	sig.AddListener(func(ctx context.Context, v int) {}, "k1")

	allocs := testing.AllocsPerRun(1000, func() {
		sig.Emit(context.Background(), 1)
	})

	if allocs != 0 {
		t.Fatalf("Expected zero allocations for single keyed listener, got %f", allocs)
	}
}
