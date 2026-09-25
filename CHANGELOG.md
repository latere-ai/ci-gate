# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

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

- `release` reads past a run cancelled because a newer run of the same
  workflow replaced it, the way it reads past that newer run while it is in
  progress. Once a concurrency group cancelled superseded runs, the cancelled
  run was the newest completed one and the cut was refused as red while the
  run that replaced it was still going.

## v0.47.0 - 2026-09-20

### Fixed

- `json-bytes` no longer reports `json.RawMessage`. pgx registers that type to
  the json column type and sends it correctly, so the finding was noise:
  seven of the nine it reported in `llm-gateway` and one of `insula`'s were
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
  named identifier type in `arca` and in `insula`.

- `json-bytes` reads a helper whose declared result is `any`, by the concrete
  types it can return, and across the whole module rather than one package at
  a time. `llm-gateway` laundered six binds through one such helper an import
  away from every statement that bound it. A value whose concrete type is out
  of reach is still reported as nothing.

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
  hand audit of `agents` found nine more.

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
  organisation's Go repositories at this release reports 53 binds across
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

### Fixed

- The `identity` gate reads a Go file's string literals without descending
  into its import declarations. An import path is a string in the grammar and
  a dependency in the file, and reading it as a literal made the `authorizer`
  rule report a file that imports a test double whose path holds `authorize`,
  and the `claims` rule report a package whose path holds a membership claim.
  Repositories had been putting those files in `identity.skip`, which stopped
  every other rule from reading them too; from v0.40.0 those entries can go.
- The `identity` gate's `audience` and `bearers` rules see a repository whose
  only command is at the module root. They recognise a container by the name
  its image was built under, and that name was read from the directories
  under `cmd/` alone, so a repository that moved its `main` to the root
  reported `SKIP audience no container in the deployment runs a command this
  repository builds` and its `AUTH_AUDIENCE` went unchecked. A module root
  that is `package main` now names a command, called after the module's last
  segment, which is the name `go build` gives the binary.

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

### Fixed

- The `identity` gate's `audience` rule reads which containers of a manifest
  are this repository's workload. A container runs the repository when the
  name its image was built under — the last path segment of the reference,
  without the registry, the tag or the digest — is exactly a command under
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

### Changed

- A deployment whose image name is not a directory under `cmd/` now reports
  `SKIP audience` with the reason, where the single-container shortcut used
  to read that container as the workload. A rule that reads nothing says so
  rather than passing: name the image after the command it runs, or put the
  path in `identity.skip` as a decision.

## v0.36.0 - 2026-09-15

### Added

- `release.stamp` in `.lateregate.yaml`: a list of `{file, pattern}` whose
  `vX.Y.Z` `lateregate release` rewrites to the version it is cutting, staged
  into the same commit as the changelog. A file that names the release — a
  `SECURITY.md` line, a deploy overlay's `newTag` — no longer lags the tag,
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

### Changed

- `identity`, the delegation rule: `act`, `agent_id` and `actor_id` as a
  struct tag in a type that carries no registered claim are a column of
  that type wherever the file lives, so a handler that verifies tokens may
  render an audit row with an `actor_id` field. A bare occurrence in a
  file about claims, and a tag beside `sub`, `aud` or `exp`, are findings
  as before.

### Added

- `identity.reached_by`: `clients` (the default) or `services`. A service
  no client acts at for a person declares `services`, and the family check
  then expects no client registry row for its audience instead of failing
  on the missing one.

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

- `lateregate release` runs the full gate before it tags. v0.31.1 was cut
  with the modernize and lint gates red on the release commit; a release is
  the one push that must never go out red.

## v0.31.1 - 2026-09-06

- The licence check on staged files (the pre-commit hook) honours
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

- The licence gate accepts `license.spdx: LicenseRef-Proprietary`, the SPDX
  form for terms not on the licence list. The root `LICENSE` must then reserve
  all rights and grant no licence, and every file carries the identifier the
  same way an open source repository does, so a proprietary repository is
  checked instead of waived.

### Added

- `license` checks the root `LICENSE` text against `license.spdx` through a
  fingerprint per identifier, so a declaration that names one licence over a
  root file carrying another fails with both named. An identifier the table
  does not know fails closed. This is the mismatch pkg carried for four
  days: `Apache-2.0` on 264 files above an MIT `LICENSE`.

## v0.29.2 - 2026-09-06

- The pre-push hook no longer fails a push that changes a Go file under a
  nested module (a directory with its own `go.mod`, such as a spike tool or
  an example). Such a file is not a package of the main module, so the hook
  skips it the way it skips `testdata`, and the full gate remains the bar.

## v0.29.1 - 2026-09-06

This repository now publishes its own GitHub releases from this file,
through `notes-release.yml` in latere-ai/ci. v0.29.0 was tagged before that
pipeline existed and has no release object; its section below is the note.

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
