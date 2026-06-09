package signals_test

import (
	"context"
	"errors"
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

// --- v1.4 path benchmarks: TryEmit, error routing, bounded async dispatch ---
//
// These cover the surfaces added in v1.4 so every README number traces to a
// committed benchmark. Sync TryEmit is the transactional (stop-on-first-error)
// path; async TryEmit waits for ALL listeners (goroutine spawn + WaitGroup sync);
// the bounded variant exercises the MaxConcurrent semaphore — a SAFETY VALVE that
// caps concurrency, never a speed win, so expect it no faster than unbounded.

// Sync TryEmit with a single error-returning listener — the transactional hot path.
func BenchmarkSyncTryEmit_SingleListener(b *testing.B) {
	signal := signals.NewSync[int]()
	signal.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = signal.TryEmit(ctx, i)
	}
}

// Sync TryEmit fanning out to ten error-returning listeners.
func BenchmarkSyncTryEmit_TenListeners(b *testing.B) {
	signal := signals.NewSync[int]()
	for i := 0; i < 10; i++ {
		signal.AddListenerWithErr(func(ctx context.Context, v int) error { return nil })
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = signal.TryEmit(ctx, i)
	}
}

// Sync Emit routing a listener error to an OnError sink — the best-effort
// error-routing path (distinct from the transactional TryEmit path).
func BenchmarkSyncEmit_ErrorRouted(b *testing.B) {
	signal := signals.NewSync[int]()
	boom := errors.New("boom")
	signal.OnError(func(ctx context.Context, err error) {})
	signal.AddListenerWithErr(func(ctx context.Context, v int) error { return boom })
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		signal.Emit(ctx, i)
	}
}

// Async TryEmit waits for all listeners — measures per-emit goroutine spawn +
// WaitGroup synchronization with ten no-op listeners (unbounded dispatch).
func BenchmarkAsyncTryEmit_TenListeners(b *testing.B) {
	signal := signals.New[int]()
	for i := 0; i < 10; i++ {
		signal.AddListener(func(ctx context.Context, v int) {})
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = signal.TryEmit(ctx, i)
	}
}

// Async TryEmit on a BOUNDED signal (MaxConcurrent=4) with ten listeners — the
// semaphore-guarded path, waited to completion (self-throttling, no backlog).
// Bounding caps concurrent execution; it is a safety valve, not an accelerator.
func BenchmarkAsyncTryEmit_Bounded_TenListeners(b *testing.B) {
	opts := &signals.SignalOptions{MaxConcurrent: 4}
	signal := signals.NewWithOptions[int](opts)
	for i := 0; i < 10; i++ {
		signal.AddListener(func(ctx context.Context, v int) {})
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = signal.TryEmit(ctx, i)
	}
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
