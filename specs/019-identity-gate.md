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
updated: 2026-09-17
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
  waive: {}                  # rule -> {until, reason}; per rule, so the other rules keep running
  roles_only: false          # the roles rule, which is one way once set
  registry: deploy/base/clients.yaml   # the client registry; issuer only, and the default
  audiences: []              # the product audiences this client presents; client only
  bff: []                    # paths of a browser frontend beside the API, which forwards the person's own token to the issuer; the request-path rule does not read them
  reached_by: clients        # clients (default): a registered client mints for this audience; services: only service tokens or operators reach it; self-hosted: an open core's default, verified only where the core is self-hosted. The last two expect no registry row
```

`none` is for a repository with no identity surface, a library or a
tool; it is a declared role, not an absent one, and the family check
below lists it.

### Waivers are per rule

A gate waiver would take the whole gate down for one rule a repository
is behind on, which in a roll-out across seventeen repositories is
most of the gate for most of the time. So the block carries its own
`waive`, keyed by rule name, with the reason and the inclusive `until`
a gate waiver has. A waived rule runs and prints its findings under
`WAIV`; a waiver whose rule already holds says so; a waiver past its
date is a failure that names the waiver; a waiver naming no rule or a
rule the role does not run fails the load.

### The rules, by role

Every rule names the family rule it enforces. A rule is a scan of the
tree: Go files outside tests, deploy manifests, and documents that
describe the current system. A record is not a document here: a
changelog, a release note, a file under `.archive`, and a spec whose
frontmatter status is one the tree's `spec.settled` list calls
finished, because a record legitimately names what it retired. Nothing
runs a service.

| Rule | Roles | What fails |
|---|---|---|
| a core reads no claim for meaning (R1, C1) | core | an identifier `OrgID`, `Roles`, `IsSuperadmin`, `PrincipalType`, or a string `org_id`, `roles`, `is_superadmin`, `principal_type` in a non-test Go file outside `claims_passthrough` |
| one verifier (C5) | core, service, platform, bff | `latere.ai/x/pkg/authkit/jwt` imported somewhere; no import of another JWT library, and no `base64.RawURLEncoding.DecodeString` applied to a segment of a bearer outside `authkit`; `depcheck`'s allow list carries the same decision, and this rule names it |
| one contract (C3) | core | `latere.ai/x/pkg/authz` imported; no hand-rolled `POST` to a path named `authorize` outside it |
| no auth on the request path (R2) | service, platform, bff | a string literal `/tokeninfo`, `/userinfo/permissions`, or `/orgs/` joined with `/members` in a non-test Go file outside the paths the block's `bff` names, which hold a browser frontend that forwards the person's own token to the issuer's API |
| one hop, no delegation (R3) | all but none | `grantor_id`, `tokens/exchange`, `actor: true`, `RFC 8693` anywhere in a non-test Go file or a non-archived document; `act`, `agent_id` and `actor_id` only where they are a token claim, which is a struct tag in a type that also carries `sub`, `aud` or `exp`, or a bare occurrence outside every struct type in a file that imports the verifier or names `Claims`. A tag in a type that carries no registered claim is a column of that type wherever the file lives: one product carries an agent identity as an attribution column, which the 2026-09-06 decision allows, and a handler that verifies tokens also renders its audit rows |
| roles, not flags (R9) | all but none | `is_superadmin`, `IsSuperadmin` or `isSuperadmin` anywhere outside tests and archives, in a Go file, a document, a manifest, or a frontend source (`.ts`, `.tsx`, `.jsx`, `.vue`, `.svelte`, `.js`, `.mjs`, `.cjs`), which is the one rule that reads a frontend because a page decides access as surely as a handler does; a bundler's output and a package manager's tree are read as nobody's decision, and a comment inside a file that is read is prose (the 2026-09-17 amendment); the rule is off until the family's id-09 ships and the block says `roles_only: true`, then it is on and cannot be turned off |
| an explicit audience (D3) | core, service, platform | every container in `deploy/**` that runs the repository's binary sets the variable `audience` names, or `AUTH_AUDIENCE` for a service, to a non-empty value that is not the issuer URL. A container runs the binary when the name its image was built under is exactly a directory under `cmd/`; an `initContainers` entry is read when it also declares a variable of the repository's own prefix, which is how it says it runs with the workload's configuration (the 2026-09-16 amendment) |
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
  token may be minted for, unless the block says `reached_by:
  services`, or the registry lists an audience nobody verifies;
- two repositories declare the same audience;
- a `client` mints for an audience no repository verifies.

It prints the layer table of `infrastructure/open-cores.md` from the
blocks, so the document is derived from the tree and the workflow
fails when the committed table differs from the derived one.

### What it does not check

Whether `platformd` decides from its own tables; whether a claim
belongs in the token or in a product table; whether a repository's
declared role is right. Those are review, and this gate is what lets
review look only at them. It does not run a service: the behavioral
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
| A waived rule with findings does not fail the gate and prints `WAIV` with its count; the same waiver past its date fails and names it; a waiver naming no rule or a rule the role does not run fails; an entry without a reason or a usable date fails the load | `TestWaivedRuleReportsAndHolds`, `TestExpiredWaiverFails`, `TestWaiverMustNameARuleTheRoleRuns`, `TestIdentityValidationRejects` |
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
its image names a directory under `cmd/` — exactly, since the 2026-09-16
amendment below. A path either reads wrong goes in `skip`.

**Three corrections to the text above, made so the spec describes what
shipped.** `skip` is a bare list of paths, as every other `skip` in this tool
is, rather than a map carrying reasons; the reason-carrying forms here are
`cover.exempt`, `depcheck.allow`, `tempdir.allow` and `waive`. The documents
rule reads "all but none", because `none` declares no identity surface and
runs no rule at all, which is also what keeps this repository's own README
from failing its own gate. The delegation and no-Latere-value rows carry the
scoping the family survey of 2026-09-13 found necessary: an agent identity
used as an attribution column, and a core's own API group in its documents.

## Amendment, 2026-09-16: which containers are this repository's

Shipped as the `audience` rule's workload detection, found while origo
retired its `audience` waiver (the family's `id-10-guardrail.md`, "State
on 2026-09-16").

The rule read three things wrong, and each was a guess standing in for
the question "is this container this repository":

1. A document with exactly one container was read whole, whatever that
   container was, on the assumption that a single-container manifest is a
   single-workload manifest. A repository's tree also holds manifests of
   other workloads: origo's two spec 013 stub manifests run the test
   double that mints tokens and answers authorization, and a test double
   verifies no audience. Each was a finding on every push, and the
   repositories worked around it with `identity.skip`, which turns off
   every deployment rule for that path.
2. The image was matched by substring, so `ghcr.io/latere-ai/origo-stubs`
   ran the command `origo`. A name built beside a repository's own is the
   normal shape of a stub, a debug build, or a migration image.
3. `initContainers` were never read, so a container that verifies a token
   before the workload starts — origo's `check`, which runs `origod check`
   against the node's environment — was outside the rule the workload
   beside it is held to.

**The decision.** A container runs this repository when the name its image
was built under is exactly a command the repository builds: the last path
segment of the reference with the registry, the tag and the digest
removed, compared whole against the directories under `cmd/`. `origo`
matches `ghcr.io/latere-ai/origo:v1` and `origo`, and never
`origo-stubs`. There is no shortcut for a lone container: a document
holding one container that is not the workload holds no container of this
repository, and the rule reports why rather than finding against it.

A container with no image is an overlay's patch of one declared elsewhere,
which kustomize merges by name, so it is read as the container whose name
it carries. That keeps the decision of v0.32.2 — a container is judged
across every file that names it, so an audience set in an overlay counts
for the base — and keeps an address named in an overlay a finding.

`initContainers` are read beside `containers`, and an init container is
held to the audience when it declares any variable of the repository's own
prefix (`CELLA_` for a core from `config_prefix`, `AUTH_` for a service or
a platform). That is the manifest's own statement that the container runs
with the workload's configuration: a check that runs against another
environment checks nothing, and a step that copies a file into a shared
volume verifies nothing. A variable reached through `envFrom` is not named
in the document and does not count.

**What a consumer sees.** A manifest of test doubles stops being a
finding and the `identity.skip` entry written for one can go. A check or
migration init container configured like the node is now held to naming
the audience. And a deployment whose image name is not a directory under
`cmd/` reports `SKIP audience` with its reason where the shortcut used to
read that container: a rule that reads nothing says so, so the image is
renamed after the command it runs or the path goes in `skip` as a
decision.

`identity.skip` is unchanged: a path it names is one the rules assert
nothing about.

## Amendment, 2026-09-17: what a command is, what a string is, and the answering half

Shipped as ci-gate v0.40.0, found by the family during the identity epic's
close (the family's `id-10-guardrail.md` and `id-11-contract-2-authorizer.md`).

**A command at the module root.** The 2026-09-16 amendment says a container
runs this repository when its image names a directory under `cmd/`. A module
whose only `main` is at its root has no such directory, and `go build` names
that binary after the module's last segment, so the rules now read the
root's package clause and count that segment as a command. wallfacer moved
its command to the root and was reporting `SKIP audience`, with
`AUTH_AUDIENCE` unchecked; that was the gap.

**A declared image.** A repository whose image was built under a name that is
neither a `cmd/` directory nor the module's last segment (`wallfacer`
deploying `wallfacerd`) declares it as `identity.image: wallfacerd`. The
value is the one segment the rules compare; a registry, tag or digest in it
is refused, and so is the key outside `issuer`, `core`, `service` and
`platform`, the roles whose deployments the `audience` and `bearers` rules
read. It is data beside `overlays`, not a guess in the gate.

**An import path is not a string the file carries.** The scan read every
string literal in a Go file, import paths included, so a file that imported
a test double whose path holds `authorize` was a finding of the `authorizer`
rule, and `claims` read a package path holding a membership claim. The scan
now stops at an import spec. origo's three `identity.skip` entries written
for this can go.

**The `envelope` rule, every role but `none`.** The `authorizer` rule catches
a repository that asks the authorizer in a shape of its own. The family's
id-11 named the other half, a repository that answers in one, and no rule
held it. A struct in a non-test Go file whose JSON tags name `action` beside
`subject` or `resource`, or `allow` beside `ttl` and `reason`, outside the
module the envelope is declared in, is that wire shape written a second
time. Tag names match whole. `identity.envelope_exempt` names the files
whose types carry those names for a reason of their own, declared per
repository; a path the tree does not hold stops the run.


## Amendment, 2026-09-17: the roles rule reads the frontend

Found by the identity epic's verification: three repositories held a green
`roles` line over a frontend that branched on the retired flag. The rule read
Go files, documents and deploy manifests, so the half of the product a person
actually clicks was never read, and the flag it was written to retire lived on
in `.tsx` and `.vue` behind a clean Go tree. A rule that reads one language of
a two-language repository reports the language, not the shape.

**The scan target.** `.ts`, `.tsx`, `.jsx`, `.vue`, `.svelte`, `.js`, `.mjs` and `.cjs` under the
repository: every extension a person in this family writes a component or a
module in, not a sample of them. `.jsx` is `.tsx` without the types, and
`.mjs` and `.cjs` are `.js` with a package's module system pinned; an
extension left out is a file the rule passes over in silence, which is the
gap this amendment exists to close. This is the one rule that reads them, in a bucket of its own
rather than in `everyFile`: the rules written against Go, documents and
manifests keep the target they were written against, so extending this one
weakens none of them.

**The patterns.** Three plain case-sensitive substrings, `is_superadmin`,
`IsSuperadmin` and `isSuperadmin`, on every target the rule reads. The third
is the spelling a browser gives the same field, and a `strings.Contains` for
`IsSuperadmin` never matched it.

**What is not read.** Four classes, each for a reason rather than for
convenience:

| Not read | Why |
|---|---|
| a `.test` or `.spec` segment before the extension, a `__tests__` directory | a test asserts and does not decide, which is why the Go half skips `_test.go`. The assertion a repository writes after retiring a flag is that the flag confers nothing, and that assertion names the flag: read as a decision, the regression test becomes the finding |
| `dist/`, `build/`, a `.min` or `.bundle` file, a file whose first five lines carry `@generated`, `Code generated by` or `DO NOT EDIT` | a bundle is the build's output. The decision belongs to the source it was built from, and that source is read |
| `node_modules/`, `testdata/` | nobody in the repository wrote them |
| an archive | a record may name what it retired, as everywhere else in this gate |
| whatever `git check-ignore` names | a frontend tree holds build residue beside its sources. One repository ignores `frontend/src/**/*.js`, where the Vue toolchain leaves a `.vue.js` sidecar next to every component, holding what the `.vue` no longer does. That file is on a laptop and not in a checkout, so reading it makes the gate red locally and green on the runner, which is the one direction a gate must never fail in. git deciding nothing, because there is no repository or nothing is ignored, leaves every file read: a scan that reads too much reports a finding a person can argue with, and a scan that reads too little reports nothing |

The telling is always the path, never the assertion inside the file. Reading
intent out of a test would be a guess; a filename is a decision the repository
already made.

**A comment is prose.** Inside a file that is read, comment spans are blanked
before matching: `//` to end of line, `/* */`, and `<!-- -->` in a single-file
component's template, each tracked across lines, with quotes tracked so a `//`
inside a string is text. A file that says "the token carries `roles` since
id-09 retired the `is_superadmin` flag" is describing the correction, not
making the decision. String contents stay, because a name quoted as a property
or read out of a template still decides. This is the same distinction the
document scan already makes with `record`, applied inside a file rather than
to a whole one; wallfacer's `frontend/src/lib/accountRole.ts` is the live case,
and it passes.

**Evidence.** Against each repository at the commit before its frontend was
corrected, the old build reports `PASS roles` and the new build reports the
lines: platform `StorageSection.tsx:73` and `OrgScreens.tsx:230`, lectio
`api/types.ts:24` and `AccountControl.vue:35`, eval `App.tsx:61`. agents,
replichai and wallfacer pass under both, wallfacer with a comment and a test
that both name the flag.

Swept across the eighteen repositories whose block says `roles_only: true`,
one more fails and is nobody's to fix here: llm-gateway names the retired flag
as the gating variable throughout `frontend/src/views/Models.vue` and
`frontend/src/lib/providerKeys.ts`, seven lines, with the value correctly
derived from `platform_admin`. The name is the finding, and renaming it is
that repository's.
