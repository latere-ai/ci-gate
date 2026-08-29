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
```

See `ci/README.md` for the full contract.

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
