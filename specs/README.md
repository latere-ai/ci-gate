# Specs

How this repository is built and why. Written for whoever changes it next;
the README is for whoever uses it.

Read [[000-bootstrap]] first for why the repository exists, then
[[001-gate-principles]] for the four decisions every gate here has to hold.

| # | Spec | Status | Scope |
|---|---|---|---|
| 000 | [Extract the shared per-push gates](000-bootstrap.md) | complete | Why this repo exists, the org survey behind it, and what shipped |
| 001 | [What makes something a gate here](001-gate-principles.md) | complete | Runs locally, config in the consumer, reasons are mandatory, nothing passes vacuously |
| 002 | [Gate this repository's own dependency graph](002-dependency-footprint.md) | complete | A `depcheck` subcommand, so the argument for this repo's existence stays true |
| 003 | [Gate the temporary directories a test run leaves behind](003-tempdir-leaks.md) | complete | A `tempdir` subcommand: run the suite against an empty TMPDIR, fail on survivors |
| 004 | [Gate the licence notice every source file carries](004-license-headers.md) | complete | A `license` subcommand: every source file carries the SPDX notice the repo declared |
| 005 | [Merge the coverage tiers, and fail the packages no tier measured](005-cover-tiers-and-unmeasured-packages.md) | complete | `cover` takes repeated `-profile`, and a package no profile mentions fails |
| 006 | [Declare the gate set, and fail a repository that is missing one](006-gate-set-contract.md) | partial | A `contract` subcommand: the required gate set is compiled in, absence fails, exemptions carry a reason and a date |
| 007 | [Lint the archive, and fail a terminal spec that never moved into it](007-archive-placement.md) | complete | `spec.archive`: finished specs belong in `.archive/`, and the specs already there are parsed and held to the vocabulary |

## Conventions

Status is one of three values, and the difference is meant to be legible
from the word alone:

- `draft` — nothing of it is in the product
- `partial` — in the product, a named criterion still open
- `complete` — in the product, every criterion holds

Frontmatter carries `title`, `status`, `depends_on`, `affects`, `effort`,
`created`, `updated`, `author` and `dispatched_task_id`. Cross-references
use `[[000-bootstrap]]` wikilinks.

`make spec-lint` checks all of it: that the frontmatter parses, that the
status comes from the vocabulary above, that `depends_on` edges resolve and
form no cycle, that every spec appears in the table with the status it
claims, and that every wikilink points at something. The rules live in
`.lateregate.yaml`.

This tree files nothing under `specs/.archive/`. Every spec here is still the
live record of a gate that runs, so none of the three statuses is terminal and
`spec.archive` is unset. A tree that retires finished specs sets it; see
[[007-archive-placement]] for what the rule then asserts.
