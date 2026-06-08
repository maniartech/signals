# PR #14 Review Notes — `pr-14-fixes` branch

Working notes from the review of [PR #14](https://github.com/maniartech/signals/pull/14)
(joshuafuller: "Fix async semantics, zero-value safety, keyed removal, README").
Intended as context for continuing this work in Claude Code.

## Current Status

- **`master`** — untouched, in sync with `origin/master` (`695e6ad`).
- **`pr-14`** — Joshua's original 5 commits, fetched verbatim from `pull/14/head`.
- **`pr-14-fixes`** — Joshua's 5 commits + 1 maintainer "Review fixes" commit (`8aa812b`). **This is the branch to test, review, and merge.**
- Nothing has been merged yet. Tests have not been executed yet (pending `go test` run on Windows).

## What PR #14 Changes (accepted direction)

1. **`AsyncSignal.Emit` is now fire-and-forget** — schedules each listener in its own goroutine and returns immediately. Previously it (surprisingly) waited for all listeners. Agreed this matches the "Async" name. **Breaking behavior change.**
2. **`SyncSignal.Emit` now invokes error-returning listeners**, discarding their errors. `TryEmit` is unchanged (stops on first error / canceled context).
3. **Zero-value safety** — `var sig signals.SyncSignal[int]` works without a constructor (`sync.Once` lazy init).
4. **Keyed-removal bug fix** — `RemoveListener("")` no longer removes unkeyed listeners (new `keyed bool` field). Real bug on master.
5. **`SignalOptions.GrowthFunc` now actually used** — it was dead code on master (stored, never called).
6. **Removed perf machinery** (worker pool, `sync.Pool`, single-listener fast path) and rewrote README to drop sub-10ns/zero-alloc claims. Async emit now allocates (goroutine per listener).
7. Both `Emit` variants skip all listeners when the context is already canceled.

## Maintainer Fixes Added on Top (commit `8aa812b`)

- **`AsyncSignal.EmitAndWait(ctx, payload)`** — concurrent listeners, but blocks until all finish. This is the escape hatch for users relying on the old blocking `Emit`.
- **`signals.SetPanicHandler(func(recovered any))`** — replaces the PR's silent `_ = recover()`. Default logs via stdlib `log`. Race-safe (`atomic.Pointer`). Pass `nil` to discard.
- **De-flaked tests**:
  - `TestAsyncSignal_ContextTimeoutStopsListeners` now waits on `<-ctx.Done()` instead of sleeping 1ms past a 1ns deadline (was the likely Windows failure — timer granularity).
  - `TestAsyncSignal_SingleListenerIsAsync` — removed 50ms wall-clock assertion (gate logic already proves non-blocking).
  - Goroutine-leak tests poll with a 2s deadline instead of one fixed 50ms sleep.
- **`TestSyncSignal_EmitIgnoresErrorListeners`** (master test asserting old semantics) renamed/updated to assert the new contract.
- **README** — restored ManiarTech production-use note, documented `EmitAndWait`, added migration section.
- **RELEASENOTES.md** — new "Unreleased" section leading with the breaking changes.

## Do's and Don'ts

### Merging

- **DO** merge with `--no-ff`:
  ```
  git checkout master
  git merge --no-ff pr-14-fixes
  git push origin master
  ```
  The merge commit keeps the branch history visible, and GitHub will automatically mark PR #14 as **Merged** and credit Joshua.
- **DON'T squash-merge.** Squashing replaces Joshua's commits with a single new commit — GitHub will NOT mark PR #14 merged and he loses commit credit.
- **DON'T rebase or amend his 5 commits.** Changed hashes ⇒ GitHub can't detect the merge. His commits must land in master byte-for-byte (`0c05756`, `746bcd5`, `9187cd9`, `d512619`, `834741c`).
- **DON'T merge before tests pass**:
  ```
  go test ./... -count=1
  go test ./... -race
  ```
- **DON'T delete the `pr-14` / `pr-14-fixes` branches until the PR shows "Merged" on GitHub.**

### Versioning

- **DO** release as **`v1.4.0`** (minor bump) after merge. Decided against `/v2`: Go's major-version mechanism (new module path, consumer import churn) is too heavy for this library; compilation doesn't break and `EmitAndWait` is a one-line migration.
- **DO** lead the GitHub Release description with the breaking `Emit` change (it's a *silent runtime* change — users won't get a compile error).
- **DO** update `v1.4-requirements.md` — it currently promises "All v1.3.x code works unchanged", which this release deviates from.
- **DON'T** tag before deciding whether the v1.4 quick wins (below) go into the same release.

### Code conventions noted during review

- Run `gofmt -s` before committing (repo standard). Trailing whitespace in `signals_concurrency_test.go` was cleaned in the fixes commit.
- Avoid timing-based test assertions (fixed sleeps, wall-clock bounds); prefer channels, `<-ctx.Done()`, and polling with deadlines — flaky on Windows otherwise.
- Don't swallow panics/errors silently anywhere; route through `SetPanicHandler` or return them.

## After the Merge

1. Verify PR #14 flips to "Merged" on GitHub; leave a thank-you comment for @joshuafuller.
2. Optionally implement the v1.4 quick wins (below) as a follow-up commit/PR.
3. Update `v1.4-requirements.md` (backward-compat wording).
4. Tag and release `v1.4.0` with the RELEASENOTES "Unreleased" section as the body.
5. Note: repo currently has **no working CI** — PR #14 showed "0 checks". CircleCI config uses a Go 1.18 image but `go.mod` requires 1.21. Consider adding a simple GitHub Actions workflow (`go test ./... -race` + `gofmt -l`) so future PRs get checked automatically.

## Roadmap Discussed

**Quick wins — candidates for v1.4.0** (small, low-risk; the new `keyed` flag helps):

- `AddOnce()` / `AddOnceWithKey()` — auto-remove after first invocation
- `Keys()` / `HasKey()` — listener introspection
- `AddListenerWithErr` on `AsyncSignal` (per v1.4-requirements; errors routed like panics)

**Deferred to v1.5** (each deserves its own release):

- Lock-free copy-on-write architecture (would restore the performance story the PR removed)
- `Stats()` / metrics, optional Prometheus exporter
- Worker pool configuration (`SignalOptions.WorkerPoolSize`)

## Useful Commands

```
git log --format="%h %an %s" master..        # the 6 commits pending merge
git diff master..pr-14-fixes                 # full change vs master
git diff pr-14..pr-14-fixes                  # only the maintainer fixes on top of the PR
```
