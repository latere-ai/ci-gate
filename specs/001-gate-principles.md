---
title: What makes something a gate here
status: complete
depends_on:
  - 000-bootstrap.md
affects:
  - internal/config/
  - cmd/lateregate/
effort: small
created: 2026-08-29
updated: 2026-08-29
author: changkun
dispatched_task_id: null
---

# What makes something a gate here

The four decisions every check in this repository is built on. A new
subcommand that breaks one of them is a bug in the subcommand, not a
special case.

## D1 — A gate runs on a laptop or it does not exist

Every gate here exists because something passed in one place and meant
nothing in the other. A gate reachable only from CI reports the problem
after the push, which is the point at which it is most expensive to fix.

So the gates are a Go binary pinned through the consumer's `go.mod`:

```
go get -tool latere.ai/x/ci-gate/cmd/lateregate
go tool lateregate cover
```

This is the reason the tool is not in `latere-ai/ci`. That repository is
not a Go module, so a gate living there would need a second checkout, and
in practice nobody checks out a CI repo to run a test.

It is also the reason the tool is not in `latere.ai/x/pkg`. That module is
45 packages with otel, grpc, golang-migrate and goldmark in its graph, and
16 repositories depend on it for runtime code. A build-time gate should not
drag that in, and a fix to a coverage tool should not force a version bump
on 16 repositories.

**Test of the decision:** every gate passes in a fresh clone with
`GOPROXY=off`, from the module cache alone.

## D2 — Repository-specific data lives in the repository

The two implementations this repository was extracted from both held their
thresholds and exemptions in Go:

```go
const threshold = 90.0
var exempt = map[string]string{ /* package suffixes for THIS repo */ }
```

That is why there were two of them. A shared binary cannot compile in one
repository's package names, so the data moves to `.lateregate.yaml` in the
consumer and the logic stays here.

One file per repository, not one per check: a repository has a single
quality bar, and splitting it across files makes the bar hard to read.

## D3 — A decision to weaken a gate carries its reason

An exemption is a decision. In the original Go maps the reason was the map
value, so an entry could not exist without one. YAML cannot enforce that
with a type, so `lateregate` enforces it as a rule and **fails** on an
empty reason rather than warning.

The same principle covers `modernize.disable`: turning a fixer off is a
decision, and `lateregate` verifies the fixer still exists before passing
the flag, because `go fix` rejects an unknown `-name=false` and the check
would then pass silently. See [[000-bootstrap]] for what shipped.

## D4 — A gate that measures nothing fails

A gate that passes because it measured nothing keeps reporting green as the
tree fills up, until somebody notices the number never moved. Every shape
that produces it is a failure here, not a pass:

- a coverage profile with no records
- a coverage profile where every package is exempt
- a spec directory with no specs

This is the decision that removed this repository's own `spec-lint` make
target for a while: the target existed, `spec.dir` was empty, and the job
reported green over nothing. The tree you are reading is the fix.
