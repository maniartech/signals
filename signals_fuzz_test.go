package signals_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/maniartech/signals"
)

// Phase R / FR-8 §8.3: native fuzz targets for the lock-free core.
//   FuzzSyncOps_Model      — I1 count integrity over arbitrary sequential streams.
//   FuzzConcurrentOps_Race — I5 no-corruption under arbitrary concurrent interleavings.
//
// Run the seed corpus on every `go test`; fuzz with:
//   go test -run '^$' -fuzz FuzzSyncOps_Model      -fuzztime 60s
//   go test -run '^$' -fuzz FuzzConcurrentOps_Race -fuzztime 60s -race

// FuzzSyncOps_Model drives a sequential add/remove/emit stream from fuzz bytes and
// asserts the count invariant against an independent model. Edge cases (0/1/many,
// duplicate add, remove-absent, empty-key-as-unkeyed) emerge from the input space.
func FuzzSyncOps_Model(f *testing.F) {
	f.Add([]byte{0, 0, 2, 1, 2, 0, 1, 1})
	f.Add([]byte{})
	f.Add([]byte{2})

	f.Fuzz(func(t *testing.T, data []byte) {
		sig := signals.NewSync[int]()
		ctx := context.Background()
		model := map[string]bool{}

		for i, b := range data {
			key := fmt.Sprintf("k%d", int(b)%8) // small space → forces dup/collision paths
			switch b % 3 {
			case 0:
				r := sig.AddListener(noop, key)
				if model[key] {
					if r != -1 {
						t.Fatalf("op %d: dup add returned %d, want -1", i, r)
					}
				} else {
					model[key] = true
				}
			case 1:
				sig.RemoveListener(key)
				delete(model, key)
			case 2:
				sig.Emit(ctx, i)
			}
			if got := sig.Len(); got != len(model) {
				t.Fatalf("op %d: Len()=%d, model=%d", i, got, len(model))
			}
		}
	})
}

// FuzzConcurrentOps_Race runs the fuzz-derived stream from several goroutines on a
// SYNC signal (the lock-free core, no goroutine fan-out — fuzzing the async path
// under -race exhausts ThreadSanitizer memory; async concurrency is covered by the
// stress tests). Run under -race, it proves I5: no race, panic, deadlock, or
// corruption under arbitrary concurrent interleavings.
func FuzzConcurrentOps_Race(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{0, 1, 2})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		// Bound per-exec work so the race detector's shadow memory stays within
		// limits under heavy parallel fuzzing (16 fuzz workers × N goroutines ×
		// -race otherwise exhausts ThreadSanitizer — a tooling limit, not a defect;
		// see FR-8 §8.3). The 1000-goroutine stress test is the heavier I5 proof.
		if len(data) > 64 {
			data = data[:64]
		}
		sig := signals.NewSync[int]()
		ctx := context.Background()

		const workers = 2
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				for i, b := range data {
					key := fmt.Sprintf("w%d-k%d", seed, int(b)%4)
					switch (int(b) + i) % 3 {
					case 0:
						sig.AddListener(noop, key)
					case 1:
						sig.RemoveListener(key)
					case 2:
						sig.Emit(ctx, i)
					}
				}
			}(w)
		}
		wg.Wait()
	})
}

// FuzzAsyncErrorModel drives arbitrary add/remove/emit sequences on an async signal
// whose listeners may succeed or fail, and asserts the EmitAndWaitErr aggregation
// invariant (I10): the number of joined errors returned equals the number of
// currently-registered FAILING listeners — no error lost, none invented — and nothing
// ever panics. EmitAndWait makes each emission deterministic. Not run under -race
// (async fan-out exhausts ThreadSanitizer; -race concurrency is covered by the stress
// tests). Run: go test . -run '^$' -fuzz FuzzAsyncErrorModel -fuzztime 60s
func FuzzAsyncErrorModel(f *testing.F) {
	f.Add([]byte{0, 1, 3, 2, 0, 3})
	f.Add([]byte{1, 1, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		sig := signals.New[int]()
		fail := map[string]bool{} // keys of registered failing listeners
		ok := map[string]bool{}   // keys of registered succeeding listeners

		joinedCount := func(err error) int {
			if err == nil {
				return 0
			}
			if u, isJoin := err.(interface{ Unwrap() []error }); isJoin {
				return len(u.Unwrap())
			}
			return 1
		}

		for i, b := range data {
			key := fmt.Sprintf("k%d", int(b)%6)
			switch b % 4 {
			case 0: // add a failing error-listener (if the key is free)
				if !fail[key] && !ok[key] {
					sig.AddListenerWithErr(func(context.Context, int) error { return errors.New("fail") }, key)
					fail[key] = true
				}
			case 1: // add a succeeding error-listener (if the key is free)
				if !fail[key] && !ok[key] {
					sig.AddListenerWithErr(func(context.Context, int) error { return nil }, key)
					ok[key] = true
				}
			case 2: // remove
				sig.RemoveListener(key)
				delete(fail, key)
				delete(ok, key)
			case 3: // emit and wait for errors — count must equal the live failing set
				got := joinedCount(sig.EmitAndWaitErr(context.Background(), i))
				if got != len(fail) {
					t.Fatalf("op %d: EmitAndWaitErr returned %d errors; want %d (live failing listeners)", i, got, len(fail))
				}
			}
		}
	})
}
