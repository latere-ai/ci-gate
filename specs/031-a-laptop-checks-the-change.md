---
title: On a laptop the bar checks what changed since the default branch, and no lint waits for another
status: complete
depends_on:
  - 001-gate-principles.md
  - 008-one-bar.md
  - 011-hooks-hold-the-cheap-gates.md
  - 014-one-suite-run.md
affects:
  - cmd/lateregate/
  - internal/scope/ (new)
  - internal/bar/
  - internal/gates/
  - internal/cover/
  - internal/golangci/
  - README.md
  - docs/commands.md
  - docs/gates.md
  - CHANGELOG.md
effort: medium
trigger: a frontend-only change to a service repository waited on the whole bar before its push, about 20 minutes of a suite that cannot see a frontend, behind another checkout's lint holding golangci-lint's machine-wide lock
created: 2026-10-30
updated: 2026-10-30
author: changkun
dispatched_task_id: null
---

# On a laptop the bar checks what changed since the default branch

## The problem

`lateregate` with no arguments runs every gate over the whole tree. On a
service repository that is about 20 minutes on a laptop, and nearly all of
it is three gates: on the last green CI run of one such repository `suite`
took 13.3 minutes, `lint` 2.5 and `vuln` 0.8, while the fourteen other
gates together took under half a minute.

The whole tree is the wrong question before a push. The default branch is
already held to the whole bar: CI runs every gate on every push to it, so
the only thing a push can break is what it changes and what depends on
that. Two things made the answer as slow as the question:

- **The go command's test cache cannot help.** `suite` builds with
  `-coverpkg=./...`, which compiles every package into every test binary,
  so one changed line reruns every test. And a checkout in a fresh
  worktree starts cold, because the build cache keys a package on its
  directory.
- **Two checkouts gated at once queue on each other.** The full `lint` gate
  kept golangci-lint's machine-wide lock, which [[011-hooks-hold-the-cheap-gates]]
  lifted for the pre-push lint only. A second lint on the machine waits for
  the first and then fails with "parallel golangci-lint is running", so
  people gating two repositories at once run them one after the other.

The cost lands on exactly the changes that need the bar least. A change
to a frontend, a document or the changelog waited the full 20 minutes for
a suite that reads none of it.

## The decision

**`lateregate`, and `lateregate check`, read the change on a laptop.** The
change is every path that differs from the merge base of `HEAD` and the
remote's default branch (`origin/HEAD`, or `origin/main` where the clone
never recorded it): committed, staged, unstaged, and untracked files git
does not ignore. A rename counts as both of its paths.

Each changed path belongs to the package of the module whose directory is
its nearest: Go source and the testdata its tests read. A file a package
or its tests embed also belongs to that package, which matters when the
file sits under a sub-package's directory: `go:embed` reads anywhere
below the embedding package, and the nearest package is then the wrong
one. A path under no package, a frontend, the changelog, a spec, belongs
to none. A path in a nested module is that module's, which `./...` does
not read.

Four gates narrow, because they are the minutes:

| Gate | On a scoped check |
| --- | --- |
| `suite` | vet and `go test` over the changed packages and every package whose build or tests import one, directly or through another package; `-coverpkg` names the changed packages, and the floor applies to them. Skipped when no package changed |
| `lint` | golangci-lint over the same packages as the suite: a changed signature or a removed export leaves its findings in the callers. Skipped when no package changed |
| `vuln` | the whole module when a Go package changed, since new code can call a vulnerable function nothing called before; skipped when none did, since the advisories that move with no commit are CI's, which scans on every push |
| `lean` | skipped unless a changed path is in a Lake project |

Every other gate reads the whole tree, as it always has. Each is seconds,
and a second rule for what a scan reads would buy nothing.

**The coverage floor is the whole suite's figure for each changed
package.** Every test that can reach a changed package imports it,
directly or through another package, so every such test is in the run,
and the profile, measured over the changed packages alone, gives each one
the figure the whole suite would. Packages the run reaches but the change
did not touch are not measured: the tests that cover them through
packages outside the run are not in it, so the run cannot give them the
whole suite's figure. Their figure can still move, when a changed
package's test that exercised them is removed or rewritten; that is left
to CI, as [What it cannot see](#what-it-cannot-see) says. A change whose
every package holds no statements or is exempt gives the floor nothing to
measure, and the floor says so and passes, as the whole run passes a
package it cannot measure for the same reasons.

**The scope never narrows on a guess.** Each of these checks the whole
tree and says why on the first line:

- `go.mod`, `go.sum`, `go.work` or `go.work.sum` changed: a dependency
  reaches every package;
- `.lateregate.yaml` or a repository-owned `.golangci.yml` changed: the
  bar's own configuration moves verdicts anywhere;
- anything under `vendor/` changed: it is a dependency;
- a changed Go file sits in a directory where `go list` reports no
  package, because the package was removed or moved or because every file
  in it is excluded by a build constraint on this machine: what imports it
  is outside the graph, so the graph says nothing about it;
- there is no default branch to measure from, `HEAD` shares no commit with
  it, git cannot list the change, or `go list` cannot list the module.

**The whole tree is still the bar of record.** A runner (`CI` set), a
release (`lateregate release` runs the bar on the tree it tags), a gate
run by name, and `list -json`, which CI's matrix reads, are never scoped.
`-all` reads the whole tree on a laptop.

**The run says what it read.** It opens with the base and the size of the
change (`checking what changed since origin/main (1a2b3c4d5e6f): 2 files,
1 Go package, 5 packages with what imports them; -all checks the whole
tree`), a narrowed gate's summary row says what it read or why it was
skipped, and the verdict ends `on what changed since origin/main (...)`.

**No lint takes the machine-wide lock.** The full gate passes
`--allow-parallel-runners` as the pre-push run does. The lock guards a
cache that does not need it: golangci-lint's is content-addressed, as the
go command's is, and safe for concurrent processes. Two checkouts now gate
side by side.

## Why this keeps the principles

[[001-gate-principles]] D4 refuses a gate that passes by measuring
nothing. A narrowed gate with nothing to read is a skip with its reason,
not a pass, and the summary says `SKIP`. The gates that do run on a
scoped check still measure: the scans read the whole tree, and a suite
that runs measures each changed package against the floor, failing on one
it cannot measure exactly as the whole run does.

[[008-one-bar]] has one bar on a laptop and a runner. The runner still
runs all of it, on every push. What a laptop skips is what the push cannot
have changed, and the cases where that is not knowable from the import
graph go back to the whole tree.

## What it cannot see

Two breaks a scoped check passes and CI catches after the push, which is
where it is caught today for any push made without the bar:

- A test that reads a file outside its own package's directory at run
  time, by a relative path such as `../docs`, depends on that file in a
  way the import graph does not record. A change to such a file reaches
  the test's package only if the file also sits under a package the test
  imports. A repository that leans on this pattern can run `-all`.
- A package the change did not touch, whose coverage a changed package's
  tests carried, drops under the floor when those tests are removed or
  rewritten. Measuring it would mean running every test that reaches it,
  which is the whole suite for a package at the bottom of the graph.

`go list` runs once, for this machine's platform and with no build tags,
so a package only another platform builds, or only the race detector's
build tag selects, is listed by its files' directory and not its
imports.

## Acceptance criteria

| # | Criterion | Test |
| --- | --- | --- |
| 1 | The change is the diff from the merge base with the default branch plus untracked files; each path belongs to its nearest package, testdata included, and to every package that embeds it; a nested module's path to none | `TestAChangeReachesItsPackageAndWhatImportsIt`, `TestAFileBelongsToItsNearestPackage`, `TestAnEmbeddingPackageOwnsWhatItEmbeds`, `TestARootPackageOwnsWhatNoOtherDoes`, `TestUntrackedFilesAreTheChangeToo`, `TestANestedModuleIsNotTheChange`, `TestResolveAgainstARealCheckout` |
| 2 | The suite reads the changed packages and everything whose build or tests import them | `TestAChangeReachesItsPackageAndWhatImportsIt`, `TestAnExternalTestReachesItsPackage`, `TestSuiteReadsThePackagesItIsGiven` |
| 3 | A dependency, `vendor/`, the bar's configuration, a Go directory with no listed package, and anything unmeasurable check the whole tree with the reason | `TestADependencyOrTheBarsConfigChecksTheWholeTree`, `TestAGoDirectoryWithNoListedPackageChecksTheWholeTree`, `TestWhatCannotBeMeasuredChecksTheWholeTree`, `TestADeletedFileChangesItsPackage` |
| 4 | Only suite, lint, vuln and lean narrow; lint reads what the suite reads; vuln runs on a Go change; the floor applies to the changed packages and passes, saying so, when none is measurable; a waived cover promises none | `TestAChangeWithNoGoPackageSkipsTheLongGates`, `TestLeanRunsOnlyForAChangedProject`, `TestAGoChangeNarrowsTheSuiteAndLintAndScans`, `TestAWaivedCoverPromisesNoFloor`, `TestOnlyKeepsTheNamedPackages`, `TestLintReadsThePackagesTheChangeReaches`, `TestTheFloorMeasuresOnlyWhatTheChangeCanBeMeasuredOn` |
| 5 | CI, `-all`, a release, a gate by name and `list` read the whole tree | `TestCheckTakesAll`, `TestAWholeTreeScopeNarrowsNothing` |
| 6 | The check opens with what it read and ends its verdict on it | `TestCheckSaysWhatItRead` |
| 7 | No lint run takes golangci-lint's machine-wide lock | `TestLintRendersThenRunsThePinnedLinter`, `TestLintPackagesLintsWhatItIsGiven` |

## Outcome

Built as designed. Measured on a service repository's checkout with the
working-tree binary: an unchanged tree checked in 28 seconds, a frontend
edit in 6, and a change to one Go package (five with its importers, in a
fresh worktree) in 2 minutes 51 seconds, against about 20 minutes for the
whole bar.

A review after the first release found the scope too narrow in four
places and too strict in one, all fixed in the next patch: lint read only
the changed packages, so a changed signature passed with its callers
broken; the vulnerability scan never ran, so new code calling a
vulnerable function passed; a file embedded through a sub-package's
directory changed only the sub-package; a package a build constraint
excludes here looked like no package at all; and a change confined to
packages with no statements, or exempt ones, failed the floor for
measuring nothing. The decisions above carry the fixed rule. The earlier
text also said an unchanged package's coverage cannot move; it can, and
[What it cannot see](#what-it-cannot-see) now says so.
