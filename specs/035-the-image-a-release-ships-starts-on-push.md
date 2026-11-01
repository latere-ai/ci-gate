---
title: The image a release ships builds, proves its version, and starts read-only on every push
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
trigger: nine core services each carried a verify.yml image job of the same shape, with the build, version, size, shell and start checks written out in shell
created: 2026-11-01
updated: 2026-11-01
author: changkun
dispatched_task_id: null
---

# The image a release ships starts on every push

## The problem

lux, cella, topos, arca, lectio, pons, sermo, examen and limen each ran an
`image` job in verify.yml: `docker build`, the binary's `version` piped to
`grep`, a size cap read from `docker image inspect`, `/bin/sh` that must
not run, and a `docker run --read-only --cap-drop ALL` probed with `curl`
in a loop. The steps were the same; the shell differed in each, none ran
on a laptop, and a host port fixed at 8080 cannot be held by two runs
on one machine. latere-ai/ci's spec `core-service-workflows` moves the check
here.

## The decision

**An `image` gate over the images a repository names** in
`image.images`. For each: build from `dockerfile` in `context` with
`build_args` under a tag of the gate's own; run `version_args` and check
the output starts with `version_expect`; check the size against
`max_mib`; check `/bin/sh` does not run; then start it with `start_args`
and `env`, read-only with every capability dropped, and ask `probe` on
`port` for a 2xx within `wait_seconds`.

**Every exception is a reason, not a switch.** An image that ships a
shell sets `allow_shell` to why. An image whose start needs a database
sets `skip_start` to why, and its start stays the repository's own job.
`version_args` and `version_expect` are required: an image that cannot
say what it is fails open on the question the check exists for.

**The engine from the machine.** Docker when it is on `PATH`, podman
otherwise, with the same arguments, except that podman's start drops
the writable tmpfs it mounts under `--read-only`, which Docker and a
Kubernetes read-only root do not give. The host port is one the engine
picks on `127.0.0.1`, read back with `port`, so runs do not collide.

**Narrowed on a laptop.** A build takes minutes, so a check scoped to a
change runs the gate only when a non-Markdown file inside an image's
context, or its Dockerfile, changed.

## Acceptance criteria

- `lateregate list` runs `image` when `image.images` names one and skips
  it with the reason otherwise.
- A failed build, a wrong version, an image over its cap, an image with a
  shell, a start that fails and a probe that never answers each fail,
  naming the image; the last prints the container's logs.
- Every named image is checked after one fails, and each image and
  container the gate made is removed.
- `image.images` refuses an unnamed image, a name given twice, a path
  outside the repository, no `version_args` or `version_expect`, a
negative cap or wait, and
  a missing port or probe unless `skip_start` is set.
- pons's `ponsd` image passes under podman with the checks its verify.yml
  job ran.

## Outcome

Implemented as specified. Run on 2026-11-01 against a clone of pons at
main under podman, configured with the version, size, shell and start
checks of its verify.yml image job: it passed, and left no image or
container behind. The Docker path is the same arguments without
`--read-only-tmpfs=false`, held by the unit tests; its first live run is
a service's first CI run with the gate.
