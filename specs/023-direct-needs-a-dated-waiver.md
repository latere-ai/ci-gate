---
title: The direct role needs a dated waiver
status: complete
depends_on:
  - 021-postgres-gate.md
affects:
  - internal/config/
  - internal/postgres/
  - internal/bar/
  - cmd/lateregate/
  - README.md
  - CHANGELOG.md
effort: small
created: 2026-09-19
updated: 2026-09-19
author: changkun
dispatched_task_id: null
---

# The direct role needs a dated waiver

## The problem

[[021-postgres-gate]] shipped three roles and made one of them free.
`none` is checked against the imports, `pooled` against the two
environment names the serving path reads, and `direct` passes on the
strength of having been written down. That was the right shape while it
shipped: the gate landed before any service moved, every Postgres
repository declared `direct`, and nothing turned red. A gate that fails
eleven repositories on the day it arrives is a gate somebody deletes.

The family's cutovers have since landed. Ten services open serving
traffic on the pooled endpoint, fall back to the direct one when the key
is absent, and hand the direct endpoint to their migrator. What is left on
`direct` is a short list with a reason each, and a `direct` that passes by
declaration cannot tell that list from the next service somebody writes.

That is the actual failure this closes. Nothing about a new repository
declaring `direct` is visible: it is a green line in a report, identical
to the green line a repository with a considered exemption prints. The
cluster has about 22 usable connection slots and the pooled budget claims
19 of them; a service that arrives on the direct endpoint by default
spends the headroom a migration needs, and learns it during a rollout.

After this step a repository on the direct endpoint is a recorded, dated
exception rather than a silent default. The list of them is a list
somebody can read, and every entry has a day on which a person looks at it
again.

## The rule

`postgres.role: direct` passes only while the repository carries a live
waiver of the `postgres` gate.

```yaml
postgres:
  role: direct

waive:
  postgres:
    reason: a library, and the consumer that calls it owns the connection
    until: 2026-12-19
```

This is the waiver mechanism the repository already has, not a second one.
[[008-one-bar]] made the top-level `waive` map the only way a gate that
applies does not run: both fields mandatory, `until` inclusive, a key that
is not a gate name refused at load. `direct` reads that same entry, and
inherits all of it. No new key, no new validation, no second date format.

Three states, and the gate prints which one it is in:

| State | Verdict | What the row says |
|---|---|---|
| no waiver | FAIL | the role is declared and nothing records why; cut the serving path over to the pooled name with a fallback to the direct one and declare `pooled`, or record staying direct as a waiver of this gate |
| a live waiver | PASS | waived until the date, with the reason it carried |
| a waiver past its date | FAIL | the date it ran out on, what it claimed, and that renewing it costs a later date beside a reason that still holds |

### Why the gate reads the waiver, and not only the plan

`bar.Plan` already reads the waiver map: a gate with a live waiver is
listed `WAIV` and never runs. That alone would nearly work, and it is not
enough for two reasons.

`lateregate postgres` runs one gate by name and builds no plan, so a
repository checking this rule on its own would fail on a waiver the bar
honours. The two paths have to agree.

And the plan's contract for an expired waiver is that the gate runs and
fails on its own terms, so that the reason for the work comes from the
gate rather than from a date. A `direct` role whose waiver has run out is
exactly that case: the gate runs, reads the expired entry, and its refusal
is the Postgres rule's own sentence naming both ways out.

So the gate takes the waiver as an argument. `bar` hands it
`Cfg.WaiverFor("postgres")` and the day, the way it hands `identity` the
day.

```
                     .lateregate.yaml
                            |
              +-------------+--------------+
              |                            |
        postgres.role: direct        waive.postgres
              |                            |
              +-------------+--------------+
                            |
     bar.Plan  <------------+------------>  postgres.Run
        |                                        |
   live -> WAIV, gate not run             live -> PASS, date and reason
   expired -> gate runs ------------------> expired -> FAIL, the rule's sentence
   absent -> gate runs --------------------> absent -> FAIL, the rule's sentence
```

`Waiver.Live(now)` is the one definition of live, and both sides call it.
`until` is inclusive, so the waiver dies when the day after it begins:

```
  live(now)  <=>  now  <  until + 1 day
```

The plan computed that inline before this change. Two copies of a date
rule is a date meaning two things in one run.

### What this does not do

The waiver is keyed on the gate, so a repository that waives `postgres`
and later flips to `pooled` has the pooled checks skipped in the bar until
somebody deletes the entry. That is the waiver mechanism working as it
does for every other gate, and refusing a `waive: postgres` under a
non-`direct` role would be this gate inventing a rule the others do not
have. The date is what catches it: the entry expires, the gate runs, and
the pooled checks report.

`none`, `pooled` and an absent block are untouched. They are decided from
the tree, and a waiver of this gate changes no verdict under them.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| `direct` with no waiver fails, and the finding names both ways out at the file the fix goes in | `TestRoleDirectWithoutAWaiverFails` |
| `direct` with a live waiver passes, and the row carries the date and the reason | `TestRoleDirectWithALiveWaiverPasses` |
| `direct` on the last day of its waiver passes, and the day after fails naming the date and what the waiver claimed | `TestRoleDirectWithAnExpiredWaiverFails` |
| `none`, `pooled` and an absent block are decided from the tree whether or not a waiver of this gate exists | `TestTheWaiverIsTheDirectRolesAlone` |
| `lateregate postgres`, which builds no plan, refuses `direct` without a waiver and passes it with one | `TestPostgresSubcommandReadsTheWaiver` in `cmd/lateregate` |
| `Live` holds the inclusive boundary, and a date that does not parse is not live | `TestAWaiverIsLiveThroughTheDayItNames` in `internal/config` |
| `WaiverFor` hands a gate the entry that covers it and nothing where there is none | `TestAWaiverIsRead` in `internal/config` |
| The plan still waives a live entry and runs an expired one | `TestALiveWaiverSkipsAndAnExpiredOneRuns` in `internal/bar`, unedited |
| The sentence of both new findings passes the `registers` gate's tells | `TestPostgresFindingsAreUserRegister` |

## Outcome

Shipped in v0.45.0: `Waiver.Live` and `Config.WaiverFor` in
`internal/config`, `ruleDirect` in `internal/postgres` reading the waiver
and the day, and the `postgres` entry in `bar.Gates` passing both. The
plan's inline date arithmetic now calls `Live`, so one rule decides a
waiver's last day everywhere.

This is step 4 of the family's build order for the pooler, and the last
one the gate owns. The five repositories still declaring `direct` at the
cut are a library whose consumer owns the connection, a template, a
scaffold, a storage core, and the one service whose run lock still holds a
session; each takes a waiver with the condition under which it stops
holding, written for whoever reads it on the date.
