package signals

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestAsyncSignal_SmallEmitDoesNotInitWorkerPool(t *testing.T) {
	sig := New[int]()

	for i := 0; i < 4; i++ {
		sig.AddListener(func(_ context.Context, _ int) {})
	}

	sig.Emit(context.Background(), 1)

	if sig.workerPool != nil {
		t.Fatalf("Expected worker pool to remain nil for small emits")
	}
}

func TestAsyncSignal_WorkerPoolSizeBounded(t *testing.T) {
	sig := New[int]()

	n := 2*runtime.NumCPU() + 1
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {})
	}

	sig.Emit(context.Background(), 1)

	if sig.poolSize > 2*runtime.NumCPU() {
		t.Fatalf("Expected worker pool size to be bounded, got %d", sig.poolSize)
	}
}

func TestAsyncSignal_ResetStopsWorkerPool(t *testing.T) {
	base := runtime.NumGoroutine()

	sig := New[int]()
	n := 2*runtime.NumCPU() + 5
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {})
	}
	sig.Emit(context.Background(), 1)
	time.Sleep(50 * time.Millisecond)

	sig.Reset()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > base+2*runtime.NumCPU() {
		t.Fatalf("Expected worker goroutines to stop after Reset; baseline=%d after=%d", base, after)
	}
}

func TestAsyncSignal_ResetStopsWorkerPoolAfterMultipleEmits(t *testing.T) {
	base := runtime.NumGoroutine()

	sig := New[int]()
	n := 2*runtime.NumCPU() + 5
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {})
	}
	for i := 0; i < 5; i++ {
		sig.Emit(context.Background(), 1)
	}
	time.Sleep(50 * time.Millisecond)

	sig.Reset()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > base+2*runtime.NumCPU() {
		t.Fatalf("Expected worker goroutines to stop after Reset; baseline=%d after=%d", base, after)
	}
}

func TestAsyncSignal_WorkerPoolStopsAfterGC(t *testing.T) {
	base := runtime.NumGoroutine()

	func() {
		sig := New[int]()
		n := 2*runtime.NumCPU() + 5
		for i := 0; i < n; i++ {
			sig.AddListener(func(_ context.Context, _ int) {})
		}
		sig.Emit(context.Background(), 1)
	}()

	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	if after > base+2*runtime.NumCPU() {
		t.Fatalf("Expected worker goroutines to stop after GC; baseline=%d after=%d", base, after)
	}
}
