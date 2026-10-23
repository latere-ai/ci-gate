---
title: A repository's Lean proofs build
status: complete
depends_on:
  - 001-gate-principles.md
  - 014-one-suite-run.md
affects:
  - internal/gates/
  - internal/bar/
  - README.md
  - docs/gates.md
  - CHANGELOG.md
effort: small
created: 2026-10-23
updated: 2026-10-23
author: changkun
dispatched_task_id: null
---

# A repository's Lean proofs build

## The problem

A repository can carry a Lean 4 model of its own design and proofs about
it, as Topos does for the statuses of a session in `formal/`. A proof is
only worth what its last build says, and nothing builds it: the per-push
bar runs Go, so a change to the code the model describes, or to the
model, can leave a proof that no longer checks, or one that a `sorry`
stands in for, and every gate stays green.

## The decision

A `lean` gate builds every Lake project the repository tracks.

### What it applies to

The gate asks git, as `spec-lint` does: a tracked `lakefile.toml` or
`lakefile.lean` makes its directory a project, outside `.lake`, where
Lake keeps the packages it fetched. A repository with none skips the gate
with `tracks no Lake project (lakefile.toml or lakefile.lean)`, so the
release reaches every consumer and changes nothing for the ones without
proofs. A git that cannot answer fails the plan, as it does for
`spec-lint`.

### What it asserts

`lake build --wfail` in each project, which fails on an error and on any
warning a module logs, a cached module's replayed warnings included. A
proof that uses `sorry` logs a warning, so a project passes only when
every theorem in it is proved. A deprecated name is a warning too and
fails the gate: a project fixes it rather than carrying it.

`lake` runs in the project's own directory. elan picks the toolchain from
the `lean-toolchain` file of the directory it runs in, so a build started
elsewhere, with `lake -d`, runs the machine's default toolchain, or fails
with none configured.

### Where lake comes from

A `lake` on `PATH` is used as it is. A machine without one, a CI runner,
gets elan at the version this repository pins, `ElanVersion`, installed
under the user's cache directory (`os.UserCacheDir()/lateregate/elan-<version>`):

- the archive for the machine's platform is downloaded with `curl` from
  elan's GitHub release and checked against the SHA-256 the release
  publishes, pinned here beside the version;
- `elan-init -y --no-modify-path --default-toolchain none` installs it
  with `ELAN_HOME` set to that directory;
- the first `lake` fetches the toolchain the project's `lean-toolchain`
  names, so the toolchain stays the project's decision.

The download goes through the gate's `Exec`, like every other tool the
gates run, rather than a Go HTTP client, so the gate holds `otel-client`
without an exemption: a release archive joins no trace.

The gate holds a file lock on the install directory for the whole build,
not only the install. Slots of one self-hosted runner share a home
directory, and elan writes each toolchain into it as `lake` first runs,
so two builds at once would write the same files.

Darwin and Linux on arm64 and amd64 are pinned. Another platform without
a `lake` on `PATH` fails with the sentence that says to install elan; it
does not skip, since the repository still has proofs to build.

### Its place in the run

After the static scans and before `lint`: a machine that installs a
toolchain takes about a minute, and a failed proof should not wait for
the suite.

## Acceptance criteria

- `lateregate list` on a repository with no tracked lakefile shows
  `SKIP lean tracks no Lake project (lakefile.toml or lakefile.lean)`;
  with one, the gate runs; a git that cannot answer fails the plan.
- The projects are the directories of tracked lakefiles, each once,
  sorted, none under `.lake`.
- With a `lake` on `PATH`, each project is built with
  `lake build --wfail` in its own directory, every project is built when
  one fails, and the failure names each that failed and says that `sorry`
  is a warning.
- Without one, the pinned archive is downloaded, a digest that differs
  stops the gate before anything runs, and the `elan-init` in it installs
  into the cache directory, whose `lake` builds with `ELAN_HOME` set to
  it; the next run downloads nothing. A failed download, an archive with
  no `elan-init`, a failed `elan-init`, one that installs no `lake`, an
  unpinned platform and a missing cache directory each stop the gate with
  what went wrong, and nothing is built.
- Run against Topos's `formal/`, the gate passes with the machine's lake
  and with none on `PATH`; run against a project whose one theorem is
  `sorry`, it fails.
- `internal/gates` and `internal/bar` clear the 90% floor.

## Outcome

Shipped as written. Against Topos's main, the gate built `formal/` with
the machine's lake, and on a machine with no lake on `PATH` it installed
elan v4.2.4, fetched the project's Lean v4.35.0, and built it. Against a
one-theorem project whose proof is `sorry`, it failed naming the project.

The first run with no lake on `PATH` built with `lake -d <project>` from
the repository root, which elan answered with "no default toolchain
configured"; on a machine whose default toolchain was a release, the same
call would have built against that release rather than the project's.
Running lake in the project's directory is what the decision above says
for that reason.

The toolchain is large: Lean v4.35.0 is about 2.8 GB unpacked, so a
hosted runner, which starts empty, downloads it on every run.
