# Commands

Every command `lateregate` takes, what it reads, and what it writes. Run
them through the tool pin, `go tool lateregate <command>`, so the version
is the one `go.mod` names.

| Command | What it does |
| --- | --- |
| `lateregate [-all]`, `lateregate check [-all]` | runs every gate that applies, reports all of them, and exits non-zero if any failed; on a laptop the four slow gates read only [what changed since the default branch](#what-a-check-reads), and `-all` reads the whole tree |
| `lateregate base -since green\|REV` | prints the full SHA of the commit a run measures its change from, and a newline; when the whole tree must be checked it prints nothing on stdout and the reason on stderr; exits 0 either way, non-zero only on a usage error |
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
| `lateregate build` | for a [`ship`](configuration.md#ship-a-repository-that-releases-from-a-workstation) repository: runs the whole bar, then builds the deploy image for the cluster's platform when `ship.image` is set |
| `lateregate push` | builds the image from the release tag HEAD sits on and publishes it; refuses without `ship.image` |
| `lateregate deploy [vX.Y.Z]` | rolls the newest stable tag, or the one named, out to the cluster and runs the live check; refuses without `ship.deploy` |
| `lateregate deploy-credential -context NAME` | the one-time step that stores the rollout identity's kubeconfig where `deploy` reads it; refuses without `ship.deploy` |
| `lateregate release [-force-red] vX.Y.Z` | refuses while CI is red or the checkout is not origin's default branch at its tip, runs the bar, moves the notes into the tag's section, commits, tags and pushes both in one atomic push; see [`releasing.md`](releasing.md) |

Flags every command accepts:

| Flag | Meaning |
| --- | --- |
| `-C DIR` | the repository root holding `.lateregate.yaml`; default the working directory |
| `-go PATH` | the Go toolchain to run; default `go` |
| `-json` | `list` only: print the plan as JSON |
| `-profile FILE` | `cover` only: read this profile instead of collecting one; repeat it per test tier |
| `-w` | `license`: write the declared notice on every file that has none. `action-pins`: move every listed third-party action to the organization's commit and version comment, and print each file changed |
| `-since green\|REV` | `check`, `list` and `base`: measure the change from the newest commit on the default branch that the calling workflow passed on a push and HEAD contains (`green`), or from a git revision, instead of the merge base with the default branch |
| `-all` | `check` only: read the whole tree rather than what changed since the default branch |
| `-force-red` | `release` only: cut over a red CI, printing every finding it overrides |
| `-context NAME` | `deploy-credential` only: the kubeconfig context of a cluster administrator; required |

`tempdir` takes the command to watch after `--`:

```sh
go tool lateregate cover -profile=out/unit.out -profile=out/integration.out
go tool lateregate tempdir -- go test -tags corpus ./...
go tool lateregate license -w
go tool lateregate action-pins -w
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
so the testdata a package's tests read counts as that package's change,
and to every package that embeds it, which can be a package above it. A
path under no package, such as a frontend or the changelog, belongs to
none.

Four gates narrow; the rest read the whole tree, since each is seconds:

| Gate | What it reads on a laptop |
| --- | --- |
| `suite` | the changed packages and every package whose build or tests import one. The floor applies to the changed packages, and holds each package the change reaches without touching to the coverage it had at the base ([below](#untouched-packages)). Skipped when no package changed |
| `lint` | the same packages as the suite, since a changed signature leaves its findings in the callers. Skipped when no package changed |
| `vuln` | the whole module, as on a runner, when a Go package changed: new code can call a vulnerable function nothing called before. Skipped when none changed |
| `lean` | skipped unless a changed path is in a Lake project |

The whole tree is checked, with the reason on the first line, when
`go.mod`, `go.sum`, `go.work`, `go.work.sum`, `.lateregate.yaml` or a
repository-owned `.golangci.yml` changed, when anything under `vendor/`
changed, when a directory holds changed Go files but no package `go list`
reports (a package removed or moved, or one a build constraint excludes
on this machine), when a changed file is inside a nested module the root
module reaches through a local `replace` or a `go.work` `use` line, or when
the change cannot be measured: no default branch, no merge base, or a
module `go list` cannot list. `-all` checks
the whole tree on request. A runner (`CI` set), `release`, a gate run by
name and `list` always read the whole tree, unless `-since` names a base.

### From the last green commit

`-since green` finds the base through the GitHub API with the job's token
(`GH_TOKEN`, then `GITHUB_TOKEN`; off CI it falls back to `gh auth token`).
It reads `GITHUB_REPOSITORY`, the calling workflow's file from
`GITHUB_WORKFLOW_REF` and the API root from `GITHUB_API_URL`, takes the
repository's default branch, and lists the workflow's successful push runs
on it, newest first. The base is the first run whose commit is `HEAD` or an
ancestor of it, which the compare endpoint answers. A commit the checkout
lacks is fetched at depth 1. Anything that goes wrong (no token, a missing
variable, an API error, no matching run, a failed fetch) checks the whole
tree, and `base` says why on stderr. A canceled run is never a base.

`-since REV` names the commit directly. On a laptop the scope then narrows
as above, measured from `REV`. With `CI` set, packages never narrow: each
gate reads the whole tree or is skipped.

| Gate | On CI with a base |
| --- | --- |
| `frontend`, `image`, `lean` | the rules above |
| `fmt-check`, `modernize`, `cgo-free`, `otel-client`, `lint`, `vuln`, `suite` and the gates the suite folds in | skipped when every changed file is inert to Go, else the whole tree |
| every other gate | runs |

A changed file is inert to Go when it is not a `.go` file or a whole-tree
file, it lies under no package directory and no nested module, no
`//go:embed` pattern matches it, and no string literal in a tracked Go file
names it: its path, a directory on its path, or its base name, as an
element of the literal split on `/`. A test that reads
`../../specs/003-api.md` holds the literal `specs`, so a spec change runs
the suite. A test that walks the tree from its root without naming anything
is the case this misses, and `release` checks the whole tree.

The run opens with what it read and ends its verdict on it:

```
checking what changed since origin/main (78d4428effa9): 2 files, 1 Go package, 5 packages with what imports them; -all checks the whole tree
...
14 packages the change reaches without touching kept the coverage the same tests gave them at origin/main (78d4428effa9)
...
PASS lint         over the 5 packages the change reaches
PASS vuln
PASS suite        over the 5 packages the change reaches; the floor on the 1 package it changes and on any it reaches that loses coverage
11 gates passed on what changed since origin/main (78d4428effa9)
```

A changed package with no statements, or one the floor exempts, gives
the floor nothing to measure; when every changed package is such a one,
the floor passes and says `the floor measures nothing here`.

### Untouched packages

A package's figure is every block any test covered, so a change can
lower the figure of a package it did not touch: package `a`'s tests may
be what cover package `b`, and a change to `a` that stops reaching part
of `b` takes `b` under the floor with no line of `b` changed.

Only the tests the run holds can do that. Every other test links no
changed package and holds no changed file, so it covers what it covered
at the base. The floor therefore runs the same tests again over the base,
rebuilt in place with `go test -overlay`, and compares, block by block,
every package the change reaches without touching:

- A package that kept every block the base's run covered keeps the
  figure CI measured on the base, and the floor says how many did.
- A package that lost one is measured over every test that reaches it
  and held to the floor, and the floor names it.
- When a changed file is one a package's tests read at run time, such as
  testdata, the overlay cannot replay that package's tests at the base,
  so what they reach is measured in full. So is every untouched package
  when the base's tests fail on this machine.

The base's run takes as long as the run itself the first time, and
replays from the test cache after that, while the base does not move.

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
they apply. Two more fields appear only when they have something to say.
`needs` is on a running gate that wants tools installed: `node` and `bun`
for `frontend` and `enum-typescript`, and `chromium:<dir>` for each project
`frontend.browser` lists. `os` is on the `test` entry, whatever its status,
and holds the runner labels from `test.os`. The reusable workflow in
`latere-ai/ci` builds its job matrix and installs its tools from it.

## The wiring: `contract` and `init`

Several files connect the binary to the places it is invoked from, and
each is a place to drift. `contract` reads all of them and names every
difference in one run:

- exactly one workflow calls `latere-ai/ci/.github/workflows/lateregate.yml`,
  at `@v1` or pinned to one commit, on push to `main` and on pull requests, with a top-level `concurrency`
  whose group names `github.ref` and which sets `cancel-in-progress: true`,
  so a push cancels the run of the commit it supersedes. A repository with
  a [`ship` block](configuration.md#ship-a-repository-that-releases-from-a-workstation)
  runs no push workflow and is not asked for one; a caller it does keep is
  read by the same rules
- `.githooks/pre-commit` is executable and runs `lateregate hook`
- `.githooks/pre-push` is executable and runs `lateregate prepush`
- `.golangci.yml` is not tracked, unless `golangci.own` declares it with a
  reason
- `.gitignore` lists `.golangci.yml` and `coverage.out`
- `CHANGELOG.md` is tracked and has a `## Unreleased` heading
- `.lateregate.yaml` has an `identity` block
- `.lateregate.yaml` restates no default
- a `Makefile` target named for a gate or a command (`cover`, `test-race`,
  `lint`, `release`, ...) delegates to `lateregate` (directly or through a
  make variable such as `$(LATEREGATE)`), or does not exist
- `go.mod` carries the `tool` line for `latere.ai/x/ci-gate/cmd/lateregate`.
  A repository that keeps the tool out of its root module's graph pins it
  in a nested module and names that directory as the caller's
  `gate_module`; `contract` then reads that module's `go.mod`, and the
  hooks run `go -C <module> tool lateregate hook -C <back to the root>`

Nothing here is waivable. A waiver says a repository is behind on a gate;
wiring is fixed in the commit that notices it.

`init` writes what `contract` checks, where it is missing:

- `.github/workflows/ci.yml`, the caller, when no workflow calls the
  reusable pipeline yet, and never in a repository with a `ship` block;
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

## Shipping from a workstation: `build`, `push`, `deploy`

A repository whose `.lateregate.yaml` has a
[`ship` block](configuration.md#ship-a-repository-that-releases-from-a-workstation)
builds, publishes and deploys from the machine of whoever releases it. Each
command is the whole of its step, so a `Makefile` target is one line,
`go tool lateregate <command>`. The flow, with `release`, is in
[`releasing.md`](releasing.md#shipping-from-a-workstation).

Container commands use `docker` when it is on `PATH` and `podman` otherwise.
The cluster is reached only through the kubeconfig `deploy` reads from
1Password; no command uses the operator's default kubectl context.

`ship.image` and `ship.deploy` are each optional. A repository with `ship: {}`
publishes no image and deploys nothing: `build` still runs the whole bar and
prints `this repository builds no image`, `push` stops with `this repository
publishes no image`, and `deploy` and `deploy-credential` stop with `this
repository deploys nothing`. `release` and `contract` treat it as any other
`ship` block.

**`build`** runs the whole bar over the whole tree, the same call `release`
makes, then, when `ship.image` is set, runs `ship.image.prepare` and builds the
image with `--platform` set to `ship.image.platform`. The image stays in the local engine, tagged
`<name>:dev-<short sha>`, and nothing is pushed. The version it carries is
`git describe --tags --always --dirty`.

**`push`** refuses unless HEAD is exactly a `vX.Y.Z` tag (a prerelease suffix
is accepted, a `+` build suffix is not, since an image tag cannot hold one)
and the working tree is clean. It runs no bar: the release that cut the tag
ran it on this tree. It runs `prepare`, builds with the tag as the version,
tags the image `<name>:<tag>`, `<name>:sha-<short sha>` and `<name>:latest`,
pushes each, and prints the pushed digest. `latest` moves only when the tag
is the newest stable release tag on origin: a prerelease, or an older
release pushed again, is published under its own tags and `push` says why
`latest` stayed where it is. When the
registry refuses the push, the error says so and that the engine needs a
login with a token that has `write:packages`, for example `podman login
ghcr.io`. The command reads and prints no token.

**`deploy [vX.Y.Z]`** deploys the named tag, or the newest stable
`vX.Y.Z` tag on origin by version order. It takes the tag's tree from `git
archive`, not from the working tree, so a dirty or moved checkout cannot
change what deploys. It resolves the digest of `<name>:<tag>` in the
registry, and refuses when the image is not there, naming `push` as the fix.
It then reads the rollout identity's kubeconfig from the 1Password Document
`ship.deploy.kubeconfig` names into a file only its owner can read, which is
removed on every exit path, including an interrupt. Every `kubectl` call
passes that file with `--kubeconfig`, and the environment of every step has
`KUBECONFIG` set to it. The steps, each stopping the deploy and naming itself
when it fails:

1. `ship.deploy.preflight`
2. `kubectl apply -k deploy/prod/` when it holds a `kustomization.yaml`,
   `kubectl apply -f deploy/prod/` otherwise
3. `kubectl set image` on the deployment's container, to `<name>@sha256:...`
4. `kubectl rollout status` with `ship.deploy.rollout_timeout`
5. `ship.deploy.after_roll`
6. the live check, `ship.deploy.smoke`, with `BASE_URL`, `TAG`, `COMMIT` and
   `OUTPUT_MD` in its environment, and without `SERVICE_TOKEN`; the rest
   of your shell's environment is passed on. Right after the rollout an
   old pod or a cache can still answer, so a failing check runs again every
   5 seconds until it passes or 2 minutes have passed since the first
   attempt. Every attempt prints as it runs, and the final failure names how
   many attempts ran. A smoke file missing from the tag is refused at once.

It prints one line, the tag, digest, namespace and deployment, and that the
live check passed. Then, if the tag has no GitHub release, it creates one
with the changelog section as its notes. A failure there is reported as an
error after a deploy that stands. Image signing and an SBOM are not part of
this path.

**`deploy-credential -context NAME`** is the one-time operator step that
makes the Document `deploy` reads, run before a repository's first `deploy`.
`-context` is required and names a context of an administrator's kubeconfig;
every call it makes with that kubeconfig passes `--context NAME`. It reads
`deploy/bootstrap/rollout-identity.yaml` for the ServiceAccount and the
`kubernetes.io/service-account-token` Secret annotated for it, and fails if
either is missing. It reads the cluster's server address from the context
and the Secret's `ca.crt` and token, writes a kubeconfig of one cluster, one
user and one context to a file only its owner can read (the token is never
on a command line, and never printed), and proves the identity with `kubectl
apply --dry-run=server` over `deploy/prod`, so a Role that lacks a kind fails
here. It stores the file with `op document create`, or `op document edit` when
the item exists; any other failure to look the item up is an error. It reads
the Document back the way `deploy` does and compares the bytes, so a wrong
reference shows now. The temporary file is removed on every exit path, and it
prints one line saying which account's kubeconfig is stored at which
reference.

## The pre-commit: `hook`

`hook` runs every gate that is a file scan, over the staged Go files, then
the modernizers over the packages holding them. In order: gofmt, which
leaves out files under `testdata`;
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
