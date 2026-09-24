# Commands

Every command `lateregate` takes, what it reads, and what it writes. Run
them through the tool pin, `go tool lateregate <command>`, so the version
is the one `go.mod` names.

| Command | What it does |
| --- | --- |
| `lateregate`, `lateregate check` | runs every gate that applies, reports all of them, and exits non-zero if any failed |
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
| `-force-red` | `release` only: cut over a red CI, printing every finding it overrides |

`tempdir` takes the command to watch after `--`:

```sh
go tool lateregate cover -profile=out/unit.out -profile=out/integration.out
go tool lateregate tempdir -- go test -tags corpus ./...
go tool lateregate license -w
```

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
rule; then `go fix`, reading `modernize.disable` from the same config the
full gate reads. Each scan reads only the staged files, so the hook takes
a few seconds. The hook script is one line that calls it:

```sh
#!/bin/sh
exec go tool lateregate hook
```

## The pre-push: `prepush`

`prepush` reads the refs git hands the hook on stdin. A release tag among
them (`vMAJOR.MINOR.PATCH`, with an optional prerelease or build suffix)
is refused unless `CHANGELOG.md` at its commit has a section for it. Then
golangci-lint runs over the packages the push changes, against the config
rendered first as the full gate renders it: each branch is diffed against
the remote's commit (or the merge base with `origin/main` for a new
branch), and exactly the packages the changed Go files sit in are linted.

The linter takes a machine-wide lock by default; this run opts out of it
with `--allow-parallel-runners`, so a lint in another checkout neither
blocks a push nor aborts it. Linting runs here and not in the pre-commit
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
