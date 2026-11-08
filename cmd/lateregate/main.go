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
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/actionpins"
	"latere.ai/x/ci-gate/internal/bar"
	"latere.ai/x/ci-gate/internal/changelog"
	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/contract"
	"latere.ai/x/ci-gate/internal/enumsetup"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
	"latere.ai/x/ci-gate/internal/greencut"
	"latere.ai/x/ci-gate/internal/identity"
	"latere.ai/x/ci-gate/internal/license"
	"latere.ai/x/ci-gate/internal/scope"
	"latere.ai/x/ci-gate/internal/ship"
)

const usage = `lateregate is the shared per-push quality bar for a Go repository.

Usage:
	lateregate [-all]          run every gate that applies, report all, fail on any; on a
	                           laptop suite, lint, vuln and lean read only what changed
	                           since the default branch, and -all reads the whole tree
	lateregate check [-all] [-since green|REV]
	                           the same, by name; -since measures from the last green commit
	                           of the calling workflow, or from REV, instead of the default
	                           branch; on CI each gate then reads the whole tree or is skipped
	lateregate list [-json]    print each gate with run / skip / waived and why
	lateregate base -since green|REV
	                           print the commit a run measures its change from, or nothing and
	                           the reason on stderr when the whole tree must be checked
	lateregate <gate> [-since green|REV]
	                           run one gate; with -since, a gate that narrows reads the change
	lateregate e2e [-only NAME] [-on tag]
	                           run the e2e suites that run on a push, or the one named, or
	                           the ones that run on a tag
	lateregate contract        report every way the wiring drifted from the shared shape
	lateregate identity family -repos DIR [-expect FILE]
	                           check the identity blocks of every repository under DIR against each other and the issuer's client registry, and print the layer table they derive
	lateregate init            write the wiring: workflow caller, both hooks, gitignore lines
	lateregate hook            the pre-commit checks over the staged Go files
	lateregate prepush         refuse a release tag with no changelog section, then golangci-lint over the packages the push on stdin changes
	lateregate golangci        render the shared .golangci.yml without linting
	lateregate enum-typescript-prepare
	                           install configured frontend dependencies from tracked lockfiles
	lateregate release-notes TAG [REF]
	                           print the CHANGELOG.md section for TAG, read at REF (default: the working tree), or fail
	lateregate release [-force-red] VERSION
	                           refuse while CI is red or HEAD is not origin's default branch, then move the notes under "## Unreleased" into a section for VERSION, commit, tag, push both atomically
	lateregate build           the whole bar, then the deploy image built for the cluster's platform (a ship repository)
	lateregate push            build the image from the release tag HEAD sits on and publish it (a ship repository)
	lateregate deploy [VERSION]
	                           roll the newest stable tag, or VERSION, out to the cluster and run the live check (a ship repository)
	lateregate deploy-credential -context NAME
	                           store the rollout identity's kubeconfig as the 1Password Document deploy reads; once per repository

Gates, in the order check runs them:
GATES
Every command reads .lateregate.yaml from -C (default: the working
directory). A missing file means defaults: a repository adopts the whole bar
without writing config. The file holds decisions, each with a reason: a
coverage exemption, a spec vocabulary, a license, a dated waiver.

cover takes -profile for a repository that runs its own tiers; otherwise it
collects one. tempdir takes the command to watch after --. license -w writes
the declared notice on every file that has none, and action-pins -w moves
every listed third-party action to the organization's commit:

	lateregate cover -profile=out/unit.out -profile=out/integration.out
	lateregate tempdir -- go test -tags corpus ./...
	lateregate license -w
	lateregate action-pins -w
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

// shipDeps wires the ship commands to the machine; the tests replace it.
var shipDeps = ship.New

// stderr is where base says why the whole tree must be checked.
var stderr io.Writer = os.Stderr

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
	// -h prints the one usage text below, not the flag package's list.
	fs.Usage = func() {}
	root := fs.String("C", ".", "repository root holding "+config.Name)
	goBin := fs.String("go", "go", "Go toolchain to run")
	asJSON := fs.Bool("json", false, "print the plan as JSON (list)")
	write := fs.Bool("w", false, "write the declared notice on every file that has none (license), or move every listed action to the organization's commit (action-pins)")
	all := fs.Bool("all", false,
		"check the whole tree rather than what changed since the default branch (check); CI always does")
	since := fs.String("since", "",
		"measure the change from `green`, the last green commit of the calling workflow, or from a git revision (check, list, base)")
	forceRed := fs.Bool("force-red", false,
		"cut over a red CI, printing what it overrides (release); the maintainer's escape hatch, and in no pipeline")
	kubeContext := fs.String("context", "", "kubeconfig context of a cluster administrator (deploy-credential)")
	only := fs.String("only", "", "run the one e2e suite of this name, whatever it runs on (e2e)")
	on := fs.String("on", "", "run the e2e suites that run on this event, push or tag (e2e); push when unset")
	var profiles profileList
	fs.Var(&profiles, "profile",
		"coverage profile to read (cover); repeat the flag for each test tier")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(out, help())
			return nil
		}
		return err
	}

	cfg, err := config.Load(*root)
	if err != nil {
		return err
	}
	// A waiver names a gate. One that names nothing is a typo or a gate that
	// was renamed, and every command refuses it. check, release and build
	// refuse it when they plan the bar, under their own refusal line, and
	// release checks the checkout against origin before that.
	switch cmd {
	case "check", "release", "build":
	default:
		if err := bar.ValidateWaivers(cfg); err != nil {
			return err
		}
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
		Only:     *only,
		On:       *on,
	}

	switch cmd {
	case "base":
		if *since == "" {
			return fmt.Errorf("usage: lateregate base -since green|REV")
		}
		sha, whole := resolveBase(*since, *root, ctx.Exec)
		if sha == "" {
			_, _ = fmt.Fprintln(stderr, "lateregate: checking the whole tree: "+whole)
			return nil
		}
		_, _ = fmt.Fprintln(out, sha)
		return nil
	case "license":
		if *write {
			return license.Write(cfg.License, *root, out, time.Now())
		}
	case "action-pins":
		if *write {
			files, err := gates.ActionFiles(ctx.Exec)
			if err != nil {
				return err
			}
			return actionpins.Write(*root, files, out)
		}
	case "check":
		// check takes flags and nothing else. A word left over is nearly
		// always a command that came after a leading flag, which parsing
		// stops at; running the bar for it would report a pass for a
		// command that never ran.
		if args := fs.Args(); len(args) > 0 {
			return fmt.Errorf("check takes no argument, but got %q; a command comes before its flags, for example `lateregate release -C DIR v1.2.3`", args[0])
		}
		ctx.Scope = measureFrom(ctx, *since, *all)
		return bar.Check(ctx)
	case "list":
		// A plan is the whole tree's unless a base was named: the pipeline
		// asks for the change only through -since.
		if *since != "" {
			ctx.Scope = measureFrom(ctx, *since, *all)
		}
		return bar.List(ctx, *asJSON)
	case "enum-typescript-prepare":
		return enumsetup.Prepare(cfg.Enums.TypeScript, *root, out, ctx.Exec)
	case "contract":
		return contract.Run(*root, cfg, out, ctx.Exec)
	case "identity":
		// `identity family` reads a directory of checkouts rather than this
		// repository, so it carries its own flags; `identity` alone is the
		// gate, and falls through to the gate dispatch below.
		if args := fs.Args(); len(args) > 0 && args[0] == "family" {
			sub := flag.NewFlagSet("lateregate identity family", flag.ContinueOnError)
			sub.SetOutput(out)
			repos := sub.String("repos", "", "directory holding one checkout per repository of the family")
			expect := sub.String("expect", "", "the committed layer table the blocks must derive")
			if err := sub.Parse(args[1:]); err != nil {
				return err
			}
			if *repos == "" {
				return fmt.Errorf("usage: lateregate identity family -repos DIR [-expect FILE]")
			}
			return identity.Family(*repos, *expect, out)
		}
	case "init":
		return contract.Init(*root, cfg, out, ctx.Exec)
	case "hook":
		// Outside a module there is no local prefix; goimports then only
		// separates the standard library, which is still the linter's rule.
		module, _ := golangci.ModulePath(*goBin, *root)
		return gates.Hook(cfg, *root, module, *goBin, out, ctx.Exec)
	case "prepush":
		// Git hands the refs over once. The changelog check runs first
		// because it is a file read and the lint is not, so a tag with no
		// note is refused in under a second.
		refs, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading the pushed refs: %w", err)
		}
		if err := changelog.Prepush(*root, bytes.NewReader(refs), out, ctx.Exec); err != nil {
			return err
		}
		return golangci.Prepush(*root, cfg, *goBin, bytes.NewReader(refs), out, ctx.Exec, time.Now())
	case "release-notes":
		args := fs.Args()
		if len(args) < 1 || len(args) > 2 {
			return fmt.Errorf("usage: lateregate release-notes TAG [REF]")
		}
		ref := ""
		if len(args) == 2 {
			ref = args[1]
		}
		notes, err := changelog.Notes(*root, args[0], ref, ctx.Exec)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(out, notes)
		return nil
	case "release":
		if len(fs.Args()) != 1 {
			return fmt.Errorf("usage: lateregate release [-force-red] vX.Y.Z")
		}
		// The guard runs before the bar: a red default branch is three API
		// reads, the bar is a full instrumented suite, and paying for the
		// second to learn the first is the wrong order.
		// A repository that ships from a workstation runs no CI to read.
		if cfg.Release.Green() && !cfg.Ships() {
			guard := &greencut.Guard{
				Root: *root,
				// Every runner sets GITHUB_API_URL, and an enterprise
				// install points it at its own host.
				API:   os.Getenv("GITHUB_API_URL"),
				Run:   ctx.Exec,
				Out:   out,
				Force: *forceRed,
			}
			if err := guard.Check(); err != nil {
				return fmt.Errorf("not releasing %s: %w", fs.Args()[0], err)
			}
		}
		// A release lands as one fast-forward of the release branch, so a
		// checkout that is not origin's tip of it, a version origin already
		// has or has passed, and an empty Unreleased are refused here, in
		// seconds, rather than by the cut after the whole bar. The cut asks
		// again before it writes, because the bar takes minutes.
		if err := changelog.Ready(*root, fs.Args()[0], ctx.Exec); err != nil {
			return fmt.Errorf("not releasing %s: %w", fs.Args()[0], err)
		}
		// A release is the one push that must never go out red: the whole
		// bar runs on the tree about to be tagged, and a failure stops the
		// cut before the changelog moves or the tag exists.
		// The version is this command's argument, not a gate's: tempdir
		// reads its test command from Args, so the bar runs with none.
		check := ctx
		check.Args = nil
		if err := bar.Check(check); err != nil {
			return fmt.Errorf("not releasing %s: %w", fs.Args()[0], err)
		}
		if err := changelog.Cut(*root, fs.Args()[0], time.Now(), out, ctx.Exec, cfg.Release.Stamp); err != nil {
			return err
		}
		if cfg.Ships() {
			if err := ship.PublishRelease(shipDeps(*root, cfg, out), fs.Args()[0]); err != nil {
				return fmt.Errorf("%s is tagged and pushed, but its GitHub release was not published; `lateregate deploy` publishes it: %w", fs.Args()[0], err)
			}
		}
		return nil
	case "build":
		if !cfg.Ships() {
			return fmt.Errorf("%s has no ship block: this repository does not build and ship from a workstation", config.Name)
		}
		// The same bar a release runs, over the whole tree.
		check := ctx
		check.Args = nil
		if err := bar.Check(check); err != nil {
			return fmt.Errorf("not building: %w", err)
		}
		return ship.Build(shipDeps(*root, cfg, out))
	case "push":
		return ship.Push(shipDeps(*root, cfg, out))
	case "deploy":
		if len(fs.Args()) > 1 {
			return fmt.Errorf("usage: lateregate deploy [vX.Y.Z]")
		}
		version := ""
		if len(fs.Args()) == 1 {
			version = fs.Args()[0]
		}
		return ship.Deploy(shipDeps(*root, cfg, out), version)
	case "deploy-credential":
		return ship.DeployCredential(shipDeps(*root, cfg, out), *kubeContext)
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
		// A gate run by name reads the whole tree unless it was given a base,
		// as list does.
		if *since != "" {
			ctx.Scope = measureFrom(ctx, *since, *all)
		}
		return g.Run(ctx)
	}
	_, _ = fmt.Fprint(out, help())
	return fmt.Errorf("unknown command %q", cmd)
}

// isCI reports whether this is a CI run: CI is set to anything but empty,
// false or 0, which is how a shell turns it off.
func isCI() bool {
	switch os.Getenv("CI") {
	case "", "false", "0":
		return false
	}
	return true
}

// measureFrom is the change a check reads. CI checks the whole tree unless
// it was given a base, because the default branch is held to the whole bar
// and a laptop is what narrows to the change; with a base, CI skips the
// gates that cannot have moved. -all wins over any base.
func measureFrom(c bar.Ctx, since string, all bool) *scope.Scope {
	ci := isCI()
	switch {
	case all:
		return nil
	case since != "":
		sha, whole := resolveBase(since, c.Root, c.Exec)
		if sha == "" {
			return scope.WholeTree(whole)
		}
		return scope.ResolveSince(c.Root, c.GoBin, c.Exec, sha, ci)
	case ci:
		return nil
	}
	return scope.Resolve(c.Root, c.GoBin, c.Exec)
}

// resolveBase is the commit -since names, or the reason there is none.
func resolveBase(since, root string, run gates.Exec) (sha, whole string) {
	if since == "green" {
		return greencut.Green(root, run)
	}
	out, err := run(nil, false, "git", "rev-parse", "--verify", since+"^{commit}")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", fmt.Sprintf("git cannot resolve %q to a commit", since)
	}
	return strings.TrimSpace(string(out)), ""
}
