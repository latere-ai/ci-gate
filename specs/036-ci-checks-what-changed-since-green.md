---
title: CI checks what changed since the last green commit, and one caller fits every repository
status: draft
depends_on:
  - 010-ci-workflow.md
  - 031-a-laptop-checks-the-change.md
  - 034-the-frontend-a-release-ships-builds-on-push.md
affects:
  - cmd/lateregate/
  - internal/scope/
  - internal/bar/
  - internal/config/
  - internal/gates/frontend.go
  - internal/contract/
  - internal/greencut/
  - docs/gates.md
  - docs/configuration.md
  - CHANGELOG.md
  - latere-ai/ci .github/workflows/lateregate.yml
effort: large
trigger: 24 of 31 repositories ran CI jobs of their own and 21 carried a hand-written path filter, each different, while CI checked the whole tree on every push; platform's frontend gate took 627 s on a push that changed only release.yml
created: 2026-11-05
updated: 2026-11-05
author: changkun
dispatched_task_id: null
---

# CI checks what changed since the last green commit, and one caller fits every repository

## The problem

Every push runs the whole bar on CI. [[031-a-laptop-checks-the-change]]
narrows a laptop run to the change, and `main.go` turns that off whenever
`CI` is set, because the default branch was the thing held to the whole
bar. So on platform a push that changed only `.github/workflows/release.yml`
ran the frontend gate for 627 s and the suite for 330 s, and a Go-only fix
ran the frontend gate for 566 s.

Repositories worked around it in their own YAML. 21 callers carry a
`paths` or `paths-ignore` filter, and each lists by hand the Markdown its
tests read: lectio's specs, topos's `prompts/`, pkg's `specs/`, lux's
`docs/`, `skills/` and `SECURITY.md`, auth's `INTEGRATION.md` and `docs/`,
insula's `specs/029` and `docs/operating.md`, faber's and insula's
`CHANGELOG.md`, replichai's `method/`, latere-ai's `content/`. A list that
misses a file skips a test that reads it; a list that names too much runs
for nothing. The shared workflow's inputs (`test_os`,
`frontend_browser_package`, `gate_module`) are per-repository YAML too, and
`contract` checks only that the caller exists, so nothing stopped any of it.

## The decision

**CI measures the change from the last green commit.** `list`, `check` and
every gate run by name take `-since green`, which resolves to the newest
commit on the default branch with a successful push run of the calling
workflow that is an ancestor of HEAD (read through the GitHub API with the
job's token; the compare endpoint answers ancestry). The commit is fetched
at depth 1 if the checkout lacks it. `-since <rev>` names a commit directly.
Whenever the base cannot be had (no token, no green run, an API error, a
fetch that fails) the run checks the whole tree and the plan's first line
says why. A canceled run is never a base: it verified nothing.

Main stays held to the whole bar by induction. Each green run passed every
gate whose inputs changed since a green commit, and `lateregate release`
still runs the whole bar on the tree it tags.

**On CI a gate either runs over the whole tree or is skipped.** Narrowing to
packages stays on the laptop, where [[032-untouched-packages-keep-their-coverage]]
holds the floor. On CI:

- `frontend`, `image` and `lean` skip when none of their inputs changed, by
  the rules they already have on a laptop.
- The gates that read Go (`fmt-check`, `modernize`, `cgo-free`,
  `otel-client`, `lint`, `vuln`, `suite` and what folds into it) skip when
  every changed file is inert to Go, and otherwise run over the whole tree.
- Every other gate runs. Each takes seconds.

A changed file is **inert to Go** when all of these hold:

1. It is not a `.go` file and not one of the whole-tree files (`go.mod`,
   `go.sum`, `go.work`, `.lateregate.yaml`, `.golangci.yml`, `vendor/`),
   which already check everything.
2. It lies under no package directory of any module in the repository.
3. No `//go:embed` pattern matches it.
4. No string literal in the repository's Go files names it: not its path,
   not its base name, and not any directory on its path as a path element.

The fourth rule is what the hand-written filters encoded. A test that
reads `../../specs/003-api.md` has the literal `specs`, so a spec change
runs the suite. A test that walks the whole tree from its root without
naming anything is the case the rule misses, and the release bar catches it.

**A frontend names what it reads outside its directory.** Platform's tests
read `docs/<capability>/api-text.yaml` and the Chinese strings beside them.
`frontend.reads` maps a project to the directories its tests read, and a
change under one of them runs the gate.

**The plan carries what the workflow installs.** Each running entry in
`list -json` gains `needs`: `node` and `bun` for `frontend` and
`enum-typescript`, `chromium:<dir>` for a project `frontend.browser` names.
The `test` entry gains `os` from `test.os`. The shared workflow installs
from these fields and stops reading `test_os` and `frontend_browser_package`
from its caller; it finds the module that pins the tool by its `tool` line,
so `gate_module` goes too. The inputs stay accepted until every caller has
moved.

**The frontend gate runs an `e2e` script.** After `typecheck`, `test` and
`build`, a project that defines `e2e` runs it, with the browser installed
when `frontend.browser` names the project. latere-ai's Playwright run and
replichai's browser tests move here.

**One caller.** `lateregate init` writes `.github/workflows/ci.yml`, the same
bytes in every repository: push to the default branch, pull requests and
dispatch, no path filter, the concurrency block, `contents: read` and
`actions: read`, and one job calling `lateregate.yml@v1` with no inputs.
`contract` accepts it beside today's shapes; a later spec makes it the only
one.

## What follows

Each is its own spec, and lands only after the one before it is live:

- Declared suites: Go tests behind a build tag that need Postgres or MinIO,
  which lateregate starts itself, and which fail when a test skips.
- Pinned tools and nested modules: kubectl, kind, and the modules beside
  the root one.
- The long tail one core at a time: kind stacks, device sandboxes, install
  walks, fuzz and mutation runs, macOS jobs.
- Releases read their shape from `.lateregate.yaml`.
- `contract` fails on any workflow file that is not the caller `init`
  writes.

## Acceptance criteria

- `lateregate list -json -since <rev>` on CI skips `suite`, `lint` and
  `vuln` for a change of only inert files, runs them over the whole tree
  otherwise, and names the base in the plan.
- `-since green` with no token, no green run or an unreachable base checks
  the whole tree and says why; a canceled run is never chosen.
- For each of the 21 path filters in use on 2026-11-05, every path the
  filter re-includes is judged read by Go or named by `frontend.reads`,
  and a test fixture holds each shape: a test naming a spec by relative
  path, a directory joined with `filepath.Join`, an embedded directory,
  a Markdown file inside a package directory.
- On platform, a push that changes only `specs/` runs no `frontend`,
  `image`, `suite`, `lint` or `vuln`; a Go-only push runs no `frontend` or
  `image`; a change under `docs/` runs `frontend`.
- `frontend` runs a defined `e2e` script after `build`, and the plan names
  `chromium:<dir>` for a project `frontend.browser` lists.
- `lateregate init` writes the one caller, and `contract` passes on it.
