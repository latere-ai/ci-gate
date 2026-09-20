---
title: A core names two audiences, its own name and the origin
status: draft
depends_on:
  - 019-identity-gate.md
affects:
  - internal/identity/
  - README.md
  - CHANGELOG.md
effort: medium
created: 2026-09-20
updated: 2026-09-20
author: changkun
dispatched_task_id: null
---

# A core names two audiences, its own name and the origin

## Scope

One rule of the `identity` gate, over the deployment manifests of role `core`.
Not a core's own configuration code, which is each core's spec; not the hosted
planes, which run role `service`; not the routing under the origin, ps-01's.

## The problem

The family decided on 2026-09-20 that each open core accepts exactly two
audiences, its own name and `api.latere.ai`: a script holding a platform key
calls `api.latere.ai/v1/<capability>/...` directly and the core asks the
authorizer, so the boundary between cores is the authorizer's and not the
audience's. The decision is ps-01's "The audience gap" revision and the dated
amendment to open-cores' "The platform key", which fix the names too: `arca`,
`origo`, `lux`, `cella`, `topos`, `insula`. Two of six hold it: arcad verifies
`arca` alone and origod `origo` alone, so a token for the origin stops at both.

The gate already reads the variable and says nothing about its value.
`ruleAudience` (`internal/identity/deploy.go:36`) takes
`<PREFIX>_OIDC_AUDIENCE` for a core and `AUTH_AUDIENCE` or `AUTH_AUDIENCES`
otherwise (`deploy.go:41`), and reports a container that sets none
(`deploy.go:101`), an empty value (`deploy.go:104`), and an address where a
name belongs (`deploy.go:107`). `ARCA_OIDC_AUDIENCE: arca` and
`ARCA_OIDC_AUDIENCE: sandboxd` are one verdict today. Six repositories carry
this decision and each writes it once, in a file nobody else reads. Held per
repository it drifts, which is the argument [[019-identity-gate]] was built
on.

## Options

### Which manifest must name both

| | Shape | For | Against |
|---|---|---|---|
| A | Any file that sets the variable lists both | One rule, nothing declared, nothing to look up | It puts `api.latere.ai` in `deploy/base`, which is a value of one company in a manifest outside a declared overlay, and `ruleNoCompanyValue` (`internal/identity/rules.go:686`) reports it. Two rules of one gate would contradict each other. A fork would also inherit the origin of an installation it does not run |
| B | The overlay `identity.overlays` names must list both; the base names the core's own name alone | `identity.overlays` already exists and already means the manifests a hosted installation deploys from (`internal/config/identity.go:98`); arca and origo declare it. The Latere value stays in the one directory that may hold one, so the no-latere-value rule keeps holding over everything else | A core that declares no overlay is held to nothing, and a core that also skips the path can delete the key and lose nothing, which is sitting outside the shape by omission. The family check listing which cores declare a hosted overlay is what closes that, and it is not this rule's |
| C | The union over base and overlays must contain both | Finds the two names wherever they are written | A union is not a deployment. A base naming `arca` and a kind example naming `api.latere.ai` would pass while neither installation verifies two |

**Recommendation: B.** A is refused by another rule of the same gate, which is
a constraint and not a preference; C asserts something no `kubectl apply`
produces. B reads the deployment the repository declares as the hosted one,
and leaves the self-hoster's base correct as it stands.

### Where the core's name comes from

`identity.audience` already carries it (`internal/config/identity.go:78`), is
required of every role that verifies a token addressed to itself
(`identity.go:182`), and is what the family check reads for a duplicate and
for a registry row. `config_prefix` names variables; `audience` names the
audience, and deriving the second from the first is the conflation
[[022-postgres-names-are-declared]] refused for the two DSN names: the gate
takes the name, it does not build one out of another. **Recommendation:
`identity.audience`, and no new key.**

### A new rule, or the existing one extended

| | Shape | For | Against |
|---|---|---|---|
| A | `audience` grows the list check | One rule reads the variable | A waiver is per rule ([[019-identity-gate]]). arca and origo need a dated waiver the day this ships, and waiving `audience` takes the no-audience, empty-value and address findings down with it on the three repositories where they matter most |
| B | A new row, `core-audiences`, role `core` alone | A different scan target, a different assertion, and a waiver that costs nothing else. `audience` keeps running for every role it runs for now | Two rows read one variable |

**Recommendation: B.** The rows assert different things: `audience` says every
container of every role names a non-address audience, `core-audiences` says a
core's hosted deployment names the two the family decided. `checkWaivers`
(`internal/identity/identity.go:105`) refuses a waiver naming a rule the role
does not run, so a `core-audiences` waiver is legal in a core alone.

## The rule

`core-audiences` runs for role `core`, over the containers the deployment
rules already recognise as this repository's (`deploy.go:281`: an image whose
last segment is a command it builds, or an overlay patch carrying that name).

The value the hosted deployment runs is the overlay's where the overlay
patches it and the base's otherwise, because that is what kustomize merges:

```
  deploy/base sets ARCA_OIDC_AUDIENCE    identity.overlays: [deploy/prod]
    the overlay patches it -> the patch decides
    the overlay does not   -> the base decides
    neither file sets it   -> `audience` reports it, this rule is silent
```

The value is split on commas and each entry trimmed, the way a core that reads
a set already splits it (`cella/internal/config/identity.go:101`), and it
holds when the entries are exactly `{ identity.audience, "api.latere.ai" }`.

| Finding | When |
|---|---|
| the hosted overlay leaves the audience at this core's own name, so a token addressed to the origin stops here | the effective value is `identity.audience` alone |
| this names N audiences and two are accepted, this core's name and the origin | the entry count is not two |
| this names one audience twice | the two entries are equal |
| this names an audience that is neither this core's name nor the origin | an entry outside the set, such as `lux.latere.ai`, `sandboxd` or `toposd` |

One finding per workload the overlay patches, keyed by the document's name
beside the container's rather than by the container name alone, which is what
`ruleAudience` aggregates on, so a reaper beside a server is held too. It is
located at the patch, the file the second name goes in. A value `audience`
reports as an address is not read again here, and `arca,api.latere.ai` does
not trip that check: it tests for `http` at the start of the value and `,http`
inside it (`deploy.go:69`).

### Reading a declared overlay a repository also skips

arca and origo name `deploy/prod` under both `skip` and `overlays`, so the
walk prunes it (`internal/identity/scan.go:110`) and no rule reads those
manifests. `readOverlays` (`scan.go:167`) already reads the same files outside
the walk, as text, for the addresses they carry; this rule reads them as
manifests, so `scan` gains an `overlayManifests` list filled in that same pass
and `core-audiences` reads that list alone. `overlays` is a positive
declaration, "this directory is the hosted deployment", and `skip` a negative
one, "assert nothing here": the rule about the hosted deployment reads what
the repository declared as one, and `skip` governs every other rule as before.

### Who it does not bind

The hosted planes: llm-gateway (`audience: lux.latere.ai`), sandbox
(`sandboxd`) and agents (`toposd`) declare role `service`, so the rule never
runs there. Those three names are the ones ps-01 retires, by a cutover and not
by a gate.

A core that declares no overlay: lux and cella today. Lux holds
`deploy/overlays/kind` and `deploy/overlays/generic`, neither a hosted
installation, and leaves `identity.overlays` unset; cella's hosted plane is
sandbox. Each reports `SKIP` and binds the day it declares one. Lux's other
half, that luxd refuses a list at all (`lux/internal/config/identity.go:36`),
no manifest rule can see.

## Rollout

The rule ships as a finding that fails: arca and origo fail the day it lands,
their bases naming one audience and their prod overlays patching none. The
dated per-rule waiver that carries them is already in the identity block
(`internal/config/identity.go:143`), the mechanism
[[023-direct-needs-a-dated-waiver]] made the postgres gate's.

1. ci-gate releases `core-audiences`, after arca 027, origo 029 and lux
   024 are drafted and before any of them is released: a release before
   the drafts leaves two repositories red with nothing that closes them.
2. arca and origo add `identity.waive.core-audiences`, the date their spec
   lands and the spec as the reason.
3. Each spec lands: the core accepts a set, the prod overlay names both,
   the waiver goes rather than being renewed.
4. lux 024 makes luxd accept a set; the rule binds lux when ps-09 puts a
   hosted overlay in the tree, with no change here.

The family check is untouched: `identity.audience` stays the core's own name
and `api.latere.ai` appears in no block, so no two repositories declare one.

## Acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| 1 | An overlay naming the core's name and the origin passes | a core fixture in `TestIdentityRules`: base `CELLA_OIDC_AUDIENCE: cella`, `deploy/prod` patching it to `cella,api.latere.ai` |
| 2 | An overlay that patches no audience, over a base naming one, fails with the sentence about the origin | the same fixture, patch removed |
| 3 | A wrong count fails and the finding names it, and one audience written twice fails | `cella,api.latere.ai,drive` and `cella,cella` |
| 4 | An entry that is neither this core's name nor the origin fails | `lux.latere.ai,api.latere.ai`, a hosted-plane name |
| 5 | An address in the overlay is the `audience` rule's finding, and this rule does not report it twice | `https://api.latere.ai` |
| 6 | A core with no declared overlay reports `SKIP` with its reason, never `PASS` | `TestIdentityRuleSkipsAreExplained` |
| 7 | The rule runs for `core` and no other role, and a waiver of it elsewhere fails the load | `TestWaiverMustNameARuleTheRoleRuns` |
| 8 | The declared overlay is read although `skip` names the same path | a fixture declaring both, which is what arca and origo declare |
| 9 | Every new sentence passes the `registers` gate's tells | `TestIdentityFindingsAreUserRegister` |
| 10 | The rule's row is in the README's identity rules table, beside `audience` | that table's "Rule, Roles, What fails" row; the gate table's `identity` row is unchanged |

The three cores, as their trees read on 2026-09-20:

| Repo | What the manifests say | Verdict |
|---|---|---|
| arca | `deploy/base/deployment.yaml:89` and `deploy/base/reaper.yaml:61` set `ARCA_OIDC_AUDIENCE: arca`; `deploy/prod` patches the `arcad` container of the `arcad` and `arcad-reaper` workloads in `public-url.yaml` and `authorizer.yaml`, naming no audience | FAIL, two findings, one per workload: the hosted overlay leaves the audience at the core's own name |
| origo | `deploy/base/deployment.yaml:83` sets `ORIGO_OIDC_AUDIENCE: origo` on the `check` init container and `:155` on `origod`; `deploy/prod` patches both in `authorizer.yaml`, naming no audience | FAIL, two findings, one per container of the one workload |
| lux | `deploy/base/deployment.yaml:61` sets `LUX_OIDC_AUDIENCE: lux`; `identity.overlays` is unset and the tree holds no hosted overlay | SKIP, this repository declares no hosted deployment overlay |

`deploy/bootstrap` is outside both hosted overlays' `kustomization.yaml`, so
arca's migrate job (`bootstrap/migrate-job.yaml:62`) stays `audience`'s.

## Dependencies

[[019-identity-gate]] holds the rule table, the per-rule waiver and the
container heuristic this rule reuses; [[001-gate-principles]] is why a core
with no overlay reports `SKIP` with a reason rather than passing. Nothing here
blocks it, and the family's arca 027, origo 029 and lux 024 close the waivers
it opens.
