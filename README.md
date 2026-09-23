# latere-ai/ci-gate

The per-push quality bar every Latere Go repository shares, as one binary
you pin in `go.mod` and run with no arguments.

`latere-ai/ci` owns the workflow that runs it on a runner. This repo owns
what the bar asserts, so the two version independently and a gate runs the
same on your laptop as it does in CI.

That last part is the point. Every gate here exists because something passed
locally and failed in CI, or passed in CI and meant nothing.

## Quick start

```sh
go get -tool latere.ai/x/ci-gate/cmd/lateregate
go tool lateregate init      # the workflow caller, both hooks, two gitignore lines, the changelog
go tool lateregate           # the whole bar
```

That is the adoption. There is no Makefile contract and no config to write
for the gates themselves: the binary decides which gates apply by looking at
the tree, and runs them all. A `Makefile` target is a convenience:

```make
check:
	@go tool lateregate
```

## The bar

`lateregate` with no arguments runs every gate below that applies, does not
stop at the first failure, and puts the summary last:

```
PASS fmt-check
PASS modernize
SKIP spec-lint    tracks no specs/ files
FAIL suite        the suite's coverage is under the floor: below 90%: internal/store 71.2%, ... (without -race (race waived until 2026-11-15: the suite is not race-clean in runner))
FOLD test         into suite
FOLD race         into suite, waived until 2026-11-15: the suite is not race-clean in runner
FOLD hermetic     into suite
FOLD tempdir      into suite
FOLD cover        into suite
lateregate: 1 of 13 gates failed: suite
```

| Gate | What it asserts | Applies when |
| --- | --- | --- |
| `fmt-check` | no tracked Go source is unformatted | always |
| `modernize` | no code that a standard library call already covers | always |
| `cgo-free` | no Go file imports `"C"` | always |
| `otel-client` | no outbound HTTP client is built without a tracing transport | always |
| `license` | every source file carries the SPDX notice the repo declared | always; needs `license.spdx` |
| `spec-lint` | the spec tree agrees with itself and with its index | git tracks `specs/` |
| `depcheck` | no build reaches a dependency nobody admitted | `depcheck.packages` names one |
| `registers` | no developer sentence in a string handed to a user-surface function | `registers.user_surfaces` names one |
| `identity` | the repository declares its identity role and holds that role's rules | always |
| `postgres` | the repository's Postgres role holds: no client under `none`, a dated waiver under `direct`, the pooled and direct DSN names read under `pooled`, an undeclared client caught by its imports | always |
| `enum-go` | declared Go domains use named types, named members and exhaustive switches | `enums.go.types` names a domain |
| `enum-typescript` | declared TypeScript domains use native enums, named members and exhaustive switches | `enums.typescript` names a project |
| `lint` | golangci-lint at the pinned version, against the shared config it renders first | always |
| `vuln` | govulncheck at the pinned version finds no reachable vulnerability | always |
| `suite` | `go vet`, then one run of the suite that holds the five properties below | always; a waived `test` waives it |
| `test` | `go vet` and the suite | folded into `suite` |
| `race` | the suite under the race detector | folded into `suite` |
| `hermetic` | the suite passes with only the toolchain on `PATH` | folded into `suite` |
| `tempdir` | the suite leaves nothing behind under `TMPDIR` | folded into `suite`, unless `tempdir.command` names another runner |
| `cover` | **every package** clears the floor, not the repository average | folded into `suite` |

The recipes are in the binary. `test` is `go vet ./...` then `go test
./...`; `race` sets `CGO_ENABLED=1`; `cover` collects with `-coverpkg=./...
-covermode=atomic`; `lint` and `vuln` run their tools through `go run` at a
version pinned here, so one commit moves every repository. Four
repositories used to hold four `cover` recipes, one of which wrote the
profile to a name the gate never read.

`suite` runs the suite once with every property on: `go vet ./...`, then
`go test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out
./...` with `CGO_ENABLED=1`, inside the `tempdir` sandbox and on the
`hermetic` PATH, then the floor over the profile and the check for
survivors. Five separate runs compiled and ran the suite five times to learn
the same five facts; `-race` already forces atomic coverage, and a sandbox
or a stripped PATH wraps whatever runs inside it. The first line of a
failure names the property: `the suite is not race-clean`, `the suite
reached for docker, which is not on the stripped PATH`, `the suite left 2
entries under TMPDIR`, `the suite's coverage is under the floor`. The race
detector needs cgo, and cgo needs the compiler the toolchain names in `CC`
and the `as` and `ld` it calls. Under `-race` the stripped PATH reaches them
through a shim: a directory under the machine's `TMPDIR` that links those
three and nothing else, at the same path on every run. The compiler's own
directory stays off the PATH, since on Linux that is `/usr/bin`, with `git`
and `docker` in it. The `PATH=` line names the shim and what it holds. Every
flag is one the go command's test cache accepts, so an unchanged package
replays its result.

The plan marks the five `folded` into `suite`, and `lateregate` runs the
suite in their place. A waiver on one of them narrows the run instead of
skipping it: a waived `race` drops `-race`, a waived `hermetic` keeps the
full PATH, a waived `tempdir` runs outside the sandbox, a waived `cover`
keeps no floor, and a waived `test` waives the suite. The suite's plan line
says what is narrowed. Each of the five still runs by name,
`lateregate race`, for a developer isolating one property.

`lateregate <gate>` runs one, for a CI job or a developer chasing a single
failure. `lateregate list` prints the plan, and `list -json` is what the
pipeline builds its job matrix from.

### Waivers

A gate that applies runs unless the repository has written down that it is
behind, and by when:

```yaml
waive:
  cover:
    reason: the tree is at 82.2% and the gap is in handler and runner
    until: 2026-11-15
```

Both fields are mandatory, and the date is the half that matters. A reason
alone becomes wallpaper: seventeen well-argued waivers is a bar written down
and abandoned. `until` is inclusive. After it, the gate runs and fails on its
own terms, and the summary says the waiver ran out.

A waiver keyed on a name that is not a gate fails the load: a waiver for a
gate nobody runs hides a typo in the name of a gate somebody does.

### What `.lateregate.yaml` is for

Decisions, each with its reason. Every value the tool can decide for a
repository it decides by default, so a key in this file is one somebody
chose: a coverage exemption, a spec vocabulary, a hermetic allowance, a
dependency allowlist, a license, a waiver. A key that restates its default
is reported by `contract` with "delete it": a restated default is the line
the next default change makes wrong.

The defaults:

| Key | Default |
| --- | --- |
| `cover.threshold` | 90 |
| `modernize.disable` | `[newexpr, errorsastype]`: both fixers emit code that does not compile or half-applies |
| `spec.dir` | `specs` |
| `spec.require` | `[title, status]` |
| `spec.index` | `specs/README.md` when the file exists |
| `golangci.sloglint` | `context: scope`, every package: where a context is in hand, the `*Context` variant is right |

An unknown key is an error rather than a silently ignored one, because a
typo that disables a gate is the failure this whole repository is against.

## The wiring: `contract`, `init`, `hook`

Five files connect the binary to the places it is invoked from, and each is
a place to drift. `lateregate contract` reads all of them and names every
difference in one run:

- exactly one workflow calls `latere-ai/ci/.github/workflows/lateregate.yml@v1`,
  on push to `main` and on pull requests, with a top-level `concurrency`
  whose group names `github.ref` and which sets `cancel-in-progress: true`,
  so a push cancels the run of the commit it supersedes
- `.githooks/pre-commit` is executable and runs `lateregate hook`
- `.githooks/pre-push` is executable and runs `lateregate prepush`
- `.golangci.yml` is not tracked, unless `golangci.own` declares it with a reason
- `.gitignore` lists `.golangci.yml` and `coverage.out`
- `CHANGELOG.md` is tracked and has a `## Unreleased` heading
- `.lateregate.yaml` restates no default
- a `Makefile` target named for a gate or a command (`cover`, `test-race`,
  `lint`, `release`, ...) delegates to `lateregate`, or does not exist
- `go.mod` carries the `tool` line

Nothing here is waivable. A waiver says a repository is behind on a gate;
wiring is fixed in the commit that notices it.

`lateregate init` writes what `contract` checks: the caller, both hooks, the
gitignore lines, the seed changelog, and `git config core.hooksPath
.githooks`. It never touches `Makefile` or `.lateregate.yaml`, because those
hold decisions, and it never overwrites a pre-push or a changelog that
exists: a pre-push that does not delegate holds a check somebody wrote, and
a changelog holds notes, so each is edited by hand and `contract` reports
it until then.

`lateregate hook` is the pre-commit: every gate that is a file scan, over the
staged Go files, then the modernizers over the packages holding them. In
order, gofmt; goimports grouping with the module path as the local prefix,
which is the linter's rule and the failure it most often reports; the
license notice, when `license.spdx` is declared; the outbound-HTTP
instrumentation rule; then `go fix`, reading `modernize.disable` from the
same config the full gate reads. Each scan reads only the staged files, so
the hook stays a few seconds. The script is one line that calls it.

`lateregate prepush` is the pre-push. It reads the refs git hands the hook
on stdin. A release tag among them (`vMAJOR.MINOR.PATCH`, with an optional
prerelease or build suffix) is refused unless `CHANGELOG.md` at its commit
has a section for it; see the next section. Then golangci-lint runs over
the packages the push changes, against the config rendered first as the
full gate renders it: each branch is diffed against the remote's commit (or
the merge base with `origin/main` for a new branch), and exactly the
packages the changed Go files sit in are linted. The linter takes a machine-wide lock by default; this run opts out of it
with `--allow-parallel-runners`, so a lint in another checkout neither
blocks a push nor aborts it. It is here and not in the pre-commit because a
push is rare and already waits on the network, and a hook that adds a
minute to every commit is one people bypass. The shared script
captures stdin once, so a repository that adds its own check below the
delegation reads `$refs`, not stdin. A dated `waive: lint` covers the hook
as it covers the full gate: until the day it names the push prints
`lint is waived until <date>; nothing to lint`, and the day after, the
hook lints again.

`vuln`, `suite` and the five it carries are in neither hook: the first needs
the network and changes verdict with no commit, and the rest run the suite.

## A tag is a release, and a release has notes: `release-notes`, `release`

`CHANGELOG.md` holds one level-two section per tag, and the section is the
body of the GitHub release. A heading's second word names the tag, so
`## v0.29.0 - 2026-09-06` and `## v0.29.0` both name `v0.29.0`, and the
section runs to the next level-two heading. `## Unreleased` holds what the
next tag will say; write under it as work lands.

`lateregate release-notes TAG [REF]` prints the section for `TAG`, read
from the working tree or from `REF:CHANGELOG.md`, and fails naming the fix
when the file, the heading, or the notes are missing. It is the one
implementation of the rule: the pre-push calls it at the pushed commit, the
release workflows in latere-ai/ci call it at the tag and fail closed, and
`release` calls it to check its own work.

`lateregate release vX.Y.Z` cuts the tag. It refuses a dirty tree, an
existing tag, and an empty `Unreleased`; otherwise it writes `## vX.Y.Z -
<today>` under a fresh `## Unreleased`, commits `changelog: vX.Y.Z`,
creates an annotated tag, and pushes `HEAD` and the tag in one push, so
the release workflow runs once. A `Makefile` target is a convenience:

```make
release:
	@go tool lateregate release $(VERSION)
```

The binary writes no draft from the commit log: a note is written for
whoever uses the release, and the commit log is written for whoever reads
the diff.

### `release` reads CI before it tags, and says who acts on the red

The bar a cut runs is the local one. It says the tree about to be tagged is
sound, and nothing about what the last push to the default branch did.

Three tags went out one night after a release run had started failing at its
publish job. Each one deployed, none of them published a release, and nobody
noticed for a day, because a CI failure reaches a person only if a person
reads CI. Between a tag push and the next cut there is one moment where a
machine is already looking at the repository and somebody is already waiting,
and that is the cut.

So before it runs the bar, `lateregate release` reads GitHub and refuses on
three things:

- **A red run in this tag's window.** For every workflow with a completed run
  on the default branch at `HEAD` or an ancestor back to the previous release
  tag, the latest such run must have ended on an answer. `failure`,
  `cancelled`, `timed_out`, `startup_failure` and `action_required` are not
  answers: a run that never started, one that died on the clock and one
  waiting for approval are all runs nobody has a result from. A run
  cancelled because a newer run of the same workflow replaced it is read
  past, the way the newer run is while it is still in progress, so a
  concurrency group that cancels superseded runs changes nothing here.
- **A red release run on the previous tag.** The run the last tag started.
- **A previous tag with no release to show for it.** If that run went green,
  a GitHub Release must exist for the tag. A workflow can finish and publish
  nothing, and this is the only check that catches it.

A window where nothing has completed yet is not green either. It is unknown,
and the guard says so rather than passing.

The guard runs before the bar, not after: a red default branch is three API
reads and the bar is a full instrumented suite.

Every refusal names the run, the failing job and its URL, and ends with one
line saying who acts:

```
lateregate: not releasing v0.39.0: ci is red
  ci #1284 failure, job "gate (cover)"
  https://github.com/latere-ai/ci-gate/actions/runs/1284
CODE: fix and push, then cut again
```

The last line is read from the failing job's log:

| Line | What the log named |
|---|---|
| `BUDGET: the maintainer must act (…)` | Actions minutes, billing, a usage or spending limit, runner capacity, an org policy refusal. Nobody but the maintainer can move it, and the phrase that matched is in the parenthesis so it can be forwarded as evidence. |
| `INFRA: re-run the job, then cut again` | A registry refusal, a reset connection, a certificate or a name that did not resolve. |
| `CODE: fix and push, then cut again` | Anything else, including a log that could not be read. |

Two conclusions decide themselves. A `timed_out` run is `INFRA` unless its log
names a test (`--- FAIL:`, `panic: test timed out` and the rest), because a
suite that ran out of time is the suite's problem and re-running it only spends
the clock twice. A `startup_failure` has no job log of its own, so its message
is what classifies it: one naming an org policy refusal or an exhausted
allowance is `BUDGET`, one naming nothing is `CODE`.

A tag whose run went green and published nothing reads:

```
lateregate: not releasing v0.39.0: v0.38.0 deployed but published nothing
  release #1201 success, no GitHub Release exists for v0.38.0
  https://github.com/latere-ai/ci-gate/actions/runs/1201
CODE: fix and push, then cut again
```

`lateregate release -force-red vX.Y.Z` cuts anyway. It does not skip the
check, it overrides the result, and prints every finding it is overriding.
It is the maintainer's escape hatch and belongs in no pipeline.

The reads go to the GitHub REST API. The token comes from `GH_TOKEN`, then
`GITHUB_TOKEN`, then `gh auth token`, so an authenticated `gh` on a laptop
and a runner's own token both work with nothing to configure.
`GITHUB_API_URL` points the reads at an enterprise host.

```yaml
release:
  require_green: true   # the default; do not restate it
```

Setting `require_green: false` turns the guard off. This repository does not,
and the reason is the night above: without it the tag still deploys, the
notes still may not publish, and the first person to find out is a reader who
wanted to know what changed. A repository that sets it to `false` is deciding
that nobody will read CI and nobody needs to, and the comment beside the key
should say who checks instead.

## The gates in detail

### `cover` gates per package, not on average

An average lets a well-tested package carry an untested one and reports a
number nobody can act on. One repository sat at 90.4% and passed while two of
its packages were at 85.7% and 87.8%, both invisible behind the average.

A package that genuinely cannot clear the floor is exempted **with a reason**,
because the value in the map is the reason:

```yaml
cover:
  threshold: 90.0
  trim_prefix: github.com/latere-ai/yourrepo/
  exempt:
    internal/harness: >-
      shells out to a real binary; the covered paths are the injectable ones
```

An exemption with an empty reason fails the load. So does a profile that
covers no packages, and one where *every* package is exempt: a gate that
passes because it measured nothing keeps reporting green as the tree fills up.

A package with **no test file at all** is the version of that hole the floor
cannot see on its own. It produces no records, so it is absent from the
profile, and a rule that reads only the profile clears it by never measuring
it. The gate lists the module's packages and fails on the ones the profiles
never mention:

```
MISS internal/notify                                 -  no coverage data
lateregate: 1 package(s) produced no coverage data, so the floor never
applied to them: internal/notify
```

A package that declares no function with a body is left out of that list. The
tool instruments statements, so such a package produces no data however it is
tested, and a finding no test can clear is one people learn to skip. A package
that genuinely has no tests is exempted the same way as any other, with the
reason attached.

Repeat `-profile` for a repository whose coverage is split across test tiers.
The tiers merge as a union rather than a sum: with `-coverpkg` the same block
appears in every tier that built it, so a service whose logic sits behind a
database boundary gates on the combined figure instead of on whichever tier
ran last.

```make
cover:
	go test ./... -covermode=atomic -coverpkg=./... -coverprofile=out/unit.out
	go test ./... -covermode=atomic -coverpkg=./... -coverprofile=out/integration.out -tags=integration
	@go tool lateregate cover -profile=out/unit.out -profile=out/integration.out
```

### `hermetic` catches tests that depend on the machine

Three CI failures in one day came from tests that depended on what happened to
be installed on the machine running them: `systemctl` absent on macOS and
present-but-unprivileged on a runner, and a harness binary on a developer's
`PATH` and not on a runner's. Each passed locally and failed in CI.

`hermetic` runs the suite with `PATH` reduced to the Go toolchain's own
directory. If your tests legitimately need a system tool, name its directory,
which makes the dependency visible instead of ambient:

```yaml
hermetic:
  allow: [/usr/bin, /bin]
```

Start with `allow: []` and add only what fails.

### `tempdir` catches tests that fill the disk

A test that makes a directory under `TMPDIR` and does not remove it leaks it
for the life of the machine. Nothing goes red. The suite passes, coverage
passes, and the first symptom arrives months later as a full disk.

One repository was measured with 8.2GB free on a 926GB volume. 168GB of that
was leaked test directories from three sites, the largest 258 directories at
160GB. All three had made the same reasonable decision: a tool built once for
the whole package cannot live in a `t.TempDir`, because the testing package
removes that when the *first* test that asked for it returns. So they used
`os.MkdirTemp`, whose removal you have to write yourself. Two of the three
carried a comment saying the directory was removed by the process that made
it. It never was.

`tempdir` points `TMPDIR`, `TMP` and `TEMP` at an empty directory, runs your
suite, and fails on whatever is still there:

```
  nanogo-corpus3529420610 (790.2MB)
  nanogo-audit774067360 (46.8MB)
lateregate: 2 entries survived the test run, 837.0MB in all
```

The check is dynamic rather than a source scan because the leak is a property
of the process tree. A suite that shells out to a compiler, a container
runtime or a package manager leaks through those too, and reading the
caller's source never finds it. That is also why it is not Go-specific:

```yaml
tempdir:
  # Whatever runs your suite. Default: go test ./...
  command: [pytest, -q]
  allow:
    go-build: >-
      a `go build -work` under test, which keeps its work directory on purpose
```

Name the target that exercises the most code. A leak the gate never runs is a
leak it reports as absent, and the slow suites are the ones that build caches
worth gigabytes.

Two behaviors are worth knowing about. An empty sandbox that was **never
written to** fails rather than passes: a suite launched through a wrapper that
resets the environment would otherwise score perfectly having proved nothing.
And when the suite fails *and* leaks, the leak is the verdict, because a red
suite gets re-run while a leak that only surfaces on a green one is never
seen.

The sandbox is one directory per repository and user on a machine, at the
same path on every run, and runs of one repository take turns through a file
lock beside it. The go command keys a cached test result on the `TMPDIR` the
test read, so a fresh directory per run would rerun every package that makes
a temporary directory on every push; the fixed one lets an unchanged package
replay its result. A run whose packages all replay still counts as having
used the sandbox, because the go command makes its build directory there on
every invocation. On a platform without `flock` each run makes a directory of
its own instead.

### `license` puts the terms on the file, not only at the root

A `LICENSE` at the root binds whoever clones the repository and reads it. Code
mostly travels some other way: pasted into an issue, vendored into another
tree, walked by an SBOM scanner, lifted into a corpus. Every one of those
routes drops the root file.

Four repositories here had three different answers, and nobody noticed because
nothing asserted anything: one carried a prose notice on every file, two
carried none, and one said "Proprietary" in its README while going open
source. The prose form was not machine-readable either. No scanner turns
"Licensed under the MIT License." into an identifier without guessing.

```yaml
license:
  spdx: AGPL-3.0-or-later
  holder: Latere AI
  # Unset means .go. List what this repository actually ships.
  extensions: ['.go', '.ts', '.tsx', '.mjs', '.sh']
  # For the files that have no extension.
  names: [Makefile, Dockerfile]
  skip: [dist]
```

The notice is the first two lines and then a blank one, in whatever marker the
file type comments with:

```go
// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audit decides what a paper released.
package audit
```

A script keeps its shebang on line 1, because the kernel only honors it
there, and the notice moves below:

```sh
#!/bin/sh
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

set -eu
```

`lateregate license -w` writes the notice on every checked file that has
none, from the declaration and nothing else, and leaves a file whose notice
disagrees with the declaration to a person. Adoption is the declaration, one
run of `-w`, and one commit.

`spdx` has no default, and a repository that runs the gate without one gets an
error rather than a pass. That is the opposite of every other gate here, and
it has to be: an identifier guessed on your behalf and printed into 300 files
is worse than none. For the same reason the gate does not infer the
identifier from `LICENSE`. It does read the file: the declaration is checked
against the text through a fingerprint per identifier (`MIT`, `Apache-2.0`,
`AGPL-3.0-only`, `AGPL-3.0-or-later`), so a root file that says MIT under an
`Apache-2.0` declaration fails, naming both. An identifier with no
fingerprint fails too, until one is added to the table.

Two details the check earns its keep on. The **blank third line** is part of
it: in Go a comment block touching `package` *is* the package documentation,
so an unseparated notice puts the license at the top of every page on
pkg.go.dev, and the mistake is invisible in review and permanent once it is in
every file. And the **year is a pattern**, `2026` or `2024-2026`, not a fixed
value, because a gate that goes red every 1 January for a reason nobody caused
is a gate people learn to skip.

A file type the gate has no comment marker for is rejected when the config
loads, not skipped during the scan. The Go marker is a legal comment in most
languages, so scanning with the wrong one finds nothing and reports a clean
pass over files nobody checked.

### `spec-lint` keeps the index honest

In one repository every row of `specs/README.md` read `draft`, including five
specs that were built, deployed and serving. The table had been hand-edited a
dozen times that day. A status column that disagrees with the code is worse
than no column, because a reader trusts it.

```yaml
spec:
  dir: specs
  status: [draft, partial, complete]
  require: [title, status]
  index: specs/README.md
  wikilinks: true
```

It checks that frontmatter parses, required keys are present and non-empty,
each status comes from your vocabulary, `depends_on` edges resolve, the graph
is acyclic, every spec appears in the index with the status it claims, and
`[[wikilinks]]` point at something.

Two rules are off unless you ask for them, because both encode a convention
rather than plain hygiene:

```yaml
spec:
  numbered: true
  started: [dispatched, in_progress, testing, complete]
  settled: [complete, superseded]
```

`numbered` requires every file to be `NNN-name.md` and no two to carry the
same number. Reuse is the half that matters: a number is what an index row, a
wikilink and a commit message all resolve through, so handing a deleted spec's
number to the next one silently repoints every citation that already exists.

`started` and `settled` turn `depends_on` from a note into an ordering anyone
kept to. A spec at a started status whose dependency has not settled was built
against a design that was still moving, and the tree then records an ordering
that never happened. Both vocabularies are yours, since where work begins is a
repository's own decision; `started` without `settled` is refused at load,
because no dependency could ever close and the gate would fail on everything.

A third rule files the specs that are finished:

```yaml
spec:
  archive:
    dir: .archive
    statuses: [complete, superseded, abandoned]
```

A spec at a terminal status belongs in `specs/.archive/`, and a spec at any
other status belongs beside the ones still being written. Both directions,
because that is what makes the pair exhaustive: the first catches the spec
that finished and nobody moved, the second the spec retired while its work was
still open.

Turning it on is also what makes the archive visible at all. Without it a
subdirectory is never read, which is how one tree accumulated fifteen distinct
archived statuses, one of them a sentence, while its root held three. Archived
specs are parsed, held to `require` and to the same `status` vocabulary, and
resolved for `depends_on` and `numbered`.
They are not held to the rules that describe work in progress — sections,
markers, registers — because a record written before a rule existed cannot
satisfy it without rewriting history.

`statuses` must be a non-empty subset of `status`, refused at load like a
`started` value the vocabulary does not list. There is no default: what is
terminal is your tree's decision, and `implemented` means "shipped, follow-on
work outstanding" in one repository here and "done" in another.

Whether the index covers the archive is read off the table rather than
configured. An index holding at least one archive row is an index of the whole
tree and must hold them all; one holding none is an index of the live work and
is asked for nothing. Both conventions exist in the fleet and both are
defensible, so the tool takes the index's own word for which it is.

An index row into the archive is checked for resolution, not for its status
cell. That cell says where the spec went — `archived
(superseded)` against a frontmatter that says `superseded` — which is a
different claim, and comparing them would force every tree onto one label
vocabulary to catch no error. A row linking into any other subdirectory is
still left alone, the same way a `depends_on` edge into another repository is.

Conventions beyond that — decision records, layers, outcome rules — stay in
your repository. This checks the parts every spec tree needs.

### `golangci` renders the config instead of committing it

golangci-lint has no configuration inheritance: its v2 schema rejects an
`extends` key outright, so a shared config cannot be referenced, only
produced. Every repository's file was byte-identical apart from goimports'
local-prefixes, which is just the module path, so there was nothing
repo-specific being duplicated — only the duplication.

`lateregate golangci` writes `.golangci.yml` to the repository root, where
editors and IDEs look for it. It is **not committed**: regenerating on every
run makes drift impossible, where a committed copy only makes drift
detectable. Gitignore it, and the gate refuses to write over a tracked file.

The shared set is the org's bar. Beyond the standard linters it turns on the
ones that catch bugs rather than style — `bodyclose`, `errorlint`, `nilerr`,
`sqlclosecheck`, `rowserrcheck`, `noctx`, `contextcheck`, `errchkjson`,
`durationcheck`, `copyloopvar`, `unconvert`, `wastedassign` — and four
settings that each closed a hole a single repository had already closed alone:

- **Nothing is truncated.** `max-issues-per-linter` and `max-same-issues` are
  both zero. The default turns a list of twenty into a list of three and hides
  the rest until the first three are fixed, so the size of the work is never
  visible at once.
- **Every vet analyzer** runs. Enabling the set by name means a toolchain that
  adds an analyzer ships it disabled and nobody notices which. `fieldalignment`
  and `shadow` are off by judgment: one trades readable structs for memory
  layout, the other flags idiomatic `if err := f(); err != nil`.
- **Type assertions are checked.** A dropped second result panics on exactly
  the value the assertion was written to handle.
- **The standard library choices are fixed**: `io/ioutil`, `math/rand`, `log`
  and `github.com/pkg/errors` are denied, each with the replacement named.
  `log/slog` is allowed explicitly, since depguard matches by prefix.

A repository adds to the set through `golangci.extra`, which merges over the
shared document — `linters.enable` appends, so adding a linter means "as well
as", never "instead of". Layering rules, which are facts about one service's
directories, belong there:

```yaml
golangci:
  extra:
    linters:
      settings:
        depguard:
          rules:
            below-transport:
              files: ['**/internal/store/**']
              deny:
                - pkg: net/http
                  desc: the storage layer sits below transport
```

A repository that genuinely cannot use the shared config says so with a
reason, and the gate then leaves its committed file alone:

```yaml
golangci:
  own: >-
    vendored third-party tree with its own lint history
```

A declared exception that points at no file fails, because a repository that
lost its config and did not notice is the case the field exists to catch.

### `modernize` will not pass silently

Turning a fixer off is a decision:

```yaml
modernize:
  disable: [newexpr, errorsastype]
```

`go fix` rejects an unknown `-name=false`, so if the toolchain ever drops a
fixer you named, the flag would be refused and the check would report green
over a gate that never ran. `lateregate` verifies each fixer still exists
before trusting the flag, and fails loudly if one is gone.

### `otel-client` keeps a distributed trace connected

Two shapes of outbound call lose the trace, both silently:

```go
c := &http.Client{Timeout: 5 * time.Second} // no Transport
http.DefaultClient.Do(req)
```

Either one calls out on the stdlib transport, so no client span is recorded
and no `traceparent` header is sent. The service on the other end opens a
fresh trace rather than joining the caller's, and the hop between them is
gone. Nothing fails, no error is logged, and the gap only shows up later as a
trace that stops at a boundary.

Instrument the client instead, and the downstream spans join the caller's
trace:

```go
c := &http.Client{Timeout: 5 * time.Second, Transport: otel.Transport(nil)}
c := otel.HTTPClient()
```

The scan parses each file rather than matching text, which matters more than
it sounds. A `Transport` field several lines below the opening brace still
counts, a comment explaining this rule does not trip the gate, and
`Timeout: cfg.Transport.Timeout` does not pass for a `Transport` field the way
a substring test would. Test files are excluded: a test dialing an httptest
server has no trace to continue.

A build-tagged harness that deliberately uses the stdlib can be named:

```yaml
otel_client:
  skip: [cellae2e]
```

Keep that list short. A directory skipped here is one whose outbound calls
nobody is asserting anything about.

### `registers` keeps the developer's sentence off the user's surface

Every sentence a product emits is written for one reader: the user, the
contributor, or the developer debugging a running system. The rule is
[docs/writing/registers.md](https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md)
in `latere.ai/x/pkg`. The leak that survives review most often is a
developer sentence handed to the function that writes the user's error:

```go
api.WriteError(w, "deploy_failed", "apply Deployment insula-p-3f2a/api: store.Deploy not found")
```

The user cannot act on any of that, and the developer detail belongs in a
separate field the CLI shows on request. Which functions are user surfaces
is the one part no shared rule can know, so the repository names them:

```yaml
registers:
  user_surfaces: [internal/api.WriteError, cmd/latere.errorf]
```

The gate parses each non-test file, finds calls to those functions by
import path and name, and reads every string literal in their arguments,
including one nested in a `fmt.Sprintf` or a concatenation. Four tells
fail it: a Go import path (`latere.ai/x/pkg/httpjson`; a URL is a page
and passes), a package-qualified identifier (`store.Deploy`,
`pgx.ErrNoRows`), a Kubernetes kind followed by an object name
(`Deployment insula-p-3f2a/api`, `pods "api-7d9c"`; `Service
unavailable` is a sentence and passes), and a file path (`/etc/latere/config.yaml`,
`~/.config/latere/token`, `handler.go:42`; a bare `latere.yaml` is the
file the user edits and passes).

A surface may be written relative to the module or as a full import path.
Only package-level functions are matched, because the scan reads no type
information; name the function the user's output goes through. A surface
that no file calls fails the gate rather than reporting clean over nothing,
so a typo in the name is a failure and not a silently disabled check. The
gate applies only when the key names a function: it is opt-in per
repository, and the review still carries the tells no regular expression
can, such as a hint with no action.

### `identity` holds the repository to the family's identity shape

The family took one decision about identity: the issuer issues identity and
membership, an open core verifies one token, forwards every claim and asks one
authorizer, a service verifies that the audience is itself and decides from its
own state, nobody calls the issuer while serving a request, there is one hop
mechanism and no delegation in a token, and access is by role.

Those rules held because somebody grepped for them, and a rule nothing runs on
every push is a rule that drifts. This gate runs them. Every repository
declares which layer of the shape it is:

```yaml
identity:
  role: core                  # issuer | core | platform | service | bff | client | none
  audience: cella             # what a token addressed here carries; core, service, platform
  config_prefix: CELLA        # the prefix of the deployment variables; core
  api_group: cella.latere.ai  # the group this core writes its own manifests under; core
  claims_passthrough: [internal/auth/claims.go]   # the files of a core that may name a claim
  skip: [deploy/prod]         # paths the scans do not enter
  overlays: [deploy/prod]     # the paths holding one company's own overlay; core
```

`none` is a library or a tool. It is a declared role and not an absent one: a
repository with no block fails the gate, and `contract` reports the missing
block the way it reports a missing hook. The role then selects the rules, and
every rule is a scan of non-test Go files, deployment manifests, and documents
that describe the current system. A record is not read: a changelog, a release
note, anything under an `.archive` directory, and a spec whose status the
tree's `spec.settled` list calls finished, because a record may name the
mechanism it retired. Nothing here runs a service.

The `roles` rule reads one thing more, the frontend: `.ts`, `.tsx`, `.jsx`, `.vue`, `.svelte`, `.js`, `.mjs` and `.cjs`
under the repository, because a page decides access as surely as a handler
does. What no person in the repository wrote is not read,
which is `node_modules/`, `testdata/`, a bundler's output (`dist/`, `build/`,
a `.min` or `.bundle` file, a file carrying a `@generated`, `Code generated by`
or `DO NOT EDIT` header), an archive, and whatever `git check-ignore` calls
ignored: a frontend tree holds build residue beside its sources, and a file
that is on a laptop and not in a checkout would make the gate red locally and
green on the runner. A test is not read either: a `.test`
or `.spec` segment before the extension, or a `__tests__` directory, told by
the path and never by reading the assertion, for the same reason the Go half
skips `_test.go`. A test asserts, and the assertion a repository writes after
retiring a flag is that the flag confers nothing, which names it. Inside a file
that is read, a comment is prose and a string is not: the sentence explaining
why a file stopped reading a flag is not a use of it.

| Rule | Roles | What fails |
| --- | --- | --- |
| `claims` | core | an identifier `OrgID`, `Roles`, `IsSuperadmin`, `PrincipalType`, or a string `org_id`, `roles`, `is_superadmin`, `principal_type`, outside `claims_passthrough` |
| `verifier` | core, service, platform, bff | nothing imports `latere.ai/x/pkg/authkit/jwt` (a bff is exempt from this half: it forwards and verifies nothing); a second token library; a token taken apart by hand |
| `authorizer` | core | nothing imports `latere.ai/x/pkg/authz`; a hand-rolled `POST` to a path named `authorize` |
| `envelope` | all but none | a struct whose JSON tags name `action` beside `subject` or `resource`, or `allow` beside `ttl` and `reason`, outside the shared module and outside `envelope_exempt` |
| `request-path` | service, platform, bff | a string literal `/tokeninfo`, `/userinfo/permissions`, or `/orgs/` joined with `/members` |
| `delegation` | all but none | `grantor_id`, `tokens/exchange`, `RFC 8693`, `actor: true` anywhere; `act`, `agent_id`, `actor_id` as a JSON key or a struct tag where they are a token claim |
| `roles` | all but none | `is_superadmin`, `IsSuperadmin` or `isSuperadmin`, in a Go file, a document, a manifest or a frontend source, once the block sets `roles_only: true` |
| `audience` | core, service, platform | a container that runs this repository and, across its base and overlays, sets no `<PREFIX>_OIDC_AUDIENCE`, or `AUTH_AUDIENCE` or `AUTH_AUDIENCES` for a service, to a name that is not an address |
| `core-audiences` | core | a container the declared `overlays` patch whose audience, the overlay's where it patches one and the base's otherwise, is not exactly `audience` and `api.latere.ai`: the core's own name alone, a third name, a wrong count, or one name twice. A core that declares no overlay reports `SKIP`, and an address stays the `audience` rule's finding |
| `bearers` | issuer, platform, core | two variables of one container reading one secret key; a host serving `/internal/` behind a public path |
| `no-latere-value` | core | `latere.ai` or `latere.svc` in code, a manifest or a user document, outside `api_group`, the `latere.ai/x/` module namespace, a contact address and what `overlays` declares; `specs/` is the contributor's record and is not read |
| `client-audiences` | client | a product audience in `audiences` that no file presents, or that two files present |
| `documents` | all but none | `pkg/oidclogin`, `pkg/jwtauth`, `pkg/oidc/`, `identity fabric`, `delegated token` in a live `*.md` |

Each finding is a file, a line, and a sentence saying what to do. A rule with
nothing to read prints `SKIP` and why, so a repository with no `deploy/`
learns that the audience rule did not run rather than reading it as a pass.

The rules that read string literals read what a file says and not what it
imports. An import path is a string in the grammar and a dependency in the
file, so a conformance case that posts and imports a test double whose path
holds `authorize`, or a package whose path holds `roles`, is neither a
hand-rolled ask nor a claim read for meaning. The rules that ask what a file
imports, `verifier` and `authorizer`, read the import list itself.

Two of the rules are heuristics and the report treats them as such. A file
that both decodes unpadded base64 and splits a string on `.` is taking a token
apart, which neither half alone would show. A container is this repository's
when the name its image was built under — the last path segment of the
reference, without the registry, the tag and the digest — is exactly a
command the repository builds: `origo` runs `ghcr.io/latere-ai/origo:v1` and
never `origo-stubs`, and a manifest of another workload beside this one is
read as none of this repository's, which the rule reports as a `SKIP` with its
reason. An `initContainers` entry is read as well when it declares a variable
of the repository's own prefix, which is how a check that runs before the
workload says it runs with the workload's configuration; one that declares
none is a step that verifies nothing. An overlay that patches a container by
name and carries no image is the container it merges into. A path a heuristic
reads wrong goes in `skip`.

A command is a directory under `cmd/`, and it is also the module root when
the root is itself a `package main`: that repository builds one binary, `go
build` names it after the module, and there is no `cmd/` to read it from.
A repository whose image was built under neither name declares it, because
an image name the rule guessed at would be a rule that stops checking as
soon as it guesses wrong:

```yaml
identity:
  role: service
  audience: wallfacer
  image: wallfacerd       # the module is wallfacer; the workload is wallfacerd
```

The value is that one segment: a registry, a path, a tag or a digest in it is
refused, because the rule compares the segment and would never match the rest.
The key belongs to `issuer`, `core`, `service` and `platform`, the roles
whose deployments the `audience` and `bearers` rules read; anywhere else it
is a name nothing reads, and the load refuses it.

A repository behind on one rule waives that rule and keeps the other ten
running, which a gate waiver could not do:

```yaml
identity:
  role: core
  waive:
    authorizer:
      until: 2026-11-30
      reason: the shared contract package lands with the family's id-03
```

A waived rule still runs and reports its findings under `WAIV`, so the count
is visible and a waiver whose rule already holds says the waiver can go. Past
its date the rule fails on its own terms and the line says which waiver
expired. A waiver naming no rule, or a rule the role does not run, fails the
load: a waiver with no effect hides a typo.

An open core sometimes keeps one company's own deployment in its tree,
because the tag deploys from it. `overlays` names those paths, and the
`no-latere-value` rule reads them rather than scanning them:

```yaml
identity:
  role: core
  overlays: [deploy/prod]
```

A declared overlay's own files are not read by that rule, so the addresses
one installation runs on belong there. The rule then collects every
`latere.ai` and `latere.svc` address those files set, which is a line's value
and not its comments, and a document may name one of them. That address is
the whole of the exemption: naming the overlay's path in a sentence admits
nothing beside it, and the same address in a Go file or in a manifest outside
the overlay, an address the overlay only mentions in a comment, and any
address no declared overlay carries are all held as before. The exemption is
read out of the overlay rather than listed beside it, so a repository cannot
claim one for a value it does not deploy, and a declared path the tree does
not hold stops the run.

The `authorizer` rule catches a repository that asks the authorizer in a
shape of its own. `envelope` catches the other half: a repository that
answers in one, or that decodes the answer into fields it wrote itself. The
evidence is the JSON tags of a struct in a non-test Go file, because a type
that marshals the envelope is what puts the shape on the wire; a Go field
with no tag names nothing a reader of the wire sees. Tag names are matched
whole, so an `actions` list, an `allowed_hosts` set, a `ttl_seconds` figure
and a `reasons` array are not the envelope. The module the envelope is
declared in, `latere.ai/x/pkg`, is where declaring it is the point, and the
rule reports a `SKIP` there.

Two shapes carry the envelope's field names for reasons of their own: a
core's `limits` type, whose ceilings are the core's, and the page a list
action answers with, which carries `allow` and `reason` and no `ttl` because
a page is not a verdict. Neither is written into the gate. A repository that
holds one names it:

```yaml
identity:
  role: core
  envelope_exempt: [authorizer/limits.go]
```

A declared path the tree does not hold stops the run, the same way an
overlay's does: an exemption that matches nothing hides a typo.

`roles_only` is one way. Once a repository has set it, the gate asks git
whether the history ever carried it, and a tree that unsets it fails: a rule
that can be turned off lasts until the first push that finds it inconvenient.

Some of the shape is only visible with every repository in view, so that part
is a subcommand over a directory of checkouts:

```sh
go tool lateregate identity family -repos ../checkouts -expect docs/layers.md
```

It fails on a repository with no block, an audience two repositories claim, an
audience a repository verifies and the issuer's client registry does not list,
an audience the registry lists and nobody verifies, and a client presenting an
audience nothing accepts. It prints the layer table the blocks derive, and
`-expect` fails when the committed copy of that table differs, which is what
makes the document derived from the tree rather than maintained beside it.

### `postgres` holds the repository to its role against the shared database

One managed Postgres serves the family, with about 22 usable connection
slots, and every service that connects directly claims its share of them per
replica and once more for its migrations at boot. The family's fix is a
transaction-mode pool per service: the serving path reads `DATABASE_POOL_URL`
and falls back to `DATABASE_URL`, and the migrator, which holds a
session-scoped lock, keeps `DATABASE_URL`. Two lines in each of eleven
repositories over weeks is the shape that drifts, so this gate runs the rule
on every push. Every repository says what it does with the database:

```yaml
postgres:
  role: pooled                 # none | direct | pooled
  direct_env: LUX_DB_URL       # pooled only: the two names this repository reads, written out
  pool_env: LUX_DB_POOL_URL
```

A repository whose names end in `DATABASE_URL` writes neither key and the
gate reads `DATABASE_POOL_URL` and `DATABASE_URL`; one whose Secret carries a
prefix may write `prefix: EVAL` instead, which derives
`EVAL_DATABASE_POOL_URL` and `EVAL_DATABASE_URL`. `direct_env` and `pool_env`
are declared together, never beside `prefix`, never under any role but
`pooled`, and they name two different variables. The gate asks whether both
endpoints are read, not how a service spells them: the variable name is the
service's own surface and the Secret key is the family contract, and a
Deployment maps the one to the other.

| Role | What is checked | What fails |
| --- | --- | --- |
| `none` | no non-test Go file imports a Postgres client | any client import; the finding names the file and the line and says to declare `direct` or `pooled` |
| `direct` | a live waiver of this gate covers the repository; the row prints its date and its reason | no waiver, or one past its date. The finding names both ways out: cut over to `pooled`, or write a reason and a later date |
| `pooled` | a client is imported; some file reads the pooled name; some file reads the direct name | the missing one of the three, named |
| absent | no client is imported | any client import; the finding names the file and the three roles. A tree with no client passes and the report says the decision came from the imports |

A Postgres client is the pgx tree at any version, `lib/pq`, golang-migrate's
`postgres` and `pgx` drivers, and the family's shared migration runner.
`database/sql` alone is generic and is not one; the driver it is opened with
is, and that import is the finding.

A read of a name is one of the two shapes the family reads a DSN in: a call
to something named for the environment (`os.Getenv`, `os.LookupEnv`, a local
`getenv`, an `envOr` helper) with the name as a string literal or as a
constant the package binds to it, or a struct tag under the `env` key. A name
in a comment, a log line or an error message is a mention and not a read.
Nothing is type-checked and nothing runs a service. What the gate cannot see
is which client each name reaches: that the pooled DSN opens the pool and the
direct DSN opens the migrator is dataflow, and it stays a review item, as the
family's document says.

`direct` is an exception and costs a dated reason. A repository whose serving
path reaches the database on the endpoint the migrator needs holds a slot the
pool would hand back, so the role passes only while the repository waives this
gate:

```yaml
postgres:
  role: direct

waive:
  postgres:
    reason: a library, and the consumer that calls it owns the connection
    until: 2026-12-19
```

It is the same waiver every other gate takes, keyed on this gate's name, so
the plan lists the repository as `WAIV postgres` and the date retires the
exception the way it retires every other one. Past that date the gate runs
and the role fails, naming the date and what the waiver claimed. The gate
reads the waiver itself rather than leaving it to the plan, because
`lateregate postgres` runs one gate by name and builds no plan.

A tree with a `pooled` role and no Go file to read fails rather than passing:
every check reports `SKIP` and the gate says the role showed nothing. `none`
over such a tree passes, because the role claims nothing a file would have to
show.

`contract` prints the declared role in its in-shape line and does not report
an absent block as drift, because the gate decides an absent block from the
imports and one question has one authority. `init` writes no block: the role
is a decision.

One check of this gate runs under every role, the absent one included.
`json-bytes` reads types rather than import strings, and reports a value a
statement cannot carry into its parameter:

```
FAIL json-bytes   2 finding(s)
  internal/store/registry.go:811: this binds a byte slice into parameter $6
  and the statement casts that parameter to jsonb; ... bind a string, or a
  pointer to string where a nil has to stay SQL NULL, and keep the byte
  slice for a bytea column
  internal/lux/event_postgres.go:46: this binds a Go struct into parameter
  $5; ... the driver picks the wire encoding from the Go type alone, and it
  holds no encoding for that type ... encode the value and bind the encoding
  as a string
```

The pooled DSN carries `default_query_exec_mode=exec`, because a transaction
pooler hands the next transaction a different backend and pgx cannot keep a
prepared statement on the server. In that mode the server describes no
parameter, so the driver picks the wire encoding from the Go type alone and
sends every parameter in the text format. Two things go wrong there, and an
endpoint that describes the statement first hides both, which is why no test
on a direct connection sees either.

A `[]byte` goes as `bytea` and arrives as a hex literal, which a json column
refuses with SQLSTATE 22P02. On a parameter something says is json, the check
names what is accepted rather than what is refused: a string under any name,
a pointer to one, `json.RawMessage`, a value with a text or a database value
of its own, a number, and an untyped nil. Everything else is reported, so a
Go type nobody measured is a finding rather than a silence. A parameter is
json when the statement casts it, `$6::jsonb` or `CAST($6 AS json)`, or when
the value is a json encoding: a marshaller's result, any `MarshalJSON`, or a
raw message, followed through conversions, locals, and the module's own
helpers including those whose declared result is `any`.

A Go struct, a Go map, or a list of a repository's own named type has no
encoding at all, and the call fails in the driver with `cannot find encode
plan` before the statement is sent. That one is reported at any parameter,
because the driver's plan lookup reads the Go type and never the column.

A `[]byte` bound to a `bytea` column is correct and is never reported, and
neither is a clock reading, an identifier type with a text method, or a
`[]string` bound to a `text[]` column. A parameter the statement casts to
something that is not json, `$3::bytea`, is never reported as a carrier.

The check type-checks the module, so a repository whose statements do not
build is an error naming what failed rather than a pass. A tree that calls
nothing statement-shaped is not type-checked at all and the report says so.

### Enum domains keep protocol values out of implementation

`enum-go` and `enum-typescript` enforce three properties for the domains a
repository names: fields retain their enum type, implementation uses named
members or typed values, and switches handle every distinct enum value.
They do not guess whether a string is a status, an ID, or an open protocol
value. Naming the domains is the repository's decision:

```yaml
enums:
  go:
    types: [internal/state.Status]
    fields:
      internal/job.Job.Status: internal/state.Status
  typescript:
    - project: frontend/tsconfig.json
      types: [src/state.ts#Status]
      fields:
        src/job.ts#Job.status: src/state.ts#Status
```

Go selectors are a package path and type name, relative to the module or
as a full import path. `.Status` names a type in the module root. Field
selectors append the struct field. A configured domain must be a defined
string or integer type with package-level typed constants. Type aliases
retain the underlying domain identity.

TypeScript selectors are relative to the tsconfig's directory and name
`file.ts#Enum` or `file.ts#Interface.field`; type aliases and classes can
also own fields. The initial representation is a native string or numeric
`enum`. A literal union or an `as const` object is not a configured native
enum. A required field cannot widen to `string`, `number`, another enum,
or a union with those types. Optional TypeScript fields may also hold
`undefined`; optional Go fields may use a pointer to the configured type.

For example, with `Status` configured, Go accepts `var s Status = Running`
and rejects `var s Status = "running"`. TypeScript accepts
`status === Status.Running` and rejects `status === "running"`. Both gates
check assignments, arguments, returns, composite values and switches.
`default` does not cover a missing member. Duplicate-valued members count
as one value. Explicit primitive casts immediately before comparisons or
switches do not hide the original enum domain.

Use a parser that validates external values and returns named members at
input boundaries. When a parser needs a direct conversion, declare the
function and why; `parsers` is available in either language's policy:

```yaml
enums:
  go:
    types: [internal/state.Status]
    parsers:
      internal/state.ParseStatus: validates persisted status before converting it
  typescript:
    - project: frontend/tsconfig.json
      types: [src/state.ts#Status]
      parsers:
        src/state.ts#parseStatus: validates the API response before asserting its type
```

The exception permits conversion inside that function. It does not permit
raw enum assignments or comparisons, incomplete switches, or conversion
inside a nested callback. It does not prove the parser validates its input;
boundary tests must establish that. Enum-to-primitive conversion remains
available for serialization. This is a source/type check, not runtime
validation or dataflow tracking through arbitrary primitive variables.

Run either gate independently:

```sh
go tool lateregate enum-go
go tool lateregate enum-typescript-prepare  # after cloning or changing a lockfile
go tool lateregate enum-typescript
```

The Go gate uses the module's pinned dependencies with `GOWORK=off` and
`-mod=readonly`, under the current platform and build tags. It scans production
packages, not tests. A configured type or field that does not resolve fails.
Run the gate for each build configuration whose domain code differs.

For a TypeScript-only repository, use an installed `lateregate` binary and
invoke `enum-typescript` directly. That command does not need a consumer
`go.mod`. The no-argument bar and the reusable Go workflow still assume a
Go repository; a frontend-only workflow installs its Node dependencies and
runs the individual enum command.

The TypeScript gate needs Node and the project's installed `typescript`.
Vue script blocks additionally need `@vue/compiler-sfc`; script-setup macros
use the project's Vue types. Use the tsconfig that includes application
source, rather than a solution tsconfig containing only project references.
The gate reads TypeScript/TSX and Vue scripts, including imported domains;
Vue templates and component props remain the responsibility of `vue-tsc`.
External Vue `script src` blocks fail with a diagnostic: include the source
as a TypeScript file instead. Test files and declaration files are not
implementation surfaces; ambient declarations still participate in typing.
Compiler and configuration errors fail the gate.

Preparation finds each project's nearest npm or Bun lockfile inside the
repository, verifies the lockfile and `package.json` are tracked, and runs
`npm ci` or `bun install --frozen-lockfile` once per package-manager root.
Multiple competing lockfiles fail. Checking itself never installs packages.
The reusable CI workflow prepares dependencies only for `enum-typescript`;
Go-only repositories do not need Node or Bun. Both gates participate in the
usual plan and dated waivers.

To test the checker itself, use `go test ./...` and, after `npm ci`,
`npm run test:enums`. Node fixtures exercise the embedded script with real
TypeScript/Vue projects and enforce coverage. No application migration is
needed to test or adopt the tooling repository.

## Running the bar in CI

The reusable workflow in `latere-ai/ci` asks the binary for its plan and
runs one job per gate that runs, `suite` included; a `folded` gate has no
job of its own. The suite job writes `coverage.out` at the repository root,
and the workflow keeps it as an artifact:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}-${{ github.event_name }}
  cancel-in-progress: true

jobs:
  gate:
    uses: latere-ai/ci/.github/workflows/lateregate.yml@v1
```

`lateregate init` writes that file. See `ci/README.md` for the inputs.

## Contributing

`specs/` records why this repository is built the way it is: start with
`specs/000-bootstrap.md`, then `specs/001-gate-principles.md` for the four
decisions every gate here has to hold. The tree is linted by this repo's own
`spec-lint`, which is the point.

## Configuration reference

Every section is optional; a repository that has made no decisions needs no
`.lateregate.yaml` at all.

```yaml
waive: {}                  # gate -> {reason, until: YYYY-MM-DD}; the only way an applicable gate does not run

cover:
  threshold: 90.0          # the default; do not restate it
  trim_prefix: ""          # dropped from package paths in the report
  exempt: {}               # package suffix -> the reason it is exempt

spec:                      # applies when git tracks specs/
  status: []               # closed vocabulary; empty allows anything
  require: [title, status] # the default; do not restate it
  index: specs/README.md   # the default when the file exists
  wikilinks: false
  exclude: []              # file names in specs/ that are not specs
  numbered: false          # require NNN-name.md, and no number used twice
  started: []              # statuses at which work on a spec has begun
  settled: []              # statuses at which a dependency stops blocking
  archive:
    dir: ""                # empty disables the archive checks
    statuses: []           # statuses that send a spec to the archive

hermetic:
  allow: []                # directories kept on PATH besides the toolchain's

race:
  timeout: ""              # go test -timeout for the detector run, e.g. 45m; empty is the toolchain default

modernize:
  disable: [newexpr, errorsastype]   # the default; [] runs every fixer

golangci:
  sloglint: {context: scope}         # the default; request_paths scopes it
  extra: {}                          # merged over the shared config; enable appends
  own: ""                            # keep a committed .golangci.yml, and why

license:
  spdx: ""                 # no default; the gate fails until it is declared
  holder: ""
  extensions: ['.go']
  names: []
  skip: []

tempdir:
  command: []              # default: go test ./...
  allow: {}                # surviving-entry prefix -> the reason

depcheck:
  platforms: []
  packages: {}             # import path -> {decision, allow: {prefix: reason}}

cgo_free: {skip: []}
otel_client: {skip: []}

release:
  require_green: true      # the default; do not restate it. false skips the CI guard before a cut
  stamp: []                # entries: {file, pattern}; the vX.Y.Z inside each match moves to the version being cut

registers:                 # applies when user_surfaces names a function
  user_surfaces: []        # package path and function: internal/api.WriteError
  skip: []                 # directories the scan does not enter

identity:                  # mandatory: a repository with no block fails the gate
  role: ""                 # issuer | core | platform | service | bff | client | none
  audience: ""             # what a token addressed here carries; core, service, platform
  config_prefix: ""        # the prefix of the deployment variables; core
  api_group: ""            # the group this core writes its own manifests under; core
  claims_passthrough: []   # the files of a core that may name a claim
  skip: []                 # paths the scans do not enter
  overlays: []             # the paths holding one company's own deployment overlay; core
  envelope_exempt: []      # the files whose types carry the envelope's field names for a reason
  image: ""                # the segment this workload's image was built under; issuer, core, service, platform
  roles_only: false        # turn on the roles rule; one way once set
  registry: deploy/base/clients.yaml   # the client registry; issuer, and the default
  audiences: []            # the product audiences this client presents; client
  waive: {}                # rule -> {until, reason}: hold every other rule while this one is behind

postgres:                  # optional: an absent block is decided from the imports, and a client import under it fails
  role: ""                 # none | direct | pooled; direct needs a dated `waive: postgres` entry
  prefix: ""               # pooled only: EVAL makes the names EVAL_DATABASE_POOL_URL and EVAL_DATABASE_URL
  direct_env: ""           # pooled only, with pool_env and without prefix: the direct name, written out
  pool_env: ""             # pooled only, with direct_env and without prefix: the pooled name, written out

enums:
  go:
    types: []              # package.Type; .Type is the module root
    fields: {}             # package.Struct.Field -> configured package.Type
    parsers: {}            # package.Function -> reason for primitive conversion
  typescript: []           # entries: {project, types, fields, parsers}; selectors are file.ts#Symbol
```
