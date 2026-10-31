---
title: A laptop check holds the packages a change reaches without touching to the coverage they had at the base
status: complete
depends_on:
  - 001-gate-principles.md
  - 014-one-suite-run.md
  - 031-a-laptop-checks-the-change.md
affects:
  - internal/scope/
  - internal/bar/
  - internal/gates/
  - internal/cover/
  - docs/commands.md
  - docs/gates.md
  - README.md
  - CHANGELOG.md
effort: medium
trigger: a change that removed a test in one package took a package it did not touch under the coverage floor, and the scoped check passed it because it measured only the changed packages
created: 2026-10-31
updated: 2026-10-31
author: changkun
dispatched_task_id: null
---

# A laptop check holds untouched packages to the coverage they had at the base

## The problem

The whole suite measures each package with `-coverpkg=./...`, so a
package's figure is every block any test binary covered, its own tests
and every other package's. [[031-a-laptop-checks-the-change]] narrowed
the suite on a laptop to the changed packages and the ones that import
them, and applied the floor to the changed packages only.

That left a package the change did not touch unmeasured, and its figure
can still fall. Package `a` imports `b`, and `a`'s tests are what reach
`b.Two`. A change to `a` that stops calling `b.Two`, or removes the test
that did, takes `b` from 100% to 50% without a line of `b` changing. The
scoped check passed it, and CI failed it after the push.

Measuring `b` the way the whole suite does means running every test that
reaches it. For a package low in the graph that is most of the suite,
which is the 13 minutes the scoped check exists to avoid.

## The decision

**Only the tests the run holds can have moved an untouched package's
coverage.** A test binary outside the run links no changed package and
holds no changed file: it is the base's, byte for byte, and what it
covered there it covers now. So a package the change did not touch kept
its whole-suite figure if the run's tests still cover every block they
covered at the base. That needs the run's tests at the base, not every
test in the module.

**The base is rebuilt in place with an overlay.** The floor writes each
changed file a package reads as the base held it, or as absent where the
change added it, and runs the run's tests again with `go test -overlay`.
The build cache is the checkout's own, so only the changed packages
compile again, and a base that did not move since the last check replays
from the test cache.

**Each untouched package the run's tests link, now or at the base, is
compared block by block.** A package keeps its figure when every block
the base's run covered is covered by the run now. A package linked only
at the base, through an import the change removed, is compared too: it
lost everything the run's tests gave it. A package that lost a block is
measured over every test that reaches it, the run's and the rest, and is
held to the floor as a changed package is. A package that kept every
block is held to nothing more; its figure is the one CI measured on the
base.

| Case | What the floor does |
| --- | --- |
| an untouched package kept every block the base's run covered | nothing more, and says how many kept their coverage |
| an untouched package lost a block | runs every other test that reaches it and applies the floor to its whole-suite figure |
| a changed file is one a package's tests read at run time, such as testdata | that package's tests stay out of the base's run, and every untouched package they link is measured in full |
| the base's tests fail here | every untouched package is measured in full |
| the base cannot be rebuilt (git or `go list` fails) | the floor fails, with the reason and `-all` |

**The base's run measures untouched packages by name.** go test compiles
a package it instruments from the files on disk and leaves the overlay out
for it, so a changed package measured in that run would run as the change
left it. An untouched package holds no changed file, so its disk is its
base. The run itself, and a full measurement, measure `./...`, which is
every package of the module their test binaries link.

**The extra runs are the suite's.** The floor runs them through the
suite, with the same race detector, stripped PATH, TMPDIR sandbox and
container label, and the suite reads the sandbox and the engine after the
floor, so what an extra run leaves behind fails the suite.

## Why this keeps the principles

[[001-gate-principles]] D4 refuses a gate that passes by measuring
nothing. A package the floor does not measure in full is still measured:
block by block against the base, which CI held to the floor. One whose
base cannot be compared is measured in full rather than passed.

## What it cannot see

The comparison rests on the base having cleared the floor in CI, which is
the premise of every scoped check: [[031-a-laptop-checks-the-change]]
reads the default branch as already held to the whole bar.

A test that reads another package's files by a relative path, such as
`../docs`, is the case [[031-a-laptop-checks-the-change]] already names:
the import graph does not record it, and the overlay does not reach it.

## Acceptance criteria

| # | Criterion | Test |
| --- | --- | --- |
| 1 | The overlay maps each changed file a package reads to its base content, or to absent for an added file, and leaves nested modules out | `TestABaselineReplaysTheBase` |
| 2 | Untouched packages are those a Reached test binary links now or at the base, minus the changed ones | `TestABaselineReplaysTheBase` |
| 3 | A changed file read at run time keeps its package's tests out of the base's run and marks what they link for a full measurement | `TestARunTimeFileCannotBeReplayed`, `TestWhatTheBaseCannotShowIsMeasuredInFull` |
| 4 | An untouched package that lost a block is measured over every test that reaches it and held to the floor | `TestAnUntouchedPackageThatLostCoverageIsHeldToTheFloor`, `TestALostPackageTheRunHoldsNeedsNoOtherRun`, `TestLostNamesThePackagesThatLostABlock` |
| 5 | One that kept every block is not measured again | `TestAnUntouchedPackageThatKeptItsCoverageIsNotRemeasured`, `TestRunOnJudgesOnlyThePackagesNamed` |
| 6 | The base's run measures only untouched packages | `TestAnUntouchedPackageThatLostCoverageIsHeldToTheFloor` |
| 7 | A base whose tests fail is measured in full; one that cannot be rebuilt fails the floor | `TestWhatTheBaseCannotShowIsMeasuredInFull`, `TestABaselineThatCannotBeBuiltSaysWhy`, `TestTheFloorMeasuresOnlyWhatTheChangeCanBeMeasuredOn`, `TestAFloorWithNothingToCompareFails` |
| 8 | The floor's runs are inside the suite's environment and checks | `TestAFloorsOwnRunIsTheSuitesToo` |

## Outcome

Built as designed. On this repository's own change (4 packages changed,
12 reached, 18 untouched), the scoped check took 15 seconds warm against
10 for the check that measured the changed packages alone, and 32
seconds against 19 on the first run after an edit, the difference being
the base's run of the reached tests. All 18 untouched packages kept their
coverage.
