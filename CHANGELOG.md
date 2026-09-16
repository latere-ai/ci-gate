# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

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
