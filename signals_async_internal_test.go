package signals

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestDefaultPanicHandler covers the default panic handler deterministically (the
// global handler is swapped by other tests, so cover it by direct invocation), and
// restores it as the active handler afterward to limit cross-test global-state
// pollution from blackbox tests that set a custom/nil handler.
func TestDefaultPanicHandler(t *testing.T) {
	defaultPanicHandler("coverage: default panic handler logs this")
	handleListenerPanic("coverage: routed via handleListenerPanic")
	SetPanicHandler(defaultPanicHandler) // restore the library default
}

func TestAsyncSignal_NoGoroutineLeakAfterEmit(t *testing.T) {
	base := runtime.NumGoroutine()

	sig := New[int]()
	var wg sync.WaitGroup
	n := 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {
			wg.Done()
		})
	}

	sig.Emit(context.Background(), 1)
	wg.Wait()
	// Poll with a deadline instead of a fixed sleep to avoid flakiness on
	// slow or heavily loaded machines.
	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > base+5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > base+5 {
		t.Fatalf("Expected goroutine count to return near baseline; baseline=%d after=%d", base, after)
	}
}

func TestAsyncSignal_NoGoroutineLeakAfterManyEmits(t *testing.T) {
	base := runtime.NumGoroutine()

	sig := New[int]()
	var wg sync.WaitGroup
	n := 20
	for i := 0; i < n; i++ {
		sig.AddListener(func(_ context.Context, _ int) {})
	}

	for i := 0; i < 100; i++ {
		wg.Add(n)
		sig.Reset()
		for j := 0; j < n; j++ {
			sig.AddListener(func(_ context.Context, _ int) {
				wg.Done()
			})
		}
		sig.Emit(context.Background(), 1)
	}
	wg.Wait()
	// Poll with a deadline instead of a fixed sleep to avoid flakiness on
	// slow or heavily loaded machines.
	deadline := time.Now().Add(2 * time.Second)
	after := runtime.NumGoroutine()
	for after > base+5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > base+5 {
		t.Fatalf("Expected goroutine count to return near baseline; baseline=%d after=%d", base, after)
	}
}
