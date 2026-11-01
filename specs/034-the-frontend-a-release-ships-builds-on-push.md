---
title: The frontend a release ships installs, typechecks, tests and builds on every push
status: complete
depends_on:
  - 001-gate-principles.md
  - 031-a-laptop-checks-the-change.md
affects:
  - internal/gates/
  - internal/bar/
  - internal/config/
  - docs/gates.md
  - docs/configuration.md
  - CHANGELOG.md
effort: small
trigger: auth, platform and insula each carried a workflow job of its own for the same four bun steps, with three different ways of choosing the Bun version
created: 2026-11-01
updated: 2026-11-01
author: changkun
dispatched_task_id: null
---

# The frontend a release ships builds on every push

## The problem

auth, platform and insula each ran `bun install --frozen-lockfile`,
`typecheck`, `test` and `build` in a job of their own: one took the newest
Bun, one any 1.x, one read a version from its Dockerfile. None ran on a
laptop, and a lockfile that drifted from `package.json` was found by
whichever job ran first. latere-ai/ci's spec `core-service-workflows` moves
the check here.

## The decision

**A `frontend` gate over the packages a repository names** in
`frontend.projects`. For each: both `package.json` and `bun.lock` tracked,
a frozen install in the package's directory, then whichever of
`typecheck`, `test` and `build` it defines, in that order.

**Opt-in, not asked of the tree.** Nine repositories track a Bun lockfile;
some hold a package only a release builds, or a tool whose tests drive a
browser (platform's `capture`). Naming the packages is the decision about
which ones the bar holds, the way `enums.typescript` names projects.

**Bun and Node from the machine.** The gate requires both on `PATH` and
pins neither: the shared pipeline sets up the Bun version it already pins
for `enum-typescript`. Without real Node, tools run under Bun's shim, where
a type checker can check nothing and pass.

**Narrowed on a laptop.** An install and a build take minutes, so a check
scoped to a change runs the gate only when a file under a named package
changed, as `lean` does for its projects.

## Acceptance criteria

- `lateregate list` runs `frontend` when `frontend.projects` names a
  package and skips it with the reason otherwise.
- A stale lockfile, an untracked lockfile, a `package.json` that does not
  parse, a failing script, a package with none of the three scripts, and a
  machine without Bun or Node each fail, naming the package.
- Every named package is checked even after one fails.
- `frontend.projects` refuses a path outside the repository and a package
  named twice.
- auth's `frontend`, platform's `frontend` and insula's `web/gallery` pass.

## Outcome

Implemented as specified. Run on 2026-11-01 against worktrees of auth,
platform and insula at origin/main, each with its one
`frontend.projects` line: all three installed, typechecked, tested and
built.
