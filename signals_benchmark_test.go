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
	signal := signals.New[int]()
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

// Benchmark concurrent add/remove listeners
func BenchmarkSignalAddRemoveListener_Concurrent(b *testing.B) {
	signal := signals.New[int]()
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
