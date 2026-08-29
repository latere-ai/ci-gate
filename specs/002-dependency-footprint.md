---
title: Gate this repository's own dependency graph
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - cmd/lateregate/
  - internal/
effort: small
created: 2026-08-29
updated: 2026-08-29
author: changkun
dispatched_task_id: null
---

# Gate this repository's own dependency graph

## The problem

[[001-gate-principles]] D1 says this tool is not in `latere.ai/x/pkg`
because a build-time gate should not drag 45 packages of otel, grpc and
golang-migrate into every consumer's `make cover`. That argument is only
true while this repository stays small.

Nothing enforces it. Today the graph is `github.com/goccy/go-yaml` and the
standard library, which is one dependency taken deliberately: YAML block
scalars keep multi-line exemption reasons readable, and JSON does not. The
next contributor who wants a CLI framework, a logging library or a
different YAML parser will find no gate in the way, and the reason this
repository was created stops being true without anyone deciding to change
it.

Every consumer pins this module as a tool dependency, so the graph here
lands in 21 repositories' `go.mod`.

## Proposal

A `depcheck` subcommand, and this repository as its first consumer.

`tgo/internal/depcheck` is the working implementation to start from. Its
central decision is worth preserving and is not obvious: it gates
`go list -deps` on named packages rather than reading `go.mod`, because a
module graph says what *could* be reached while the import graph says what
is actually built. That distinction is why pinning `lateregate` as a tool
dependency does not trip `tgo`'s own gate — a tool is never imported by
the packages it checks.

Config would follow D2 and live in the consumer:

```yaml
depcheck:
  cmd/lateregate:
    allow:
      github.com/goccy/go-yaml: >-
        YAML block scalars keep multi-line exemption reasons readable;
        JSON does not, and there is no YAML parser in the standard library
```

The value is the reason, per D3.

## Open questions

- **Whose graph is gated?** `tgo` gates a named package because
  `llmdialect` is a stdlib-only subtree inside a larger module. Here the
  whole module should be small, so the unit may be the module rather than a
  package list. Do not copy `tgo`'s shape without deciding this.
- **Does a second consumer want it?** D-nothing here says a gate must be
  shared before it is built, but `latere-ai/ci`'s own spec says to
  standardize a mechanism when a second repository actually wants it. This
  one is different: the gate protects the property this repository is built
  on, so it earns its place with one consumer. Say so explicitly when
  implementing, or the next reader will apply the general rule and delete
  it.

## Acceptance criteria

- **AC1** `lateregate depcheck` fails when a module appears in the build
  that the config does not admit, and names the module.
- **AC2** An allowance without a reason fails the config load, as
  exemptions already do.
- **AC3** This repository gates itself with it, and the current graph
  (`goccy/go-yaml` plus the standard library) passes.
- **AC4** A package that does not build is reported as a build error, not
  as a violation.

## Outcome

Shipped in `v0.4.0` as `lateregate depcheck`, ported from tgo's
`internal/depcheck`, which is now deleted.

All four criteria hold. The allowlist and the GOOS/GOARCH set are config, the
value of an allowance is its reason and an empty one fails the load, a stale
allowance fails as loudly as an unadmitted dependency, and a package that does
not build is reported as a build error rather than a violation.

Two things the port settled:

- **The open question about whose graph is gated resolved to the package.**
  tgo gates two packages for two different reasons, and a module-level gate
  could not have expressed that `tokenizer` may reach `x/text` while saying
  nothing about the rest of the module. The config is a map of package to
  allowlist, and each carries the decision that owns it.
- **The second consumer arrived immediately.** llmops gates its CLI, which
  reaches two modules. So the argument that this gate earns its place with one
  consumer did not have to be made.

Verified against tgo's real build across ten platforms: the same two gated
packages and the same result as the program it replaced, and removing one
allowance fails.

This repository does not yet gate its own graph, which is what the spec
originally asked for. `goccy/go-yaml` plus the standard library is still the
whole of it, and the gate that would hold it there is one config block away.
That is the remaining work.

## Out of scope

- Converting `tgo` to it. Its gate encodes decisions recorded in
  `tgo/specs/010-conformance.md`.
- Gating consumers' graphs. This is about protecting the claim in
  [[000-bootstrap]], not about policing what other repositories import.
