---
title: TypeScript enum domains use named types and members
status: complete
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
updated: 2026-09-13
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

## Outcome

Implemented as the embedded `internal/tsenum/analyzer.mjs` and Go wrapper.
The analyzer resolves the consumer's TypeScript compiler and Vue compiler
from the configured project. It retains ambient declarations and original
Vue script positions, and leaves template/prop checking to `vue-tsc`.
External Vue script sources fail with an explicit diagnostic. Native enum
members must have constant values; duplicate-valued members count once.

Sixteen Node tests cover successful and rejected source, aliases, parser
scope, assertions and primitive-cast comparisons, declaration/field errors,
compiler failures and Vue handling. The Node analyzer has 100% line and
function coverage; the Go wrapper has 91.7% statement coverage. A compiled
CLI run verified failing/fixed TypeScript and Vue source using a relative
`-C` in a repository without `go.mod`.
