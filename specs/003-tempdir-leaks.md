---
title: Gate the temporary directories a test run leaves behind
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - cmd/lateregate/
  - internal/config/
  - internal/gates/
effort: small
created: 2026-08-30
updated: 2026-08-30
author: changkun
dispatched_task_id: null
---

# Gate the temporary directories a test run leaves behind

## The problem

A test that makes a directory under `TMPDIR` and does not remove it has
leaked it for the life of the machine. Nothing goes red. The suite passes,
the coverage gate passes, the hermetic gate passes, and the only symptom is
a disk that fills months later.

This is not a hypothetical. One Go repository was measured on 2026-08-30
with 8.2GB free on a 926GB volume, of which 168GB was leaked test
directories in a single user's temporary folder:

| Site | Directories | Size | Why |
|---|---|---|---|
| `internal/gotest` | 258 | 160GB | shared nanogo install and `GOCACHE`, no `TestMain` |
| `internal/audit` | 145 | 7.1GB | same shape, no `TestMain` |
| `ssagen` | 432 | 776MB | `TestMain` present, but neither directory was reachable from it |

Each site had made the same reasonable decision. A tool built once for the
whole package cannot live in a `t.TempDir`, because the testing package
removes that when the *first* test that asked for it returns and every later
test finds the tools gone. So they used `os.MkdirTemp`, which nothing removes
on your behalf, and the removal has to be written by hand in a `TestMain`.

Two of the three carried a comment saying the directory was "removed by the
process that made it". It never was. A comment is not a gate.

macOS does not clear `/var/folders` on reboot, so the space is never
reclaimed by anything short of a manual `rm -rf`.

## Why a gate and not a linter

A source scan cannot see this. Consider the third site:

```
go build -work        →  the go command prints WORK=/var/folders/…/go-build123
                         and, because -work was passed, does not remove it
```

The directory is created by a subprocess, in another program, and named only
on that program's stdout. No amount of reading the caller's Go source finds
it. The same is true of a suite that shells out to a container runtime, a
package manager or a second compiler.

The leak is a property of the process tree, so the check has to observe the
process tree:

```
  mkdir sandbox                    empty, and nobody else's
  stat  sandbox            → t0
  run   <suite>  with TMPDIR=TMP=TEMP=sandbox
  stat  sandbox            → t1
  read  sandbox            → entries

  entries ≠ ∅  ∧  ¬allowed   →  fail: these leaked, with sizes
  entries = ∅  ∧  t1 = t0    →  fail: the run never used the sandbox
  otherwise                  →  pass
```

That formulation also makes the gate language-agnostic. `tempdir.command`
names whatever runs the suite, so a Python or Rust repository gets the same
check with the same one-line config. This is a standard for every repository,
not a fix for the one that was measured.

## D1 — the empty-sandbox case must fail, not pass

[[001-gate-principles]] P4 says nothing passes vacuously, and an empty
sandbox is the exact shape that would: a suite launched through a wrapper
that resets the environment, or a runner with a temporary directory of its
own, lands somewhere else entirely and leaves the sandbox pristine. Read
naively, that is a perfect score over a measurement that never happened.

The directory's own mtime separates the two. It moves when an entry is
created *or* removed inside it, so a suite that cleaned up perfectly is
still distinguishable from one that was never there. `go test` writes its
build work directory under `TMPDIR` on every run, so the signal is reliable
for the default command.

## D2 — allowances carry a reason, as everywhere else

`tempdir.allow` is `map[prefix]reason`, matching `cover.exempt` and
`depcheck.allow`, and `config.Load` rejects an empty reason. Matching is by
prefix because a temporary name carries a random suffix.

The bar for an entry is deliberately higher than for the other gates. A
package that cannot reach the coverage floor is a fact about the code; a
directory that outlives the test run is a directory nobody will ever delete.
The honest entry is a run that keeps its work on purpose, such as a
`go build -work` under test.

## D3 — the leak is reported before the suite's own failure

When the suite fails *and* leaks, the gate reports the leak. A red suite gets
re-run and fixed; a leak that only surfaces on a green run is a leak nobody
ever sees.

## D4 — the gate removes its own sandbox

On every path, including the one where the suite failed. A gate that leaked
while checking for leaks would be worse than no gate.

## What shipped

- `lateregate tempdir`, with the command overridable after `--`.
- `tempdir.command` and `tempdir.allow` in `.lateregate.yaml`, the second
  validated for empty reasons.
- `make test-tempdir` in this repository, gating itself.
- Sizes in the report, because 432 directories says less than 160GB does.

## Acceptance

- [x] A suite that removes its own directories passes.
- [x] A surviving entry fails the gate and is named with its size.
- [x] An allowed prefix passes and the reason is printed.
- [x] A run that never wrote to the sandbox fails rather than passes.
- [x] A leak outranks a failing suite in the verdict.
- [x] The sandbox does not outlive the gate.
- [x] `TMPDIR`, `TMP` and `TEMP` all point at the sandbox, replacing any
      value the caller had.
- [x] Verified against the repository in the table above: the gate fails on
      the commit before the leaks were fixed and passes on the commit after.
