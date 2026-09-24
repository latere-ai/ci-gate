# ci-gate

The per-push quality bar Latere's Go repositories share, as one binary,
`lateregate`, that a repository pins in `go.mod` and runs with no
arguments. It decides which gates apply by reading the tree, runs all of
them, and reports every failure in one pass. The same binary runs in the
pre-commit and pre-push hooks, on a laptop, and in CI, so a gate cannot
pass in one place and fail in another.

[![CI](https://github.com/latere-ai/ci-gate/actions/workflows/ci.yml/badge.svg)](https://github.com/latere-ai/ci-gate/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/latere-ai/ci-gate)](https://github.com/latere-ai/ci-gate/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/latere-ai/ci-gate)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

This repository owns what each gate asserts.
[`latere-ai/ci`](https://github.com/latere-ai/ci) owns the GitHub Actions
workflows that run the binary on a runner and cut releases. The two
version independently: a repository takes new gates by bumping its
`go.mod` pin, and a new pipeline when the `@v1` tag it calls moves.

## Adopt it

In a Go module:

```sh
go get -tool latere.ai/x/ci-gate/cmd/lateregate
go tool lateregate init
```

`init` writes the wiring the bar expects, where it is missing: the CI
caller at `.github/workflows/ci.yml`, the two git hooks under
`.githooks/` (and `git config core.hooksPath .githooks`), two lines in
`.gitignore`, and a `CHANGELOG.md` with an `## Unreleased` heading. It
never overwrites a changelog or a pre-push hook that exists, and never
touches `Makefile` or `.lateregate.yaml`.

Then write the two decisions the tool cannot make for you in
`.lateregate.yaml`:

```yaml
identity:
  role: none        # a library or a tool; a service declares its own role
license:
  spdx: MIT
  holder: Your Name
```

Stamp the notice on every file, check the wiring, and run the bar:

```sh
go tool lateregate license -w   # writes the SPDX notice where it is missing
go tool lateregate contract     # "in shape", or every drift named
go tool lateregate              # the whole bar
```

Commit the result. A release workflow is a separate caller, chosen by what
the repository ships; the [ci README](https://github.com/latere-ai/ci#readme)
lists them.

## The bar

`lateregate` with no arguments runs every gate that applies, does not
stop at the first failure, and puts the summary last:

```
PASS fmt-check
PASS modernize
SKIP spec-lint    tracks no specs/ files
FAIL suite        the suite's coverage is under the floor: below 90%: internal/store 71.2%, ...
FOLD test         into suite
FOLD race         into suite
FOLD hermetic     into suite
FOLD tempdir      into suite
FOLD cover        into suite
lateregate: 1 of 13 gates failed: suite
```

| Gate | What it asserts |
| --- | --- |
| `fmt-check` | no tracked Go source is unformatted |
| `modernize` | no code that a standard library call already covers |
| `cgo-free` | no Go file imports `"C"` |
| `otel-client` | no outbound HTTP client is built without a tracing transport |
| `license` | every source file carries the declared SPDX notice |
| `spec-lint` | the spec tree agrees with itself and with its index |
| `depcheck` | no build reaches a dependency nobody admitted |
| `registers` | no developer sentence in a string handed to a user-facing function |
| `identity` | the repository holds the rules of the identity role it declares |
| `postgres` | the repository holds the Postgres connection role it declares |
| `enum-go`, `enum-typescript` | declared enum domains use named members and exhaustive switches |
| `lint` | golangci-lint at a pinned version, against a shared config rendered on every run |
| `vuln` | govulncheck at a pinned version finds no reachable vulnerability |
| `suite` | `go vet`, then one run of the test suite that is race-clean, over a per-package coverage floor, leaves nothing under `TMPDIR`, and needs only the Go toolchain on `PATH` |

A gate that does not apply is skipped with the reason: `spec-lint` needs a
`specs/` directory, `depcheck`, `registers` and the enum gates need a key
naming what to check. `test`, `race`, `hermetic`, `tempdir` and `cover`
are the five properties `suite` checks in one run, and each still runs by
name for isolating one of them. [`docs/gates.md`](docs/gates.md) is the
reference: what each gate checks, how it fails, and how to configure it.

`go tool lateregate list` prints the plan without running it, and `go
tool lateregate <gate>` runs one gate.

## Waivers and configuration

A gate that applies runs unless the repository has written down that it
is behind, and until when:

```yaml
waive:
  cover:
    reason: the tree is at 82.2% and the gap is in handler and runner
    until: 2026-11-15
```

Both fields are mandatory. After the date the gate runs and fails on its
own terms.

Everything else in `.lateregate.yaml` is a decision with its reason:
a coverage exemption, a spec vocabulary, a dependency allowlist. The tool
decides every other value by default, rejects unknown keys, and reports a
key that restates its default. [`docs/configuration.md`](docs/configuration.md)
is the full reference.

## Documentation

| | |
|---|---|
| [Gates](docs/gates.md) | every gate: what it asserts, when it applies, how it fails, its keys |
| [Commands](docs/commands.md) | every subcommand and flag, the wiring `contract` checks, and the two hooks |
| [Configuration](docs/configuration.md) | `.lateregate.yaml`: defaults, waivers, and every key |
| [Releasing](docs/releasing.md) | the changelog rule, `lateregate release`, stamps, and the CI guard before a cut |

## In CI

`init` writes the caller. It is the whole CI configuration for the bar:

```yaml
jobs:
  gate:
    uses: latere-ai/ci/.github/workflows/lateregate.yml@v1
```

The reusable workflow asks the binary for its plan (`lateregate list
-json`) and runs one job per gate that runs, so a repository never lists
its gates in YAML. Its inputs and runner options are in the
[ci README](https://github.com/latere-ai/ci#readme).

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) is how to build, test and release
this tool. The design and the reasoning behind each gate are in
[`specs/`](specs/README.md).

## License

MIT. See [`LICENSE`](LICENSE).
