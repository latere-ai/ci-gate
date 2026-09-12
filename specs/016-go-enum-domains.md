---
title: Go enum domains use named types and members
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - internal/enumcheck/
  - internal/config/
  - internal/bar/
  - go.mod
effort: medium
created: 2026-09-12
updated: 2026-09-13
author: changkun
dispatched_task_id: null
---

# Go enum domains use named types and members

## Decision

The `enum-go` gate checks explicitly declared domains in `enums.go`.
`types` names Go types as module-relative or full import path plus `.Type`.
Each must resolve to a defined string or integer type with named constants.
`fields` maps `package.Struct.Field` to one of those types and prevents a
domain field from becoming a primitive. IDs, counters and open strings are
outside the policy unless the repository explicitly names them.

The analyzer uses Go type information, including imported members and aliases,
over the module's production packages under the current build configuration.
Enum declarations may contain their underlying literals. Assignments, calls,
returns, comparisons, composite literals and switch cases must use named enum
members or values of the declared enum type. Casts from primitives are refused
outside explicitly configured parser functions. `parsers` maps qualified
function names to reasons; it permits conversion at a reviewed input boundary,
not missing cases in switches. Runtime validation remains the parser's job.

Every switch over a configured enum lists all distinct constant values;
`default` does not stand in for a missing member. Unknown types, fields or
parser names, domains with no constants, and package loading errors fail.
Test files are outside the production rule so invalid inputs remain testable.

## Acceptance

- Real package fixtures pass with named string/integer members and fail for
  raw literals at every supported use site, conversion escapes, incorrect
  field types and missing switch cases, including a switch with `default`.
- Imported enum members, aliased imports, duplicate-valued constants, named
  enum variables, zero-valued members and ordinary primitive values work.
- Configuration mistakes fail instead of measuring nothing.
- Package coverage exceeds 90%; an end-to-end gate fixture exercises loading
  and a failing/passing source edit. No fixture needs the network.

## Outcome

Implemented in `internal/enumcheck` and wired into the shared plan as
`enum-go`. The repository checks `internal/bar.Status` and `Entry.Status`.
The analyzer uses `go/packages` with workspace resolution disabled and
read-only module selection. Typed constant conversions are accepted at
member declarations; the same conversions at use sites require a parser.

Real fixture packages cover string/integer domains, imports and aliases,
containers, calls/returns, conversions, parser scope, missing switch cases,
configuration mistakes, and a failing source edit followed by its fix.
The Go command fixture exercises the public gate command. The package has
98.0% statement coverage; the full suite passes under the race detector.
