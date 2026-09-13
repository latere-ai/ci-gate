---
title: Gate the identity shape every repository integrates
status: partial
depends_on:
  - 001-gate-principles.md
  - 002-dependency-footprint.md
  - 013-registers-gate.md
affects:
  - internal/bar/
  - internal/config/
  - internal/identity/ (new)
  - internal/contract/
  - internal/registers/
  - cmd/lateregate/
  - README.md
effort: medium
created: 2026-09-13
updated: 2026-09-13
author: changkun
dispatched_task_id: null
---

# Gate the identity shape every repository integrates

## The problem

The family decided one way to do identity, recorded in latere-ai/specs
as `infrastructure/identity.md` and `infrastructure/open-cores.md`:
auth issues identity and membership; an open core verifies one token,
forwards every claim and asks one authorizer; a service verifies
`aud = self` and decides from its own state; nobody calls auth on a
request path; one hop mechanism, no delegation in tokens; roles, not
flags. Nine rules and fourteen decisions, spread over seventeen
repositories that are touched one at a time over months.

The rules held once because a person grepped for them. Three
generations of identity documents accumulated the same way: each was
true the day it was written and nothing checked it afterwards. A rule
that is not run on every push is a rule that drifts, and a repository
the epic has not reached yet is one nobody is checking at all.

## The gate

`identity` is a gate every repository runs, driven by a block in
`.lateregate.yaml` that names which layer of the shape the repository
is. The role selects the rules. A Go repository with no block fails
the gate with the sentence that names the block, so a repository
cannot sit outside the shape by omission; `contract` reports the
missing block as drift the way it reports a missing gate.

```yaml
identity:
  role: core                 # issuer | core | platform | service | bff | client | none
  audience: cella            # what this repository verifies as aud = self; required for core, service, platform
  config_prefix: CELLA       # the prefix of *_OIDC_ISSUERS, *_OIDC_AUDIENCE, *_AUTHORIZER_URL; required for core
  api_group: cella.latere.ai # the group this core writes its own manifests under; required for core
  claims_passthrough: [internal/auth/claims.go]   # core: the files that may name a claim, to forward it
  skip: []                   # paths the scans do not enter, as every other skip in this tool
  roles_only: false          # the roles rule, which is one way once set
  registry: deploy/base/clients.yaml   # the client registry; issuer only, and the default
  audiences: []              # the product audiences this client presents; client only
```

`none` is for a repository with no identity surface, a library or a
tool; it is a declared role, not an absent one, and the family check
below lists it.

### The rules, by role

Every rule names the family rule it enforces. A rule is a scan of the
tree: Go files outside tests, deploy manifests, and documents outside
archives. Nothing runs a service.

| Rule | Roles | What fails |
|---|---|---|
| a core reads no claim for meaning (R1, C1) | core | an identifier `OrgID`, `Roles`, `IsSuperadmin`, `PrincipalType`, or a string `org_id`, `roles`, `is_superadmin`, `principal_type` in a non-test Go file outside `claims_passthrough` |
| one verifier (C5) | core, service, platform, bff | `latere.ai/x/pkg/authkit/jwt` imported somewhere; no import of another JWT library, and no `base64.RawURLEncoding.DecodeString` applied to a segment of a bearer outside `authkit`; `depcheck`'s allow list carries the same decision, and this rule names it |
| one contract (C3) | core | `latere.ai/x/pkg/authz` imported; no hand-rolled `POST` to a path named `authorize` outside it |
| no auth on the request path (R2) | service, platform, bff | a string literal `/tokeninfo`, `/userinfo/permissions`, or `/orgs/` joined with `/members` in a non-test Go file |
| one hop, no delegation (R3) | all but none | `grantor_id`, `tokens/exchange`, `actor: true`, `RFC 8693` anywhere in a non-test Go file or a non-archived document; `act`, `agent_id` and `actor_id` only where they are a token claim, which is a JSON key or a struct tag in a type that also carries `sub`, `aud` or `exp`, or in a file that imports the verifier or names `Claims`. One product carries an agent identity as an attribution column, which the 2026-09-06 decision allows, so the English word and the column both pass |
| roles, not flags (R9) | all but none | `is_superadmin` or `IsSuperadmin` anywhere outside tests and archives; the rule is off until the family's id-09 ships and the block says `roles_only: true`, then it is on and cannot be turned off |
| an explicit audience (D3) | core, service, platform | every container in `deploy/**` that runs the repository's binary sets the variable `audience` names, or `AUTH_AUDIENCE` for a service, to a non-empty value that is not the issuer URL |
| per-endpoint bearers, internal stays internal (R8) | issuer, platform, core | two environment variables of one container reading one secret key; an ingress rule whose path is `/` or a prefix of `/internal/` on a host also serving `/internal/` |
| no Latere value in a core (open-cores invariant 5) | core | `latere.ai` or `latere.svc` in a non-test Go file, a deploy manifest, or a user document (`specs/` is the contributor's record, holds the hosted deployment's history and examples, and is not read), outside an import path, the API group the block declares in `api_group`, wherever it appears, and the paths the block's `skip` names. `go.mod` is not scanned: every occurrence in it is a module path, which the import-path exemption already covers |
| a client presents one audience per product (id-01) | client | a string literal that is a product audience the block's `audiences` names appears in exactly one non-test Go file, the one that mints for it. The second half, that a bearer read from a token file is never written to an `Authorization` header outside that file, is dataflow and is left to the client's own tests |
| documents describe the current generation (id-02) | all but none | `pkg/oidclogin`, `pkg/jwtauth`, `pkg/oidc/` , `identity fabric`, `delegated token` in a non-archived `*.md` |

A finding is one line: the rule, the file and line, and what to do,
in the register the family's `registers` gate holds this repository
to. A rule with no scan target, a role with no `deploy/`, passes
with a `SKIP` that says why, per [[001-gate-principles]]: nothing
passes vacuously.

### The family check

Some of the shape is only visible with every repository in view. A
subcommand, `lateregate identity family`, reads a list of repositories
and their `identity` blocks, which the workflow in latere-ai/specs
gathers by checkout, and fails when:

- a repository has no block;
- an audience a `core`, `service` or `platform` verifies is not in the
  issuer repository's client registry under the audiences an actor
  token may be minted for, or the registry lists an audience nobody
  verifies;
- two repositories declare the same audience;
- a `client` mints for an audience no repository verifies.

It prints the layer table of `infrastructure/open-cores.md` from the
blocks, so the document is derived from the tree and the workflow
fails when the committed table differs from the derived one.

### What it does not check

Whether `platformd` decides from its own tables; whether a claim
belongs in the token or in a product table; whether a repository's
declared role is right. Those are review, and this gate is what lets
review look only at them. It does not run a service: the behavioural
half of the shape is the conformance packages `authkit/conformance`
and `authz/conformance` in `latere.ai/x/pkg`, which a repository runs
as tests under the `suite` gate.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| A Go repository with no `identity` block fails with the sentence naming it; `contract` reports it as drift | `TestIdentityBlockIsRequired`, and `TestContractReportsMissingIdentityBlock` in `internal/contract` |
| A block whose role could not act on what it carries is rejected at load | `TestIdentityValidationRejects` in `internal/config`, over every required value, and `TestIdentityAcceptsEveryRole` over the vocabulary |
| Each rule in the table fails a fixture tree that violates it and passes the same tree with the violation removed, per role | `TestIdentityRules`, table-driven over rule, role and fixture tree, with `TestSkipAndPassthroughAreHonoured` and `TestTheContainerThatRunsThisRepository` for the two admissions the block carries |
| A rule with no scan target reports `SKIP` with its reason and never `PASS` | `TestIdentityRuleSkipsAreExplained`, over every rule of every role, which also fails a rule no role reaches |
| `roles_only` cannot be unset once set in a tree whose history had it set | `TestRolesOnlyIsOneWay`, and `TestRolesOnlyNeedsTheHistory` for a history the rule cannot read |
| The family check fails on a missing block, an unregistered audience, a registered audience nobody verifies, a duplicate audience, and a client minting for nobody; it prints the layer table and fails when the committed one differs | `TestIdentityFamily`, table-driven, with `TestIdentityFamilyDerivesTheCommittedTable` and `TestIdentityFamilyNeedsTheRegistry` |
| The sentence of every finding, after its location, passes the `registers` gate's four tells | `TestIdentityFindingsAreUserRegister`, which reads the tells from that gate through `registers.Tells` rather than restating them |
| Rolled to every repository of the family with the rules that hold today green and the rest waived with a dated reason | open: the family's id-10 closes it, at step 2 of its order |

## Outcome

Shipped on 2026-09-13: the `identity` block in `internal/config`, the gate in
`internal/identity`, the missing block as drift in `internal/contract`, and
`lateregate identity family`. The status is `partial` because one criterion is
open and one is deliberately left to another test.

**Open.** The roll-out row is the family's, not this repository's: the gate
exists and this repository declares `role: none`, and the block reaches the
other sixteen repositories at step 2 of [id-10's order](https://github.com/latere-ai/specs/blob/main/infrastructure/identity/id-10-guardrail.md).

**Left elsewhere.** The client rule's second half, that a bearer read from a
token file never reaches an `Authorization` header outside the file that mints
it, is dataflow through arbitrary variables. A scan of that would be a guess
with a false-positive rate, so the gate holds the first half from the block's
`audiences` and the second stays a test in the client itself.

**Two heuristics, named as such in the report and the README.** A file that
both decodes unpadded base64 and splits a string on `.` is taking a token
apart; neither half alone is evidence. A container runs this repository when
its image names a directory under `cmd/`, or when the document holds one
container. A path either reads wrong goes in `skip`.

**Three corrections to the text above, made so the spec describes what
shipped.** `skip` is a bare list of paths, as every other `skip` in this tool
is, rather than a map carrying reasons; the reason-carrying forms here are
`cover.exempt`, `depcheck.allow`, `tempdir.allow` and `waive`. The documents
rule reads "all but none", because `none` declares no identity surface and
runs no rule at all, which is also what keeps this repository's own README
from failing its own gate. The delegation and no-Latere-value rows carry the
scoping the family survey of 2026-09-13 found necessary: an agent identity
used as an attribution column, and a core's own API group in its documents.
