---
title: TypeScript enum domains use named types and members
status: draft
depends_on:
  - 001-gate-principles.md
affects:
  - internal/tsenum/
  - package.json
  - package-lock.json
  - internal/config/
  - internal/bar/
effort: medium
created: 2026-09-12
updated: 2026-09-12
author: changkun
dispatched_task_id: null
---

# TypeScript enum domains use named types and members

## Decision

The `enum-typescript` gate checks projects listed under `enums.typescript`.
Each entry has `project` (a repository-relative tsconfig), `types` (enum
declarations named `source/file.ts#Type`, relative to the tsconfig directory),
`fields` (a map of `source/file.ts#Interface.field` to a configured enum),
and `parsers` (named functions mapped to reasons for a reviewed conversion
boundary). Native string and numeric enums are the initial representation.

The Go binary embeds a JavaScript analyzer and executes it with Node and the
project's installed TypeScript compiler. This avoids a second frontend lint
configuration and uses the compiler version pinned by the consumer lockfile.
The checker resolves symbols and inferred/contextual types; it rejects raw
values in enum assignments, comparisons, arguments, returns, composite
objects/arrays and assertions, and requires every switch member even with a
default. Domain fields cannot widen to `string`, `number`, or a union with
those primitives. Parser exceptions only permit primitive conversion; they
do not waive exhaustiveness or domain declarations.

Source imports, aliases, and Vue script blocks must retain enum identity.
Tests and declaration files are excluded as implementation surfaces. Missing
projects, dependencies, symbols, empty domains and compiler errors fail with
an actionable diagnostic. Input schemas remain responsible for runtime data.

## Acceptance

- Node fixtures cover string/numeric enums, imports/aliases, all literal use
  sites, missing cases, widening, parser boundaries and configuration errors.
- CLI fixtures exercise the actual embedded analyzer with failing and fixed
  projects, including a Vue script importing a domain enum.
- Coverage exceeds 90% for analyzer statements/lines; Go wrapper coverage
  exceeds 90%. Test dependencies are pinned with a frozen install.
