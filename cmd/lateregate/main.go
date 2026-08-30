// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Command lateregate runs the per-push quality gates every Latere Go
// repository shares.
//
// Each gate is a subcommand, and every repository configures them in one
// .lateregate.yaml at its root. The binary is pinned through go.mod as a tool
// dependency, so a gate runs identically on a laptop and on a runner. That is
// the property the whole design exists for: a gate you can only run in CI
// tells you too late.
//
//	go get -tool latere.ai/x/ci-gate/cmd/lateregate
//	go tool lateregate cover
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/cover"
	"latere.ai/x/ci-gate/internal/depcheck"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
	"latere.ai/x/ci-gate/internal/license"
	"latere.ai/x/ci-gate/internal/speclint"
)

const usage = `lateregate runs the shared per-push gates for a Go repository.

Usage:
	lateregate <command> [flags]

Commands:
	cover       gate coverage per package against the configured floor
	spec-lint   check that the spec tree describes itself consistently
	hermetic    run the test suite with only the toolchain on PATH
	fmt-check   fail if any Go source is not gofmt-formatted
	modernize   fail on code the standard library already covers
	depcheck    fail when a build reaches a dependency nobody admitted
	cgo-free    fail on any Go file that imports \"C\"
	otel-client fail on an outbound HTTP client with no tracing transport
	license     fail on a source file without the declared SPDX notice
	golangci    render the shared .golangci.yml (generated, gitignored)
	tempdir     run the suite against an empty TMPDIR and fail on what survives

Every command reads .lateregate.yaml from -C (default: the working
directory). A missing file means defaults, so a repository can adopt the
required gates without writing config.

tempdir takes the command to watch after --, which overrides tempdir.command:

	lateregate tempdir -- go test -tags corpus ./...
`

// profileList collects a repeatable -profile flag, so a repository whose
// coverage is split across test tiers gates on the merged figure rather than
// on whichever tier ran last.
type profileList []string

func (p *profileList) String() string { return strings.Join(*p, ",") }

func (p *profileList) Set(v string) error {
	if v == "" {
		return fmt.Errorf("a profile path cannot be empty")
	}
	*p = append(*p, v)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "lateregate:", err)
		os.Exit(1)
	}
}

func run(argv []string, out io.Writer) error {
	if len(argv) == 0 {
		_, _ = fmt.Fprint(out, usage)
		return fmt.Errorf("no command given")
	}
	cmd, argv := argv[0], argv[1:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		_, _ = fmt.Fprint(out, usage)
		return nil
	}

	fs := flag.NewFlagSet("lateregate "+cmd, flag.ContinueOnError)
	fs.SetOutput(out)
	root := fs.String("C", ".", "repository root holding "+config.Name)
	goBin := fs.String("go", "go", "Go toolchain to run")
	var profiles profileList
	fs.Var(&profiles, "profile",
		"coverage profile to read (cover); repeat the flag for each test tier")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if len(profiles) == 0 {
		profiles = profileList{"coverage.out"}
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	exec := gates.OSExec(*root, out)

	switch cmd {
	case "cover":
		return cover.Run(cfg.Cover, profiles, out, cover.GoLister(*goBin, *root))
	case "spec-lint":
		return speclint.Run(cfg.Spec, *root, out)
	case "hermetic":
		return gates.Hermetic(cfg.Hermetic, *goBin, out, exec)
	case "fmt-check":
		return gates.FmtCheck(out, exec)
	case "modernize":
		return gates.Modernize(cfg.Modernize, *goBin, out, exec)
	case "license":
		return license.Run(cfg.License, *root, out)
	case "cgo-free":
		return gates.CgoFree(*root, out, cfg.CgoFree.Skip)
	case "otel-client":
		return gates.OtelClient(*root, out, cfg.OtelClient.Skip)
	case "tempdir":
		return gates.TempDir(cfg.TempDir, fs.Args(), out, exec)
	case "golangci":
		if reason, err := golangci.Own(*root, cfg); err != nil {
			return err
		} else if reason != "" {
			_, _ = fmt.Fprintf(out, "%s is this repository's own, not generated: %s\n", golangci.Name, reason)
			return nil
		}
		path, err := golangci.Write(*root, cfg, *goBin)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "wrote "+path)
		return nil
	case "depcheck":
		return depcheck.Run(cfg.Depcheck, out, depcheck.GoLister(*goBin, *root))
	default:
		_, _ = fmt.Fprint(out, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}
