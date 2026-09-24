# `.lateregate.yaml`

Every command reads `.lateregate.yaml` from the repository root (or from
`-C DIR`). The file holds decisions, each with its reason: a coverage
exemption, a spec vocabulary, a hermetic allowance, a dependency
allowlist, a license, a waiver. Every value the tool can decide for a
repository it decides by default, so a missing file is a valid
configuration and a key in the file is one somebody chose.

Two rules keep the file honest:

- **An unknown key is an error**, not a key silently ignored, because a
  typo that disables a gate is the failure this tool exists to prevent.
- **A key that restates its default** is reported by `lateregate
  contract` with "delete it": a restated default is the line the next
  default change makes wrong.

Two things have no default and must be written: the `identity` block,
which every repository declares, and `license.spdx` with `license.holder`,
which the `license` gate needs.
The smallest complete file for a library or a tool is:

```yaml
identity:
  role: none
license:
  spdx: MIT
  holder: Your Name
```

## Defaults

| Key | Default |
| --- | --- |
| `cover.threshold` | 90 |
| `modernize.disable` | `[newexpr, errorsastype]`: both fixers emit code that does not compile or half-applies |
| `spec.dir` | `specs` |
| `spec.require` | `[title, status]` |
| `spec.index` | `specs/README.md` when the file exists |
| `golangci.sloglint` | `context: scope`, every package: where a context is in hand, the `*Context` variant is right |
| `release.require_green` | `true` |

## Waivers

A gate that applies runs unless the repository has written down that it
is behind, and until when:

```yaml
waive:
  cover:
    reason: the tree is at 82.2% and the gap is in handler and runner
    until: 2026-11-15
```

Both fields are mandatory, and the date is the half that matters: a
reason alone turns into a permanent exception. `until` is inclusive. After
it, the gate runs and fails on its own terms, and the summary says the
waiver ran out. A waiver keyed on a name that is not a gate fails the
load, because a waiver for a gate nobody runs hides a typo in the name of
a gate somebody does.

A waiver on one of the five gates `suite` folds narrows the suite run
rather than skipping it; see [`gates.md`](gates.md#suite-and-the-five-gates-it-folds).
The `identity` block takes waivers per rule, with the same two fields.

## Reference

Every section is optional unless noted. The values shown are the
defaults; do not restate them.

```yaml
waive: {}                  # gate -> {reason, until: YYYY-MM-DD}; the only way an applicable gate does not run

cover:
  threshold: 90.0          # the per-package floor
  trim_prefix: ""          # dropped from package paths in the report; default the module path
  exempt: {}               # package suffix -> the reason it is exempt

spec:                      # applies when git tracks the spec directory
  dir: specs               # empty disables spec-lint
  status: []               # closed vocabulary; empty allows anything
  require: [title, status] # frontmatter keys every spec carries, non-empty
  index: specs/README.md   # the default when the file exists; empty disables the index checks
  wikilinks: false         # resolve [[name]] against dir
  exclude: []              # file names in dir that are not specs
  vocabulary: {}           # frontmatter key -> the values it may hold, like status
  status_requires: {}      # status -> {field, match, hint}: a key present at that status and absent otherwise
  require_section: []      # headings every spec carries
  require_section_by_status: {}   # status -> headings a spec carries once it reaches it
  section_exempt: []       # files the section rules do not apply to
  scoped_ids: ""           # pattern for identifiers that belong to the spec they appear in
  tables: false            # check Markdown tables are well formed
  register:                # an id table one spec defines and the rest of the tree cites
    file: ""               # the spec that defines the rows; empty disables the check
    define: ""             # pattern for a defining row; group 1 is the id
    cite: ""               # pattern for a citation; the first non-empty group is the id
    sequential: false      # ids run from 1 with no gaps
    prefix: ""             # the letter the ids carry, e.g. C for C1, C2
  status_linked_from: {}   # status -> file that must link a spec at that status
  status_marker:           # a paragraph whose content differs between statuses
    pattern: ""            # matches the paragraph; group 1 is compared
    required: []           # statuses that must carry it
    expect: {}             # status -> pattern group 1 must match
    reject: {}             # status -> pattern group 1 must not match
  numbered: false          # require NNN-name.md, and no number used twice
  started: []              # statuses at which work on a spec has begun
  settled: []              # statuses at which a dependency stops blocking
  archive:
    dir: ""                # relative to dir; empty disables the archive checks
    statuses: []           # statuses that send a spec to the archive

hermetic:
  allow: []                # directories kept on PATH besides the toolchain's

race:
  timeout: ""              # go test -timeout for the race run, e.g. 45m; empty is the toolchain default

modernize:
  disable: [newexpr, errorsastype]   # [] runs every fixer

golangci:
  sloglint:
    context: scope         # scope, or all
    request_paths: ""      # a regexp; when set, only these packages are checked
    exempt: []             # paths inside request_paths that do not serve requests
  extra: {}                # merged over the shared config; linters.enable appends
  own: ""                  # keep a committed .golangci.yml, and why

license:
  spdx: ""                 # no default; the gate fails until it is declared
  holder: ""               # no default; matched literally in every notice
  extensions: ['.go']
  names: []                # files without an extension, e.g. Makefile
  skip: []

tempdir:
  command: []              # default: go test ./...
  allow: {}                # surviving-entry prefix -> the reason

depcheck:
  platforms: []            # GOOS/GOARCH pairs; empty is the host's own
  packages: {}             # import path -> {decision, allow: {prefix: reason}}

cgo_free: {skip: []}
otel_client: {skip: []}

release:
  require_green: true      # false skips the CI guard before a cut
  stamp: []                # entries: {file, pattern}; the vX.Y.Z inside each match moves to the version being cut

registers:                 # applies when user_surfaces names a function
  user_surfaces: []        # package path and function: internal/api.WriteError
  skip: []                 # directories the scan does not enter

identity:                  # mandatory: a repository with no block fails the gate
  role: ""                 # issuer | core | platform | service | bff | client | none
  audience: ""             # what a token addressed here carries; core, service, platform
  config_prefix: ""        # the prefix of the deployment variables; core
  api_group: ""            # the group this core writes its own manifests under; core
  claims_passthrough: []   # the files of a core that may name a claim
  skip: []                 # paths the scans do not enter
  overlays: []             # the paths holding one company's own deployment overlay; core
  envelope_exempt: []      # the files whose types carry the envelope's field names for a reason
  image: ""                # the segment this workload's image was built under; issuer, core, service, platform
  roles_only: false        # turn on the roles rule; one way once set
  bff: []                  # the paths of this repository's browser frontend; roles that verify
  reached_by: clients      # clients | services | self-hosted; roles that verify
  registry: deploy/base/clients.yaml   # the client registry; issuer
  audiences: []            # the product audiences this client presents; client
  waive: {}                # rule -> {until, reason}: hold every other rule while this one is behind

postgres:                  # optional: an absent block is decided from the imports
  role: ""                 # none | direct | pooled; direct needs a dated `waive: postgres` entry
  prefix: ""               # pooled only: EVAL makes the names EVAL_DATABASE_POOL_URL and EVAL_DATABASE_URL
  direct_env: ""           # pooled only, with pool_env and without prefix: the direct name, written out
  pool_env: ""             # pooled only, with direct_env and without prefix: the pooled name, written out

enums:
  go:
    types: []              # package.Type; .Type is the module root
    fields: {}             # package.Struct.Field -> configured package.Type
    parsers: {}            # package.Function -> reason for primitive conversion
  typescript: []           # entries: {project, types, fields, parsers}; selectors are file.ts#Symbol
```

[`gates.md`](gates.md) explains what each gate does with these keys.
