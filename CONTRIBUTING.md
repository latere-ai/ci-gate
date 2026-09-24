# Contributing

This file is for people changing `lateregate` itself. Using it in a
repository is the [README](README.md) and [`docs/`](docs/).

## Getting set up

You need Go at the version `go.mod` names, `git`, and, for the TypeScript
enum analyzer, Node 24 with npm.

```sh
make hooks                 # git config core.hooksPath .githooks
go test ./...              # the Go suite
go tool lateregate         # the whole bar, run on this repository by the binary it pins
npm ci && npm run test:enums   # the TypeScript enum analyzer's fixtures and coverage floor
```

The repository runs its own gates: `go.mod` pins
`latere.ai/x/ci-gate/cmd/lateregate` as a tool, and `.lateregate.yaml`
holds this repository's decisions. A change to a gate is therefore
checked against the gate as released, and the change's own effect shows
when you run `go run ./cmd/lateregate` from the working tree.

## Where things live

| Path | What it holds |
| --- | --- |
| `cmd/lateregate` | flag parsing and command dispatch, nothing else |
| `internal/bar` | the gate list, the plan, folding and waivers, the summary |
| `internal/config` | `.lateregate.yaml`: the schema, the defaults, and load-time validation |
| `internal/gates` | the recipes and file-scan gates, the hooks, the `suite` run and the `tempdir` sandbox |
| `internal/cover` | the per-package floor and the union of profiles |
| `internal/golangci` | the rendered linter config and the pre-push lint |
| `internal/contract` | `contract` and `init` |
| `internal/changelog` | `release-notes`, the pre-push tag check, `release` and its stamps |
| `internal/greencut` | the CI guard `release` runs before a cut |
| `internal/identity`, `internal/postgres`, `internal/license`, `internal/registers`, `internal/speclint`, `internal/depcheck`, `internal/enumcheck` | one gate each |
| `internal/tsenum`, `internal/enumsetup` | the TypeScript enum analyzer and the dependency preparation it needs |
| `specs/` | the design record: why each gate is shaped the way it is |

## What makes something a gate

[`specs/001-gate-principles.md`](specs/001-gate-principles.md) holds the
four decisions every gate here keeps: it runs on a laptop, its
configuration lives in the consuming repository, every exception carries
a reason, and nothing passes because it measured nothing. A new gate
starts as a spec with testable acceptance criteria, and the spec index is
held to the specs by this repository's own `spec-lint`.

A bug fix carries a test that fails without it. A gate's failure message
says what to do next, in the words of the person running the bar.

## Writing

Every sentence the tool emits is written for one reader, and the register
follows the reader: the maintainer running the bar reads the findings and
the docs, the contributor reads specs, package documentation and commit
messages. The rule and the review checklist are
[`docs/writing/registers.md`](https://github.com/latere-ai/pkg/blob/main/docs/writing/registers.md)
in `latere.ai/x/pkg`.

## Releasing

Write under `## Unreleased` in `CHANGELOG.md` as work lands, saying what
changed for whoever runs the bar. Then:

```sh
go tool lateregate release vX.Y.Z
```

The tag runs `.github/workflows/release.yml`, which publishes the GitHub
release from the changelog section through `latere-ai/ci`'s
`notes-release.yml`. Consuming repositories move to the release by
bumping their `go.mod` pin; nothing moves them automatically. A change
that alters a gate's verdict on existing trees is worth a line in the
section saying what a repository has to do.

## Reporting a vulnerability

Do not open an issue. Report through GitHub private vulnerability
reporting on this repository, or by email to security@latere.ai; the
[organization's security policy](https://github.com/latere-ai/.github/blob/main/SECURITY.md)
applies.
