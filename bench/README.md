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
