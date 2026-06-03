<!--
Before opening: please read CONTRIBUTING.md.
This library accepts a NARROW scope of changes. Public API changes, behavioral/
semantic changes, and architectural changes are maintainer-led and require an
APPROVED issue first. PRs that do these without one will be closed.
-->

## Linked issue

Closes #<!-- issue number — required for anything beyond a trivial typo/doc fix -->

## What this PR does

<!-- One concern per PR. Describe the single specific fix. -->

## Scope checklist (required)

- [ ] This PR addresses **one** specific, filed issue.
- [ ] It does **not** change any **public/exported interface** (the `Signal[T]`
      interface, `New`/`NewSync`, signal methods, `SignalOptions`, exported types).
- [ ] It does **not** change **runtime behavior or semantics** of existing code
      (emit/ordering/error/cancel/panic behavior).
- [ ] It does **not** make **architectural / library-level** changes (concurrency
      model, storage, allocation strategy, dependencies, module path).
- [ ] It does **not** add or modify **performance claims** without a committed,
      reproducible benchmark.

> If you checked any box as **"it does change this"**, STOP: that change is
> maintainer-led and needs an approved issue first. Link it here:
> Approved-by-issue: #____

## Tests

- [ ] `go test ./... -race` passes
- [ ] `gofmt -l .` is clean
- [ ] For a bug fix: a test that fails without the change and passes with it
