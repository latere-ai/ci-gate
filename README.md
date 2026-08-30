# latere-ai/ci-gate

The per-push quality gates every Latere Go repository shares, as one binary
you pin in `go.mod` and run from `make`.

`latere-ai/ci` owns the workflow that orders these gates in CI. This repo owns
what each gate actually asserts, so the two version independently and a gate
runs the same on your laptop as it does on a runner.

That last part is the point. Every gate here exists because something passed
locally and failed in CI, or passed in CI and meant nothing.

## Quick start

```sh
go get -tool latere.ai/x/ci-gate/cmd/lateregate
```

Add the targets you want to your `Makefile`:

```make
fmt-check:
	@go tool lateregate fmt-check

lint-modernize:
	@go tool lateregate modernize

cover:
	go test ./... -coverprofile=coverage.out -coverpkg=./...
	@go tool lateregate cover

test-hermetic:
	@go tool lateregate hermetic

spec-lint:
	@go tool lateregate spec-lint

test-tempdir:
	@go tool lateregate tempdir

license:
	@go tool lateregate license
```

`fmt-check` and `modernize` need no configuration. The rest read
`.lateregate.yaml` from your repository root.

## The gates

| Command | What it asserts | Needs config |
| --- | --- | --- |
| `fmt-check` | no Go source is unformatted | no |
| `modernize` | no code that a standard library call already covers | no |
| `cover` | **every package** clears the floor, not the repository average | threshold and exemptions |
| `hermetic` | the suite passes with only the toolchain on `PATH` | the directories you allow |
| `spec-lint` | the spec tree agrees with itself and with its index | your spec conventions |
| `tempdir` | the suite leaves nothing behind under `TMPDIR` | the prefixes you allow |
| `license` | every source file carries the SPDX notice the repo declared | the identifier and holder |

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

Conventions beyond hygiene — decision records, layers, outcome rules — stay in
your repository. This checks the parts every spec tree needs.

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

## Running the gates in CI

Use the reusable workflow in `latere-ai/ci`, which orders these across an OS
matrix and skips the targets your repository does not have:

```yaml
jobs:
  verify:
    uses: latere-ai/ci/.github/workflows/go-verify.yml@v1
    with:
      hermetic: true
      cover: true
      tempdir: true
```

See `ci/README.md` for the full contract. `tempdir` needs a runner input in
`latere-ai/ci` before that flag does anything; until it lands, call the target
from your own job.

## Contributing

`specs/` records why this repository is built the way it is: start with
`specs/000-bootstrap.md`, then `specs/001-gate-principles.md` for the four
decisions every gate here has to hold. The tree is linted by this repo's own
`spec-lint`, which is the point.

## Configuration reference

Every section is optional; a repository that only wants `fmt-check`, `test`
and `modernize` needs no `.lateregate.yaml` at all.

```yaml
cover:
  threshold: 90.0          # default 90.0
  trim_prefix: ""          # dropped from package paths in the report
  exempt: {}               # package suffix -> the reason it is exempt

spec:
  dir: ""                  # empty disables spec-lint
  status: []               # closed vocabulary; empty allows anything
  require: []              # frontmatter keys that must be present and non-empty
  index: ""                # empty disables the index checks
  wikilinks: false
  exclude: []              # file names in dir that are not specs

hermetic:
  allow: []                # directories kept on PATH besides the toolchain's

modernize:
  disable: []              # fixers to turn off; each is verified to exist
```

An unknown key is an error rather than a silently ignored one, because a typo
that disables a gate is the failure this whole repository is against.
