// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Command lateregate is the per-push quality bar every Latere Go repository
// shares, as one binary.
//
// Run with no arguments it runs every gate that applies to the repository it
// is in and reports all of them. The binary is pinned through go.mod as a
// tool dependency, so the bar is the same on a laptop and on a runner, and
// there is nothing per repository to write for the gates themselves.
//
//	go get -tool latere.ai/x/ci-gate/cmd/lateregate
//	go tool lateregate
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/bar"
	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/contract"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
)

const usage = `lateregate is the shared per-push quality bar for a Go repository.

Usage:
	lateregate                 run every gate that applies, report all, fail on any
	lateregate check           the same, by name
	lateregate list [-json]    print each gate with run / skip / waived and why
	lateregate <gate>          run one gate
	lateregate contract        report every way the wiring drifted from the shared shape
	lateregate init            write the wiring: workflow caller, hook, gitignore lines
	lateregate hook            the pre-commit checks over the staged Go files
	lateregate golangci        render the shared .golangci.yml without linting

Gates, in the order check runs them:
GATES
Every command reads .lateregate.yaml from -C (default: the working
directory). A missing file means defaults: a repository adopts the whole bar
without writing config. The file holds decisions, each with a reason: a
coverage exemption, a spec vocabulary, a licence, a dated waiver.

cover takes -profile for a repository that runs its own tiers; otherwise it
collects one. tempdir takes the command to watch after --:

	lateregate cover -profile=out/unit.out -profile=out/integration.out
	lateregate tempdir -- go test -tags corpus ./...
`

// profileList collects a repeatable -profile flag.
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

func help() string {
	var b strings.Builder
	for _, g := range bar.Gates {
		_, _ = fmt.Fprintf(&b, "\t%-12s %s\n", g.Name, g.Doc)
	}
	return strings.Replace(usage, "GATES\n", b.String(), 1)
}

func run(argv []string, out io.Writer) error {
	if len(argv) > 0 && (argv[0] == "help" || argv[0] == "-h" || argv[0] == "--help") {
		_, _ = fmt.Fprint(out, help())
		return nil
	}
	// No command is the whole bar; a leading flag belongs to it.
	cmd := "check"
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		cmd, argv = argv[0], argv[1:]
	}

	fs := flag.NewFlagSet("lateregate "+cmd, flag.ContinueOnError)
	fs.SetOutput(out)
	root := fs.String("C", ".", "repository root holding "+config.Name)
	goBin := fs.String("go", "go", "Go toolchain to run")
	asJSON := fs.Bool("json", false, "print the plan as JSON (list)")
	var profiles profileList
	fs.Var(&profiles, "profile",
		"coverage profile to read (cover); repeat the flag for each test tier")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	ctx := bar.Ctx{
		Cfg:      cfg,
		Root:     *root,
		GoBin:    *goBin,
		Out:      out,
		Exec:     gates.OSExec(*root, out),
		Profiles: profiles,
		Args:     fs.Args(),
		Now:      time.Now(),
	}

	switch cmd {
	case "check":
		return bar.Check(ctx)
	case "list":
		return bar.List(ctx, *asJSON)
	case "contract":
		return contract.Run(*root, cfg, out, ctx.Exec)
	case "init":
		return contract.Init(*root, out, ctx.Exec)
	case "hook":
		return gates.Hook(cfg.Modernize, *goBin, out, ctx.Exec)
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
	}
	if g, ok := bar.Find(cmd); ok {
		if err := bar.ValidateWaivers(cfg); err != nil {
			return err
		}
		return g.Run(ctx)
	}
	_, _ = fmt.Fprint(out, help())
	return fmt.Errorf("unknown command %q", cmd)
}
