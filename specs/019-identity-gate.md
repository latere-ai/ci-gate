---
title: Gate the identity shape every repository integrates
status: draft
depends_on:
  - 001-gate-principles.md
  - 002-dependency-footprint.md
  - 013-registers-gate.md
affects:
  - internal/bar/
  - internal/config/
  - internal/identity/ (new)
  - internal/contract/
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
  claims_passthrough: [internal/auth/claims.go]   # core: the files that may name a claim, to forward it
  skip: []                   # directories the scans do not enter, each with a reason in contract's waiver form
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
| one hop, no delegation (R3) | all but none | a string or identifier `act`, `grantor_id`, `agent_id`, `actor_id`, `tokens/exchange`, `actor: true`, `RFC 8693` in a non-test Go file or a non-archived document; `act` matches only as a JSON key or a struct tag, so the English word passes |
| roles, not flags (R9) | all but none | `is_superadmin` or `IsSuperadmin` anywhere outside tests and archives; the rule is off until the family's id-09 ships and the block says `roles_only: true`, then it is on and cannot be turned off |
| an explicit audience (D3) | core, service, platform | every container in `deploy/**` that runs the repository's binary sets the variable `audience` names, or `AUTH_AUDIENCE` for a service, to a non-empty value that is not the issuer URL |
| per-endpoint bearers, internal stays internal (R8) | issuer, platform, core | two environment variables of one container reading one secret key; an ingress rule whose path is `/` or a prefix of `/internal/` on a host also serving `/internal/` |
| no Latere value in a core (open-cores invariant 5) | core | `latere.ai` or `latere.svc` in a non-test Go file, a deploy manifest, or a document, outside the module path, the API group, and lines the block's `skip` names with a reason |
| a client presents one audience per product (id-01) | client | a string literal that is a product audience appears in exactly one Go file, the one that mints for it; a bearer read from a token file is never written to an `Authorization` header outside the file that mints |
| documents describe the current generation (id-02) | all | `pkg/oidclogin`, `pkg/jwtauth`, `pkg/oidc/` , `identity fabric`, `delegated token` in a non-archived `*.md` |

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
| A Go repository with no `identity` block fails with the sentence naming it; `contract` reports it as drift | `TestIdentityBlockIsRequired`, `TestContractReportsMissingIdentityBlock` |
| Each rule in the table fails a fixture tree that violates it and passes the same tree with the violation removed, per role | `TestIdentityRules`, table-driven over rule, role and fixture |
| A rule with no scan target reports `SKIP` with its reason and never `PASS` | `TestIdentityRuleSkipsAreExplained` |
| `roles_only` cannot be unset once set in a tree whose history had it set | `TestRolesOnlyIsOneWay` |
| The family check fails on a missing block, an unregistered audience, a registered audience nobody verifies, a duplicate audience, and a client minting for nobody; it prints the layer table and fails when the committed one differs | `TestIdentityFamily`, table-driven |
| Every finding passes the `registers` gate's four tells | `TestIdentityFindingsAreUserRegister` |
| Rolled to every repository of the family with the rules that hold today green and the rest waived with a dated reason | the family check's first run, recorded in the family's id-10 |
