# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

### Added

- **`scripts/bump-private.sh` moves a private repository's pin from a
  workstation.** It clones each private repository, moves the
  `lateregate` pin to the latest release, runs the whole bar and pushes
  only when every gate passes. A private repository no longer moves its
  pin on GitHub Actions ([releasing guide](docs/releasing.md#moving-the-pin)).

### Changed

- **`hermetic` and the suite's stripped `PATH` hold only `go` and `gofmt`.**
  The stripped `PATH` used to hold the whole directory `go` was found in,
  which on Homebrew is hundreds of tools. A test that calls another of them
  fails here as it would on a runner: add that tool's directory to
  `hermetic.allow`, or stop calling it.
- **`otel-client` reports the `net/http` helpers that use the default
  client.** `http.Get`, `http.Post`, `http.Head`, `http.PostForm`,
  `new(http.Client)` and a zero `var c http.Client` are reported, by the gate
  and by the pre-commit hook. Send the call through an `http.Client` whose
  `Transport` is instrumented.
- **`registers` reads messages held in constants.** A package-level string
  constant passed to a register call is read, whether it is declared in the
  same package, in another package of the tree, or as a sum of constants, and
  a call through a versioned import path such as `.../v2` is matched. A
  developer sentence kept in a constant fails the gate: move it to the log or
  the details field.
- **The source scans read the files git tracks.** `cgo-free`, `otel-client`,
  `registers`, `platform-via-sdk` and `license` read what `git ls-files`
  lists, not a walk of the directory, so a laptop with scratch files or
  ignored build output reads what a clean CI checkout reads. Outside a git
  checkout they fail and say so. `license` leaves out `vendor`, and
  `cgo-free` leaves out `node_modules`.
- **`cgo-free` parses imports.** `import "C"` is found however it is written,
  and a tracked Go file that does not parse fails the gate with its name and
  the parse error.
- **`license.holder` has no default.** The gate fails until it is set to the
  copyright holder. A copyright year must lie between 1970 and next year.
- **`golangci.extra` may only add to the shared bar.** An `extra` that
  disables a linter, empties a deny list or replaces the exclusion rules is
  refused.
- **`spec-lint` applies where `spec.dir` points.** A repository whose specs
  live outside `specs/` is linted.
- **`modernize` fails when `go fix` does not run.** A failing `go fix` with
  no patch passes only when its error is a build error, which the build gates
  report; an unknown flag, a crash or a missing tool fails the gate.
- **A `cover.exempt` key matches whole path elements.** `api` exempts
  `example.com/m/api`, not `example.com/m/grapi`. An empty key in
  `cover.exempt` or `tempdir.allow` is refused, since it would match
  everything.
- **`contract` reports every key that restates its default.** The list
  includes `license.extensions: [.go]`, `tempdir.command`,
  `identity.reached_by: clients`, and an e2e suite's default `timeout` and
  `on`. Delete the line.
- **Every command refuses a waiver that names no gate.** The hooks,
  `contract` and `init` refuse it too, where only the commands that plan the
  bar did.
- **A leak is reported on every run until it is gone.** After the suite or
  `tempdir` finds something left behind, the next runs bypass the test cache
  until one leaves nothing, so a cached pass cannot hide the leak.
- **A helper's container sweep leaves the current run's containers to the
  run.** A container one test package leaks is no longer removed by a later
  package's `reap.Sweep`, so the suite names it. Give each test package a
  `TestMain` that stops what it starts.
- **A removal the engine refuses no longer turns the leak check off.** The
  opening sweep prints the refusal as a warning and the watch starts, so a
  suite that leaks fails where it used to pass silently.
- **A command comes before its flags.** `lateregate -C DIR release v1.2.3`
  ran the bar and exited 0; it is refused with a message that shows the right
  order, and `check` takes no argument. `lateregate <command> -h` prints the
  usage. `CI=false` and `CI=0` are not a CI run, so a laptop that sets them
  gets the change-scoped bar.
- **`init` leaves a repository's own choices alone.** It writes
  `.github/workflows/ci.yml` only when no workflow calls the shared one, at
  any ref, and keeps a `core.hooksPath` set to something other than
  `.githooks`, printing the command to change it. It needs a git repository
  and writes nothing outside one. An existing `pre-commit` or `pre-push`
  without the executable bit is made executable, since git never ran it. The
  closing `next:` line lists only the steps still left.
- **`release` reads its version more strictly.** It refuses a version that is
  not valid semver, a version the changelog already has a section for, and a
  cut while a CI run at `HEAD` is unfinished, even when an older run is
  green. `release.stamp` replaces a prerelease version whole, suffix
  included.

### Fixed

- **The change-scoped bar reaches what a change reaches.** A file the build
  leaves out by a build tag counts when finding the importers of a changed
  package, and a change in a nested module the root module reaches through
  `replace` or `go.work` checks the whole tree.
- **`push` moves `latest` only for origin's newest release tag**, and stops
  when origin's tags cannot be read.
- **`deploy` refuses an archive entry written through a link it created.**
- **`scripts/bump-private.sh` goes on to the next repository after a command
  fails**, and refuses a repository argument that is not a plain name.
- **`fmt-check` and the pre-commit hook agree with the compiler.** They use
  the `gofmt` of the toolchain `-go` names, leave out `testdata` and files
  deleted in the working tree, and show `gofmt`'s own message when it fails.
- **A package below the coverage floor never shows the floor's figure.** The
  percentage is cut off at one decimal, not rounded up. A package whose only
  code is a function literal in a `var` is listed.
- **`spec-lint` resolves a wikilink with an alias or a heading**, and
  compares the index status ignoring case when there is no status vocabulary,
  exactly when there is one.
- **`identity`'s closing line counts only the rules that ran** and names the
  ones it skipped.
- **`manifests` reads a kustomization's `kind` as YAML**, so a `Component`
  written with a comment, quotes or flow style is left out.
- **A `words` glob's `**` matches any number of directories**, none
  included, over the tracked files. A glob that only an ignored or untracked
  file satisfies matches no file.
- **A tracked path with a space or a byte outside ASCII is read.** Every list
  of tracked files is read NUL separated, where git quoted such a path and
  the gates passed over it.
- **The `tempdir` sandbox belongs to one checkout.** Two checkouts whose
  directories share a name get a sandbox and a lock each. A read-only tree
  under the sandbox is made writable before it is removed, and an interrupt
  during the suite or `tempdir` releases the sandbox, removes the run's
  containers, and exits 130 or 143.
- **The container gate says which engine it could not reach.** It names a TCP
  or SSH `DOCKER_HOST`, or lists the places it looked, and reads `docker.host`
  from `~/.testcontainers.properties` as the Testcontainers clients do. The
  run's own check removes only what carries its run label, whatever the
  engine's filter returns, and a machine that cannot name itself labels its
  containers with a host name a sweep can read.
- **An e2e service container is the run's own.** Its name carries a random
  suffix, only the container a start reported is removed, and a start that
  fails shows the engine's reason.
- **The `check` summary shows `FAIL` for a gate whose error has an empty
  first line.**

## v0.70.1 - 2026-11-06

### Fixed

- **`deploy` with podman rolls out the digest the registry has.** Podman
  lists every digest it knows for an image, and one under the image's own
  name can be the digest it computed at build time, which the registry
  never had; `deploy` took the first listed, so the rollout could not pull
  its image. It now sets the digest of the manifest it just pulled, and
  refuses before touching the cluster when podman does not list that digest
  under the image's name.

## v0.70.0 - 2026-11-06

### Changed

- **`lint` refuses `encoding/json` (v1).** The shared depguard rule now
  denies the v1 package and allows `encoding/json/v2` and
  `encoding/json/jsontext` by name. v1 keeps legacy behavior that v2 makes
  explicit: case-insensitive field names, duplicate object keys, and invalid
  UTF-8 silently replaced. `json.RawMessage` is an alias of
  `jsontext.Value`, so no exported type needs the v1 import.

### Fixed

- **The pre-push lint skips a directory no build compiles.** A push that
  changed a program tagged `//go:build ignore` (run as `go run file.go`)
  named its directory to golangci-lint, which cannot type-check it, and the
  push was refused with `build constraints exclude all Go files`. The full
  gate never read that directory either, since `./...` leaves it out.

## v0.69.0 - 2026-11-06

### Changed

- `ship.image` and `ship.deploy` are optional. A private repository that
  publishes no image and deploys nothing declares `ship: {}` and still
  stops running push CI on GitHub: `release` reads no CI, `contract` asks
  for no push workflow and `init` writes none. `build` runs the whole bar
  and says it builds no image; `push` refuses without an image, and `deploy`
  and `deploy-credential` refuse without a deploy block. A `deploy` with no
  `image`, and a `ship:` or `image:` or `deploy:` key written with no value,
  are refused.
- `lateregate deploy` runs the live check again when it fails, every 5
  seconds until it passes or 2 minutes have passed since the first attempt,
  instead of failing the deploy on the first answer from an old pod or a
  cache. The final failure says how many attempts ran. A smoke file missing
  from the tag is still refused at once.

## v0.68.1 - 2026-11-06

### Changed

- `lateregate deploy` runs `preflight` and `after_roll` with `TAG`,
  `COMMIT` and `IMAGE` (the image pinned by digest) in their environment,
  so a step can check or apply what the tag carries.

## v0.68.0 - 2026-11-06

### Added

- **A `ship` block makes a repository build, publish and deploy from a
  workstation.** It names the image (registry path, Dockerfile, platform,
  version argument, `prepare` commands) and the deploy (a 1Password reference
  to the rollout kubeconfig, namespace, deployment, live-check URL and
  script, `preflight` and `after_roll` commands). See
  `docs/configuration.md` and `docs/releasing.md`.
- **`lateregate build`** runs the whole bar and builds the image for the
  cluster's platform, left in the local engine as `dev-<short sha>`.
- **`lateregate push`** builds from a clean tree on a `vX.Y.Z` tag and
  publishes `<tag>`, `sha-<short sha>` and `latest` (not for a prerelease),
  and prints the digest. A refused login says which token scope the engine
  needs.
- **`lateregate deploy [vX.Y.Z]`** rolls the newest stable tag, or the one
  named, out from the tag's own tree, pinned to the image's digest, through
  a kubeconfig read from 1Password into a file that is removed on every exit,
  then runs the live check and creates the GitHub release if it is missing.
- **`lateregate deploy-credential -context NAME`** stores the rollout
  identity's kubeconfig as the 1Password Document `deploy` reads, once per
  repository, after proving the identity can apply `deploy/prod`.

### Changed

- **`lateregate release` of a repository with a `ship` block reads no CI**
  and creates the GitHub release from the changelog once the tag is pushed.
- **`contract` and `init` ask for no push workflow in a repository with a
  `ship` block.** A caller it does keep is checked as before.

## v0.67.0 - 2026-11-05

### Added

- **`action-pins` holds every third-party action to the organization's one
  commit.** It applies when git tracks a workflow under `.github/workflows` or
  an `action.yml`, and it fails, one `file:line` per reference, on an action
  that is not in the list, a ref that is a tag, a branch, a short commit or
  another commit, and a trailing comment that is not `# <version>`.
  Repositories of `latere-ai` itself, `./` paths and `docker://` images are
  left out. Run `go tool lateregate action-pins -w` to move every listed
  action to the list's commit and comment; an action missing from the list
  needs it added to `internal/actionpins/action.yml` in latere-ai/ci-gate first.
  A repository that is not ready waives the gate with a reason and a date.

## v0.66.0 - 2026-11-05

### Added

- **An `e2e` suite may list `node` and `bun` under `tools`.** The plan then
  asks the pipeline to install them the way it does for the frontend gate, so
  a suite that builds a Bun frontend runs on CI. lateregate does not fetch
  them: it checks they are on `PATH` and fails the suite, naming the tool, when
  they are not.

## v0.65.0 - 2026-11-05

### Added

- **`e2e` runs the suites that need real services.** A repository lists
  each in `e2e` with a `name`, a `why`, and either `test` (Go packages, tags,
  `run`, `race`, `count`) or a `command`. `services` starts Postgres or MinIO
  from images pinned by digest on ports the engine picks, with `buckets`
  created first, and `env` receives their addresses as `${postgres.url}`,
  `${minio.endpoint}`, `${minio.access_key}`, `${minio.secret_key}` and
  `${minio.region}`. `tools` fetches `kubectl`, `kind` and `helm` at pinned,
  checksum-verified versions onto the suite's `PATH`. Every container is
  removed when the suite ends or the run is interrupted. A skipped test
  fails its suite, named with its skip message, unless `allow_skip` gives the
  reason; a suite in which no test ran fails too. `lateregate e2e` runs the
  suites that run on a push, `-only NAME` runs one, and `-on tag` runs the
  tag suites. Under `-since` or on a laptop a test suite follows the Go
  rule, and a command suite runs when a change is under its `reads`. The
  plan names `docker` and `tool:NAME` in `needs`.
- **A gate run by name takes `-since`.** `lateregate <gate> -since green|REV`
  measures the change as `check` does, so a gate that narrows reads only
  what it reaches. Without `-since` a gate run by name reads the whole tree
  as before.
- **`modules` tests the Go modules beside the root.** A directory with its
  own `go.mod`, such as `tools/specindex`, is outside the root's `./...`
  and nothing tested it. List it under `modules` and the `suite` gate runs,
  after the root, `go vet` and the covering race test in it, under the same
  stripped `PATH` and `TMPDIR` sandbox, with the cover floor on its
  packages and a report per module. A failing module does not stop the
  next, and a waiver applies to all. `lint` runs in each module too, unless
  `golangci.own` keeps a single configuration at the root. On a laptop a
  module runs when a changed file lies inside it, and a change to its
  `go.mod` does not send the root to its whole tree.

## v0.64.0 - 2026-11-05

### Added

- **CI can measure a change from the last green commit.** `check` and
  `list` take `-since green` or `-since REV`. `green` is the newest commit
  on the default branch that the calling workflow passed on a push and that
  HEAD contains, read through the GitHub API; when it cannot be had, for any
  reason, the whole tree is checked and the reason is printed. With `CI`
  set and a base, `frontend`, `image` and `lean` keep their rules, the gates
  that read Go (`fmt-check`, `modernize`, `cgo-free`, `otel-client`, `lint`,
  `vuln`, `suite` and what folds into it) are skipped when every changed
  file is inert to Go and otherwise read the whole tree, and every other
  gate runs. A file is inert to Go when no package directory, embed pattern
  or string literal in the repository's Go files reaches it.
- **`lateregate base -since green|REV` prints the base.** It prints the
  commit's SHA, or nothing and the reason on stderr when the whole tree
  must be checked, and exits 0 either way, so a workflow can resolve the
  base once and pass it to every `list`.
- **`frontend.reads` names what a project's tests read outside it.** A
  changed file under one of the listed directories runs the frontend gate,
  on a laptop and on CI.
- **The plan names the tools each running gate needs.** `list -json` entries
  gain `needs`: `node` and `bun` for `frontend` and `enum-typescript`, and
  `chromium:<dir>` for each project `frontend.browser` lists. The `test`
  entry gains `os`, the runner labels from the new `test.os` key. Both are
  left out when empty, and the JSON stays an array.
- **`frontend.browser` and `test.os` in `.lateregate.yaml`.** The first
  lists projects, each also in `frontend.projects`, whose tests drive
  Chromium. The second lists runner labels for the plain test on other
  systems, such as `macos-latest`.
- **The frontend gate runs an `e2e` script.** After `typecheck`, `test` and
  `build`, a package that defines `e2e` runs it. The gate installs no
  browser: the pipeline installs one from the plan.

### Changed

- **`lateregate init` writes one caller.** `.github/workflows/ci.yml` is the
  same bytes in every repository: push to main, pull requests and dispatch,
  the concurrency block, `contents: read` and `actions: read`, and one job
  with no inputs. `contract` accepts it under `ci.yml` or `verify.yml`, finds
  the module that pins the tool without `gate_module` (the root, or the one
  tracked `go.mod` that has the `tool` line, and names them when there are
  none or several), and notes a caller that differs. A caller that still
  passes `gate_module` keeps working.

## v0.63.0 - 2026-11-05

### Changed

- **`lateregate release` works in a repository with no Go.** The gates
  that check Go code (`fmt-check`, `modernize`, `cgo-free`, `otel-client`,
  `lint`, `vuln` and `suite`, with the properties `suite` folds) now apply
  only when git tracks a Go file. With none, each skips itself and says
  `tracks no Go file`, as `tidy` does with no `go.mod`, so a TypeScript,
  Dockerfile or shell repository that keeps a `go.mod` only to pin
  `lateregate` passes the bar and can cut a release. `license` applies when
  git tracks a file of a type it is configured to check, and `tempdir` with
  its own `tempdir.command` still runs in any language.
- **A `release.stamp` can move a version written without a leading `v`.** A
  match that holds no `vX.Y.Z` but holds a bare `X.Y.Z`, such as a
  package.json `"version": "2.2.3"` or a Go `const Version = "0.32.0"`, is
  rewritten to the release without its `v`. A match that holds a `vX.Y.Z`
  behaves as before. The refusal for a match with nothing to move now reads
  `holds no vX.Y.Z or X.Y.Z to move`.

## v0.62.1 - 2026-11-01

### Fixed

- **The `image` gate prints a container's logs when it exits at start.**
  It printed them only when the probe ran out, so a server that died
  before its port could be read failed with no reason given.

## v0.62.0 - 2026-11-01

### Added

- **`contract` reads a nested gate module.** A caller may pin the shared
  workflow to one commit as well as `@v1`, and may pass it a
  `gate_module`, the directory of the module that pins the tool; the pin
  is then read from that module's `go.mod`. A Makefile recipe that reaches
  the tool through a make variable such as `$(LATEREGATE)` reads as
  delegating. See docs/commands.md, "The wiring".

- **Four gates for what a release reads: `tidy`, `actionlint`,
  `manifests` and `rules`.** `tidy` fails when `go mod tidy` would change
  a tracked module. `actionlint` lints the workflows with actionlint
  v1.7.12. `manifests` renders every kustomization with kustomize v5.7.1.
  `rules` checks every PrometheusRule's groups with promtool v3.15.1,
  downloaded once per machine and checked against its digest. Each runs
  where git tracks its subject, so a service can drop the workflow job
  that did the same check with a pin of its own. See docs/gates.md.
- **A `frontend` gate for the Bun packages a repository names** in
  `frontend.projects`: a frozen install from the tracked `bun.lock`, then
  whichever of `typecheck`, `test` and `build` the package defines. Bun
  and Node come from `PATH`; the shared pipeline sets them up when the
  gate runs. On a laptop it runs only when a named package changed.
- **An `image` gate for the container images a repository names** in
  `image.images`: each builds, prints a version that starts with the
  expected prefix, stays under its size cap, holds no shell unless
  `allow_shell` gives the reason, and starts with a read-only root file
  system and no capabilities and answers its probe, unless `skip_start`
  gives the reason. Docker or podman comes from `PATH`. On a laptop it
  runs only when a file an image is built from changed. A service can
  drop the workflow job that ran the same checks. See docs/gates.md.

## v0.61.0 - 2026-10-31

### Fixed

- **Two releases cut at once can no longer both push a tag.**
  `lateregate release` now fetches origin's default branch before the bar
  and again before it writes, and refuses unless `HEAD` is origin's tip of
  that branch. Any checkout at that tip can cut it, a worktree on a branch
  of its own or a detached `HEAD` included. A checkout behind is told how
  many commits to pull; one ahead is told to push and let CI verify first.
  It reads tags from origin rather than the checkout, and refuses a
  version origin already has or one that is not newer than origin's newest
  release tag. The commit and the tag go out in one atomic push to the
  default branch by name, so a release that lost the race pushes nothing,
  not a tag on a commit off main, and a release cut from a worktree's own
  branch no longer creates a stray remote branch. A rejected push deletes
  the local tag and resets to the commit the cut started from; run
  `git pull --ff-only` and cut again, and if `## Unreleased` is then
  empty, the other release already shipped those notes.

## v0.60.0 - 2026-10-31

### Changed

- **A laptop check holds the packages a change reaches without touching
  to the coverage they had at the base.** A change to one package can
  lower another's coverage when the first package's tests were what
  covered it. The floor now runs the same tests again over the base,
  rebuilt in place with `go test -overlay`, and compares every untouched
  package their test binaries link, block by block. A package that kept
  every block keeps the figure CI measured; one that lost a block is
  measured over every test that reaches it and held to the floor. When a
  changed file is read by tests at run time, such as testdata, or the
  base's tests fail here, the packages those tests reach are measured in
  full. The base's run costs about as long as the run itself the first
  time and replays from the test cache after; on this repository a
  four-package change checks in 15 seconds warm, against 10 before.

## v0.59.1 - 2026-10-31

### Fixed

- **A laptop check now catches what a change breaks in its callers.**
  Lint reads the changed packages and every package that imports them,
  as the test suite does, so a changed signature or a removed export
  fails before the push instead of in CI.
- **The vulnerability scan runs on a laptop when a Go package changed.**
  New code can call a vulnerable function nothing called before; the scan
  reads the whole module, as on a runner. A change to no Go package
  still skips it.
- **A file a package embeds from a sub-package's directory is that
  package's change too**, so its tests run when the file changes.
- **A directory with changed Go files but no package `go list` reports
  checks the whole tree**, which covers a package a build constraint
  excludes on this machine as well as a removed one. So does any change
  under `vendor/`.
- **A change confined to packages with no statements, or ones the floor
  exempts, passes the floor** and says `the floor measures nothing here`,
  instead of failing for measuring nothing.
- **A module below its repository's top level reads its own change.**
  Changed paths were relative to the repository rather than the module,
  so no package matched them and the check narrowed to nothing.
- The coverage floor applies to the changed packages only, so a package
  the change did not touch can still drop under it when a changed
  package's tests that covered it are removed; CI, which reads the whole
  tree, catches that. `docs/commands.md` lists it with the other case a
  laptop check cannot see.

## v0.59.0 - 2026-10-30

### Changed

- **On a laptop, `lateregate` checks what changed since the default
  branch.** The test suite runs over the packages the change touches and
  every package that imports them, with the coverage floor on the changed
  packages; lint reads the changed packages; the vulnerability scan is
  left to CI, which runs on every push; and the Lean gate runs only when a
  Lake project changed. Every other gate still reads the whole tree. A
  change to a frontend, a document or the changelog checks in seconds
  instead of running the whole suite, and a change to one Go package in
  minutes. The first line says what the run read, and the verdict ends
  `on what changed since origin/main (...)`. A change to `go.mod`,
  `go.sum`, `.lateregate.yaml` or an owned `.golangci.yml`, a removed
  package, or a change that cannot be measured checks the whole tree and
  says why. `lateregate -all` checks the whole tree on request; CI (`CI`
  set), `lateregate release`, a gate run by name and `list` always do. A
  test that reads files outside its package by a relative path, such as
  `../docs`, can pass on a laptop and fail in CI when only those files
  changed: run `-all` before such a push.
- **No lint run waits for another.** The full `lint` gate no longer takes
  golangci-lint's machine-wide lock, as the pre-push lint already did, so
  two checkouts gated at once run side by side instead of one failing
  with "parallel golangci-lint is running".

## v0.58.0 - 2026-10-29

### Changed

- **`platform-via-sdk` names the memory package's call for each memory
  route.** A client that writes a route under `/v1/account/memory` itself
  is told which call of `latere.ai/x/sdk-go/memory` makes it, such as
  `use UpdateSettings in latere.ai/x/sdk-go/memory` for
  `/v1/account/memory/settings`, where the finding said to add the call
  to the SDK. The table covers every memory route of the platform's
  account document: the list, add and forget everything, the settings,
  what was forgotten and its restore, export, import, and one memory of
  You or of a project. The memory base followed by a path the source does
  not fix names the package.
  A repository changes nothing: a memory route a client writes was already a finding, and only what the finding says to use is new.

## v0.57.0 - 2026-10-29

### Added

- **`words`: what a model or a person reads keeps to the do-not-use list.** The binary carries the organization's do-not-use list, `internal/words/do-not-use.yaml`: twenty-one words, each with the readers it is banned for (model-facing, person-facing, spec-overview), the forms matched, the reason and the words to write instead. In a repository that names a place to read, each form fails the gate where its reader reads it: the files `words.model_facing` matches and their names, read as tool names, with only the highest `-vN` of each tool's description read, since a released version is kept for replay and no longer offered (`words.older_versions` keeps an older one a repository still offers); the string files `words.person_facing` matches; and, with `words.spec_overview`, each spec's frontmatter title and `## Overview` section. Matching is by whole word and case-insensitive, with identifiers split at `_`, `-` and a change of case. In a `.go`, `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs` or `.json` file only the strings are read, not code, comments, an object's keys or a `{name}` placeholder; in a spec, neither a link's target nor a `[[wikilink]]`'s file name is read. Each finding names the file, the line, the word, the reader, the reason and the words to write instead. `words.allow` takes a list word, a longer text such as a wire name, a path glob or a text and a path, each with its reason; an entry no hit needs fails the gate. `words.vocabulary` adds a list of the repository's own in the same shape; a word or a form the carried list already has fails. A glob that matches nothing, and a `words.vocabulary` file that is missing or holds no list, fail with what to do. Nothing changes for a repository whose `.lateregate.yaml` has no `words` block: the gate is skipped with that reason. The list is in the binary, so the gate needs no other checkout on a runner.

### Changed

- **`enum-typescript` reads TypeScript and TSX only.** It no longer reads Vue single-file components and needs no `@vue/compiler-sfc`: a project's installed `typescript` is all it loads. A selector under `enums.typescript` names a `.ts` or `.tsx` file; one that names a `.vue` file is refused when `.lateregate.yaml` is read. Nothing changes for a repository that holds no `.vue` file. In one that does, `enum-typescript` no longer checks those files, and an `enums.typescript` selector that names one is refused until it names a `.ts` or `.tsx` file.
- **The `identity` gate's `roles` rule no longer reads `.vue` files.** It reads `.ts`, `.tsx`, `.jsx`, `.svelte`, `.js`, `.mjs` and `.cjs`. In a repository that holds `.vue` files, those are no longer checked.

## v0.56.0 - 2026-10-23

### Added

- **`lean`: a repository's Lean proofs build.** A repository that tracks a `lakefile.toml` or `lakefile.lean` gets a gate that runs `lake build --wfail` in each Lake project's directory, so a proof that uses `sorry`, or any other warning, fails it. A `lake` on `PATH` is used as it is; without one, as on a CI runner, the gate installs elan v4.2.4, checked against its published SHA-256, under the user's cache directory, and elan fetches the toolchain the project's `lean-toolchain` names. A repository with no Lake project skips the gate.

## v0.55.0 - 2026-10-22

### Added

- **`platform-via-sdk`: a client reaches the platform through the Go SDK.** In a repository that sets `platform_via_sdk.enabled`, a string that names a platform route fails the gate: `/v1/` and a segment the platform origin serves, such as `agents`, `account` or `storage`, read through concatenation, the module's own constants in any package, and fmt formats. So does a `net/http` request or a `url.URL` addressed to a platform route or to `api.latere.ai`. Test files and the SDK's own module are not read. Each finding names the file, the line and the route, and the call of SDK v0.7.0 that makes it, or says to add the call to `latere.ai/x/sdk-go`. `platform_via_sdk.allow` takes a route with the reason it is not a call to the platform; an entry no finding needs fails the gate. Nothing changes for a repository that does not set `platform_via_sdk.enabled`: the gate is skipped with that reason. A client turns it on once its calls go through the SDK.

### Fixed

- **Built with Go 1.27.3,** which fixes GO-2026-6809, a flaw in the
  standard library's certificate verification that this code can reach.

## v0.54.0 - 2026-10-14

### Added

- **`container-labels`: a package that imports testcontainers-go imports `reap`.** The `containers` watch sees only containers and networks carrying the `reap` labels, so a helper that never adopted the contract leaked as before while the suite reported nothing left behind. The new gate reads every Go package of the tree, test files included, and names each one that imports a testcontainers-go package and no file of which imports `latere.ai/x/ci-gate/reap`. It applies once a package imports testcontainers-go, and is skipped with that reason before. A nested checkout under the tree, such as an agent's worktree, is another repository and is not read. A repository whose tests start containers through testcontainers-go and do not use `reap` fails this gate: adopt the contract in the helper, as the `containers` section of `docs/gates.md` shows, or write a dated waiver while it is behind.

## v0.53.0 - 2026-10-14

### Added

- **`containers` covers networks.** A network a test helper makes with `network.WithLabels(reap.Labels())` is swept once its owner process has exited, after that owner's containers, and a network a run leaves is named, removed and fails the suite beside its containers. `reap.Sweep` counts the networks it removed. A helper that puts its containers on a network of its own labels the network too; until it does, a killed run's network stays on the engine. It holds no memory, but nothing removes it.

## v0.52.0 - 2026-10-13

### Added

- **`containers`: the suite leaves no container behind.** A test helper that starts a container labels it with `reap.Labels()` and sweeps with `reap.Sweep(ctx)`, from the new package `latere.ai/x/ci-gate/reap`. The suite then sweeps what exited test binaries left on the machine, names its run in `LATEREGATE_RUN`, and after `go test` exits removes every container still carrying the run's label and fails on it. A container is removed when its owner process is gone, never for its age. This covers what testcontainers' Ryuk covers, a panic on `-timeout`, a kill, a package with no `TestMain`, on engines where Ryuk cannot start, such as Podman. With no engine socket the suite runs as before and says nothing was watched. `containers` folds into `suite`; a waiver on it turns the watch off. A helper that starts containers adopts both calls, and stops the container a failed start returns along with its error. Until it does, its containers carry no label, and the suite neither sweeps nor checks them.

## v0.51.0 - 2026-10-09

### Changed

- **The lint gate runs golangci-lint v2.14.0.** v2.13.1 is built against a golang.org/x/tools that cannot read the export data Go 1.27.2 writes, so on Go 1.27.2 it failed every package with "export data version 5 is greater than maximum supported version 4". v2.14.0 also brings the `stringscut` modernizer, which asks for `strings.Cut`, `strings.CutLast` and the like over an index and two slices. A repository moving to this release on Go 1.27.2 may see new `stringscut` findings; `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --fix ./...` rewrites them.

### Security

- Built with Go 1.27.2 and golang.org/x/net v0.60.0 (GO-2026-6611, GO-2026-6612, GO-2026-6613, GO-2026-6617).

## v0.50.3 - 2026-10-03

### Changed

- `docs/gates.md` says the rule that compares a started spec with its
  dependencies runs only when `spec.started` is set, and that Latere's
  repositories leave it unset: a spec's status records its own criteria
  and review holds the order `depends_on` states. This repository's own
  `.lateregate.yaml` drops `started` and keeps `settled` for the identity
  gate. No behavior changed.

### Fixed

- `lateregate prepush` lets the first push to an empty remote through. A
  branch the remote lacks was diffed against its merge base with
  `origin/main`, and a remote with no `main` has none, so the hook refused
  the push that would have created it. With no merge base the hook now
  lints the packages of every Go file the pushed commit holds, says so, and
  runs nothing when the commit holds no Go file.
- The coverage floor leaves out Go source under `node_modules`. A
  JavaScript package that ships a Go package, as `flatted` does, was
  instrumented by `-coverpkg=./...` and judged at 0%, so `suite` and
  `cover` failed on every workstation with the frontend's dependencies
  installed and passed on a runner without them. The profile's rows and
  the list of packages that must be measured both skip the directory, as
  `modernize` already did.

## v0.50.2 - 2026-09-26

### Fixed

- `lateregate hook` applies the outbound HTTP client rule to the staged
  files the `otel-client` gate reads, and no others. It checked every
  staged Go file, so a commit that touched a file under a directory
  `otel_client.skip` names, such as a command-line tool with no inbound
  trace to continue, was refused although the full gate passes it. The
  hook now leaves out what the gate's walk leaves out: a directory the
  skip list names anywhere on the path, and `.claude`, `node_modules` and
  `testdata`. A commit refused for that reason alone now goes through.

## v0.50.1 - 2026-09-25

### Fixed

- `lateregate prepush` checks the changelog section of a release tag
  whatever the local ref is called. It read only lines whose local ref was
  under `refs/tags/`, but `git push origin HEAD:refs/tags/v1.2.3` hands the
  hook the local ref `HEAD`, and a commit sha pushed straight to the tag
  hands it the sha, so a release tag with no section went through
  unchecked. The remote ref now decides, as it does for the lint: every
  push to a release tag is checked at the pushed commit, and a tag deletion
  or a push to a branch still checks nothing. A tag push that went through
  before can now be refused until its commit has a section.

## v0.50.0 - 2026-09-25

### Added

- A `release.stamp` entry takes an optional `placeholder`: a literal that
  stands for no release yet, such as `newTag: unreleased` in a deploy
  overlay no tag has pinned. The cut replaces it inside the match as it
  replaces a version, so the first release pins itself; the pattern matches
  both states (`newTag: (v\d+\.\d+\.\d+|unreleased)`). An overlay that
  carried a never-cut `v0.0.0` only so the first cut had a version to move
  can name `unreleased` instead. Without the key nothing changes: a match
  with no `vX.Y.Z` still refuses the cut, and with it a match holding
  neither the version nor the placeholder does.

### Changed

- The README covers adopting the bar and the gate list; the reference moved
  to `docs/`: every gate in `docs/gates.md`, every command and the two hooks
  in `docs/commands.md`, `.lateregate.yaml` in `docs/configuration.md`, and
  cutting a release in `docs/releasing.md`. `CONTRIBUTING.md` is new. No
  behavior changed.

### Fixed

- Several `release.stamp` entries naming one file all land. The cut planned
  each entry from the file as committed and wrote the file once per entry,
  so the last entry's write dropped every earlier stamp without a word.
  Entries on one file now apply in the order listed, each to the file as the
  entries before it left it, and each pattern must still match exactly once.
  A single pattern spanning two markers to work around this can become one
  entry per marker.
- `lateregate prepush` lints a push to a branch whatever the local ref is
  called. It read only lines whose local ref was under `refs/heads/`, but
  `git push origin HEAD:main` from a worktree, and the push `lateregate
  release` makes, hand the hook the local ref `HEAD`, so the hook printed
  "no branch pushed; nothing to lint" and let the push through unlinted. The
  remote ref now decides: every commit pushed to `refs/heads/*` is linted,
  and a tag push or a branch deletion still lints nothing. A push that went
  through before can now be refused for findings in the packages it
  changes.

## v0.49.0 - 2026-09-23

### Added

- A `suite` gate runs the suite once with every property the five suite
  gates checked apart: `go vet ./...`, then one `go test -race
  -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...` with
  `CGO_ENABLED=1`, on the `hermetic` PATH and inside the `tempdir` sandbox,
  then the coverage floor over the profile and the check for survivors. The
  first line of a failure names the property that broke. Under `-race` the
  stripped PATH reaches the C compiler the toolchain names, and the `as` and
  `ld` it calls, through a shim directory that links those and nothing else,
  so the rest of `/usr/bin` stays off the PATH; the `PATH=` line names the
  shim and what it holds. Every flag is one the go test cache accepts, so an
  unchanged package replays its result.

### Changed

- `test`, `race`, `cover`, `tempdir` and `hermetic` are `folded` into `suite`
  in the plan: `list -json` reports them with `"status": "folded"` and
  `"into": "suite"`, and `lateregate` runs the suite once in their place. A
  pipeline that built a job per gate from the plan runs one suite job where
  it ran five. Each of the five still runs by name. `tempdir` stays a gate of
  its own where `tempdir.command` names a runner other than go test.
- A waiver on a folded gate narrows the suite instead of skipping a job: a
  waived `race` drops `-race`, a waived `hermetic` keeps the full PATH, a
  waived `tempdir` runs outside the sandbox, a waived `cover` keeps no floor,
  and a waived `test` waives `suite`. The suite's plan line names what is
  narrowed, and an expired waiver shows there as it does on any gate. Existing
  waivers keep their names and need no edit.

## v0.48.0 - 2026-09-23

### Changed

- `contract` wants the workflow that calls the shared pipeline to cancel a
  run once a newer push to the same ref supersedes it: a top-level
  `concurrency` whose group names `github.ref`, with `cancel-in-progress:
  true`. Without it a burst of pushes queues one whole gate set per commit,
  and every set but the last checks a tree nobody ships. `lateregate init`
  writes the block into a new caller; a repository that bumps to this release
  adds it to its existing caller in the same commit, as the finding says.
- `tempdir` runs the suite against one directory per repository and user, at
  the same path on every run, and runs of one repository take turns through a
  file lock beside it. The go command keys a cached test result on the
  `TMPDIR` a test read, so the fresh directory the gate made each time reran
  every package that creates a temporary directory on every push. A
  directory a killed run left behind is emptied before the next run reads it.

### Fixed

- `release` reads past a run canceled because a newer run of the same
  workflow replaced it, the way it reads past that newer run while it is in
  progress. Once a concurrency group canceled superseded runs, the canceled
  run was the newest completed one and the cut was refused as red while the
  run that replaced it was still going.

## v0.47.0 - 2026-09-20

### Added

- `identity` gains `core-audiences`, a rule for role `core`. The deployment a
  repository declares under `identity.overlays` names exactly two audiences,
  the core's own `identity.audience` and `api.latere.ai`. A script holding a
  platform key calls the origin directly and the core it reaches asks the
  authorizer, so a core that accepts its own name alone stops a token
  addressed to the origin.

  The value read is the one kustomize merges: the overlay's where the overlay
  patches it, the base's otherwise, keyed by the workload beside the
  container, so a reconciler standing next to a server is held on its own and
  two files patching one container are one verdict. The declared overlay is
  read even where `skip` names the same path, because `overlays` is the
  positive declaration, this directory is the hosted deployment. A core that
  declares no overlay reports `SKIP` with its reason, and an address where a
  name belongs stays the `audience` rule's finding rather than being reported
  twice.

  ```
  FAIL core-audiences    2 finding(s)
    deploy/prod/authorizer.yaml:19: the hosted overlay leaves the audience of
    container "arcad" at this repository's own name, so a token addressed to
    api.latere.ai stops here; name both, separated by a comma
  ```

  A repository behind on it waives `core-audiences` with a date and a reason,
  like any other rule of the gate.

- `json-bytes` reports a value the driver holds no encoding for at all, at any
  parameter. A Go struct, a Go map, or a list of a repository's own named type
  fails harder than a byte slice does: the driver picks the wire encoding from
  the Go type alone when the server has described nothing, holds no encoding
  for those, and fails with `cannot find encode plan` before the statement is
  sent.

  ```
  FAIL json-bytes   1 finding(s)
    internal/lux/event_postgres.go:46: this binds a Go struct into parameter
    $5; the pooled endpoint runs in exec mode, where the server describes no
    parameter and the driver picks the wire encoding from the Go type alone,
    and it holds no encoding for that type, ... encode the value and bind the
    encoding as a string
  ```

  This one is not about the column: the driver's plan lookup never reads the
  column, so nothing in the statement and nothing in the value says json, and
  the previous rule passed every instance of it. `platform` had three. Across
  the family at `origin/main` it finds ten more that nobody had seen, which
  takes the fleet from 10 findings to 19: a `map[string]string` bound to a
  labels column in `cella` three times and in `lux` four, and a list of a
  named identifier type in `arca` and in a private repository.

- `json-bytes` reads a helper whose declared result is `any`, by the concrete
  types it can return, and across the whole module rather than one package at
  a time. A private repository laundered six binds through one such helper an import
  away from every statement that bound it. A value whose concrete type is out
  of reach is still reported as nothing.

### Fixed

- `json-bytes` no longer reports `json.RawMessage`. pgx registers that type to
  the json column type and sends it correctly, so the finding was noise:
  seven of the nine it reported in one private repository and one of a second one's were
  this, and a check that is mostly noise gets waived and then guards nothing.
  A pointer to one, a string under any name, a value with a text or a
  database value of its own, a number and an untyped nil are accepted for the
  same reason, each of them measured against a server with parameters sent in
  the text format rather than reasoned about.

  The rule now names what a json parameter **takes** instead of listing what
  it refuses, so a Go type nobody measured is reported rather than let
  through. A byte slice under any other name, a bool, a list and a clock
  reading are refused, all four measured. A method named `Value` returning
  something narrower than the standard library's database value is not that
  method and is no longer read as one.

## v0.46.0 - 2026-09-20

### Added

- `json-bytes`, a check in the `postgres` gate: nothing binds a byte slice
  into a statement parameter the database reads as json.

  ```
  FAIL json-bytes   2 finding(s)
    internal/store/registry.go:811: this binds a byte slice into parameter $6
    and the statement casts that parameter to jsonb; ... bind a string, or a
    pointer to string where a nil has to stay SQL NULL, and keep the byte
    slice for a bytea column
  ```

  The pooled DSN carries `default_query_exec_mode=exec`, because a
  transaction pooler hands the next transaction a different backend and pgx
  cannot keep a prepared statement on the server. Exec mode also sends every
  parameter in the text format, where a Go `[]byte` goes as `bytea` and
  arrives as a hex literal. A hex literal is not json, so the statement
  fails with SQLSTATE 22P02 against the pool and succeeds against the direct
  endpoint, which is why no test on a direct connection can see it. `auth`
  v0.37.0 deployed and crash-looped at start-up on two such parameters; a
  hand audit of one repository found nine more.

  A `[]byte` bound to a `bytea` column is correct and is not reported. The
  check asks what the value is and what the statement does with it, not what
  its Go type is: a parameter is json when the statement casts it,
  `$6::jsonb` or `CAST($6 AS json)`, or when the value is a json encoding,
  which it is when it comes from `json.Marshal`, from any `MarshalJSON`, or
  from a `json.RawMessage`. A conversion, a local variable of any declared
  type, and a function of the same package all carry it, because all three
  were how the real instances reached the statement.

  A call is a statement when its name starts with `Exec` or `Query`, or is
  `Queue`, and its signature takes the SQL as a string followed by the
  variadic empty interface. No import path is matched, so a repository's own
  `Querier` interface is read as one. Argument positions are mapped to `$n`,
  so a finding names the parameter.

  The check is in every role's row, `direct` and an absent block included: a
  repository the failure is latent in is one that has not been pooled yet.
  It reads types, so it runs a type-check, and only where the syntactic scan
  found a statement-shaped call; a tree with none passes with that as its
  reason, and a tree that has them and does not type-check is an error
  naming what failed.

  To adopt: a repository with findings binds a `string`, or a `*string`
  where SQL NULL and the empty document have to stay apart. A sweep of the
  organization's Go repositories at this release reports 53 binds across
  twelve of them.

### Changed

- A previous tag whose Release run went red no longer refuses the next cut.
  It is printed instead, in the same shape a refusal uses, ending with the
  `BUDGET`, `INFRA` or `CODE` line that names who acts:

  ```
  v0.37.0 rolled out red, and this cut is not refused on it
    release #1201 failure, job "release / deploy"
    https://github.com/latere-ai/auth/actions/runs/35458476040
    CODE: fix and push, then cut again
  ```

  A Release run is a rollout, not a test of the repository: it builds an
  image, deploys it, smokes the deployment and publishes the notes. A
  failure in it is a fact about the tag before this one, and the tag being
  cut is frequently the repair. Vetoing on it blocked `auth` v0.37.1, which
  carried the fix for the crash-loop that reddened v0.37.0, and it has kept
  `eval` unreleasable for two versions because its deployment is held at
  zero replicas and its smoke fails at every tag.

  What decides a cut is unchanged otherwise. Every workflow with a completed
  run on the default branch over `prev..HEAD` must be green, a window in
  which nothing has completed still refuses, and a previous tag whose
  Release run concluded `success` while leaving no GitHub Release still
  refuses. That last one is the case nobody can see, so it keeps its veto; a
  red run that published nothing is that run's own visible consequence.

  `-force-red` is unaffected and is now needed for fewer things, which is
  the point: an escape hatch the ordinary path runs through stops meaning
  anything.

## v0.45.0 - 2026-09-19

### Changed

- `postgres.role: direct` needs a dated waiver of the `postgres` gate. It
  passed by declaration in v0.43.0, which was right while every Postgres
  repository was on the direct endpoint and the pooled path was being built.
  The family's services have since cut over, so a repository still serving
  from the direct endpoint is an exception, and an exception costs a reason
  and a date:

  ```yaml
  postgres:
    role: direct

  waive:
    postgres:
      reason: a library, and the consumer that calls it owns the connection
      until: 2026-12-19
  ```

  It is the waiver every other gate takes, keyed on this gate's name, so the
  plan lists the repository as `WAIV postgres` and the same date retires it.
  Without one the role fails, naming both ways out: cut the serving path over
  to the pooled name with a fallback to the direct one and declare `pooled`,
  or record why the repository stays. Past the date the gate runs and the
  role fails naming the date and what the waiver claimed, so an exception is
  renewed by somebody deciding again.

  The gate reads the waiver itself rather than leaving it to the plan,
  because `lateregate postgres` runs one gate by name and builds no plan, and
  because a refusal a day past the date should be the Postgres rule's own
  sentence rather than a line about a calendar.

  To adopt: a repository declaring `direct` writes the waiver in the same
  change as the bump. `none`, `pooled` and an absent block are unaffected,
  and a waiver of this gate changes no verdict under them.

## v0.44.0 - 2026-09-19

### Added

- `postgres.direct_env` and `postgres.pool_env`: a `pooled` repository
  writes out the two environment names it reads.

  ```yaml
  postgres:
    role: pooled
    direct_env: LUX_DB_URL
    pool_env: LUX_DB_POOL_URL
  ```

  The gate's question is whether the serving path reads a pooled endpoint
  and falls back to a direct one, and whether the migrator receives the
  direct one. How a service spells those two variables is the service's
  own surface; the key inside the Secret is the family's contract, and a
  Deployment already maps the one to the other. `postgres.prefix` derived
  the names from that spelling, which made the gate refuse a service
  satisfying the rule under a name of its own, and charged an open core a
  breaking configuration rename for nothing.

  The two keys are declared together, never beside `prefix`, never under a
  role but `pooled`, each shaped like an environment variable name, and
  they name two different variables. Each refusal says which rule it is.

  Nothing changes for a repository that writes neither key: the names are
  the bare `DATABASE_POOL_URL` and `DATABASE_URL`, or the pair `prefix`
  derives, exactly as in v0.43.0.

## v0.43.0 - 2026-09-19

### Added

- A `postgres` gate. One managed Postgres serves the family with about 22
  usable connection slots, and every service that connects directly claims
  its share of them; the family's fix is a transaction-mode pool per
  service, with the serving path reading `DATABASE_POOL_URL` and falling
  back to `DATABASE_URL`, and the migrator keeping `DATABASE_URL`. Each
  repository now declares its relationship to that database under
  `postgres.role` in `.lateregate.yaml`: `none`, `direct` or `pooled`.

  `none` fails on any non-test Go file importing a Postgres client (the pgx
  tree, `lib/pq`, golang-migrate's `postgres` and `pgx` drivers, the shared
  migration runner), naming the file and the roles to declare instead.
  `direct` passes by declaration in this release, and its row says so; a
  later release makes it require a dated reason once the family's cutovers
  land. `pooled` requires a client import, a read of `DATABASE_POOL_URL` and
  a read of `DATABASE_URL`, where a read is a call to something named for the
  environment (`os.Getenv`, `os.LookupEnv`, a local `getenv`, an `envOr`)
  with the name as a literal or a package constant, or a struct tag under the
  `env` key; a mention in a comment, a log line or an error string is not
  one. `postgres.prefix: EVAL` moves the names to `EVAL_DATABASE_POOL_URL`
  and `EVAL_DATABASE_URL` for a repository whose Secret carries a prefix.

  A repository with no block passes when nothing imports a client and fails
  when something does, naming the three roles: that is the undeclared
  consumer the gate exists to catch. Nothing passes vacuously: a `pooled`
  tree with no Go file fails with `SKIP` on every check. The gate does not
  check which client each name reaches; that the pooled DSN opens the pool
  and the direct one opens the migrator stays a review item.

  The gate runs in the default bar and appears in `lateregate list -json`,
  so the shared workflow's matrix picks it up without a change. `contract`
  prints the declared role in its in-shape line and does not treat an absent
  block as drift. To adopt: every repository that connects to the shared
  database declares `direct` in this release; nothing turns red.

## v0.42.0 - 2026-09-17

### Fixed

- The `identity` gate's `roles` rule reads the frontend. It read Go files,
  documents and deploy manifests, so a page that branched on the retired
  `is_superadmin` flag passed the gate while the Go beside it was clean; the
  identity epic's verification found three such frontends behind a green
  `roles` line. The rule now also reads `.ts`, `.tsx`, `.jsx`, `.vue`, `.svelte`, `.js`, `.mjs` and `.cjs`
  under the repository, and matches a third spelling, the camel case
  `isSuperadmin`, beside `is_superadmin` and `IsSuperadmin`.

  Four things in a frontend tree are not decisions and are not read: a test
  (a `.test` or `.spec` segment before the extension, or a `__tests__`
  directory), because the assertion a repository writes after retiring the
  flag is that the flag confers nothing, and reading it as a decision would
  make the regression test the finding; a bundler's output (`dist/`,
  `build/`, a `.min` or `.bundle` file, or a file carrying a generated
  header); `node_modules/` and `testdata/`; and an archive. Inside a file
  that is read, a comment is prose: the sentence saying why a file stopped
  reading the flag is not a use of it, while a name in a string still is.

  A file the repository ignores is not read either, asked of `git
  check-ignore` rather than guessed. A frontend tree holds build residue
  beside its sources, and a file that is on a laptop and not in a checkout
  would make the gate red locally and green on the runner. git deciding
  nothing leaves every file read.

## v0.41.0 - 2026-09-17

### Fixed

- `lateregate prepush` honours a dated `waive: lint`. The full gate reported
  `WAIV lint` on a tree with such a waiver while the pre-push hook ran the
  linter anyway over every package a push changed, so a repository that had
  written the waiver could not push any change touching a package the
  waiver was written for, whatever the change was: a fix beside waived debt
  was refused for the debt. The hook now reads the same waiver on the same
  inclusive date, prints `lint is waived until <date>; nothing to lint`,
  and lints again the day after.

## v0.40.0 - 2026-09-17

### Added

- The `identity` gate holds an `envelope` rule, which every role but `none`
  runs. The authorizer's question and its decision are declared once, in
  `latere.ai/x/pkg`; a struct in a non-test Go file whose JSON tags name
  `action` beside `subject` or `resource`, or `allow` beside `ttl` and
  `reason`, is that wire shape written out a second time, and the second copy
  stops matching the first on the day the first changes. Tag names are
  matched whole, so an `actions` list, an `allowed_hosts` set, a `ttl_seconds`
  figure and a `reasons` array are not the envelope. The rule skips inside the
  module the envelope is declared in. This is the row of the family's id-11
  that the `authorizer` rule never held: that one catches a repository that
  asks in a shape of its own, not one that answers in one.
- `identity.envelope_exempt` names the files whose types carry the envelope's
  field names for a reason of their own: a core's `limits` type, and the page
  a list action answers with. The two reasons are declared per repository
  rather than written into the gate, and a declared path the tree does not
  hold stops the run, the way an overlay's does.
- `identity.image` names the segment a repository's workload image was built
  under, for an image that carries neither a command name under `cmd/` nor the
  module's own: a module called `wallfacer` that deploys `wallfacerd` writes
  `image: wallfacerd`. It is a declaration and not a guess, so the deployment
  rules keep checking a repository whose image and module were named apart.
  The value is the one segment the rules compare: a registry, a path, a tag or
  a digest in it is refused, and so is the key outside `issuer`, `core`,
  `service` and `platform`, the roles whose deployments the `audience` and
  `bearers` rules read.

### Fixed

- The `identity` gate reads a Go file's string literals without descending
  into its import declarations. An import path is a string in the grammar and
  a dependency in the file, and reading it as a literal made the `authorizer`
  rule report a file that imports a test double whose path holds `authorize`,
  and the `claims` rule report a package whose path holds a membership claim.
  Repositories had been putting those files in `identity.skip`, which stopped
  every other rule from reading them too; from v0.40.0 those entries can go.
- The `identity` gate's `audience` and `bearers` rules see a repository whose
  only command is at the module root. They recognize a container by the name
  its image was built under, and that name was read from the directories
  under `cmd/` alone, so a repository that moved its `main` to the root
  reported `SKIP audience no container in the deployment runs a command this
  repository builds` and its `AUTH_AUDIENCE` went unchecked. A module root
  that is `package main` now names a command, called after the module's last
  segment, which is the name `go build` gives the binary.

## v0.39.0 - 2026-09-17

### Added

- `lateregate release` reads CI through the GitHub API before it runs the bar
  and refuses to cut while the repository is red. It checks the latest
  completed run of every workflow on the default branch for `HEAD` and its
  ancestors back to the previous release tag, the previous tag's own release
  run, and whether a GitHub Release actually exists for that tag: a run that
  finished green and published nothing is the failure this catches, and it is
  the one that let three tags deploy without notes. A window where nothing has
  completed yet is unknown rather than green, and is refused too.
- A run refuses the cut on every conclusion that is not an answer:
  `failure`, `cancelled`, `timed_out`, `startup_failure` and
  `action_required`. A run that never started, one that died on the clock and
  one waiting for approval are all runs nobody has a result from. `neutral`,
  `skipped` and `stale` are answers and pass.
- Every refusal names the run, the failing job and its URL, and ends with one
  line saying who acts, read from the failing job's log:
  `BUDGET: the maintainer must act (…)` for Actions minutes, billing, a usage
  limit, runner capacity or an org policy refusal, with the phrase that
  matched so it can be forwarded; `INFRA: re-run the job, then cut again` for
  a registry refusal, a reset connection, a certificate or a name that did not
  resolve; and `CODE: fix and push, then cut again` for anything else,
  including a log that could not be read. Two conclusions decide themselves:
  a `timed_out` run is `INFRA` unless its log names a test, because re-running
  a suite that ran out of time only spends the clock twice, and a
  `startup_failure` has no job log of its own, so its message classifies it.
- `release.require_green` in `.lateregate.yaml` is `true` when the file says
  nothing. Setting it to `false` turns the guard off, and the README says what
  that costs. `lateregate release -force-red vX.Y.Z` cuts over the findings
  instead: it still reads CI, and prints everything it is overriding.

## v0.38.0 - 2026-09-17

### Added

- `identity.overlays` names the paths that hold one company's own deployment
  overlay, which an open core keeps in its tree because the tag deploys from
  it. The `no-latere-value` rule reads a declared overlay instead of scanning
  it: the overlay's own files are not a finding, and the `latere.ai` and
  `latere.svc` addresses those files set are the ones a document may name. A
  core that had to waive the rule for a README sentence saying where the
  hosted installation runs can declare the overlay and keep the rule. Nothing
  else moves: the exemption is that address and nothing beside it, so naming
  the overlay's path in a sentence admits no other address, the same address
  in code or in a manifest outside the overlay is still a finding, an address
  the overlay only mentions in a comment is not exempt, and a declared path
  the tree does not hold stops the run rather than exempting nothing quietly.
  The key is core only.

## v0.37.0 - 2026-09-16

### Changed

- A deployment whose image name is not a directory under `cmd/` now reports
  `SKIP audience` with the reason, where the single-container shortcut used
  to read that container as the workload. A rule that reads nothing says so
  rather than passing: name the image after the command it runs, or put the
  path in `identity.skip` as a decision.

### Fixed

- The `identity` gate's `audience` rule reads which containers of a manifest
  are this repository's workload. A container runs the repository when the
  name its image was built under, the last path segment of the reference,
  without the registry, the tag or the digest, is exactly a command under
  `cmd/`. It was a substring of the image, so `ghcr.io/latere-ai/origo-stubs`
  counted as the command `origo`; and every document holding a single
  container was read as this repository's whatever that container was. A
  manifest of test doubles beside the workload is no longer a finding, and
  the `identity.skip` entries repositories carry for one can go. An overlay
  that patches a container by name and carries no image is still read as the
  container it merges into, so an audience set in an overlay still counts for
  the base and an address set there is still a finding.
- The deployment rules read `initContainers` beside `containers`. An init
  container that runs with the workload's configuration, which it says by
  declaring a variable of the repository's own prefix (`CELLA_` for a core,
  `AUTH_` for a service or a platform), names the audience it verifies like
  the workload does; one that declares none, a step that copies a file, is
  passed over. A check container that verifies tokens before the node starts
  was invisible to the gate and is now held. The `bearers` rule reads init
  containers too, so a credential shared between two of their variables is a
  finding where it was not seen.

## v0.36.0 - 2026-09-15

### Added

- `release.stamp` in `.lateregate.yaml`: a list of `{file, pattern}` whose
  `vX.Y.Z` `lateregate release` rewrites to the version it is cutting, staged
  into the same commit as the changelog. A file that names the release, such as a
  `SECURITY.md` line or a deploy overlay's `newTag`, no longer lags the tag,
  so main does not go red after a cut waiting for a hand-edit. Each pattern
  must match its file exactly once and hold one `vX.Y.Z`; a pattern that does
  not refuses the cut with a clean tree, like every other release refusal.

## v0.35.0 - 2026-09-13

### Changed

- `identity family` reads only directories that are repositories; a scratch
  folder beside the checkouts on a workstation is not part of the family.
- `identity.reached_by` gains `self-hosted`: an open core's default audience,
  verified only where the core is self-hosted while the hosted plane is
  another repository, so the family check expects no registry row for it.

## v0.34.0 - 2026-09-13

### Added

- `identity.bff`: the paths of a browser frontend a repository serves beside
  its API. The request-path rule does not read them, because a frontend
  forwards the person's own token to the issuer's API; the API's files are
  held as before.

## v0.33.0 - 2026-09-13

### Added

- `identity.reached_by`: `clients` (the default) or `services`. A service
  no client acts at for a person declares `services`, and the family check
  then expects no client registry row for its audience instead of failing
  on the missing one.

### Changed

- `identity`, the delegation rule: `act`, `agent_id` and `actor_id` as a
  struct tag in a type that carries no registered claim are a column of
  that type wherever the file lives, so a handler that verifies tokens may
  render an audit row with an `actor_id` field. A bare occurrence in a
  file about claims, and a tag beside `sub`, `aud` or `exp`, are findings
  as before.

## v0.32.2 - 2026-09-13

### Changed

- The `identity` gate's document rules read no record: a changelog, a
  release note, a file under `.archive`, or a spec whose status the tree's
  `spec.settled` list calls finished. A record may name what it retired.
- `verifier` no longer requires a bff to import the shared verifier: a bff
  forwards the person's token and verifies nothing of its own. The other two
  halves of the rule still hold it.
- `no-latere-value` treats the `latere.ai/x/` module namespace and a contact
  address such as `security@latere.ai` as the project's coordinates wherever
  they appear.
- `audience` accepts `AUTH_AUDIENCES` beside `AUTH_AUDIENCE` and judges a
  container across its base and overlay files, so a value set in the
  production overlay counts for the base.

## v0.32.1 - 2026-09-13

### Added

- The `identity` block takes a `waive` map keyed by rule, so a repository
  behind on one rule keeps the other rules running. A waived rule still
  reports its findings under `WAIV`, says when it already holds, and fails
  on its own terms after its date. A waiver naming no rule, or a rule the
  role does not run, fails the load.

### Changed

- `no-latere-value` reads code, manifests and user documents; a core's
  `specs/` tree is the contributor's record and is not read.

## v0.32.0 - 2026-09-13

### Added

- `enum-go` and `enum-typescript` enforce declared enum domains: fields use
  their named type, implementation uses named members, and switches cover
  every distinct value even when a default exists. Configure domain types
  and fields under `enums`; reviewed parser exceptions require a reason.
- TypeScript checking uses the consumer's installed compiler and supports
  Vue script blocks. `enum-typescript-prepare` installs frontend dependencies
  from tracked npm or Bun lockfiles; checking does not install packages.
- `identity` holds a repository to the family's identity shape. The new
  `identity` block in `.lateregate.yaml` declares which layer the repository
  is, and the role selects the rules the gate runs over the tree: what a core
  may read, one token verifier, one authorizer contract, no call to the issuer
  while serving a request, no delegation vocabulary, access by role rather
  than by a flag, an explicit audience in every deployment, a credential per
  endpoint with the internal route inside the cluster, no value of one company
  in an open core, one audience per product in a client, and a document that
  describes the system that exists. A rule with nothing to read reports why.
  A repository with no block fails the gate, and `contract` reports it as
  drift.
- `lateregate identity family -repos DIR [-expect FILE]` checks the blocks of
  every repository against each other and the issuer's client registry, and
  prints the layer table they derive.

### Fixed

- `contract` cleans up temporary files from its Makefile probe, including
  Apple's `xcrun_db` cache. Running the test suite through `tempdir` on macOS
  no longer fails because the probe leaves that cache behind.

## v0.31.3 - 2026-09-10

### Fixed

- `depcheck` no longer fails at random on an unchanged tree. An import path
  admitted by two allowances at once, such as `golang.org/x/oauth2` under
  both its own entry and a broader `golang.org/x`, marked only one of them
  as reached, and which one depended on Go's map iteration order. The entry
  that lost was then reported as a stale allowance the build does not reach.
  A repository with nested entries saw this on roughly a third of runs, with
  an identical reached count on the passing and the failing run. Every
  matching allowance is now marked reached, so the verdict is the same every
  time. No allowlist that passes today starts failing; an entry that is
  redundant rather than stale, because a narrower entry admits everything
  under it, is no longer reported at all.
- `cover` and `tempdir` name the same reason on every run for a package or a
  temporary entry that two waivers match. The most specific waiver's reason
  wins. Neither gate's pass or fail was affected.

## v0.31.2 - 2026-09-06

### Changed

- `lateregate release` runs the full gate before it tags. v0.31.1 was cut
  with the modernize and lint gates red on the release commit; a release is
  the one push that must never go out red.

## v0.31.1 - 2026-09-06

### Fixed

- The license check on staged files (the pre-commit hook) honours
  `license.skip` the way the full walk does, so a file under a skipped
  directory such as a template skeleton no longer blocks a commit that the
  gate itself passes.

## v0.31.0 - 2026-09-06

### Added

- A `registers` gate: no developer sentence in a string literal handed to a
  user-surface function. A repository opts in by naming its surfaces in
  `.lateregate.yaml` (`registers.user_surfaces: [internal/api.WriteError]`);
  the gate then reads every string literal passed to them, including
  through `fmt.Sprintf` or concatenation, and fails on a Go import path, a
  package-qualified identifier, a Kubernetes kind and object name, or a
  file path. A named surface that nothing calls fails rather than passing
  over nothing. The rule it enforces is `docs/writing/registers.md` in
  `latere.ai/x/pkg`. Without the key the gate is skipped, so no existing
  repository changes verdict.

## v0.30.0 - 2026-09-06

### Added

- `license` checks the root `LICENSE` text against `license.spdx` through a
  fingerprint per identifier, so a declaration that names one license over a
  root file carrying another fails with both named. An identifier the table
  does not know fails closed. This is the mismatch pkg carried for four
  days: `Apache-2.0` on 264 files above an MIT `LICENSE`.

### Changed

- The license gate accepts `license.spdx: LicenseRef-Proprietary`, the SPDX
  form for terms not on the license list. The root `LICENSE` must then reserve
  all rights and grant no license, and every file carries the identifier the
  same way an open source repository does, so a proprietary repository is
  checked instead of waived.

## v0.29.2 - 2026-09-06

### Fixed

- The pre-push hook no longer fails a push that changes a Go file under a
  nested module (a directory with its own `go.mod`, such as a spike tool or
  an example). Such a file is not a package of the main module, so the hook
  skips it the way it skips `testdata`, and the full gate remains the bar.

## v0.29.1 - 2026-09-06

### Changed

- **Nothing changes for a repository that runs the bar.** This repository now publishes its own GitHub releases from this file, through `notes-release.yml` in latere-ai/ci. v0.29.0 was tagged before that pipeline existed and has no release object; its section below is the note.

## v0.29.0 - 2026-09-06

A tag is a release, and a release has notes. The changelog rule pkg kept in
three shell scripts is now in the binary, for every repository.

### Added

- `lateregate release-notes TAG [REF]` prints the `CHANGELOG.md` section
  for a tag, read from the working tree or at a git ref, and fails naming
  the fix when the file, the heading, or the notes are missing. The
  release workflows in latere-ai/ci call it and fail closed.
- `lateregate release vX.Y.Z` moves what sits under `## Unreleased` into a
  dated section for the version, commits, creates an annotated tag, and
  pushes `HEAD` and the tag in one push. It refuses a dirty tree, an
  existing tag, and an empty `Unreleased`.

### Changed

- `lateregate prepush` refuses a release tag (`vMAJOR.MINOR.PATCH`, with an
  optional prerelease or build suffix) whose commit has no changelog
  section, before it lints. A moving major tag such as `v1`, a tag
  deletion, and a branch are unaffected.
- `lateregate contract` wants `CHANGELOG.md` tracked at the root with a
  `## Unreleased` heading, and reports a `Makefile` target named `release`
  or `release-notes` that does not delegate. `lateregate init` writes the
  seed changelog when there is none.

## v0.28.0 - 2026-09-06

### Added

- **`lateregate prepush` is the pre-push hook: golangci-lint over the packages the push changes.** It reads the refs git hands the hook, diffs each branch against the remote's commit (or the merge base with `origin/main` for a new branch), and lints exactly the packages the changed Go files sit in, against the config rendered as the full gate renders it. Tags run nothing. It runs with `--allow-parallel-runners`, so a lint in another checkout neither blocks a push nor aborts it. `lateregate init` writes `.githooks/pre-push`, never overwrites one that exists, and `contract` reports a pre-push that does not run `lateregate prepush` until the delegation line is added by hand. A repository that adds its own check below the delegation reads the saved `$refs`, not stdin.

### Changed

- **The pre-commit hook holds every cheap file-scan gate over the staged files.** In order: `gofmt`; `goimports` grouping with the module path as the local prefix; the license notice, when `license.spdx` is declared; the outbound HTTP client rule; then `go fix` for the packages that hold them. Each scan reads only the staged files, so the hook stays a few seconds. `vuln`, `tempdir`, `race`, `cover` and `hermetic` are in neither hook. On upgrading, run `go tool lateregate init` to add the pre-push hook; `contract` reports it until then.

## v0.27.4 - 2026-09-02

### Fixed

- **`license` stays out of an untracked directory and a nested checkout.** git lists an untracked directory once, not its files, and an agent worktree under `.claude` is a checkout with its own history; `license -w` had written notices into both.

## v0.27.3 - 2026-09-02

### Fixed

- **`license` skips a file git does not track.** Another change's work in progress in the tree no longer fails the gate, and `license -w` no longer gives it a notice it did not ask for. `fmt-check` already held the same rule.

## v0.27.2 - 2026-09-02

### Fixed

- **A README listed in `spec.exclude` is no longer taken as the default index.** A repository that keeps `specs/README.md` as prose and excludes it had it read as an index table nobody wrote, and the tree failed for it.

## v0.27.1 - 2026-09-02

### Fixed

- **The pre-commit hook holds a staged file under `testdata` to `gofmt` only.** Its package is outside `./...`, so the full `modernize` gate never reads it, and the hook was applying a rule no gate holds.

## v0.27.0 - 2026-09-02

### Added

- **`lateregate license -w` writes the declared notice on every file that has none.** The identifier and holder come from `license.spdx` and `license.holder`, the year is the year it is written in, and the notice goes below a shebang when there is one. A notice that disagrees with the declaration is reported, not rewritten, because replacing a holder is a legal edit.

## v0.26.2 - 2026-09-02

### Changed

- Nothing changes for a repository that runs the bar; this release updates one of this repository's own tests to the config the race gate now takes.

## v0.26.1 - 2026-09-02

### Added

- **`race.timeout` sets the race detector's budget.** A suite that costs the detector far more than the toolchain's 10 minute default sets the figure, with its reason, in `.lateregate.yaml`, instead of failing for the wrong reason.

## v0.26.0 - 2026-09-01

### Added

- **`lateregate` with no arguments runs the whole bar.** It runs every gate that applies, does not stop at the first failure, and ends with a summary line. The recipes for `test`, `race`, `cover`, `lint` and `vuln` are in the binary, so a repository no longer keeps them in its Makefile. `lateregate list` prints the plan and `list -json` is what the pipeline builds its job matrix from. A Makefile target named for a gate must delegate to `lateregate` or not exist.
- **`lateregate init` writes the wiring and `lateregate contract` reports every difference.** `init` writes the caller workflow for `latere-ai/ci/.github/workflows/lateregate.yml@v1`, `.githooks/pre-commit`, the gitignore lines for `.golangci.yml` and `coverage.out`, and `git config core.hooksPath .githooks`. It never touches `Makefile` or `.lateregate.yaml`. `contract` names drift in the workflow, the hook, the ignore lines, the `tool` line in `go.mod` and any key that restates a default.
- **`lateregate hook` is the pre-commit.** It runs `gofmt` over the staged Go files and the modernizers over the packages that hold them, reading `modernize.disable` from the same config the full gate reads.

### Changed

- **A repository that was behind on a gate writes `waive`, keyed by gate name, instead of `contract.exempt`.** `waive.<gate>` takes the same `reason` and `until`. A waiver keyed on a name that is not a gate fails the run, and the old `contract.exempt` key is refused with a message that says where it went. On upgrading, move each entry to the top-level `waive` map under its gate name. After `until`, the gate runs and fails on its own terms and the summary says the waiver ran out.

### Removed

- **`contract.exempt` is gone.** `waive` replaces it, as described above.

## v0.25.2 - 2026-09-01

### Fixed

- **`spec-lint` ignores an edge into another repository's archive.** A path such as `../../other/specs/.archive/name.md` matched the archive by its last directory and was reported as a missing file. The path must now be the archive relative to the spec directory or the repository root; a `..` segment anywhere means the edge left the tree.

## v0.25.1 - 2026-09-01

### Fixed

- **`spec-lint` takes the index's own word on whether it covers the archive.** An index that holds at least one archive row is an index of the whole tree and must hold them all; an index that holds none is asked for nothing. Requiring a row for every archived spec had encoded one tree's convention as a rule. Rows that exist are still checked for resolution.

## v0.25.0 - 2026-09-01

### Added

- **`spec-lint` lints the archive and fails a finished spec that never moved into it.** `spec.archive.dir` names the folder and `spec.archive.statuses` the terminal statuses; `statuses` must be a subset of `status`, and there is no default, because the same word means done in one tree and shipped with work outstanding in another. A terminal spec at the root fails, and so does an archived spec at a working status. Archived specs are now parsed, held to `require` and `status`, resolved for `depends_on`, numbered and required in the index, but not held to the section, marker or register rules, which a record written before the rule cannot satisfy. An archive folder that does not exist yet counts as empty, so adopting the rule waits on nothing.

## v0.24.0 - 2026-08-31

### Changed

- **`contract` requires `spec-lint` only in a repository that tracks specs.** It asks git whether `specs/` files are tracked rather than reading the config, so a repository that keeps no specs needs no exemption, and one that tracks specs cannot avoid the gate by deleting its `spec` block.

## v0.23.0 - 2026-08-31

### Added

- **`contract`: the gate set is declared, and a missing gate fails.** The shared pipeline runs whichever optional Makefile targets it finds, so a repository with no `cover` target was not gated on coverage and the run still reported success; nine of eighteen migrated repositories had no coverage gate at all. The required set is compiled into the binary. A repository that is behind records it under `contract.exempt` with a `reason` and an `until` date (`YYYY-MM-DD`); an entry without either fails when the file is read, and the date covers its whole day. Retiring the exemptions is therefore checked rather than remembered. A repository with a missing required target adds it or writes the exemption. The target probe reads make's rule database, so a file named like a target, such as a `test/` directory or a `LICENSE` file, does not stand in for it.

## v0.22.0 - 2026-08-31

### Changed

- **`bodyclose` is off in test files.** It does not follow a response into a test helper, so it reported a close that the helper makes.

## v0.21.0 - 2026-08-30

### Fixed

- **`modernize` leaves out a vendored JavaScript tree.** A JavaScript package can ship Go source, and a repository with a frontend has a tree of them under `node_modules` once dependencies are installed. The gate named them all, so it failed on every workstation with the frontend set up and passed on a runner without it. It now lists the module's packages and drops any under `node_modules`.

## v0.20.0 - 2026-08-30

### Added

- **`cover` fails a package that no profile measured, and merges test tiers.** A package with no test file produces no records, so the floor cleared it by never measuring it. The gate now lists the module's packages and fails on the ones the profiles never mention, as `MISS internal/notify - no coverage data`. A package that declares no function with a body is left out, since no test can produce data for it. Repeat `-profile` for a repository whose coverage is split across tiers, such as unit and integration tests; the tiers merge as a union, not a sum.
- **`spec-lint` can gate stable spec numbers and the ordering `depends_on` states.** `spec.numbered: true` requires every file to be `NNN-name.md` and no two to share a number. `spec.started` and `spec.settled` list the statuses where work has begun and where it is done, and a spec at a started status whose dependency has not settled fails. `started` without `settled` is refused when the file is read. Both are off by default.

### Changed

- **The shared lint set is raised to the bar the strictest repository already held.** Nothing is truncated (`max-issues-per-linter` and `max-same-issues` are zero), every vet analyzer runs except `fieldalignment` and `shadow`, unchecked type assertions are findings, and `io/ioutil`, `math/rand`, `log` and `github.com/pkg/errors` are denied with the replacement named, while `log/slog` stays allowed. Expect new findings in a repository that had not yet adopted these on its own.

## v0.19.0 - 2026-08-30

### Fixed

- **`fmt-check` takes its file list from git.** `gofmt -l .` descended into a nested checkout, such as an agent worktree parked under `.claude`, and reported an unformatted file the repository neither owns nor can fix. The gate now checks the tracked Go files, and a checkout with no tracked Go file fails rather than reporting a formatted tree.

## v0.18.0 - 2026-08-30

### Fixed

- **`spec-lint` leaves alone an index row that links into a subdirectory.** An archive is outside the linted set, so the index cannot be asked to agree with it, the same way a `depends_on` edge into another repository is not.

## v0.17.0 - 2026-08-30

### Added

- **`otel-client`: no outbound HTTP client is built without a tracing transport.** It fails on an `http.Client` with no `Transport` field and on `http.DefaultClient`. The gate parses the file, so a `Transport` several lines down still counts, comments and string literals cannot trip it, and a renamed `net/http` import is still checked. `otel_client.skip` names directories it does not read; `.git` and `.claude` are always skipped. This replaces the awk programs that repositories kept in their Makefiles, which had drifted into four variants.

## v0.16.0 - 2026-08-30

### Added

- **`license` reads the notice in each file type's own comment marker.** `license.extensions` takes `.ts`, `.tsx`, `.js`, `.mjs`, `.rs`, `.java`, `.swift`, `.proto` and the other types the gate knows, and `license.names` checks files by their whole name, such as `Makefile` and `Dockerfile`, with `#` as the marker. A script keeps its shebang on line 1 and the notice moves below it. A file type the gate has no marker for is rejected when the config is read, so a repository cannot believe it is checking files it is not.

### Changed

- **The shared lint set adds the linters that catch bugs.** `bodyclose`, `errorlint`, `nilerr`, `sqlclosecheck`, `rowserrcheck`, `noctx`, `contextcheck`, `errchkjson`, `durationcheck`, `copyloopvar`, `unconvert` and `wastedassign` are now on for every repository, and tests are excluded from `errcheck`, `noctx` and `errchkjson`. Across six repositories that was about 105 findings. A repository that enabled some of them through `golangci.extra` can delete those lines. `gosec`, `misspell` and `revive`'s exported rule stay out, since they encode facts about one repository.

## v0.15.0 - 2026-08-30

### Added

- **`golangci.extra` merges a repository's own lint policy over the shared set.** Maps merge key by key, so a repository adds one setting without restating the block it lives in; `linters.enable` appends, so adding a linter means "as well as"; any other list replaces. This replaces exempting the repository from the shared config. The rendered file no longer carries inline comments.

## v0.14.0 - 2026-08-30

### Added

- **`license`: every source file carries the SPDX notice the repository declared.** `license.spdx` names the identifier and `license.holder` the copyright holder, matched literally. There is no default, because a guessed license would be printed into every file; with `license.spdx` unset the gate fails rather than passes. By default it reads Go files; `license.extensions` lists the file types a repository ships and `license.skip` names directories the scan does not enter, besides `.git` and `node_modules`. The notice is the first two lines of the file, then a blank line.

## v0.13.1 - 2026-08-30

### Changed

- **`depguard` denies the subpackages of testify by name.** The rule named only the module path and left the subpackages to prefix matching. A repository that imports one of them now gets the finding that names it.

## v0.13.0 - 2026-08-30

### Added

- **`golangci.sloglint` scopes `sloglint` to a repository's request paths.** `sloglint` requires the `*Context` variant of every `slog` call on a request path, so a trace and span id can be attached. `context` sets the rule, `request_paths` is a regexp for the packages that serve requests, and `exempt` lists paths inside them that do not. An unconfigured repository gets no `sloglint` and no empty exclusions block.

## v0.12.0 - 2026-08-30

### Added

- **`golangci.own` declares that a repository keeps its own lint config.** The value is the reason. The gate prints it, leaves the committed file alone, and fails if the file is not there, since a declared exception pointing at nothing is a repository that lost its config.

## v0.11.0 - 2026-08-30

### Changed

- **The shared lint set is the standard linters, not just `modernize`.** The standard linters, `errcheck`, `staticcheck` and `unused` among them, now run, and `depguard` denies any test framework but the standard library's `testing`. A repository moving to this release should expect new findings, mostly discarded errors; four real bugs surfaced across the first repositories that adopted it. Measure with `--max-issues-per-linter=0 --max-same-issues=0`, because golangci-lint caps its output at fifty issues per linter by default.

## v0.10.0 - 2026-08-30

### Changed

- **`.golangci.yml` is generated on every run and never committed.** A hand edit does not survive the next run, so there is nothing to detect. A repository deletes its committed `.golangci.yml` and gitignores it; the gate refuses to write over a tracked file. After a fresh clone, run the gate once before running `golangci-lint` directly, or it falls back to its own defaults.

## v0.9.0 - 2026-08-30

### Added

- **`lateregate golangci` renders the shared golangci-lint config.** golangci-lint v2 has no config inheritance, so the shared file is produced instead of copied into every repository. In this release the rendered file is committed, and the gate fails when the committed copy stops matching it.

### Changed

- **`modernize.disable` feeds both the `modernize` gate and the linter of the same name.** They run the same analyzers, and a repository that disabled one for one and not the other now has both.

## v0.8.0 - 2026-08-30

### Added

- **`tempdir`: the suite leaves nothing behind under `TMPDIR`.** `lateregate tempdir -- <command>` runs the suite against an empty `TMPDIR` and fails on every entry that survives it. A test that makes a directory and does not remove it leaks it for the life of the machine and nothing else fails. An empty sandbox that nothing ever wrote to fails too, since a runner that resets the environment would otherwise score perfectly having measured nothing. A leak outranks a failing suite in the verdict. The `tempdir` section takes the `command` that runs the suite, so a suite written in another language is measured the same way, and `tempdir.allow` maps a path prefix to the reason it may stay; an allowance with an empty reason fails when the file is read.

## v0.7.0 - 2026-08-30

### Added

- **`cgo-free`: no Go file imports `"C"`.** The gate reads source rather than trusting a build, because a file can import `"C"` behind a build tag the current platform does not select and still break the promise for another supported build. It understands import blocks and blank imports, ignores comments, and reports file and line. A scan that read no Go files fails.

## v0.6.0 - 2026-08-30

### Added

- **`spec-lint` checks the outcome of a finished spec.** `spec.status_linked_from` requires that a status meaning "the outcome is recorded elsewhere" is linked from that file. `spec.status_marker` gates the paragraph that tells two statuses apart, in both directions: a spec at the finished status must not name open work, and one at the other must. Each takes `expect` and `reject` patterns as separate fields.

## v0.5.0 - 2026-08-30

### Added

- **`spec-lint` keeps an id register honest.** A tree that numbers something, such as the rows of a conformance register, and cites those numbers elsewhere gets two checks from `spec.register`: the rows are numbered without gaps, and nothing in the tree cites a row that does not exist. A gap means a row was deleted and every later number now means something else; a dangling citation is the same drift one level up.

## v0.4.0 - 2026-08-29

### Added

- **`depcheck`: no build reaches a dependency nobody admitted.** The list comes from `go list -deps` on the packages `depcheck.packages` names, not from `go.mod`, because a module graph says what could be reached and the claim being defended is about what is. `depcheck.allow` maps a dependency to the reason it is admitted, and `depcheck.platforms` sets the GOOS/GOARCH pairs to resolve, since a dependency reached only on darwin is invisible to a linux-only gate.
- **`spec-lint` rules that a repository kept in its own code are now config.** `spec.vocabulary` adds a closed vocabulary beyond status, such as a layer; `spec.status_requires` ties a frontmatter key to a status and checks it against a pattern, in both directions, so the key cannot be left behind when the status moves on; `spec.require_section`, `spec.require_section_by_status` and `spec.section_exempt` require sections globally and per status; `spec.scoped_ids` requires ids to carry their own spec's number; and `spec.tables` requires table rows to match their header. A heading match strips section numbering.

### Fixed

- **Every gate honors `-C`.** The gates shelled out in the process's own working directory, so a check addressed at another repository read that repository's config and then reported on the tree it was run from.

## v0.3.0 - 2026-08-29

### Fixed

- **`spec-lint` tells an index row from a legend row by column order.** An index row names the spec before its status; a legend row is about the status and cites a spec afterwards, as an example. Reading every table with a Status column as the index made a legend's examples into claims about the specs they cite, and reading only the largest table lost most of the index. A tree whose index is split across several tables, one for shipped specs and others for next and blocked, is now read whole, and a tree that failed on its legend passes.

## v0.2.0 - 2026-08-29

### Changed

- Nothing changes for a repository that runs the bar. This release only moves this repository's own lint settings to the organization's linter set.

## v0.1.0 - 2026-08-29

### Added

- **The shared per-push gates as one binary and one config.** `go get -tool latere.ai/x/ci-gate/cmd/lateregate` adds `lateregate`, and `go tool lateregate <gate>` runs one gate. The first five are `fmt-check` (no Go source is unformatted), `modernize` (no code that a standard library call already covers), `cover`, `hermetic` (the suite passes with only the toolchain on `PATH`, plus the directories `.lateregate.yaml` allows) and `spec-lint` (the spec tree agrees with itself and with its index). `fmt-check` and `modernize` need no configuration; the others read `.lateregate.yaml` at the repository root.
- **`cover` gates every package against the floor, not the repository average.** A package that cannot clear the floor is exempted under `cover.exempt` with a reason, and an exemption with an empty reason fails when the file is read. So does a profile that covers no packages, and one where every package is exempt, since a gate that passed because it measured nothing would keep reporting green.
