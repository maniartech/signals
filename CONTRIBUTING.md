# Contributing to `signals`

Thank you for your interest in improving `signals`. This library is small,
focused, and used in production — so contributions are accepted under a
**deliberately narrow scope policy**. Please read this before opening a pull
request; it will save us both time.

## TL;DR

- ✅ **Accepted:** small, specific fixes tied to a filed issue — bug fixes, doc
  corrections, test additions, typo/lint fixes.
- ⛔ **Not accepted as unsolicited PRs:** public API / interface changes, and
  large library-level or architectural changes. These are **maintainer-led** and
  must be agreed in an issue *before* any code is written.
- **One PR = one concern.** No drive-by refactors bundled with a fix.

## Why this policy

`signals` has a stable public API and a published behavioral contract that real
applications depend on. A single PR that changes semantics, the interface, or the
internal architecture — even with good intentions — can silently break callers,
invalidate documented guarantees, or commit the project to a direction it hasn't
chosen. We would rather keep the library small and predictable than grow it by
accretion. Scope discipline is a feature, not bureaucracy.

## What requires a maintainer-approved issue FIRST

Open an issue and wait for explicit maintainer agreement **before** writing code
if your change touches any of the following. PRs that do these without a linked,
approved issue will be closed with a pointer back here — regardless of code
quality:

1. **Public interface changes** — adding, removing, renaming, or changing the
   signature of anything exported: the `Signal[T]` interface, `New`/`NewSync`,
   `SyncSignal`/`AsyncSignal` methods, `SignalOptions`, exported types/functions.
2. **Behavioral / semantic changes** — anything that alters what existing code
   does at runtime (emit semantics, ordering, error/cancel handling, panic
   behavior), even if it still compiles. Silent runtime changes are the most
   dangerous kind.
3. **Architectural / library-level changes** — concurrency model (locking,
   lock-free, worker pools), storage strategy, allocation behavior, dependency
   additions, module path, or removing/replacing existing subsystems.
4. **Performance work and any change to README performance claims** — every
   performance number must come from a committed, reproducible benchmark. Do not
   add or edit performance claims without the benchmark that backs them.
5. **New features** — new methods, options, or packages.

These are decided by the maintainers and tracked in the project requirements
docs (e.g. `v1.4-requirements.md`). The roadmap is intentionally maintainer-owned.

## What you can send directly as a PR

No pre-approval needed (though an issue is still appreciated for anything
non-trivial):

- Fixing a **specific, reproducible bug** — include a failing test that the fix makes pass.
- **Documentation** corrections that match existing behavior (no new claims).
- **Tests** that increase coverage of existing behavior without changing it.
- Typo, formatting (`gofmt -s`), and lint fixes.

## Ground rules for any PR

- **Link an issue.** State which issue this addresses.
- **One concern per PR.** Don't mix a bug fix with refactors, reformatting, or
  unrelated cleanups — they make review and revert harder.
- **No public API or behavior change** unless the linked issue explicitly
  approved it (see above).
- **Tests must pass:** `go test ./... -race` and `gofmt -l .` clean.
- **Don't restructure files** or rename symbols as a side effect of a fix.
- **Match the surrounding code** — style, naming, comment density.

## A note on larger ideas

Big ideas are welcome — as **issues**, not surprise PRs. If you want to help with
maintainer-led work (e.g. an item on the roadmap), say so in the issue and we can
scope a contribution that will actually land. A large PR opened without that
agreement is very likely to be declined no matter how good it is, simply because
it commits the project to decisions that aren't the contributor's to make.

Thanks for understanding — and for helping keep `signals` small and dependable.
