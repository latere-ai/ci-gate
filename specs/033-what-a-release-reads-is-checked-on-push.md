---
title: What a release reads is checked on every push, with the tool pinned once
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - internal/gates/
  - internal/bar/
  - docs/gates.md
  - CHANGELOG.md
effort: medium
trigger: thirteen services each carried their own workflow jobs for go mod tidy, actionlint, kustomize and promtool, each pinning its own version, and one downloaded promtool by hand
created: 2026-11-01
updated: 2026-11-01
author: changkun
dispatched_task_id: null
---

# What a release reads is checked on every push, with the tool pinned once

## The problem

Ten of the thirteen core services ran `go mod tidy -diff` in a job of
their own; four ran actionlint, three of them fetching it with a download
script and one with shellcheck on some runners and not others; several
rendered their kustomizations; and lux, arca and origo each checked their
PrometheusRule with promtool, one through a container image, two through
a hand-pinned release download. Each job was the same check with its own
pin, and none of them ran on a laptop. latere-ai/ci's spec
`core-service-workflows` moves them here.

## The decision

**Four gates, each applying when git tracks its subject.**

- `tidy`: `go mod tidy -diff` in every tracked module outside `testdata/`
  and `vendor/`.
- `actionlint`: actionlint `v1.7.12` over the workflows directly under
  `.github/workflows`, with its shellcheck and pyflakes passes off, since
  they depend on what the machine has installed.
- `manifests`: kustomize `v5.7.1` renders every tracked kustomization that
  is not a Component.
- `rules`: the `spec` of every `kind: PrometheusRule` document in a tracked
  YAML file passes `promtool check rules` at `v3.15.1`.

**Tools are pinned in the binary.** actionlint and kustomize run through
`go run module@version`, so the Go checksum database verifies the build.
promtool's module holds replace directives, which `go run @version`
refuses, so the gate downloads the release archive for the platform,
checks the SHA-256 pinned beside the version, and installs it under the
user's cache directory, under a lock. It never takes a promtool from
`PATH`, so every machine checks with the same parser.

**No configuration.** A repository that cannot meet one yet waives it with
a reason and a date. None narrows on a laptop: each reads the whole tree
in seconds once its tool is built.

## What it cannot see

`manifests` renders; it does not validate the rendered objects against a
cluster's schemas. `rules` checks the rules parse and are well formed, not
that the metrics they name exist; origo keeps that check in its own
tooling. The `frontend` check the ci spec lists is
[[034-the-frontend-a-release-ships-builds-on-push]]. The `image` check is
not here: it needs a container engine the self-hosted runner and a laptop
provide differently.

## Acceptance criteria

- `lateregate list` names each of the four as running where its subject is
  tracked and skipped with the reason where it is not.
- Each gate fails naming what failed, and a gate run by name with no
  subject fails rather than passing.
- A git that cannot answer is an error from `Applies`, not a skip.
- promtool installs once per machine, refuses an archive whose digest
  differs, and is reused on the next run.
- Across the thirteen core services each gate passes wherever it applies.

## Outcome

Implemented as specified. Run against platform, auth, insula, lux, cella,
topos, arca, origo, lectio, pons, sermo, examen and limen on 2026-11-01:
every applicable gate passed. `extractFile` takes its size limit as an
argument, since promtool is larger than elan-init.
