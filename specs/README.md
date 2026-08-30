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
