package signals_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

func TestAsyncSignal_EmitAndWaitWaitsForAllListeners(t *testing.T) {
	sig := signals.New[int]()

	var finished int32
	const n = 10
	for i := 0; i < n; i++ {
		sig.AddListener(func(ctx context.Context, v int) {
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&finished, 1)
		})
	}

	sig.EmitAndWait(context.Background(), 1)

	if got := atomic.LoadInt32(&finished); got != n {
		t.Fatalf("Expected all %d listeners to finish before EmitAndWait returns, got %d", n, got)
	}
}

func TestAsyncSignal_EmitAndWaitRunsListenersConcurrently(t *testing.T) {
	sig := signals.New[int]()

	var inFlight int32
	var sawOverlap int32
	ready := make(chan struct{}, 2)
	release := make(chan struct{})

	listener := func(ctx context.Context, v int) {
		if atomic.AddInt32(&inFlight, 1) > 1 {
			atomic.StoreInt32(&sawOverlap, 1)
		}
		ready <- struct{}{}
		<-release
		atomic.AddInt32(&inFlight, -1)
	}
	sig.AddListener(listener)
	sig.AddListener(listener)

	done := make(chan struct{})
	go func() {
		sig.EmitAndWait(context.Background(), 1)
		close(done)
	}()

	<-ready
	<-ready
	close(release)
	<-done

	if atomic.LoadInt32(&sawOverlap) == 0 {
		t.Fatal("Expected EmitAndWait listeners to run concurrently")
	}
}

func TestAsyncSignal_EmitAndWaitSkipsWhenContextCanceled(t *testing.T) {
	sig := signals.New[int]()

	var called int32
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&called, 1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sig.EmitAndWait(ctx, 1)

	if atomic.LoadInt32(&called) != 0 {
		t.Fatalf("Expected no listener calls when context is canceled, got %d", called)
	}
}

func TestAsyncSignal_EmitAndWaitZeroValueUsable(t *testing.T) {
	var sig signals.AsyncSignal[int]

	var called int32
	sig.AddListener(func(ctx context.Context, v int) {
		atomic.AddInt32(&called, 1)
	})

	sig.EmitAndWait(context.Background(), 1)

	if atomic.LoadInt32(&called) != 1 {
		t.Fatalf("Expected zero-value AsyncSignal EmitAndWait to invoke listener, got %d", called)
	}
}

func TestSetPanicHandlerReceivesListenerPanic(t *testing.T) {
	got := make(chan string, 1)
	signals.SetPanicHandler(func(recovered any) {
		select {
		case got <- fmt.Sprintf("%v", recovered):
		default:
		}
	})
	// Restore the default behavior of discarding into the log afterwards.
	defer signals.SetPanicHandler(nil)

	sig := signals.New[int]()
	sig.AddListener(func(ctx context.Context, v int) {
		panic("boom")
	})

	// EmitAndWait guarantees the panicking listener has finished.
	sig.EmitAndWait(context.Background(), 1)

	select {
	case r := <-got:
		if r != "boom" {
			t.Fatalf("Expected panic handler to receive \"boom\", got %q", r)
		}
	case <-time.After(time.Second):
		t.Fatal("Expected panic handler to be invoked")
	}
}
