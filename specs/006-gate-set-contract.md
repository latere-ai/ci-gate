---
title: Declare the gate set, and fail a repository that is missing one
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - cmd/lateregate/
  - internal/config/
  - internal/contract/
effort: medium
created: 2026-08-31
updated: 2026-09-01
author: changkun
dispatched_task_id: null
---

# Declare the gate set, and fail a repository that is missing one

## The problem

[[001-gate-principles]] D4 says a gate that measures nothing fails. Every
gate in this repository holds that rule about its own input. None of them
holds it about the gate set.

The shared pipeline probes a consumer's Makefile and runs whichever
optional targets it finds:

```sh
emit cover cover        # cover=false, "skipping cover (no such target)"
```

A repository with no `cover` target is therefore not gated on coverage, and
the pipeline reports success. The absence is printed once into a step log
nobody reads and never appears as a failure. This is D4 one level up: the
pipeline measured nothing and called it green.

The result, measured across the 18 migrated repositories:

| Gate | Repositories holding it |
|---|---|
| `test-hermetic`, `lint`, `lint-config` | 18 of 18 |
| `spec-lint` | 16 of 18 |
| `test-race` | 11 of 18 |
| `cover` (any gate) | 7 of 18 |
| `cover` (this repository's per-package gate) | 5 of 18 |
| `license` | 1 of 18 |

Nine repositories have no coverage gate at all. Four more hand-rolled a
repository-average gate before the migration and kept it, one of which
exits 0 on an empty profile -- the precise shape D4 exists to refuse.

Nothing here was a decision. Each repository kept whatever it had, and the
probe made the difference invisible.

## The decision

The required set is a property of the organisation, so it is compiled into
this repository. A consumer cannot lower it. A consumer can only record
that it does not hold a gate **yet**, with a reason and a date:

```yaml
contract:
  exempt:
    cover:
      reason: the suite needs Postgres and MinIO, so the floor lands with dr-31
      until: 2026-11-01
```

Three properties, each of which is the reason for the next:

- **Reason mandatory.** D3. An exemption is a decision and a decision
  carries its reason. An empty reason fails, as in `cover.exempt` and
  `depcheck.allow`.
- **Date mandatory.** A reason alone becomes wallpaper. Seventeen permanent
  exemptions with good reasons is not a bar. The date is what makes "retire
  them one at a time" enforceable rather than aspirational, and it is the
  shape `service-template`'s generator already uses for waivers.
- **Expiry fails, and does not warn.** A warning on an expired waiver is a
  reason to write the next one.

## The subcommand

`lateregate contract` reads `.lateregate.yaml`, probes the Makefile, and
fails on any required gate that is neither present nor exempt. The name is
the vocabulary the pipeline already uses: "See ci/README.md for the
go-verify contract."

It runs from the repository root with no target of its own, because a
repository missing its targets still has to be checkable:

```
go tool lateregate contract
```

D1 holds: it is the same binary, pinned through the consumer's `go.mod`,
and it answers the same on a laptop as in CI.

### Probing the Makefile

The probe reads make's rule database and matches a target at the start of
a line:

```
make -np  →  ^cover:
```

Not `make -n cover`. That form succeeds when a **file** of the target's
name exists, so on a case-insensitive filesystem a `LICENSE` file answers
for a `license` target and a `dist/` directory answers for `dist`. Both
were live in this organisation.

The database is read to completion before matching. Terminating that pipe
early kills make with SIGPIPE, which exits 141 and reads as "no target".

## One authority

The pipeline's `probe` job currently holds its own required list and exits
1 on a missing target. That list is deleted. Two lists in two repositories
drift, which is the failure this spec exists to fix.

The roles split:

- `lateregate contract` is the **only** pass/fail authority on the gate set.
- The workflow's `emit` outputs stay. They decide which jobs to skip, and
  only the workflow can do that.

A repository that is missing a target and has no exemption fails at the
`contract` step, before any job is skipped for it.

## The required set

As of today. A gate joins the set when a repository can hold it now, or
hold it behind an exemption someone intends to retire.

| Required | Cost, measured with `lateregate contract` |
|---|---|
| `fmt-check`, `test`, `lint-modernize` | already required |
| `test-hermetic`, `lint`, `lint-config` | free, 18 of 18 hold it |
| `spec-lint` | 1 (managed-agents); `pkg` is not subject to it |
| `test-race` | 7 (auth, lectio, platform, latere-ai, replichai, sandbox, lux) |
| `cover` | 8 (agents, drive, eval, lectio, platform, service-template, latere-cli, and the same seven overlap) |

Four repositories -- topos, pay, llmops, tgo -- already hold the whole set.
Twenty-two adoptions across the other fourteen, not the fifty-odd a gate
set drawn up without measuring would have asked for.

### A gate whose subject can be absent

An exemption says a repository is behind and names the day it stops being
allowed to be. That is the wrong shape for a gate with nothing to check.

`pkg` is public and carries client primitives only; its planning documents
live in a private repository, and a `no-tracked-specs` target fails if a
spec ever appears. Requiring `spec-lint` there would mean a dated
exemption renewed forever for a decision nobody intends to change, and the
day somebody forgot, the gate would fail permanently for a repository that
was never wrong.

So `spec-lint` carries a predicate: it applies to a repository that tracks
files under `specs/`. The subject either exists or it does not, and the
gate asks rather than being told. A repository that adds its first spec
starts being gated the same day, with nobody to remember it.

The predicate reads **git**, not `.lateregate.yaml`. Deriving it from
whether a `spec:` section is configured would let any repository escape the
gate by deleting six lines of YAML -- and `managed-agents` tracks two
specs with no `spec:` section at all, which is precisely the gap this gate
exists to report. Config-derived applicability would have turned a detected
gap into an undetectable one.

This stays narrow. `spec-lint` is the only gate here whose subject can be
absent; `cover`, `test-race` and `lint` all have subjects that always
exist. It is a predicate on one gate, not a precondition framework.

### What this gate does not check

It checks that a target exists, not what the target does. `topos`, `pay`
and `auth` hold `cover` with a hand-rolled repository-average gate, and
this gate reports them as holding it. That is the right split: presence is
a property of the Makefile and this gate can see it, whereas shape is a
property of the check and belongs to the gate that runs. Replacing the
hand-rolled gates is rollout work, not contract work.

Two gates stay out, and the reasons are not the same:

- **`license` is not required.** One repository of 18 holds it. Requiring
  it produces 17 exemptions, which declares an intention rather than sets a
  bar. It is adopted as its own decision, in its own spec.
- **`validate` is not a bar.** Its content is per repository -- in `drive`
  an instrumented-transport grep plus govulncheck, elsewhere something
  else. Requiring the target would not mean the repositories run the same
  check, so its 17-of-18 presence must not be read as uniformity. It stays
  a repository extension point. If govulncheck is to be org-wide, it joins
  the set under its own name.

## Acceptance

1. `lateregate contract` fails a repository missing a required target, and
   names every missing target in one run rather than the first.
1b. A repository that tracks no `specs/` files passes without an exemption,
   and one that tracks specs but configures no `spec:` section still fails.
2. An exemption with an empty reason fails. An exemption with no date
   fails. An exemption whose date has passed fails, and says so as an
   expiry rather than as an absence.
3. A `LICENSE` file does not satisfy a `license` target; a `dist/`
   directory does not satisfy a `dist` target. Both are regression tests
   with the file and the directory planted.
4. A Makefile whose rule database is large is read to completion; the probe
   does not exit 141.
5. `go-verify.yml` holds no required-target list, and its `probe` job runs
   `lateregate contract` for the verdict. **Open.** The workflow is shared
   and unversioned while `lateregate` is pinned per repository, so the
   moment the step lands it runs against whatever version each consumer
   pins -- and the four repositories on v0.11.0 have no `contract`
   subcommand at all. Flipping it before they carry it would fail every one
   of them for the wrong reason, and making the step tolerate a missing
   subcommand would reintroduce the vacuous pass this spec exists to
   remove. So the flip is the last step of the rollout, after every
   consumer carries the subcommand.
   **Closed by [[008-one-bar]]**: the probe list is not flipped but
   replaced. The gates leave the Makefile altogether, so there is no
   target list in either repository to keep in step.
6. This repository satisfies the set it declares, and exempts nothing.
