package signals_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maniartech/signals"
)

func TestAsyncSignal_EmitOverlapsListeners(t *testing.T) {
	sig := signals.New[int]()

	var inFlight int32
	var sawOverlap int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	sig.AddListener(func(ctx context.Context, v int) {
		if atomic.AddInt32(&inFlight, 1) > 1 {
			atomic.StoreInt32(&sawOverlap, 1)
		}
		started <- struct{}{}
		<-release
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
	})
	sig.AddListener(func(ctx context.Context, v int) {
		if atomic.AddInt32(&inFlight, 1) > 1 {
			atomic.StoreInt32(&sawOverlap, 1)
		}
		started <- struct{}{}
		<-release
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
	})

	sig.Emit(context.Background(), 1)

	<-started
	<-started
	close(release)

	if atomic.LoadInt32(&sawOverlap) == 0 {
		t.Fatal("Expected async listeners to overlap in time")
	}
}
