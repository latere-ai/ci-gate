---
title: Enum gates run from the shared plan locally and in CI
status: complete
depends_on:
  - 016-go-enum-domains.md
  - 017-typescript-enum-domains.md
affects:
  - internal/config/
  - internal/bar/
  - cmd/lateregate/
  - .github/workflows/ci.yml
  - .lateregate.yaml
  - README.md
  - ../ci/.github/workflows/lateregate.yml
  - ../ci/test/lateregate_test.sh
effort: medium
created: 2026-09-12
updated: 2026-09-13
author: changkun
dispatched_task_id: null
---

# Enum gates run from the shared plan locally and in CI

## Decision

Both gates are in `lateregate list`, the whole bar, and individual commands.
They apply when configuration names domains and accept the existing dated
waivers. Strict configuration validation rejects empty/malformed selectors,
duplicate types/projects, fields pointing to undeclared domains and parser
exceptions without reasons. This repository adopts the Go gate for its own
bar status domain. Frontend checks have a separate gate so they can install
their runtime without imposing Node on Go-only repositories.

A preparation command installs configured TypeScript projects from committed
lockfiles, using npm ci or bun install --frozen-lockfile. It is explicit in CI;
the checking command itself never installs dependencies. The reusable workflow
sets up Node and Bun only for the TypeScript gate, prepares its projects and
runs the same command a developer runs. The checker reports how to prepare a
project when its compiler is absent. The ci-gate repository runs the Node
analyzer tests in a separate CI job with a frozen install.

## Acceptance

- Plan applicability, waivers, dispatch, strict configuration and preparation
  have regression tests, including install failures and absent lockfiles.
- Workflow tests prove TypeScript setup/preparation is conditional on its
  gate and precedes checking, and both enum gates survive matrix selection.
- A local CLI run verifies the repository's own status domain; a fixture
  proves the gate returns nonzero for a raw enum value.
- Documentation explains domain selectors, parser exceptions, commands,
  prerequisites and limitations. Changes land as scoped commits on main.

## Outcome

Both enum gates participate in the plan, command dispatch and dated waivers.
Strict configuration validation and `enum-typescript-prepare` are covered
by regression tests; preparation has 98.1% statement coverage. It verifies
all configured projects before installing and deduplicates lockfile owners.

The reusable workflow changes in `latere-ai/ci` set up Node/Bun and prepare
projects only for `enum-typescript`; workflow tests and actionlint pass.
The tooling repository's own workflow runs the Node analyzer suite with
its locked development dependencies. README and changelog document the
configuration, commands and limits. Application repositories were not
migrated, as requested. Publishing a ci-gate release and promoting the
reusable workflow's `v1` tag remain the normal release process, separate
from this source implementation.

Local validation passed lint, vet, race tests, coverage, hermetic tests,
workflow tests, actionlint and the reachable-vulnerability scan. The macOS
`tempdir` check initially reported Apple's 608-byte `xcrun_db` cache on both
this tree and a clean checkout of pre-change commit `3329543`. A follow-up
fix makes `contract` own and clean the temporary directory used by its
Makefile probe; regression tests cover real make execution and cleanup on
failure without adding an allowance to `tempdir`.
