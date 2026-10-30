# Commands

Every command `lateregate` takes, what it reads, and what it writes. Run
them through the tool pin, `go tool lateregate <command>`, so the version
is the one `go.mod` names.

| Command | What it does |
| --- | --- |
| `lateregate [-all]`, `lateregate check [-all]` | runs every gate that applies, reports all of them, and exits non-zero if any failed; on a laptop the four slow gates read only [what changed since the default branch](#what-a-check-reads), and `-all` reads the whole tree |
| `lateregate list [-json]` | prints the plan: each gate with `RUN`, `SKIP`, `WAIV` or `FOLD` and why |
| `lateregate <gate>` | runs one gate; [`gates.md`](gates.md) lists them |
| `lateregate contract` | reports every way the repository's wiring drifted from the shared shape |
| `lateregate init` | writes the wiring `contract` checks, where it is missing |
| `lateregate hook` | the pre-commit checks over the staged Go files |
| `lateregate prepush` | the pre-push: refuses a release tag with no changelog section, then lints the packages the push changes |
| `lateregate golangci` | renders the shared `.golangci.yml` without linting |
| `lateregate enum-typescript-prepare` | installs the configured frontend projects' dependencies from their tracked lockfiles |
| `lateregate identity family -repos DIR [-expect FILE]` | checks the identity blocks of every checkout under `DIR` against each other and the issuer's client registry |
| `lateregate release-notes TAG [REF]` | prints the `CHANGELOG.md` section for `TAG`, or fails |
| `lateregate release [-force-red] vX.Y.Z` | refuses while CI is red, runs the bar, moves the notes into the tag's section, commits, tags and pushes; see [`releasing.md`](releasing.md) |

Flags every command accepts:

| Flag | Meaning |
| --- | --- |
| `-C DIR` | the repository root holding `.lateregate.yaml`; default the working directory |
| `-go PATH` | the Go toolchain to run; default `go` |
| `-json` | `list` only: print the plan as JSON |
| `-profile FILE` | `cover` only: read this profile instead of collecting one; repeat it per test tier |
| `-w` | `license` only: write the declared notice on every file that has none |
| `-all` | `check` only: read the whole tree rather than what changed since the default branch |
| `-force-red` | `release` only: cut over a red CI, printing every finding it overrides |

`tempdir` takes the command to watch after `--`:

```sh
go tool lateregate cover -profile=out/unit.out -profile=out/integration.out
go tool lateregate tempdir -- go test -tags corpus ./...
go tool lateregate license -w
```

## What a check reads

On a laptop, `lateregate` checks what changed since the default branch.
The default branch already passed the whole bar in CI, which runs every
gate on every push, so what a push can break is what it changes and what
depends on that.

The change is every path that differs from the merge base of `HEAD` and
`origin/HEAD` (or `origin/main`, where the clone never recorded
`origin/HEAD`), committed or not, and every untracked path git does not
ignore. Each path belongs to the package whose directory is its nearest,
so a file a package embeds and the testdata its tests read count as that
package's change. A path under no package, such as a frontend or the
changelog, belongs to none.

Four gates narrow; the rest read the whole tree, since each is seconds:

| Gate | What it reads on a laptop |
| --- | --- |
| `suite` | the changed packages and every package whose build or tests import one; the profile measures the changed packages, and the floor applies to them. Skipped when no package changed |
| `lint` | the changed packages. Skipped when none changed |
| `vuln` | skipped; CI scans on every push, and a dependency change checks the whole tree |
| `lean` | skipped unless a changed path is in a Lake project |

The whole tree is checked, with the reason on the first line, when
`go.mod`, `go.sum`, `go.work`, `go.work.sum`, `.lateregate.yaml` or a
repository-owned `.golangci.yml` changed, when a package was removed or
moved, or when the change cannot be measured: no default branch, no merge
base, or a module `go list` cannot list. `-all` checks the whole tree on
request. A runner (`CI` set), `release`, a gate run by name and `list`
always read the whole tree.

The run opens with what it read and ends its verdict on it:

```
checking what changed since origin/main (78d4428effa9): 2 files, 1 Go package, 5 packages with what imports them; -all checks the whole tree
...
PASS lint         over the 1 package the change touches
SKIP vuln         no dependency changed since origin/main (78d4428effa9)
PASS suite        over the 5 packages the change reaches; the floor on the 1 package it changes
10 gates passed on what changed since origin/main (78d4428effa9)
```

A test that reads a file outside its package's directory by a relative
path, such as `../docs`, depends on it in a way the import graph does not
record, so a change to that file alone can pass on a laptop and fail in
CI. Run `-all` before a push that changes such a file.

## The plan: `list`

`list` prints what `lateregate` would do in this repository without doing
it:

```
RUN  fmt-check
SKIP depcheck     depcheck.packages names no package
WAIV race         waived until 2026-11-15: the suite is not race-clean in runner
FOLD test         into suite
```

`list -json` is the same plan as an array of objects with `name`,
`status` (`run`, `skip`, `waived`, `folded`), and `reason` or `into` where
they apply. The reusable workflow in `latere-ai/ci` builds its job matrix
from it.

## The wiring: `contract` and `init`

Several files connect the binary to the places it is invoked from, and
each is a place to drift. `contract` reads all of them and names every
difference in one run:

- exactly one workflow calls `latere-ai/ci/.github/workflows/lateregate.yml@v1`,
  on push to `main` and on pull requests, with a top-level `concurrency`
  whose group names `github.ref` and which sets `cancel-in-progress: true`,
  so a push cancels the run of the commit it supersedes
- `.githooks/pre-commit` is executable and runs `lateregate hook`
- `.githooks/pre-push` is executable and runs `lateregate prepush`
- `.golangci.yml` is not tracked, unless `golangci.own` declares it with a
  reason
- `.gitignore` lists `.golangci.yml` and `coverage.out`
- `CHANGELOG.md` is tracked and has a `## Unreleased` heading
- `.lateregate.yaml` has an `identity` block
- `.lateregate.yaml` restates no default
- a `Makefile` target named for a gate or a command (`cover`, `test-race`,
  `lint`, `release`, ...) delegates to `lateregate`, or does not exist
- `go.mod` carries the `tool` line for `latere.ai/x/ci-gate/cmd/lateregate`

Nothing here is waivable. A waiver says a repository is behind on a gate;
wiring is fixed in the commit that notices it.

`init` writes what `contract` checks, where it is missing:

- `.github/workflows/ci.yml`, the caller, when no workflow calls the
  reusable pipeline yet;
- `.githooks/pre-commit`, and `.githooks/pre-push` when there is none;
- `git config core.hooksPath .githooks`;
- `.golangci.yml` and `coverage.out` in `.gitignore`;
- `CHANGELOG.md` with an `## Unreleased` heading, when there is none.

It never touches `Makefile` or `.lateregate.yaml`, because those hold
decisions, and it never overwrites a pre-push or a changelog that exists:
a pre-push that does not delegate holds a check somebody wrote, and a
changelog holds notes, so each is edited by hand and `contract` reports it
until then. The caller it writes is:

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}-${{ github.event_name }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  gate:
    uses: latere-ai/ci/.github/workflows/lateregate.yml@v1
```

## The pre-commit: `hook`

`hook` runs every gate that is a file scan, over the staged Go files, then
the modernizers over the packages holding them. In order: gofmt;
goimports grouping with the module path as the local prefix; the license
notice, when `license.spdx` is declared; the outbound HTTP instrumentation
rule, leaving out the directories `otel_client.skip` names as the gate
does; then `go fix`, reading `modernize.disable` from the same config the
full gate reads. Each scan reads only the staged files, so the hook takes
a few seconds. The hook script is one line that calls it:

```sh
#!/bin/sh
exec go tool lateregate hook
```

## The pre-push: `prepush`

`prepush` reads the refs git hands the hook on stdin. A push to a release
tag (`refs/tags/vMAJOR.MINOR.PATCH`, with an optional prerelease or build
suffix) is refused unless `CHANGELOG.md` at the pushed commit has a
section for it. The ref the remote receives decides, not the local ref's
name, so `git push origin HEAD:refs/tags/v1.2.3` and a commit sha pushed
straight to the tag are checked as `git push origin v1.2.3` is. A tag
deletion and a moving major tag such as `v1` are not checked. Then
golangci-lint runs over the packages the push changes, against the config
rendered first as the full gate renders it: each commit the push sends to a
branch on the remote (`refs/heads/*`) is diffed against the remote's commit
(or the merge base with `origin/main` for a branch the remote does not have
yet), and exactly the packages the changed Go files sit in are linted. A
push with no merge base either, the first push to an empty remote, lints
the packages of every Go file the pushed commit holds, and goes through
when it holds none. The
remote ref decides, not the local ref's name, so `git push origin HEAD:main`
from a worktree is linted as `git push origin main` is. A tag push and a
branch deletion lint nothing.

golangci-lint takes a machine-wide lock by default; every lint run opts
out of it with `--allow-parallel-runners`, this one and the full gate's,
so a lint in another checkout neither blocks a push nor aborts it. Linting runs here and not in the pre-commit
because a push is rare and already waits on the network, and a hook that
adds a minute to every commit is one people bypass. A dated `waive: lint`
covers the hook as it covers the full gate: until the day it names, the
push prints `lint is waived until <date>; nothing to lint`.

The hook script captures stdin once, so a repository that adds its own
check below the delegation reads `$refs`, not stdin:

```sh
#!/bin/sh
refs=$(cat)
printf '%s\n' "$refs" | go tool lateregate prepush || exit 1
```

`vuln`, `suite` and the five gates it carries are in neither hook: the
first needs the network and can change verdict with no commit, and the
rest run the whole suite.

## Cross-repository identity: `identity family`

```sh
go tool lateregate identity family -repos ../checkouts -expect docs/layers.md
```

Reads one checkout per repository under `-repos` and fails on a
repository with no identity block, an audience two repositories claim, an
audience a repository verifies and the issuer's client registry does not
list, an audience the registry lists and nobody verifies, and a client
presenting an audience nothing accepts. It prints the layer table the
blocks derive, and `-expect` fails when the committed copy of that table
differs. [`gates.md`](gates.md#identity-the-familys-identity-shape)
describes the block each repository declares.
