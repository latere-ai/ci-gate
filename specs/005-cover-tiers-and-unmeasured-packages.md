---
title: Merge the coverage tiers, and fail the packages no tier measured
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - cmd/lateregate/
  - internal/cover/
effort: small
created: 2026-08-30
updated: 2026-08-30
author: changkun
dispatched_task_id: null
---

# Merge the coverage tiers, and fail the packages no tier measured

## The problem

The `cover` gate read one profile and judged the packages it found in it.
Both halves of that are holes, and `latere-ai/service-template` had already
closed them in a tool of its own before this repository existed.

**A service's coverage is split across tiers.** The unit run reaches the pure
logic; the integration run reaches what sits behind the database boundary. A
gate that reads one profile gates on whichever tier ran last and fails code
the other tier proves. Every service repository therefore either kept a
private merging tool or measured only its unit tier.

**A package with no test file is absent from the profile entirely.** It
produces no records, so a per-package rule that reads only the profile never
sees it, and it clears the floor by not being measured. This is the same
failure as the average that hid two packages at 85.7% and 87.8%, one step
further along: the average at least counted the statements.

That second hole is the more serious one, because it grows in the direction
nobody watches. A new package added without tests raises no finding, and the
gate keeps reporting that every package clears 90%.

## The decision

`-profile` repeats, and the profiles merge as a **union of statement blocks**
rather than a sum of numbers. With `-coverpkg` the same block appears in every
tier that built it, keyed by file and span, so a block either tier covered is
covered and summing the appearances would inflate both the numerator and the
denominator.

The gate lists the module's packages with `go list` and fails on every one the
profiles never mention. Two rules keep that honest:

- A package that declares no function with a body is **not** listed. The tool
  instruments statements, so such a package produces no data however it is
  tested, and a finding no test can clear is one people learn to skip.
- An **exempt** package may be unmeasured. The exemption already carries a
  written reason, and the reason covers being untested as much as being
  under-tested. This is the only escape, which keeps it visible in the diff.

The package list is injected as a `Lister`, matching `depcheck`. A nil list
turns the rule off, which is what the unit tests use to judge the profile
arithmetic on its own.

## What holds

- Two tiers that each cover half a package's statements pass together and
  fail apart, and the merged report shows 10/10 rather than 20/20.
- A package the profile omits fails the gate and is named in the error.
- An exempt package the profile omits passes.
- A package of constants, or one whose only function has an empty body, is
  not reported as unmeasured.
- A package list that cannot be read fails the gate rather than skipping the
  rule, per [[001-gate-principles]]: nothing passes vacuously.

## Outcome

Shipped. This repository gates itself with it and reports seven measured
packages and no unmeasured ones. `service-template`'s `tools/coverage` is
removed in favour of it, which was the point: the private tool existed only
because the shared gate could not express what a service needs.
