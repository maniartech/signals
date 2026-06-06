package signals_test

import (
	"context"
	"sync"
	"testing"

	"github.com/maniartech/signals"
)

// Benchmark emitting signals with a single listener
func BenchmarkSignalEmit_SingleListener(b *testing.B) {
	signal := signals.New[int]()
	signal.AddListener(func(ctx context.Context, v int) {})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		signal.Emit(ctx, i)
	}
}

// Benchmark emitting signals with many listeners
func BenchmarkSignalEmit_ManyListeners(b *testing.B) {
	opts := &signals.SignalOptions{InitialCapacity: 101} // next prime after 100
	signal := signals.NewWithOptions[int](opts)
	for i := 0; i < 100; i++ {
		signal.AddListener(func(ctx context.Context, v int) {})
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		signal.Emit(ctx, i)
	}
}

// Benchmark concurrent emission
func BenchmarkSignalEmit_Concurrent(b *testing.B) {
	signal := signals.New[int]()
	signal.AddListener(func(ctx context.Context, v int) {})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for i := 0; pb.Next(); i++ {
			signal.Emit(ctx, i)
		}
	})
}

// --- Sync emission benchmarks (the lock-free copy-on-write target path) ---
//
// These measure SyncSignal.Emit, whose read path currently takes an RWMutex
// RLock and copies the subscriber slice into a snapshot on every emit. They are
// the "before" baseline for the v1.4 lock-free (atomic.Pointer + write-mutex COW)
// rewrite; the headline single-listener and concurrent paths were previously
// unmeasured. See docs/patterns and v1.4-requirements.md (FR-0, Phase 1).

// Benchmark sync emit with a single listener — the headline hot path.
func BenchmarkSyncEmit_SingleListener(b *testing.B) {
	signal := signals.NewSync[int]()
	signal.AddListener(func(ctx context.Context, v int) {})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		signal.Emit(ctx, i)
	}
}

// Benchmark sync emit with ten listeners — exercises per-emit snapshot cost.
func BenchmarkSyncEmit_TenListeners(b *testing.B) {
	signal := signals.NewSync[int]()
	for i := 0; i < 10; i++ {
		signal.AddListener(func(ctx context.Context, v int) {})
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		signal.Emit(ctx, i)
	}
}

// Benchmark concurrent sync emit — measures read-path contention under the
// RWMutex baseline; the lock-free rewrite should scale near-linearly here.
func BenchmarkSyncEmit_Concurrent(b *testing.B) {
	signal := signals.NewSync[int]()
	signal.AddListener(func(ctx context.Context, v int) {})
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for i := 0; pb.Next(); i++ {
			signal.Emit(ctx, i)
		}
	})
}

// Benchmark concurrent add/remove listeners
func BenchmarkSignalAddRemoveListener_Concurrent(b *testing.B) {
	opts := &signals.SignalOptions{InitialCapacity: 1024}
	signal := signals.NewWithOptions[int](opts)
	var mu sync.Mutex
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for i := 0; pb.Next(); i++ {
			key := string(rune(i % 1000))
			mu.Lock()
			signal.AddListener(func(ctx context.Context, v int) {}, key)
			signal.RemoveListener(key)
			mu.Unlock()
		}
	})
}
