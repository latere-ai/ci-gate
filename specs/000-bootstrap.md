---
title: Extract the shared per-push gates out of the repos that grew them
status: complete
depends_on: []
affects:
  - cmd/lateregate/
  - internal/
  - .lateregate.yaml
  - ../ci/.github/workflows/go-verify.yml
effort: medium
created: 2026-08-29
updated: 2026-08-29
author: changkun
dispatched_task_id: null
---

# Extract the shared per-push gates

## Why this repository exists

`latere-ai/ci` centralized **release** — three `workflow_call` pipelines
triggered by a version tag — and nothing about the gates that run on every
push. So every Go repository built those itself, or went without.

A survey of all 21 Go repositories in the org on 2026-08-29:

| Property | Repos |
| --- | --- |
| Spec-driven (`specs/*.md`, 1 to 116 specs) | 18 |
| Have `fmt-check` + `test` + `lint-modernize` | 15 |
| Have `cover` | 8 |
| Have `test-hermetic`, `test-race` or `validate` | 1 |
| Have a per-package coverage gate | 2 |
| Have a spec linter | 2 |

Two numbers decided the shape. **Seventeen of eighteen spec-driven
repositories lint nothing**, so their frontmatter, indexes and dependency
edges are maintained by hand. And **one repository of twenty-one** had the
full gate set, so the cost is adoption, not duplication — a contract
demanding seven hand-written make targets would have moved the duplication
from workflows into Makefiles rather than removing it.

## What the gates are for

Three CI failures in one day in `llmops` shared one root cause: tests that
depended on what happened to be installed on the machine running them.
`systemctl` was absent on macOS and present-but-unprivileged on a runner; a
harness binary was on a developer's `PATH` and not on a runner's. Each
passed locally and failed in CI, which is the worst order to find out.

Separately, a repository-average coverage gate passed at 90.4% while two of
its packages sat at 85.7% and 87.8%, both invisible behind the average. And
in the same week every row of a 29-spec index read `draft`, including five
specs that were built, deployed and serving.

## What shipped

One binary, one config file per consumer, five subcommands:

| Subcommand | Replaces |
| --- | --- |
| `cover` | `internal/covercheck`, which existed twice |
| `spec-lint` | `internal/speclint`, which existed twice |
| `hermetic` | six lines of shell in one Makefile |
| `fmt-check` | a `gofmt -l` one-liner in fifteen Makefiles |
| `modernize` | a `go fix` diff check with a fixer-existence guard |

The split with `latere-ai/ci` is deliberate: that repository owns
**orchestration and ordering** through `go-verify.yml`, which probes the
consumer's Makefile with `make -n` and runs the targets it finds. This
repository owns **what each gate asserts**. They are coupled only by make
target names, so they version independently.

The reasoning behind each decision is [[001-gate-principles]].

## Outcome

`v0.1.0` on 2026-08-29, `v0.2.0` the same day. `llmops` migrated as the
first consumer: its workflow went from 112 lines to 27, its two internal
tools were deleted, and all ten pipeline jobs pass including macOS,
cross-compile and validate. Its coverage now clears 90% in all eight
packages with **no exemptions at all** — the single entry it had was for
the `covercheck` package this work removed.

Two things the build changed:

- **The index check is anchored on the table header**, not on any row
  holding a Markdown link. A spec cites other specs from prose tables, and
  matching those reported drift that was not there.
- **A public repository cannot call a private repository's reusable
  workflow.** This was not in the design and blocked everything: callers
  failed at startup with zero jobs and no log to read. It predated the
  work — another public repository had been failing the same way and just
  as silently. `latere-ai/ci` is public now.

Not done: the adoption ramp past `llmops`. Fifteen repositories need a
caller and three one-line Makefile edits; `tgo` keeps its own tools,
because its spec linter also enforces layers, decision records and an
outcome rule, which are conventions rather than hygiene.
