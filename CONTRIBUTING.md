# Contributing to OpenModelPool

This project belongs to everyone who uses it. You do not need permission to start.

OpenModelPool is **non-profit by construction**. There is no company behind it, no
revenue, and no plan to have either. That shapes what gets merged — see
[What we will not merge](#what-we-will-not-merge).

---

## Quick start

```bash
git clone https://github.com/lisiyu/openmodelpool.git
cd openmodelpool
go build ./...      # single binary, no codegen step, no database to provision
go run .            # starts on :8000, writes state under ./data
```

There is no build system beyond the Go toolchain. If a change requires one, it is
probably the wrong change.

---

## Ways to contribute

| I want to… | Do this |
|------------|---------|
| Propose a feature or challenge a design decision | Open a **Feature / design proposal** issue |
| Report something broken | Open a **Bug report** issue |
| Pick up planned work | The roadmap is public in [docs/BACKLOG.md](docs/BACKLOG.md). Comment on the item to claim it, then send a PR |
| Report a security issue | **Do not open an issue** — see [SECURITY.md](SECURITY.md) |
| Understand the codebase first | [docs/INDEX.md](docs/INDEX.md) is the navigation hub; [docs/reference/openmodelpool-v4-design.md](docs/reference/openmodelpool-v4-design.md) is the architecture |

Small PRs are easier to merge than large ones. A typo fix is a perfectly good
first contribution.

---

## Before you open a PR

Three commands must pass:

```bash
go build ./...
go vet ./...
go test ./...
```

Notes on the test suite, so you do not waste time on someone else's problem:

- The full suite takes **roughly 2 minutes** and runs entirely offline. No API
  keys, no network, no Docker required.
- CI runs the **same suite you do**, with `-race` and `-count=1` added:
  `go test -race -count=1 ./...` is the hard gate. There is no short/full
  split — the codebase has no `//go:build integration` files and no
  `testing.Short()` branches, so nothing is held back from you locally. A
  second, identical run is also executed as a soft gate purely to catch flaky
  tests; it is allowed to fail.
- **On Windows**, real-time antivirus scanning can hold handles on files that a
  test just wrote, which surfaces as `The system cannot find the path specified`
  or a slow `t.TempDir()` cleanup. If a test fails once and passes on a re-run,
  that is the likely cause — check whether it reproduces before filing a bug.
- If you find a test that fails **randomly**, that is a real defect worth
  reporting even if the code under test is fine. A test gate nobody trusts is
  the same as no gate.

---

## Code conventions

**Standard library first.** No web framework, no ORM, no DI container. Routing is
`net/http`, persistence is JSON files under `data/`, config is a struct. The five
direct dependencies (`golang-jwt`, `x/crypto`, `x/net`, `go-bip39`, `chromedp`)
each exist because the standard library has no equivalent. Adding a sixth needs a
reason in the PR description.

**Additive over invasive.** Most features here landed as a new file plus a
nil-safe hook at one call site, so that the existing path is unchanged when the
new subsystem is absent. Prefer that shape — it keeps regressions local and makes
your PR reviewable.

**Do not oversell in docs.** The README marks unfinished work with ⚠️ on purpose;
that honesty is a feature of this project, not an embarrassment. If your PR
implements something previously marked as planned, move the marker. If it only
partly implements it, say so.

**Tests come with the change.** New behaviour needs a test that would fail
without it. Table-driven tests are the house style.

**One behaviour per commit** where practical, with a message that says what
changed and why.

---

## What we will not merge

Anything that introduces a **token, points economy, paid tier, mining, staking,
revenue share, or "incentive layer"**. The contribution ledger is bookkeeping, not
currency: it is 1:1, non-transferable, non-withdrawable, and running out of quota
never blocks anyone — requests fall back to the community free pool.

This is not a temporary stance pending monetisation. Proposals in this direction
will be closed, politely, with a link to this section.

Also out of scope:

- Features that require a hosted service the maintainers would have to run and pay for
- Telemetry or analytics that phone home by default
- Marketing language in code comments or docs that claims capability the code does not have

---

## Review and merge

Expect a first response within a few days. Reviews focus on: does it work, is it
tested, does it keep the dependency footprint small, and does the documentation
match reality afterwards.

Design disagreement is welcome and is best raised in an issue *before* you write
the code, so nobody's evening gets wasted.

---

## License

By contributing you agree that your contribution is licensed under the
[MIT License](LICENSE), same as the rest of the project.
