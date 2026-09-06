---
title: Gate the register of the strings a user reads
status: complete
depends_on:
  - 001-gate-principles.md
affects:
  - internal/bar/
  - internal/config/
  - internal/registers/ (new)
  - README.md
effort: small
created: 2026-09-06
updated: 2026-09-06
author: changkun
dispatched_task_id: null
---

# Gate the register of the strings a user reads

## The problem

Every sentence a Latere product emits is written for one of three readers,
and the register follows the reader: the user of the product, the
contributor changing it, or the developer debugging a running system. The
rule, adopted first in insula and now canonical for every repository, is
`docs/writing/registers.md` in `latere.ai/x/pkg`.

The rule holds by review, and one leak survives review more than any
other: a developer sentence handed to the function that writes the user's
error.

```go
api.WriteError(w, "deploy_failed", "apply Deployment insula-p-3f2a/api: store.Deploy not found")
```

The author wrote it while looking at the failure, so every word is true
and none of it is for the user. The user cannot open a Deployment, does
not know what `store.Deploy` is, and has no next step. The developer
detail belongs in `details`, which the CLI shows on request, and the
`message` should be one fixed sentence per code.

This leak has four mechanical shapes, and a mechanical shape is a gate:

| Tell | Example | What passes |
|---|---|---|
| Go import path | `latere.ai/x/pkg/httpjson` | a URL: `https://platform.latere.ai/docs` is a page the user opens |
| package-qualified identifier | `store.Deploy`, `pgx.ErrNoRows` | a sentence break: `Wait for it. Then retry.` |
| Kubernetes kind and object name | `Deployment insula-p-3f2a/api`, `pods "api-7d9c"` | a kind in prose: `Service unavailable` |
| file path | `/etc/latere/config.yaml`, `~/.config/latere/token`, `handler.go:42` | a bare file the user edits: `latere.yaml` |

The tells the gate cannot see stay with the review: a hint with no action,
`something went wrong`, an internal code in the sentence, an interpolated
underlying error.

## The decision

The gate is `registers`, in the binary with the rest of the bar, and it
holds the four principles of [[001-gate-principles]]:

- **D1**: it parses source with `go/parser` and reads `go.mod` for the
  module path. No toolchain call, no network; it runs from the module
  cache.
- **D2**: which functions are user surfaces is the one fact no shared rule
  can know, so the repository names them in `.lateregate.yaml`:

  ```yaml
  registers:
    user_surfaces: [internal/api.WriteError, cmd/latere.errorf]
  ```

  A surface is a package path, relative to the module or in full, and a
  function name. `Load` rejects an entry it cannot split into the two.
- **D3**: the gate applies only when the key names a function. This is
  opt-in rather than a waiver: a repository with no key has not decided
  anything, and a check that fires on a repository whose user surfaces
  nobody named would flag whatever function happened to share a name.
  Once opted in, the only way out is `waive`, with a reason and a date.
- **D4**: a named surface that no file calls fails the gate. A typo in the
  name would otherwise turn the check off silently, which is the failure
  this repository exists to refuse. A tree with no non-test Go file fails
  too.

### What is matched

A call is a surface when its callee is `pkg.Func` and `pkg` is the local
name of an import whose path is the surface's package, or when it is the
bare `Func` inside the surface's own package. The import's local name is
the explicit alias or the last path element; a package whose name differs
from its directory is bound under the wrong default and is not seen. The
scan reads no type information, so a method (`s.Fail(...)`) or a function
passed as a value is not seen either. Naming the package-level function
the user's output goes through covers every repository surveyed; type
resolution is the extension if a repository needs a method.

Every string literal under a matched call's arguments is read, so a literal
built by concatenation or passed through `fmt.Sprintf` is read where it
sits. Test files, `testdata`, `node_modules`, `.git`, `.claude`, and the
`skip` list are not entered.

### What is reported

One line per finding, in the developer register because a gate's output
is a developer surface:

```
  internal/handler/deploy.go:42: Kubernetes object "Deployment insula-p-3f2a/api" in a string passed to internal/api.WriteError
lateregate: 1 developer sentence(s) on a user surface; move the detail to the details field or the log line, and keep the message in the user's terms
```

A pass names how many calls it read across how many files, so a pass over
two calls and a pass over two hundred are distinguishable.

### Alternatives considered

A golangci-lint linter through `golangci.extra`. golangci-lint has no way
to load a custom analyzer without a plugin build, and `extra` merges
configuration for the linters it already ships; none of them reads string
literals by callee. A `forbidigo` pattern over the whole tree would flag
the same text in a log line, which is where it belongs.

The pre-commit hook. The hook holds the file scans, and this is one, but a
finding here is a sentence to rewrite rather than a formatter to run, and
the surfaces are read from the same config the full gate reads. It can
join the hook once a repository has opted in and asked for it; the full
gate is the bar.

## Acceptance criteria

- `lateregate list` on a repository without `registers.user_surfaces`
  shows `SKIP registers registers.user_surfaces names no function`.
- Each of the four tells, in a literal passed directly, through
  `fmt.Sprintf`, through concatenation, and through a renamed import, is
  reported with the file, the line, the tell, the text, and the surface.
- Each of the four look-alikes in the table above passes.
- A surface written as a full import path resolves to the same calls as
  the relative form.
- An unqualified call inside the surface's package is the surface; a
  same-named function in another package is not.
- A surface no file calls fails naming the surface.
- `internal/registers` clears the 90% floor.

## Outcome

Shipped as written. `internal/registers` carries the scan and the tells,
`config.Registers` the section with `Surfaces()` and the load-time shape
check, and the bar entry applies on the key. Coverage of the new package
is 94.8%. No repository opts in yet; the rollout names the surfaces per
repository in its own pass.
