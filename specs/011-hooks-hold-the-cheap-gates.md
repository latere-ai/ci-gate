---
title: The hooks hold every cheap gate, and pre-push lints what the push changes
status: complete
depends_on:
  - 009-contract-reports-drift.md
affects:
  - cmd/lateregate/
  - internal/gates/
  - internal/license/
  - internal/golangci/
  - internal/contract/
  - README.md
effort: medium
created: 2026-09-06
updated: 2026-09-06
author: changkun
dispatched_task_id: null
---

# The hooks hold every cheap gate

## The problem

On 2026-09-06 one pkg release and its thirteen consumer bumps were
committed through `lateregate hook` in every repository, and five of the
fourteen CI runs failed. None of the failures was in a gate the hook runs:

| Repository | Gate that failed in CI | What it found |
|---|---|---|
| pkg | `lint` | goimports: the module's own imports not grouped under the local prefix |
| lux | `lint` | staticcheck ST1019: one package imported twice |
| auth | `lint` | ineffassign in a new test |
| latere-cli | `license`, `otel-client` | four files without the notice; `http.DefaultClient` |
| auth, latere-cli | `vuln`, `tempdir` | reachable vulnerability in testdb; a test leaving TMPDIR entries |

The hook runs gofmt and the modernizers over the staged files. [[009-contract-reports-drift]]
kept golangci-lint out of it on purpose: the linter takes a global lock, and
a hook that serialises every commit on a machine is one people bypass. That
reasoning is right for the linter and wrong for `license` and `otel-client`,
which are file scans that finish in well under a second, and it left the
goimports grouping rule, which is a file-local formatting rule like gofmt,
to a linter that runs only in CI.

The cost of the gap is one CI round trip per failure, plus a fix commit on
main that carries nothing but formatting. Three of the five failures above
were that.

## The decision

Two hooks, each holding the gates that fit its cadence.

**`lateregate hook` (pre-commit) runs every gate that is a file scan**, over
the staged Go files, in this order: gofmt, goimports grouping with the
module path as the local prefix, the licence notice, the outbound-HTTP
instrumentation rule, then the modernizers over the packages holding the
files. Each of the first four is a read of the staged files with no
type-check and no network, so the hook stays a few seconds. A file under
`testdata` is skipped by every check, as it is today.

**`lateregate prepush` (pre-push) runs golangci-lint over the packages the
push changes.** It reads the ref lines git hands a pre-push hook on stdin,
and for each branch ref diffs the pushed commit against the remote's
commit, or against the merge base with `origin/main` when the remote has
no such ref yet. The Go files in that diff name the packages, and the
linter runs on exactly those, against the shared config rendered first as
the full gate does. Tag refs are ignored: a tag points at a commit that
was already pushed on a branch. A push that changes no Go file runs nothing.

A push is rarer than a commit and already waits on the network. The run
passes `--allow-parallel-runners`: golangci-lint's machine-wide lock exists
so two full runs do not fight over one cache, and the first end-to-end run
of this hook was aborted by a lint in another checkout. A subset run in a
session that is already waiting opts out of the lock; the full gate keeps
it. The full `lint` gate in CI is
unchanged and remains the authority; the hook is the same linter on a
subset, so a package it passes is a package CI passes.

**`vuln`, `tempdir`, `race`, `cover`, `hermetic` stay out of both hooks.**
`vuln` needs the network and its verdict changes with no commit. The others
run the suite, which is minutes, and a hook that runs the suite is the one
[[009-contract-reports-drift]] argued against. The Windows-only failure in
wallfacer's matrix is not reproducible on a developer's machine at all.

**`contract` and `init` learn the second hook.** `contract` wants
`.githooks/pre-push` executable and delegating to `lateregate prepush`, by
the same test `IsSharedHook` applies to the pre-commit. `init` writes it
when missing. A repository with its own pre-push logic, as pkg has for the
changelog-before-tag rule, keeps it: the check is that one non-comment line
calls the binary, not that the file is byte-identical. Because both the
shared hook and a repository's own logic read the ref lines from stdin, the
shared script captures stdin once and feeds it to each consumer:

```sh
#!/bin/sh
# pre-push: golangci-lint over the packages this push changes. The check
# lives in lateregate; this file only calls it. Refs arrive on stdin, once,
# so a repository that adds its own check below reads $refs, not stdin.
refs=$(cat)
printf '%s\n' "$refs" | go tool lateregate prepush || exit 1
```

## How the goimports rule runs

The grouping rule is goimports with `-local <module path>`, which is what
the rendered golangci config already declares. The hook runs the same tool
the same way the linter is run, through the toolchain at a pinned version
(`go run golang.org/x/tools/cmd/goimports@<pin> -local <module> -l <files>`),
rather than taking `golang.org/x/tools` as a dependency of this module.
[[002-dependency-footprint]] gates that graph, and one binary invocation
costs nothing after the first build. The pin lives beside the golangci pin
and moves with it.

## The per-file entry points

`license.Run` and `gates.OtelClient` walk a root. Each gains a function that
takes a list of relative paths and returns the same findings for just those,
and `Run` becomes the walk plus that function, so the hook and the gate
cannot disagree on what a violation is. The vacuous-pass rule (a scan that
read nothing fails) applies to the gates and not to the hook: a commit with
no Go file staged is a pass, as it is today.

## Acceptance

1. `hook` with a staged Go file whose imports put the module's own package
   in the third-party group fails and names the file; after `goimports
   -local` the same file passes.
2. `hook` with a staged Go file missing the licence notice fails with the
   same message `license` prints; with the notice it passes.
3. `hook` with a staged non-test file building `http.Client{}` without a
   transport fails; the same content in a `_test.go` file passes.
4. `hook` on a commit that stages only files under `testdata` passes and
   says so.
5. `prepush` fed a branch ref whose diff touches two packages runs the
   linter on exactly those two patterns, with the config rendered first;
   fed a tag ref it runs nothing; fed a ref with a zero remote sha it diffs
   against the merge base with `origin/main`.
6. `prepush` fed a branch ref whose diff has no Go file runs nothing and
   says so.
7. `contract` on a repository without `.githooks/pre-push` fails and names
   `init`; `init` writes the shared script; a pre-push that carries extra
   lines and calls `lateregate prepush` passes.
8. The pkg repository's pre-push keeps its changelog check and passes
   `contract`.
9. Every gate keeps its behaviour: `license` and `otel-client` on a tree
   report what they reported before the refactor.
10. Each package in this repository stays at or above the coverage floor.

## Rollout

Tag this repository. In each of the fourteen repositories: bump the tool
pin, run `lateregate init`, commit the new pre-push. That is one commit per
repository and `contract` in CI holds the shape from then on.

## Outcome

Shipped as v0.28.0 on 2026-09-06 and rolled to all fourteen repositories
the same hour; `contract` in CI holds the shape from here.

- The first end-to-end pre-push on this repository was aborted by a
  golangci-lint running in another checkout. The run now passes
  `--allow-parallel-runners`; the full gate does not.
- The first real pre-push on pkg refused the push: eight sloglint findings
  in an `egress` package another session had committed locally and not yet
  pushed. That is the case the spec was written for, one push early.
- One repository's pre-push ran an old binary because a concurrent session
  had reverted the go.mod bump before the commit; the pin and the hook were
  re-applied on a clean worktree of origin/main and pushed from there. The
  same route was used wherever another session had unpushed commits, so
  the rollout pushed nothing that was not its own.
- Acceptance 8: pkg's pre-push keeps the changelog-before-tag check below
  the delegation and reads the captured `$refs`; `contract` passes on it.
- The hook takes about four seconds on this repository once goimports is
  built; the goimports pin is v0.49.0.
