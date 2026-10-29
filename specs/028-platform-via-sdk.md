---
title: A client reaches the platform through the Go SDK
status: complete
depends_on:
  - 001-gate-principles.md
  - 013-registers-gate.md
affects:
  - internal/bar/
  - internal/config/
  - internal/platformsdk/ (new)
  - README.md
  - docs/gates.md
  - docs/configuration.md
  - CHANGELOG.md
effort: medium
created: 2026-10-22
updated: 2026-10-29
author: changkun
dispatched_task_id: null
---

# A client reaches the platform through the Go SDK

## The problem

Latere's clients reach the platform's API at `api.latere.ai`: Latere
Chat's server, the `latere` command line, and Latere Studio's Go core. The
Go SDK, `latere.ai/x/sdk-go`, holds that API once: its routes, its error
envelope, its retries, its pagination, the token a call carries. The rule
is that every client goes through it.

Today each client also keeps a copy of its own. One declares the
capability prefixes as constants (`Agents = Root + "/agents"`) and builds
every route from them; one keeps a default URL per capability with the
route in it (`https://api.latere.ai/v1/apps`); one writes a route literal
beside an SDK call. A copy drifts from the SDK when the API changes, and
the drift shows as a call that fails against the platform, not as a build
that fails. Review does not hold the line: a route written by hand reads
as correct, because on the day it is written it is.

## The decision

A gate, `platform-via-sdk`, that a client turns on in `.lateregate.yaml`
once its calls go through the SDK:

```yaml
platform_via_sdk:
  enabled: true
```

Off by default, because the rule is a client's. A service that serves the
routes, and the SDK itself, write them for a reason, and a repository that
has not moved yet would fail on every call it makes.

### What is matched

A **platform route** is a path that starts with `/v1/` and a segment the
platform origin serves: `account`, `agents`, `apps`, `connections`,
`environments`, `evals`, `models`, `parsing`, `repos`, `shares`, `storage`
or `web`, followed by the end, a `/`, a `?` or a part the scan cannot
know. The list is the origin's mount table. A path under `/v1/` with any
other first segment is not a platform route: two of the clients talk to a
local device service whose own routes are `/v1/status`, `/v1/policy` and
`/v1/sessions/...`.

A **string names a platform route** when its value starts with one: as a
path, after a scheme and the host `api.latere.ai`, or after a part the
scan cannot know, such as a base URL read from configuration. A value
that starts with anything else names no call, however much of a route it
mentions, so a help text that names the default URL and an error message
that names the path it failed on pass. A URL on another host passes.

The value is folded from the source, with no type information:

- literals, `+`, parentheses, and `string(...)`;
- the module's own package-level string constants, in any package, through
  the file's imports and a renamed import alike, and a constant's implicit
  repetition in a `const` block;
- `fmt.Sprintf`, `Errorf`, `Printf` and `Fprintf` with a constant format,
  each `%s` and `%v` filled by its argument's value.

A part the source does not fix, a variable, a call's result, any other
verb, is written `{}` in the route a finding prints. A name declared
inside the function, a parameter or a local variable, shadows a package
constant of the same name and is a part the source does not fix.

A **request to the platform** is a call of `net/http`'s `NewRequest`,
`NewRequestWithContext`, `Get`, `Head`, `Post` or `PostForm`, or a
`url.URL` literal, whose URL is a platform route, or names the host
`api.latere.ai` on any path. A bare base URL, `"https://api.latere.ai"`,
in a string handed to the SDK's configuration is not one.

Each string is read once, as part of the outermost expression that holds
it: a concatenation is one finding, a request is one finding and not a
second for its URL's literal, and a prefix constant is a finding where it
is declared and again wherever a route is built from it, since each is a
place the copy lives.

### What is not read

Test files, `testdata`, `vendor`, `.claude` and `node_modules`; the SDK's
own module, a directory whose `go.mod` declares `latere.ai/x/sdk-go` or a
module under it; the directories `platform_via_sdk.skip` names. A tree
with nothing left to read fails as a vacuous pass, per
[[001-gate-principles]].

### What is reported

```
  internal/chat/sessions.go:42: platform route /v1/agents/sessions in a string; use ListSessions or CreateSession in latere.ai/x/sdk-go/agents
  internal/chat/wallet.go:17: platform route /v1/account/wallet/checkout in a string; add it to latere.ai/x/sdk-go
  internal/api/client.go:12: http.NewRequestWithContext to platform route /v1/account/wallet; use GetWallet in latere.ai/x/sdk-go/account
lateregate: 3 platform route(s) outside latere.ai/x/sdk-go; make each call through the SDK, and add a call it does not make yet to the SDK
```

The instead-clause comes from a table in the gate of the routes the SDK's
calls make, keyed by the route as a finding prints it, with a list's
iterator and its Page form alike. A capability's base path names its
package, and so does a base followed by a part the source does not fix,
such as a base constant with a path a caller passes: every call under
the base is the package's. A route whose parameter is a kind, such as an
evals definition's, names the calls of every kind it can be. The table
is the SDK's v0.7.0 surface. A route it does not have is named as one to
add, which is wrong only for a call the SDK added after the table was
written, so it is refreshed when an SDK release adds calls.

### Exceptions

`platform_via_sdk.allow` maps a route prefix, as a finding prints it, to
the reason it is not a call to the platform. Load rejects an empty
reason, per [[001-gate-principles]]. An entry covers its route and the
routes under it at a segment boundary. An entry that no finding needs
fails the gate, as an unreached surface fails `registers`
([[013-registers-gate]]): an exception nothing uses is where the next
real call hides.

There is no line marker. No gate here has an inline suppression, and
every exception the tool knows lives in `.lateregate.yaml` beside its
reason. A marker would be the first, and is a decision of its own.

### Alternatives considered

- **Type information.** `go/packages` would give constant values for
  free, with exact scoping. It also needs a build: a desktop client's core
  imports `"C"` and builds for one operating system, so on a Linux runner
  a type-checked load drops the files a tag or cgo excludes, and those are
  files a route can sit in. The parse reads every file, needs only the
  source, and costs nothing on a laptop, as `otel-client` and `registers`
  do. What it gives up is a shadowing rule finer than the function.
- **Data flow from the configured base URL.** A request built on
  `c.BaseURL + path` with `path` a parameter is a call to the platform
  that the scan cannot see at the request. It sees the route where the
  caller wrote it, which is where a fix goes anyway. Following values
  through variables is a type-aware analysis with a much larger surface,
  for no finding the strings do not already give.
- **Any `/v1/` path.** Flags every local service that versions its API,
  which on these clients is the device service on every call.

## Acceptance criteria

- `lateregate list` on a repository without `platform_via_sdk.enabled`
  shows `SKIP platform-via-sdk platform_via_sdk.enabled is not set`; with
  it, the gate runs.
- A route in a literal, a concatenation, a prefix constant declared in
  another package, a `fmt.Sprintf`, a `url.URL` with a `Path`, and the URL
  of a `net/http` request is reported once, with the file, the line, the
  route with `{}` for what the source does not fix, and the SDK call or
  "add it to latere.ai/x/sdk-go".
- A request or a `url.URL` addressed to `api.latere.ai` on a path the
  source does not fix is reported.
- An allowed route passes and is counted; an allowance with no reason
  fails the load; an allowance no finding needs fails the gate.
- Test files, the SDK's module, nested or vendored, and the pruned and
  skipped directories are not read; a tree with nothing else fails.
- These pass: a local service's `/v1/status`, `/v1/agents` on another
  host, a sentence that mentions a route, a base URL with no path, a
  dialect door under another prefix (`/openai/v1/models`), and a name
  that shadows a route constant.
- `internal/platformsdk` clears the 90% floor.

## Outcome

Shipped as written. `internal/platformsdk` carries the scan, the folding
and the route and call tables; `config.PlatformViaSDK` the section and
its reason rule; the bar entry applies on `enabled`. Coverage of the new
package is 95.3%.

No client turns it on yet. Run by name against each client's main on
2026-10-22, before any has moved onto the SDK: Latere Chat 89 findings,
the command line 18, the Studio core 3.

The call table moved to the SDK's v0.7.0 surface the same day, which adds
a call for every route the clients sent directly. Run again against each
client's main: Latere Chat 89, the command line 18, the Studio core 2,
one of its routes already moved. Every finding names its call but four,
the command line's and the Studio core's `/v1/environments`, which reach
environments through the environments core's own client rather than the
SDK. A client that turns the gate on writes that route in
`platform_via_sdk.allow` with the reason, or the SDK gains the calls.

On 2026-10-29 the table gained the routes under `/v1/account/memory`,
each named with the call of the SDK's `memory` package that makes it, as
the platform's account document lists them, and the memory base followed
by a path the source does not fix, which names the package.
