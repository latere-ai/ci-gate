---
title: Gate the licence notice every source file carries
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - cmd/lateregate/
  - internal/config/
  - internal/license/
effort: small
created: 2026-08-30
updated: 2026-08-30
author: changkun
dispatched_task_id: null
---

# Gate the licence notice every source file carries

## The problem

A repository's licence lives in one file at the root. That is enough for
someone who clones the repository and reads it. It is not enough for anyone
who receives the code some other way, which is most of the ways code
actually travels: a file pasted into a bug report, a package vendored into
another tree, a snippet lifted into a model's training corpus, an SBOM
scanner walking a build.

The org's state on 2026-08-30, measured across the four Go repositories:

| Repository | Licence | Root file | Per-file notice |
|---|---|---|---|
| `ci-gate` | MIT | yes | all 22 Go files, prose form |
| `pkg` | MIT | yes | none |
| `pay` | MIT | yes | none |
| `replichai` | AGPL-3.0-or-later | no, and README said "Proprietary" | none |

Four repositories, three different answers, and the divergence went
unnoticed because nothing asserted anything. `ci-gate` carried a notice on
every file only because one person happened to keep doing it by hand.

The prose form it used is also not machine-readable:

```
// Copyright 2026 Latere AI.
// Licensed under the MIT License.
```

No scanner can turn "Licensed under the MIT License." into an identifier
without guessing. `SPDX-License-Identifier` exists precisely so it does not
have to, and it is what `reuse`, `scancode`, `syft` and GitHub's own
licence detection read.

## Why this is a gate and not a convention

The failure is silent in both directions. A file added without a notice
looks exactly like a file that has one, until someone greps. A repository
that changes its licence leaves every existing header stating the old one,
and the stale header is the one a downstream reader trusts.

`replichai` makes the second case concrete: it is going open source under
AGPL-3.0-or-later, which is a licence whose terms only bind anyone if they
travel with the code. A copyleft notice nobody can find is a permissive
licence with extra steps.

## The check

For each source file under the repository root, of a configured type, with
`<c>` the comment marker that type uses and `n = 2` when the file opens with
a shebang, `1` otherwise:

```
  line n     ==  "<c> SPDX-FileCopyrightText: <year> <holder>"
  line n+1   ==  "<c> SPDX-License-Identifier: <spdx>"
  line n+2   ==  ""                      (or the file ends at line n+1)

  <year>     ~   \d{4}(-\d{4})?          a year or a range, not a fixed value
```

with `<holder>` and `<spdx>` from `.lateregate.yaml`, matched literally.

## D1 — the identifier is declared, and an undeclared repository fails

`license.spdx` has no default. A repository that runs the gate without
declaring one gets an error naming the field, not a pass.

This is the opposite of the other gates, which default to something
sensible so a repo can adopt them without config ([[001-gate-principles]]
D2). It has to be, because there is no sensible default for a licence: a
guessed identifier printed into 300 files is worse than no identifier. The
whole value of the gate is that the answer was decided by a person.

For the same reason the gate does not read `LICENSE` and infer the
identifier from its text. Detection is a heuristic, and a heuristic that
silently picks `AGPL-3.0-only` for a repository that meant
`AGPL-3.0-or-later` produces exactly the stale-header failure this gate
exists to prevent. It does check that a root `LICENSE` file exists, because
a header pointing at terms that are not in the tree names nothing.

## D2 — the blank third line is part of the check

In Go, a comment block immediately above `package` *is* the package
documentation. Without a blank line separating them, the licence text
becomes the first paragraph of the package doc and appears at the top of
every rendered page on `pkg.go.dev`:

```go
// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later
                                                 ← this line is required
// Package audit decides what a paper released.
package audit
```

The mistake is invisible in review and permanent once it is in 300 files, so
the gate checks the separation rather than only the two lines above it.

## D3 — the year is a pattern, not a value

Checking `2026` literally turns every 1 January into a 300-file diff, and a
gate that fails for a reason nobody caused is a gate people learn to skip.
The check accepts a year or a range, so `2026`, `2024-2026` and a file
written next year all pass. What the gate asserts is that a human wrote a
year, not which one.

## D4 — one file-type list, `.go` by default

The gate is written for a Go tool, so `.go` is what it checks when nothing
says otherwise. A repository that ships more than Go lists what it ships:

```yaml
license:
  spdx: AGPL-3.0-or-later
  holder: Latere AI
  extensions: ['.go', '.ts', '.tsx', '.mjs', '.sh']
  names: [Makefile, Dockerfile]
```

`names` exists because the files most likely to have no extension -- the
Makefile, the Dockerfile -- are still part of the work. Listing anything
turns the Go default off, so what a repository names is what gets checked.

The comment marker comes from the file type, not from the gate: `//` for Go
and TypeScript, `#` for shell, YAML and a Makefile. A type the table has no
marker for is rejected when the config loads rather than skipped at scan
time. This is the one that would fail silently: `//` is a legal comment in
most languages, so a mismatched marker matches nothing and reports a clean
pass over files nobody checked.

## D5 — nothing passes vacuously

[[001-gate-principles]] D4. A scan that matched no file fails, which is what
catches a wrong `extensions` entry or a root pointed at the wrong directory.
A gate reporting "0 files, all correct" is the failure mode that makes every
other gate here suspicious.

## D6 — a shebang keeps line 1

The kernel honours `#!` only as the first two bytes of the file, so a notice
pushed above it makes the script unexecutable:

```sh
#!/bin/sh
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: AGPL-3.0-or-later

set -eu
```

The whole check moves down one line with it, the blank-line rule included,
so a script is held to the same shape as everything else rather than
exempted from it.

## What shipped

- `lateregate license`, and `make license` in this repository.
- `license.spdx`, `license.holder`, `license.extensions`, `license.names` and
  `license.skip` in `.lateregate.yaml`, every file type validated against the
  table of comment markers the scanner knows.
- This repository's own 24 Go files moved from the prose notice to the SPDX
  form, which is the change that made the old one's unreadability concrete.

## Acceptance

- [x] A file with the two lines and a blank third passes.
- [x] A missing header fails and the file is named.
- [x] A header whose identifier differs from `license.spdx` fails, and the
      failure prints both values.
- [x] A header whose holder differs from `license.holder` fails.
- [x] A header with no blank line before the doc comment fails.
- [x] A year range passes; a missing year fails.
- [x] A `//go:build` line between the header and `package` passes.
- [x] A shell script's notice uses `#`, and the Go marker there fails.
- [x] A notice below a shebang passes; the line numbers in a failure count
      from below it.
- [x] A file listed by whole name is checked; listing anything turns the Go
      default off.
- [x] `license.spdx` unset fails with a message naming the field.
- [x] A repository with no root `LICENSE` fails.
- [x] A file type the gate has no comment marker for is rejected by
      `config.Load`, not at scan time.
- [x] A scan that matched no file fails.
- [x] This repository gates itself: all 22 Go files carry the MIT form.
