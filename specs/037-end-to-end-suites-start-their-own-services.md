---
title: End-to-end suites start their own services and fail when a test skips
status: draft
depends_on:
  - 036-ci-checks-what-changed-since-green.md
  - 014-one-suite-run.md
affects:
  - internal/bar/
  - internal/config/
  - internal/gates/
  - docs/gates.md
  - docs/configuration.md
  - CHANGELOG.md
  - latere-ai/ci .github/workflows/lateregate.yml
effort: large
trigger: fourteen repositories ran Go tests behind a build tag, against Postgres, MinIO, kubectl or a kind cluster, in jobs of their own, and five of those jobs grepped the log for `--- SKIP` by hand because a test that skips proves nothing
created: 2026-11-05
updated: 2026-11-05
author: changkun
dispatched_task_id: null
---

# End-to-end suites start their own services and fail when a test skips

## The problem

The `suite` gate runs `go test` with no build tags, under a stripped PATH,
with no database. Every test that needs more than that ran somewhere else:

- **Tagged Go tests.** platform's `lux-e2e` (`-tags=integration ./test/luxe2e`)
  and `repose2e` (MinIO), topos's and lux's `-tags postgres` stores, pay's
  and replichai's Postgres contracts, insula's `integration` store, arca's
  `tiers` store over compose, origo's `integration` and `e2e` tiers over MinIO.
- **Tests that need a tool.** lectio's and faber's deploy tests, which skip
  without kubectl.
- **Nested modules.** insula's and origo's `tools/specindex`,
  managed-agents' adapters, sdk's `tools/`. None are covered by the root
  suite.
- **Whole stacks.** cella, lux, arca and origo bring up kind clusters,
  install walks and conformance runs.

Each was a job in the repository's own workflow, with its own service block
and its own pins. lectio, latere-cli, cella, platform and lux each grepped
the log for `--- SKIP`, because a test that skips for want of a database is
green and proves nothing.

## The decision

**`e2e` declares the suites that run against real services.** Each entry
names the suite, says in `why` what it proves, and runs either Go tests or
a command:

```yaml
e2e:
  - name: lux-policy
    why: platform's policy holds against the pinned Lux core
    test: {tags: [integration], packages: [./test/luxe2e]}
  - name: store
    why: the Postgres store keeps the contract the memory store keeps
    test: {tags: [postgres], packages: [./internal/store/postgres/...], race: true}
    services: [postgres]
    env: {DATABASE_URL: postgres.url}
  - name: install
    why: the install document, run block by block, brings up a working cluster
    command: make install-walk
    tools: [kind, helm, kubectl]
    reads: [docs/install.md, deploy]
    timeout: 50m
```

**lateregate starts the services.** It runs `postgres` and `minio` from
images it pins by digest, on ports the engine picks on `127.0.0.1`, with
Docker or podman from `PATH` as the `image` gate does. It waits for each
service to answer, hands its address to the suite through the `env` names
the entry maps, and removes it afterward. A MinIO entry may list
`buckets` to create first. The same run works on a laptop and on any
runner, and the workflow stays the same in every repository.

**lateregate pins the tools.** `tools` takes names from a fixed list
(`kubectl`, `kind`, `helm`). Each is downloaded once at a pinned version,
its checksum is checked, and it is put first on the suite's `PATH`. A
tool that needs the machine changed (bubblewrap and its sysctl, a rootless
podman, a macOS runner) is not on this list. Those come one core at a
time, in a later spec.

**A skipped test fails the suite.** Test suites run with `-json`, and a test
whose action is `skip` fails the suite, naming the test and its skip
message. An entry that expects skips sets `allow_skip` to the reason. A
command suite whose output shows `--- SKIP` fails the same way.

**Suites run on push or on a tag.** `on` defaults to `[push]`. A suite
marked `[tag]` is skipped on push with that reason, and the release
pipeline runs it once releases read their shape from configuration.

**CI's scope covers the suites.** Test suites follow the Go rule of
[[036-ci-checks-what-changed-since-green]]. A command suite runs when a
file under its `reads` changed; with no `reads`, it follows the Go rule.

**`modules` names the Go modules beside the root.** The `suite` gate runs
vet and the race test in each, with the cover floor applied per module.

## Acceptance criteria

- `lateregate list` shows `e2e` running when an entry runs on push, and
  `lateregate e2e -only <name>` runs one suite.
- A Postgres suite passes against the service lateregate starts, under
  podman on a laptop and under Docker on the runner. Every container it
  made is gone afterward, whether the suite passed or failed.
- A test that calls `t.Skip` fails its suite, unless `allow_skip` gives
  the reason.
- `kubectl`, `kind` and `helm` are fetched at their pins and verified, and
  a suite without them on the machine's `PATH` still finds them.
- `modules` runs the suite in each named module and fails on its floor.
- platform's lux-e2e, topos's postgres and insula's specindex run through
  `e2e` and `modules` in CI, with the repos' own jobs removed.
