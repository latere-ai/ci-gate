---
title: Gate the containers a test run leaves behind
status: complete
depends_on:
  - 001-gate-principles.md
  - 003-tempdir-leaks.md
  - 014-one-suite-run.md
affects:
  - internal/containers/ (new)
  - reap/ (new)
  - internal/gates/
  - internal/bar/
  - README.md
  - docs/gates.md
  - CHANGELOG.md
effort: medium
created: 2026-10-13
updated: 2026-10-13
author: changkun
dispatched_task_id: null
---

# Gate the containers a test run leaves behind

## The problem

A Go test helper that needs a database starts one container per test
binary and stops it from `TestMain`, after `m.Run` returns. Three paths
never reach that line:

- **A failed start.** testcontainers returns the container together with
  the error when the container started and then failed its wait. A helper
  that keeps the container only on success has no handle left to stop it.
- **No `TestMain`.** A package that calls the helper from a test and has
  no `TestMain` starts the container and never stops it.
- **A binary that never returns from `m.Run`.** A panic on `-timeout`, a
  kill, an interrupt. Deferred code and `TestMain`'s tail do not run.

testcontainers covers the last path with Ryuk, a reaper container that
removes a session's containers once the session's connection drops. Ryuk
cannot start on Podman: it asks to join the network named `bridge`, and
Podman reserves that name and refuses to create a network called it. The
helpers turn Ryuk off on Podman so that their tests run at all, and from
then on nothing removes anything.

Measured on 2026-10-13 on one workstation that runs the family's suites on
a Podman machine with 3.6GB of memory: 260 Postgres containers left by 154
test sessions in about 30 hours, 129 of them running at about 26MB each
and 131 created and never started. The machine had 80MB of memory left,
the kernel's page reclaimer held a core, and the hypervisor process sat at
330% CPU on the host. Every suite's Postgres then missed its 60-second
start, the helpers dropped the failed container, and each failure leaked
another. The same workstation had counted 695 a week earlier.

## Two halves of one contract

The gate cannot do this alone. A run that was killed killed its gate too,
so whatever it left is found only by something that runs later. And the
gate cannot tell one run's containers from another's on a machine where
several suites share the engine, unless each container says whose it is.
So the helper labels and sweeps, and the gate names the run and checks it.

```
test helper, in the test binary            lateregate, around go test
───────────────────────────────            ──────────────────────────
                                           sweep: remove what exited owners left
                                           LATEREGATE_RUN=<id>  go test ./...
reap.Sweep()                     ◄────────
start the container, labelled:
  ai.latere.ci-gate.owner = <pid>@<host>
  ai.latere.ci-gate.run   = <id>
a failed start: Terminate
TestMain: Terminate
                                           every container labelled run=<id>:
                                           remove it and fail the gate
```

| How a test binary ends | What removes its container |
|---|---|
| `m.Run` returns | the helper's `TestMain` |
| the container failed its start | the helper, from the container the error came with |
| the package has no `TestMain` | the gate, after `go test` exits; the gate fails |
| `-timeout` panic, kill or interrupt, under `lateregate` | the gate, after `go test` exits; the gate fails |
| the same, under a bare `go test`, or `lateregate` itself killed | the next sweep on the machine: the next helper start or the next gate run, in any repository |

The helper half is the package `latere.ai/x/ci-gate/reap`, two functions:

```go
_, _ = reap.Sweep(ctx)
ctr, err := postgres.Run(ctx, image, testcontainers.WithLabels(reap.Labels()))
if err != nil && ctr != nil {
	_ = ctr.Terminate(ctx)
}
```

Every repository that runs the gate already requires this module, so the
helper half adds no module to anyone's graph.

## D1 — a container is judged by its owner, never by its age

A sweep removes a container only when its owner label names this host and
a process that no longer exists. Age is no evidence: a suite on this
machine may be minutes into a container, and a long one hours. A pid means
nothing on another machine, or in another container's pid namespace,
which is why the host travels with it and a foreign host is left alone.

A pid can be reused. A reused pid makes a dead owner look alive, and the
sweep then keeps a container it could have removed; it never makes a live
owner look dead. The failure is a later sweep, not a lost database.

## D2 — the run label is read past the test cache

The go command keys a package's cached result on each environment
variable the test reads through package `os`. The run label changes on
every run, so a helper reading it with `os.Getenv` would make every
package that starts a container run again on every push. `reap.Labels`
reads it with `syscall.Getenv`, which the cache does not observe. The
label decides nothing about a test's outcome, so replaying a result
without it is correct, and a replayed package starts no container to
label.

## D3 — the standard library, not a container client

The gate and the helper half speak the Docker Engine API over a unix
socket, which Docker and Podman both serve: one listing filtered by label
and one forced removal. A container client would put its dependency graph
into the binary every repository pins, for two requests, against
[[001-gate-principles]] D1's test that every gate builds from the module
cache alone.

The socket is found the way the clients find it: `DOCKER_HOST` when set
and only it, then `/var/run/docker.sock`, Docker Desktop's per-user
socket, and rootless Podman's. A `DOCKER_HOST` over TCP names an engine
this package does not reach, so it finds nothing rather than falling back
to a default socket that may be another engine.

## D4 — no engine is not a failure, and is not reported as a pass

With no engine socket, nothing the suite runs can have started a
container, so there is nothing to leak. The property does not apply, the
way a gate with no subject does not, and the output says so. The verdict
line names "no container left behind" only when a run was watched, so
[[001-gate-principles]] D4 holds: nothing claims a check that did not run.

An engine that does not answer before the run is the same case: it started
nothing either. One that answered before and does not answer after fails
the gate, because the check the run owed did not happen.

## D5 — the leak is reported beside the suite's failure, and removed

As in [[003-tempdir-leaks]]: a red suite gets re-run, and a leak shown
only on a green one is never seen. Whatever carries the run's label is
removed on every path, including the one where the suite failed. What the
opening sweep removes fails nothing: it is another run's leak, reclaimed.

## Folded into the suite

`containers` is a gate of its own, `lateregate containers`, and folds into
`suite` like the five before it ([[014-one-suite-run]]): the one run of
the suite carries the label, and a waiver on `containers` turns the watch
off rather than skipping the suite.

## Networks

A helper that puts its containers on a network of its own makes the network
with the same labels, `network.WithLabels(reap.Labels())`, and the sweep
and the run's check treat it as they treat a container. Containers go
first, so a network is free by its turn; one still in use after them holds
a container nothing labelled, and its removal fails with the engine's
reason. Added after the first release, once a helper that makes networks
adopted the contract and the networks of killed runs were found on the
engine with nothing attached.

## Limits

- A container or network made without the labels is invisible to both
  halves. A repository's helper adopts the contract; the gate cannot see
  one that has not.
- A container started over a `DOCKER_HOST` on TCP is not watched.
- A host whose name changed between runs leaves containers named for the
  old one alone, by D1.

## Acceptance

- [x] `reap.Labels` names the owner as pid@host and, under a run, the run.
- [x] `reap.Sweep` removes a labelled container whose owner exited on this
      host, and leaves one whose owner is alive or ran on another host.
- [x] The suite names its run in `LATEREGATE_RUN`, and a container still
      carrying that label after `go test` exits is named, removed, and
      fails the suite.
- [x] Before the run, the suite sweeps what exited owners left, says what
      it removed, and does not fail for it.
- [x] Without an engine socket the suite runs, says nothing was watched,
      and claims nothing.
- [x] A waiver on `containers` narrows the suite and shows on its plan
      line.
- [x] A package that starts a container keeps replaying from the test
      cache while the run label changes.
- [x] A labelled network is swept after the containers when its owner has
      exited, and a network the run left is named, removed, and fails the
      suite beside its containers.
- [x] Verified on a consumer: a package with no `TestMain` fails the
      gate, and passes once it stops its container. On 2026-10-13, against
      a Podman machine: a one-package module whose test starts Postgres
      and never stops it failed `lateregate containers` with "the suite
      left 1 container behind", and passed once a `TestMain` stopped it;
      a repository's package with no `TestMain` left one labelled
      container, and the next start's sweep removed it after its owner
      exited.
