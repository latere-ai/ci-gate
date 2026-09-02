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
go tool lateregate init      # the workflow caller, the pre-commit hook, two gitignore lines
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
FAIL cover        3 package(s) below 90%
SKIP spec-lint    tracks no specs/ files
WAIV race         until 2026-11-15: the suite is not race-clean in runner
lateregate: 1 of 13 gates failed: cover
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
| `lint` | golangci-lint at the pinned version, against the shared config it renders first | always |
| `vuln` | govulncheck at the pinned version finds no reachable vulnerability | always |
| `test` | `go vet` and the suite | always |
| `race` | the suite under the race detector | always |
| `hermetic` | the suite passes with only the toolchain on `PATH` | always |
| `tempdir` | the suite leaves nothing behind under `TMPDIR` | always |
| `cover` | **every package** clears the floor, not the repository average | always |

The recipes are in the binary. `test` is `go vet ./...` then `go test
./...`; `race` sets `CGO_ENABLED=1`; `cover` collects with `-coverpkg=./...
-covermode=atomic`; `lint` and `vuln` run their tools through `go run` at a
version pinned here, so one commit moves every repository. Four
repositories used to hold four `cover` recipes, one of which wrote the
profile to a name the gate never read.

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
dependency allowlist, a licence, a waiver. A key that restates its default
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

Four files connect the binary to the places it is invoked from, and each is
a place to drift. `lateregate contract` reads all of them and names every
difference in one run:

- exactly one workflow calls `latere-ai/ci/.github/workflows/lateregate.yml@v1`,
  on push to `main` and on pull requests
- `.githooks/pre-commit` is executable and runs `lateregate hook`
- `.golangci.yml` is not tracked, unless `golangci.own` declares it with a reason
- `.gitignore` lists `.golangci.yml` and `coverage.out`
- `.lateregate.yaml` restates no default
- a `Makefile` target named for a gate (`cover`, `test-race`, `lint`, ...)
  delegates to `lateregate`, or does not exist
- `go.mod` carries the `tool` line

Nothing here is waivable. A waiver says a repository is behind on a gate;
wiring is fixed in the commit that notices it.

`lateregate init` writes what `contract` checks: the caller, the hook, the
gitignore lines, and `git config core.hooksPath .githooks`. It never touches
`Makefile` or `.lateregate.yaml`, because those hold decisions.

`lateregate hook` is the pre-commit: gofmt over the staged Go files, and the
modernizers over the packages holding them, reading `modernize.disable` from
the same config the full gate reads. The hook script is one line that calls
it. golangci-lint is deliberately not in the hook: it takes a global lock,
and a hook that serialises every commit on a machine is one people bypass.

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

Two behaviours are worth knowing about. An empty sandbox that was **never
written to** fails rather than passes: a suite launched through a wrapper that
resets the environment would otherwise score perfectly having proved nothing.
And when the suite fails *and* leaks, the leak is the verdict, because a red
suite gets re-run while a leak that only surfaces on a green one is never
seen.

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

A script keeps its shebang on line 1, because the kernel only honours it
there, and the notice moves below:

```sh
#!/bin/sh
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

set -eu
```

`spdx` has no default, and a repository that runs the gate without one gets an
error rather than a pass. That is the opposite of every other gate here, and
it has to be: an identifier guessed on your behalf and printed into 300 files
is worse than none. For the same reason the gate does not read `LICENSE` and
infer the identifier from its text, though it does check the file is there.

Two details the check earns its keep on. The **blank third line** is part of
it: in Go a comment block touching `package` *is* the package documentation,
so an unseparated notice puts the licence at the top of every page on
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
  and `shadow` are off by judgement: one trades readable structs for memory
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
a substring test would. Test files are excluded: a test dialling an httptest
server has no trace to continue.

A build-tagged harness that deliberately uses the stdlib can be named:

```yaml
otel_client:
  skip: [cellae2e]
```

Keep that list short. A directory skipped here is one whose outbound calls
nobody is asserting anything about.

## Running the bar in CI

The reusable workflow in `latere-ai/ci` asks the binary for its plan and
runs one job per gate:

```yaml
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
```
