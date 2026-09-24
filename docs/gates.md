# The gates

What each gate asserts, what makes it apply, how it fails, and the
`.lateregate.yaml` keys that shape it. The full key reference is
[`configuration.md`](configuration.md).

`go tool lateregate list` prints the plan for the repository you are in:
every gate, and whether it runs, is skipped, is waived, or is folded into
another. `go tool lateregate <gate>` runs one.

| Gate | What it asserts | Applies when |
| --- | --- | --- |
| `fmt-check` | no tracked Go source is unformatted | always |
| `modernize` | no code that a standard library call already covers | always |
| `cgo-free` | no Go file imports `"C"` | always |
| `otel-client` | no outbound HTTP client is built without a tracing transport | always |
| `license` | every source file carries the SPDX notice the repository declared | always; needs `license.spdx` |
| `spec-lint` | the spec tree agrees with itself and with its index | git tracks `specs/` |
| `depcheck` | no build reaches a dependency nobody admitted | `depcheck.packages` names one |
| `registers` | no developer sentence in a string handed to a user-surface function | `registers.user_surfaces` names one |
| `identity` | the repository declares its identity role and holds that role's rules | always |
| `postgres` | the repository's Postgres role holds | always |
| `enum-go` | declared Go domains use named types, named members and exhaustive switches | `enums.go.types` names a domain |
| `enum-typescript` | declared TypeScript domains use native enums, named members and exhaustive switches | `enums.typescript` names a project |
| `lint` | golangci-lint at the pinned version, against the shared config it renders first | always |
| `vuln` | govulncheck at the pinned version finds no reachable vulnerability | always |
| `suite` | `go vet`, then one run of the suite that holds the five properties below | always; a waived `test` waives it |
| `test` | `go vet` and the suite | folded into `suite` |
| `race` | the suite under the race detector | folded into `suite` |
| `hermetic` | the suite passes with only the toolchain on `PATH` | folded into `suite` |
| `tempdir` | the suite leaves nothing behind under `TMPDIR` | folded into `suite`, unless `tempdir.command` names another runner |
| `cover` | every package clears the floor, not the repository average | folded into `suite` |

The recipes are in the binary, so no repository carries its own copy of
one. `test` is `go vet ./...` then `go test ./...`; `race` sets
`CGO_ENABLED=1`; `cover` collects with `-coverpkg=./... -covermode=atomic`;
`lint` and `vuln` run their tools through `go run` at a version pinned in
this repository, so one release moves every repository to the same tool
versions.

## `suite` and the five gates it folds

`suite` runs the test suite once with every property on: `go vet ./...`,
then `go test -race -covermode=atomic -coverpkg=./...
-coverprofile=coverage.out ./...` with `CGO_ENABLED=1`, inside the
`tempdir` sandbox and on the `hermetic` PATH, then the coverage floor over
the profile and the check for surviving temporary files. Running the five
properties separately compiles and runs the suite five times to learn the
same five facts; `-race` already forces atomic coverage, and a sandbox or a
stripped PATH wraps whatever runs inside it.

The first line of a failure names the property: `the suite is not
race-clean`, `the suite reached for docker, which is not on the stripped
PATH`, `the suite left 2 entries under TMPDIR`, `the suite's coverage is
under the floor`.

The race detector needs cgo, and cgo needs the compiler the toolchain
names in `CC` and the `as` and `ld` it calls. Under `-race` the stripped
PATH reaches them through a shim: a directory under the machine's `TMPDIR`
that links those three and nothing else, at the same path on every run. The
compiler's own directory stays off the PATH, since on Linux that is
`/usr/bin`, with `git` and `docker` in it. The `PATH=` line of the output
names the shim and what it holds. Every flag is one the go command's test
cache accepts, so an unchanged package replays its result.

The plan marks `test`, `race`, `hermetic`, `tempdir` and `cover` as
`FOLD`, and `lateregate` runs the suite in their place. A waiver on one of
them narrows the run instead of skipping it: a waived `race` drops
`-race`, a waived `hermetic` keeps the full PATH, a waived `tempdir` runs
outside the sandbox, a waived `cover` keeps no floor, and a waived `test`
waives the suite. The suite's plan line says what is narrowed. Each of the
five still runs by name, `lateregate race`, for isolating one property.

## `cover`: a floor per package, not an average

An average lets a well-tested package carry an untested one and reports a
number nobody can act on: a repository at 90.4% overall passes an average
floor while two of its packages sit at 85.7% and 87.8%. The gate applies
the floor to every package.

A package that cannot clear the floor is exempted with a reason, and the
value in the map is the reason:

```yaml
cover:
  threshold: 90.0
  trim_prefix: github.com/latere-ai/yourrepo/
  exempt:
    internal/harness: >-
      shells out to a real binary; the covered paths are the injectable ones
```

An exemption with an empty reason fails the load. So does a profile that
covers no packages, and one where every package is exempt: a gate that
passes because it measured nothing stays green as the tree fills up.

A package with no test file at all produces no coverage records, so it is
absent from the profile, and a rule that reads only the profile clears it
by never measuring it. The gate lists the module's packages and fails on
the ones the profiles never mention:

```
MISS internal/notify                                 -  no coverage data
lateregate: 1 package(s) produced no coverage data, so the floor never
applied to them: internal/notify
```

A package that declares no function with a body is left out of that list.
The tool instruments statements, so such a package produces no data however
it is tested. A package that has no tests on purpose is exempted like any
other, with the reason attached.

Repeat `-profile` for a repository whose coverage is split across test
tiers. The tiers merge as a union rather than a sum: with `-coverpkg` the
same block appears in every tier that built it, so a service whose logic
sits behind a database boundary gates on the combined figure instead of on
whichever tier ran last.

```make
cover:
	go test ./... -covermode=atomic -coverpkg=./... -coverprofile=out/unit.out
	go test ./... -covermode=atomic -coverpkg=./... -coverprofile=out/integration.out -tags=integration
	@go tool lateregate cover -profile=out/unit.out -profile=out/integration.out
```

## `hermetic`: tests that depend on the machine

A test that shells out to a tool installed on the developer's machine
passes there and fails on a runner that lacks it, or that has it without
the privileges the test assumed. `hermetic` runs the suite with `PATH`
reduced to the Go toolchain's own directory. If your tests need a system
tool, name its directory, which makes the dependency visible instead of
ambient:

```yaml
hermetic:
  allow: [/usr/bin, /bin]
```

Start with `allow: []` and add only what fails.

## `tempdir`: tests that fill the disk

A test that makes a directory under `TMPDIR` and does not remove it leaks
it for the life of the machine. The suite passes, coverage passes, and the
first symptom is a full disk. The common cause is a tool built once for a
whole package: it cannot live in a `t.TempDir`, because the testing
package removes that when the first test that asked for it returns, so the
test uses `os.MkdirTemp`, whose removal has to be written by hand and
often is not.

`tempdir` points `TMPDIR`, `TMP` and `TEMP` at an empty directory, runs
the suite, and fails on whatever is still there:

```
  nanogo-corpus3529420610 (790.2MB)
  nanogo-audit774067360 (46.8MB)
lateregate: 2 entries survived the test run, 837.0MB in all
```

The check is dynamic rather than a source scan because the leak is a
property of the process tree. A suite that shells out to a compiler, a
container runtime or a package manager leaks through those too, and
reading the caller's source never finds it. That is also why it is not
specific to Go:

```yaml
tempdir:
  # Whatever runs your suite. Default: go test ./...
  command: [pytest, -q]
  allow:
    go-build: >-
      a `go build -work` under test, which keeps its work directory on purpose
```

Name the command that exercises the most code. A leak the gate never runs
is a leak it reports as absent, and the slow suites are the ones that
build caches worth gigabytes.

Two behaviors to know. An empty sandbox that was never written to fails
rather than passes: a suite launched through a wrapper that resets the
environment would otherwise score perfectly having proved nothing. And
when the suite fails and also leaks, the leak is the verdict, because a
red suite gets re-run while a leak that only surfaces on a green one is
never seen.

The sandbox is one directory per repository and user on a machine, at the
same path on every run, and runs of one repository take turns through a
file lock beside it. The go command keys a cached test result on the
`TMPDIR` the test read, so a fresh directory per run would rerun every
package that makes a temporary directory on every push; the fixed one lets
an unchanged package replay its result. A run whose packages all replay
still counts as having used the sandbox, because the go command makes its
build directory there on every invocation. On a platform without `flock`
each run makes a directory of its own instead.

## `license`: the terms on every file

A `LICENSE` at the root binds whoever clones the repository and reads it.
Code also travels without it: pasted into an issue, vendored into another
tree, walked by an SBOM scanner. Every one of those routes drops the root
file, and a prose notice ("Licensed under the MIT License.") is not
machine-readable either. The gate puts an SPDX identifier on every file.

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

The notice is the first two lines and then a blank one, in whatever marker
the file type comments with:

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
disagrees with the declaration to a person. Adoption is the declaration,
one run of `-w`, and one commit.

`spdx` has no default, and a repository that runs the gate without one
gets an error rather than a pass. That is the opposite of every other gate
here: an identifier guessed on your behalf and printed into every file is
worse than none. For the same reason the gate does not infer the
identifier from `LICENSE`. It does read the file: the declaration is
checked against the text through a fingerprint per identifier (`MIT`,
`Apache-2.0`, `AGPL-3.0-only`, `AGPL-3.0-or-later`), so a root file that
says MIT under an `Apache-2.0` declaration fails, naming both. An
identifier with no fingerprint fails too, until one is added to the table.

Two details the check enforces. The blank third line is part of it: in Go
a comment block touching `package` is the package documentation, so an
unseparated notice puts the license at the top of every page on
pkg.go.dev. And the year is a pattern, `2026` or `2024-2026`, not a fixed
value, so the gate does not go red every 1 January.

A file type the gate has no comment marker for is rejected when the config
loads, not skipped during the scan. The Go marker is a legal comment in
most languages, so scanning with the wrong one finds nothing and reports a
clean pass over files nobody checked.

## `spec-lint`: the spec tree and its index

A status column in an index that disagrees with the specs is worse than no
column, because a reader trusts it. The gate holds the index and the specs
to each other.

```yaml
spec:
  dir: specs
  status: [draft, partial, complete]
  require: [title, status]
  index: specs/README.md
  wikilinks: true
```

It checks that frontmatter parses, required keys are present and
non-empty, each status comes from your vocabulary, `depends_on` edges
resolve, the graph is acyclic, every spec appears in the index with the
status it claims, and `[[wikilinks]]` point at something.

Two rules are off unless you ask for them, because both encode a
convention rather than plain hygiene:

```yaml
spec:
  numbered: true
  started: [dispatched, in_progress, testing, complete]
  settled: [complete, superseded]
```

`numbered` requires every file to be `NNN-name.md` and no two to carry the
same number. Reuse is the half that matters: a number is what an index
row, a wikilink and a commit message all resolve through, so handing a
deleted spec's number to the next one silently repoints every citation
that already exists.

`started` and `settled` turn `depends_on` from a note into an ordering. A
spec at a started status whose dependency has not settled was built
against a design that was still moving, and the tree then records an
ordering that never happened. Both vocabularies are yours; `started`
without `settled` is refused at load, because no dependency could ever
close and the gate would fail on everything.

A third rule files the specs that are finished:

```yaml
spec:
  archive:
    dir: .archive
    statuses: [complete, superseded, abandoned]
```

A spec at a terminal status belongs in `specs/.archive/`, and a spec at
any other status belongs beside the ones still being written. Both
directions are checked: the first catches a spec that finished and nobody
moved, the second a spec retired while its work was still open.

Turning it on is also what makes the archive visible at all. Without it a
subdirectory is never read, and statuses inside it drift into free text
because the vocabulary check never sees them. Archived specs are parsed,
held to `require` and to the same `status` vocabulary, and resolved for
`depends_on` and `numbered`. They are not held to the rules that describe
work in progress (sections, markers, registers), because a record written
before a rule existed cannot satisfy it without rewriting history.

`statuses` must be a non-empty subset of `status`, refused at load like a
`started` value the vocabulary does not list. There is no default: what is
terminal is your tree's decision, and `implemented` can mean "shipped,
follow-on work outstanding" in one tree and "done" in another.

Whether the index covers the archive is read off the table rather than
configured. An index holding at least one archive row is an index of the
whole tree and must hold them all; one holding none is an index of the
live work and is asked for nothing.

An index row into the archive is checked for resolution, not for its
status cell. That cell says where the spec went (`archived (superseded)`
against a frontmatter that says `superseded`), which is a different claim.
A row linking into any other subdirectory is left alone, the same way a
`depends_on` edge into another repository is.

Further rules are available for trees that want them: closed vocabularies
for other frontmatter keys, keys a status requires, required sections
overall and by status, spec-scoped identifiers, table well-formedness, an
identifier register one spec defines and others cite, and a status marker
paragraph. [`configuration.md`](configuration.md) lists each key.
Conventions beyond those stay in your repository.

## `lint`: golangci-lint against a rendered config

golangci-lint has no configuration inheritance: its v2 schema rejects an
`extends` key, so a shared config cannot be referenced, only produced.
Before every run, `lint` renders `.golangci.yml` from the module path and
the repository's `.lateregate.yaml` and writes it to the repository root,
where editors look for it. It is not committed: regenerating on every run
makes drift impossible, where a committed copy only makes drift
detectable. Gitignore it; the gate refuses to write over a tracked file.
`lateregate golangci` renders it without linting.

Beyond the standard linters the shared set turns on the ones that catch
bugs rather than style: `bodyclose`, `errorlint`, `nilerr`,
`sqlclosecheck`, `rowserrcheck`, `noctx`, `contextcheck`, `errchkjson`,
`durationcheck`, `copyloopvar`, `unconvert`, `wastedassign`. Four settings
close common holes:

- **Nothing is truncated.** `max-issues-per-linter` and `max-same-issues`
  are both zero. The default turns a list of twenty findings into a list
  of three and hides the rest until the first three are fixed.
- **Every vet analyzer runs.** Enabling the set by name means a toolchain
  that adds an analyzer ships it disabled. `fieldalignment` and `shadow`
  are off by judgment: one trades readable structs for memory layout, the
  other flags idiomatic `if err := f(); err != nil`.
- **Type assertions are checked.** A dropped second result panics on
  exactly the value the assertion was written to handle.
- **The standard library choices are fixed**: `io/ioutil`, `math/rand`,
  `log` and `github.com/pkg/errors` are denied, each with the replacement
  named. `log/slog` is allowed explicitly, since depguard matches by
  prefix.

Log-trace correlation is on by default for every package, as sloglint's
`context: scope`: where a context is in hand, a slog call must use the
`*Context` variant, so the OpenTelemetry bridge can attach a trace and span
id to the record. `golangci.sloglint.request_paths` narrows it to the
packages that serve requests, `exempt` removes paths inside those, and
`context: all` requires the variant everywhere in scope.

A repository adds to the set through `golangci.extra`, which merges over
the shared document. `linters.enable` appends, so adding a linter means "as
well as", never "instead of". Layering rules, which are facts about one
service's directories, belong there:

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

A repository that cannot use the shared config says so with a reason, and
the gate then leaves its committed file alone:

```yaml
golangci:
  own: >-
    vendored third-party tree with its own lint history
```

A declared exception that points at no file fails, because a repository
that lost its config and did not notice is the case the field exists to
catch.

The pre-push hook runs the same linter over the packages a push changes;
see [`commands.md`](commands.md#the-pre-push-prepush).

## `modernize`: fixers that cannot pass silently

`modernize` runs `go fix`'s modernizers and fails on any change they would
make. Turning a fixer off is a decision:

```yaml
modernize:
  disable: [newexpr, errorsastype]
```

That list is the default, because both fixers emit code that does not
compile or that half-applies. `go fix` rejects an unknown `-name=false`, so
if the toolchain drops a fixer you named, the flag would be refused and
the check would report green over a gate that never ran. `lateregate`
verifies each fixer still exists before trusting the flag, and fails if
one is gone.

## `cgo-free`: no `import "C"`

No Go file imports `"C"`. The gate reads the source rather than trusting
one build, because a file can import `"C"` behind a build tag this
platform does not select: the promise a repository that ships static
binaries or cross-compiles makes is that no supported build reaches cgo.
`cgo_free.skip` names directories the scan does not enter, besides `.git`
and `testdata`.

## `otel-client`: a distributed trace stays connected

Two shapes of outbound call lose the trace, both silently:

```go
c := &http.Client{Timeout: 5 * time.Second} // no Transport
http.DefaultClient.Do(req)
```

Either one calls out on the standard library transport, so no client span
is recorded and no `traceparent` header is sent. The service on the other
end opens a fresh trace rather than joining the caller's. Nothing fails
and nothing is logged; the gap shows up later as a trace that stops at a
boundary.

Instrument the client instead, and the downstream spans join the caller's
trace:

```go
c := &http.Client{Timeout: 5 * time.Second, Transport: otel.Transport(nil)}
c := otel.HTTPClient()
```

The scan parses each file rather than matching text. A `Transport` field
several lines below the opening brace still counts, a comment explaining
this rule does not trip the gate, and `Timeout: cfg.Transport.Timeout`
does not pass for a `Transport` field. Test files are excluded: a test
dialing an httptest server has no trace to continue.

A build-tagged harness that deliberately uses the standard library can be
named:

```yaml
otel_client:
  skip: [cellae2e]
```

Keep that list short. A directory skipped here is one whose outbound calls
nobody is asserting anything about.

## `depcheck`: dependencies somebody admitted

A subtree that promises a small footprint loses it on the next `go get`
that pulls in one more import, and nothing else notices. For each package
it names, `depcheck` takes the build list with `go list -deps` (what the
build actually reaches, not what `go.mod` could reach, so a pinned tool
does not count), drops the standard library and the module's own
packages, and fails on any import path no allow prefix admits. Each
package carries the decision behind its list, and each allowed prefix
carries its reason:

```yaml
depcheck:
  packages:
    example.com/yourrepo/cmd/server:
      decision: the server reaches the standard library and the shared module only
      allow:
        latere.ai/x/pkg: the shared library
        go.opentelemetry.io: the telemetry SDK
```

A prefix rather than an exact package, because a dependency's own
subpackages are its business. `depcheck.platforms` lists the
`GOOS/GOARCH` pairs the build list is taken on; empty is the host's own
platform, so a dependency reached only on another platform needs that
platform listed.

## `registers`: the developer's sentence stays off the user's surface

Every sentence a product emits is written for one reader: the user, the
contributor, or the developer debugging a running system. The rule is
[docs/writing/registers.md](https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md)
in `latere.ai/x/pkg`. The leak that survives review most often is a
developer sentence handed to the function that writes the user's error:

```go
api.WriteError(w, "deploy_failed", "apply Deployment insula-p-3f2a/api: store.Deploy not found")
```

The user cannot act on any of that, and the developer detail belongs in a
separate field shown on request. Which functions are user surfaces is the
one part no shared rule can know, so the repository names them:

```yaml
registers:
  user_surfaces: [internal/api.WriteError, cmd/latere.errorf]
```

The gate parses each non-test file, finds calls to those functions by
import path and name, and reads every string literal in their arguments,
including one nested in a `fmt.Sprintf` or a concatenation. Four tells
fail it: a Go import path (`latere.ai/x/pkg/httpjson`; a URL is a page and
passes), a package-qualified identifier (`store.Deploy`, `pgx.ErrNoRows`),
a Kubernetes kind followed by an object name (`Deployment
insula-p-3f2a/api`, `pods "api-7d9c"`; `Service unavailable` is a sentence
and passes), and a file path (`/etc/latere/config.yaml`,
`~/.config/latere/token`, `handler.go:42`; a bare `latere.yaml` is the
file the user edits and passes).

A surface may be written relative to the module or as a full import path.
Only package-level functions are matched, because the scan reads no type
information; name the function the user's output goes through. A surface
that no file calls fails the gate rather than reporting clean over
nothing, so a typo in the name is a failure and not a silently disabled
check. Review still carries the tells no pattern can, such as a hint with
no action.

## `identity`: the family's identity shape

The identity decision the Latere repositories share: the issuer issues
identity and membership; an open core verifies one token, forwards every
claim, and asks one authorizer; a service verifies that the audience is
itself and decides from its own state; nobody calls the issuer while
serving a request; there is one hop mechanism and no delegation inside a
token; and access is by role. This gate runs those rules on every push.
Every repository declares which layer it is:

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

`none` is a library or a tool. It is a declared role and not an absent
one: a repository with no block fails the gate, and `contract` reports the
missing block the way it reports a missing hook. The role then selects the
rules, and every rule is a scan of non-test Go files, deployment
manifests, and documents that describe the current system. A record is
not read: a changelog, a release note, anything under an `.archive`
directory, and a spec whose status the tree's `spec.settled` list calls
finished, because a record may name the mechanism it retired. Nothing here
runs a service.

The `roles` rule also reads the frontend: `.ts`, `.tsx`, `.jsx`, `.vue`,
`.svelte`, `.js`, `.mjs` and `.cjs` files, because a page decides access
as surely as a handler does. What no person in the repository wrote is not
read: `node_modules/`, `testdata/`, a bundler's output (`dist/`, `build/`,
a `.min` or `.bundle` file, a file carrying a `@generated`, `Code
generated by` or `DO NOT EDIT` header), an archive, and whatever `git
check-ignore` calls ignored, so a file on a laptop and not in a checkout
cannot make the gate red locally and green on the runner. A test is not
read either: a `.test` or `.spec` segment before the extension, or a
`__tests__` directory, told by the path. Inside a file that is read, a
comment is prose and a string is not: the sentence explaining why a file
stopped reading a flag is not a use of it.

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

Each finding is a file, a line, and a sentence saying what to do. A rule
with nothing to read prints `SKIP` and why, so a repository with no
`deploy/` learns that the audience rule did not run rather than reading it
as a pass.

The rules that read string literals read what a file says and not what it
imports. An import path is a string in the grammar and a dependency in the
file, so a package whose path holds `authorize` or `roles` is neither a
hand-rolled request nor a claim read for meaning. The rules that ask what
a file imports, `verifier` and `authorizer`, read the import list itself.

Two of the rules are heuristics. A file that both decodes unpadded base64
and splits a string on `.` is taking a token apart, which neither half
alone would show. A container is this repository's when the name its
image was built under (the last path segment of the reference, without the
registry, the tag and the digest) is exactly a command the repository
builds: `origo` runs `ghcr.io/latere-ai/origo:v1` and never
`origo-stubs`, and a manifest of another workload beside this one is
reported as a `SKIP` with its reason. An `initContainers` entry is read as
well when it declares a variable of the repository's own prefix, which is
how a check that runs before the workload says it runs with the workload's
configuration. An overlay that patches a container by name and carries no
image is the container it merges into. A path a heuristic reads wrong goes
in `skip`.

A command is a directory under `cmd/`, and it is also the module root when
the root is itself a `package main`. A repository whose image was built
under neither name declares it:

```yaml
identity:
  role: service
  audience: wallfacer
  image: wallfacerd       # the module is wallfacer; the workload is wallfacerd
```

The value is that one segment: a registry, a path, a tag or a digest in it
is refused. The key belongs to `issuer`, `core`, `service` and `platform`,
the roles whose deployments the `audience` and `bearers` rules read;
anywhere else the load refuses it.

A repository behind on one rule waives that rule and keeps the others
running, which a gate waiver could not do:

```yaml
identity:
  role: core
  waive:
    authorizer:
      until: 2026-11-30
      reason: the shared authorizer package is not adopted yet
```

A waived rule still runs and reports its findings under `WAIV`, so the
count stays visible and a waiver whose rule already holds says the waiver
can go. Past its date the rule fails on its own terms and the line says
which waiver expired. A waiver naming no rule, or a rule the role does not
run, fails the load.

An open core sometimes keeps one company's own deployment in its tree,
because the tag deploys from it. `overlays` names those paths, and the
`no-latere-value` rule reads them rather than scanning them. The rule
collects every `latere.ai` and `latere.svc` address the overlay's files
set (a line's value, not its comments), and a document may name one of
those. That address is the whole of the exemption: naming the overlay's
path in a sentence admits nothing beside it, and the same address in a Go
file or in a manifest outside the overlay, an address the overlay only
mentions in a comment, and any address no declared overlay carries are all
held as before. A declared path the tree does not hold stops the run.

`authorizer` catches a repository that asks the authorizer in a shape of
its own. `envelope` catches the other half: a repository that answers in
one, or that decodes the answer into fields it wrote itself. The evidence
is the JSON tags of a struct in a non-test Go file, because a type that
marshals the envelope is what puts the shape on the wire. Tag names are
matched whole, so an `actions` list, an `allowed_hosts` set, a
`ttl_seconds` figure and a `reasons` array are not the envelope. The
module the envelope is declared in, `latere.ai/x/pkg`, reports a `SKIP`.

Two shapes carry the envelope's field names for reasons of their own: a
core's `limits` type, and the page a list action answers with, which
carries `allow` and `reason` and no `ttl` because a page is not a verdict.
A repository that holds one names it:

```yaml
identity:
  role: core
  envelope_exempt: [authorizer/limits.go]
```

`roles_only` is one way. Once a repository has set it, the gate asks git
whether the history ever carried it, and a tree that unsets it fails.

`bff` lists the paths of a repository's browser frontend, which calls the
issuer's API with the signed-in person's own token; the `request-path`
rule does not read them. `reached_by` says who presents the repository's
audience (`clients`, the default; `services`; or `self-hosted`), which is
what the family check below expects of the issuer's client registry.

Some of the shape is only visible with every repository in view, so that
part is a subcommand over a directory of checkouts:

```sh
go tool lateregate identity family -repos ../checkouts -expect docs/layers.md
```

It fails on a repository with no block, an audience two repositories
claim, an audience a repository verifies and the issuer's client registry
does not list, an audience the registry lists and nobody verifies, and a
client presenting an audience nothing accepts. It prints the layer table
the blocks derive, and `-expect` fails when the committed copy of that
table differs.

## `postgres`: the repository's role against a shared database

When one managed Postgres serves many services, every service that
connects directly claims connection slots per replica, and once more for
its migrations at boot. The fix is a transaction-mode pool per service:
the serving path reads the pooled DSN and the migrator, which holds a
session-scoped lock, keeps the direct one. A two-line convention repeated
across many repositories drifts, so this gate holds it on every push.
Every repository says what it does with the database:

```yaml
postgres:
  role: pooled                 # none | direct | pooled
  direct_env: LUX_DB_URL       # pooled only: the two names this repository reads, written out
  pool_env: LUX_DB_POOL_URL
```

A repository whose names end in `DATABASE_URL` writes neither key and the
gate reads `DATABASE_POOL_URL` and `DATABASE_URL`; one whose Secret
carries a prefix may write `prefix: EVAL` instead, which derives
`EVAL_DATABASE_POOL_URL` and `EVAL_DATABASE_URL`. `direct_env` and
`pool_env` are declared together, never beside `prefix`, never under any
role but `pooled`, and they name two different variables. The gate asks
whether both endpoints are read, not how a service spells them.

| Role | What is checked | What fails |
| --- | --- | --- |
| `none` | no non-test Go file imports a Postgres client | any client import; the finding names the file and the line and says to declare `direct` or `pooled` |
| `direct` | a live waiver of this gate covers the repository; the row prints its date and its reason | no waiver, or one past its date. The finding names both ways out: move to `pooled`, or write a reason and a later date |
| `pooled` | a client is imported; some file reads the pooled name; some file reads the direct name | the missing one of the three, named |
| absent | no client is imported | any client import; the finding names the file and the three roles. A tree with no client passes and the report says the decision came from the imports |

A Postgres client is the pgx tree at any version, `lib/pq`,
golang-migrate's `postgres` and `pgx` drivers, and the shared migration
runner in `latere.ai/x/pkg`. `database/sql` alone is generic and is not
one; the driver it is opened with is, and that import is the finding.

A read of a name is one of two shapes: a call to something named for the
environment (`os.Getenv`, `os.LookupEnv`, a local `getenv`, an `envOr`
helper) with the name as a string literal or as a constant the package
binds to it, or a struct tag under the `env` key. A name in a comment, a
log line or an error message is a mention and not a read. Nothing is
type-checked for this part. What the gate cannot see is which client each
name reaches: that the pooled DSN opens the pool and the direct DSN opens
the migrator is dataflow, and stays a review item.

`direct` is an exception and costs a dated reason. The role passes only
while the repository waives this gate:

```yaml
postgres:
  role: direct

waive:
  postgres:
    reason: a library, and the consumer that calls it owns the connection
    until: 2026-12-19
```

It is the same waiver every other gate takes, so the plan lists the
repository as `WAIV postgres` and the date retires the exception. Past
that date the gate runs and the role fails, naming the date and what the
waiver claimed. The gate reads the waiver itself rather than leaving it to
the plan, because `lateregate postgres` runs one gate by name and builds
no plan.

A tree with a `pooled` role and no Go file to read fails rather than
passing. `none` over such a tree passes, because the role claims nothing a
file would have to show. `contract` prints the declared role in its
in-shape line and does not report an absent block as drift; `init` writes
no block, because the role is a decision.

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

A pooled DSN carries `default_query_exec_mode=exec`, because a
transaction pooler hands the next transaction a different backend and pgx
cannot keep a prepared statement on the server. In that mode the server
describes no parameter, so the driver picks the wire encoding from the Go
type alone and sends every parameter in the text format. Two things go
wrong there, and an endpoint that describes the statement first hides
both, which is why no test on a direct connection sees either.

A `[]byte` goes as `bytea` and arrives as a hex literal, which a json
column refuses with SQLSTATE 22P02. On a parameter something says is json,
the check names what is accepted rather than what is refused: a string
under any name, a pointer to one, `json.RawMessage`, a value with a text
or a database value of its own, a number, and an untyped nil. Everything
else is reported. A parameter is json when the statement casts it,
`$6::jsonb` or `CAST($6 AS json)`, or when the value is a json encoding: a
marshaller's result, any `MarshalJSON`, or a raw message, followed through
conversions, locals, and the module's own helpers including those whose
declared result is `any`.

A Go struct, a Go map, or a list of a repository's own named type has no
encoding at all, and the call fails in the driver with `cannot find encode
plan` before the statement is sent. That one is reported at any
parameter, because the driver's plan lookup reads the Go type and never
the column.

A `[]byte` bound to a `bytea` column is correct and is never reported,
and neither is a clock reading, an identifier type with a text method, or
a `[]string` bound to a `text[]` column. A parameter the statement casts
to something that is not json, `$3::bytea`, is never reported as a
carrier. The check type-checks the module, so a repository whose
statements do not build is an error naming what failed rather than a
pass. A tree that calls nothing statement-shaped is not type-checked at
all and the report says so.

## `enum-go` and `enum-typescript`: protocol values stay out of implementation

These gates enforce three properties for the domains a repository names:
fields retain their enum type, implementation uses named members or typed
values, and switches handle every distinct enum value. They do not guess
whether a string is a status, an ID, or an open protocol value. Naming the
domains is the repository's decision:

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
also own fields. The representation is a native string or numeric `enum`.
A literal union or an `as const` object is not a configured native enum. A
required field cannot widen to `string`, `number`, another enum, or a
union with those types. Optional TypeScript fields may also hold
`undefined`; optional Go fields may use a pointer to the configured type.

With `Status` configured, Go accepts `var s Status = Running` and rejects
`var s Status = "running"`. TypeScript accepts `status ===
Status.Running` and rejects `status === "running"`. Both gates check
assignments, arguments, returns, composite values and switches. `default`
does not cover a missing member. Duplicate-valued members count as one
value. Explicit primitive casts immediately before comparisons or switches
do not hide the original enum domain.

Use a parser that validates external values and returns named members at
input boundaries. When a parser needs a direct conversion, declare the
function and why:

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
inside a nested callback. It does not prove the parser validates its
input; boundary tests must establish that. Enum-to-primitive conversion
remains available for serialization. This is a source and type check, not
runtime validation or dataflow tracking through arbitrary primitive
variables.

```sh
go tool lateregate enum-go
go tool lateregate enum-typescript-prepare  # after cloning or changing a lockfile
go tool lateregate enum-typescript
```

The Go gate uses the module's pinned dependencies with `GOWORK=off` and
`-mod=readonly`, under the current platform and build tags. It scans
production packages, not tests. A configured type or field that does not
resolve fails. Run the gate for each build configuration whose domain code
differs.

For a TypeScript-only repository, use an installed `lateregate` binary and
invoke `enum-typescript` directly; that command does not need a consumer
`go.mod`. The no-argument bar and the reusable Go workflow still assume a
Go repository.

The TypeScript gate needs Node and the project's installed `typescript`.
Vue script blocks additionally need `@vue/compiler-sfc`; script-setup
macros use the project's Vue types. Use the tsconfig that includes
application source, rather than a solution tsconfig containing only
project references. The gate reads TypeScript, TSX and Vue scripts,
including imported domains; Vue templates and component props remain the
responsibility of `vue-tsc`. External Vue `script src` blocks fail with a
diagnostic: include the source as a TypeScript file instead. Test files
and declaration files are not implementation surfaces; ambient
declarations still participate in typing. Compiler and configuration
errors fail the gate.

`enum-typescript-prepare` finds each project's nearest npm or Bun lockfile
inside the repository, verifies the lockfile and `package.json` are
tracked, and runs `npm ci` or `bun install --frozen-lockfile` once per
package-manager root. Multiple competing lockfiles fail. Checking itself
never installs packages. The reusable CI workflow prepares dependencies
only for `enum-typescript`, so Go-only repositories need neither Node nor
Bun.

## `fmt-check` and `vuln`

`fmt-check` fails on any tracked Go file `gofmt` would change. `vuln` runs
govulncheck at the version pinned in this repository and fails on a
vulnerability reachable from the module's code. `vuln` needs the network
and can change verdict with no commit, which is why neither hook runs it.
