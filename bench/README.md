# Benchmarks & Performance Baseline

This directory holds the **committed performance baseline** for the v1.4 work. The
project rule (see [`v1.4-requirements.md`](../v1.4-requirements.md), FR-0) is:

> No performance number reaches the README until it is produced by a committed
> benchmark and reproducible via `go test -bench`.

`baseline.txt` is the **"before" measurement** — the current `sync.RWMutex` +
per-emit snapshot implementation — captured *prior* to the lock-free copy-on-write
rewrite (FR-0 / Phase 2). Every later performance claim is compared against it with
`benchstat`.

## How to reproduce the baseline

```bash
go test -run '^$' -bench=. -benchmem -count=6 . > bench/baseline.txt
```

## How to compare a change (the honest workflow)

```bash
# after a change (e.g. the lock-free core):
go test -run '^$' -bench=. -benchmem -count=6 . > bench/new.txt
go run golang.org/x/perf/cmd/benchstat@latest bench/baseline.txt bench/new.txt
```

Only differences that `benchstat` reports as significant may be quoted.

## Captured baseline (RWMutex, pre-lock-free)

Environment: `goos: windows · goarch: amd64 · AMD Ryzen 7 5700G` · `go test -count=6`.
Numbers are machine-specific; reproduce locally for your hardware.

| Benchmark | sec/op | B/op | allocs/op |
|---|---|---|---|
| `SyncEmit_SingleListener` | 17.77 ns | 0 | 0 |
| `SyncEmit_TenListeners` | 210.9 ns | 416 | 1 |
| `SyncEmit_Concurrent` | 49.79 ns | 0 | 0 |
| `SignalEmit_SingleListener` (async) | 351.4 ns | 112 | 2 |
| `SignalEmit_ManyListeners` (async, 100) | 28.20 µs | 10.25 Ki | 101 |
| `SignalEmit_Concurrent` (async) | 480.4 ns | 112 | 2 |
| `SignalAddRemoveListener_Concurrent` | 199.2 ns | 4 | 1 |

### What the lock-free core (Phase 2) is expected to change

- **`SyncEmit_TenListeners`**: the `416 B / 1 alloc` is the per-emit heap snapshot of
  the subscriber slice. Copy-on-write removes it → target **0 allocs**.
- **`SyncEmit_Concurrent`**: RWMutex `RLock` contention; `atomic.Pointer` reads should
  improve multi-core scaling.
- **`SyncEmit_SingleListener`**: already 0-alloc (existing 4-element stack fast path);
  removing the lock/snapshot overhead targets ~10–12 ns.

> Async numbers are **dispatch rate** (goroutine-per-listener), not listener
> completion, and are the baseline for the separate async worker-pool work (FR-7).

## Phase 2 result — lock-free copy-on-write (measured)

`lockfree.txt` is the "after" measurement; `lockfree-vs-baseline.txt` is the committed
`benchstat baseline.txt lockfree.txt` comparison. Reproduce both with the workflow
above. Headlines (same hardware as the baseline):

| Benchmark | Before | After | Change |
|---|---|---|---|
| `SyncEmit_TenListeners` | 210.9 ns · 416 B · 1 alloc | 69.8 ns · **0 B · 0 alloc** | **−67% time, snapshot alloc eliminated** |
| `SyncEmit_Concurrent` | 49.8 ns | **3.0 ns** | **−94% (near-linear scaling)** |
| `SyncEmit_SingleListener` | 17.77 ns · 0 alloc | 17.91 ns · 0 alloc | ~unchanged |
| `SignalEmit_*` (async) | — | — | −43% B/op, −50% allocs (snapshot removed) |

**Honest caveats (read these before quoting numbers):**

- **Single-listener sync did not get faster.** The previous code already had a
  zero-allocation 4-element stack snapshot fast path (~17.8 ns); the atomic load is
  comparable. We did **not** reach the aspirational ~10–12 ns. The lock-free win is
  in the **multi-listener** and **concurrent** read paths, not single-listener.
- **Writes got more expensive — by design.** Copy-on-write rebuilds the whole
  subscriber slice on every `AddListener`/`RemoveListener`, so writes are O(n) instead
  of in-place O(1). The `SignalAddRemoveListener_Concurrent` microbenchmark (tight
  add/remove churn against a ~1000-element slice) regresses sharply (~200 ns → ~29 µs,
  +82 KB/op) for exactly this reason. This is the intended "lock-free reads, locked
  writes" trade: a signals library emits orders of magnitude more often than it
  mutates its listener set, so paying O(n) on rare writes to make reads lock-free and
  allocation-free is the correct bargain. Workloads that churn listeners as hot as
  they emit are **not** a good fit for this design.

## v1.4 final numbers (the README perf section)

`v1.4-final.txt` is the full `-count=6` capture of the **complete v1.4 benchmark
suite** (sync emit/try, error-routing, async dispatch + waited + bounded, write
churn) on the same hardware. It is the source for the README "Performance &
Benchmarks" tables. Reproduce with the command at the top of this file. Representative
medians (AMD Ryzen 7 5700G, Windows, Go, `-count=6`):

| Path | Result |
|---|---|
| `SyncEmit` · 1 listener | ~8 ns · 0 allocs |
| `SyncEmit` · 10 listeners | ~33 ns · 0 allocs |
| `SyncEmit` · concurrent (16 threads) | ~1.1 ns · 0 allocs |
| `SyncTryEmit` · 1 listener | ~9 ns · 0 allocs |
| `SyncEmit` · error → `OnError` | ~16 ns · 0 allocs |
| `Emit` (async dispatch) · 1 listener | ~230 ns · 208 B · 2 allocs |
| `Emit` (async dispatch) · 100 listeners | ~24 µs · ~12 KB · ~95 allocs |
| `TryEmit` (async, waited) · 10 listeners | ~4 µs · 1.5 KB · 12 allocs |
| `TryEmit` (async, bounded `MaxConcurrent=4`) · 10 listeners | ~6 µs · 1.5 KB · 12 allocs |
| Add/Remove churn (~1000 listeners, concurrent) | ~23 µs · ~82 KB · 5 allocs |

The async **bounded** `TryEmit` is *slower* than unbounded (~6 µs vs ~4 µs) — the
counting-semaphore bound is a safety valve to protect a slow downstream dependency,
**not** a throughput optimization. Async `Emit` numbers are **dispatch rate**
(goroutine-per-listener scheduling), not listener completion.

