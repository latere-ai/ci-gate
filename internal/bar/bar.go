// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package bar is the gate set, and the run of all of it.
//
// The organization has one bar, so the binary that holds it is the one thing
// a repository runs: `lateregate` with no arguments runs every gate that
// applies and reports all of them. Which gates apply is decided by asking
// the tree, not by configuring a list, and the only way a gate that applies
// does not run is a waiver with a reason and a date.
package bar

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/actionpins"
	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/cover"
	"latere.ai/x/ci-gate/internal/depcheck"
	"latere.ai/x/ci-gate/internal/enumcheck"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
	"latere.ai/x/ci-gate/internal/identity"
	"latere.ai/x/ci-gate/internal/license"
	"latere.ai/x/ci-gate/internal/platformsdk"
	"latere.ai/x/ci-gate/internal/postgres"
	"latere.ai/x/ci-gate/internal/registers"
	"latere.ai/x/ci-gate/internal/scope"
	"latere.ai/x/ci-gate/internal/speclint"
	"latere.ai/x/ci-gate/internal/tsenum"
	"latere.ai/x/ci-gate/internal/words"
)

// Ctx is what a gate runs with.
type Ctx struct {
	Cfg   *config.Config
	Root  string
	GoBin string
	Out   io.Writer
	Exec  gates.Exec
	// Profiles are coverage profiles given explicitly, for a repository whose
	// coverage is split across tiers it runs itself. Empty means collect.
	Profiles []string
	// Args is whatever followed --, which tempdir reads as the command to
	// watch.
	Args []string
	Now  time.Time
	// Scope is the change a check reads, measured against the default
	// branch. Nil reads the whole tree, as CI, a release and a gate run by
	// name always do.
	Scope *scope.Scope
	// Only names the one e2e suite to run, whatever it runs on, and On the
	// event whose suites run: push when empty.
	Only string
	On   string
	// ExecIn runs commands in a directory below Root, a slash path, for the
	// gates that run in each module the configuration lists. Nil runs them
	// for real.
	ExecIn func(dir string) gates.Exec
}

// Gate is one check in the set.
type Gate struct {
	Name string
	Doc  string
	// Applies reports whether the tree has a subject for this gate, and if
	// not, why not. nil means it always does. This is not a waiver: a gate
	// with no subject has nothing to be behind on.
	Applies func(Ctx) (bool, string, error)
	Run     func(Ctx) error
}

// Gates is the set, in the order check runs them: the static scans first
// because they are seconds, the suite runs last because they are minutes and
// a formatting failure should not wait for them.
var Gates = []Gate{
	{Name: "fmt-check", Doc: "no tracked Go source is unformatted",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.FmtCheckWith(c.GoBin, c.Out, c.Exec) }},
	{Name: "modernize", Doc: "no code a standard library call already covers",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.Modernize(c.Cfg.Modernize, c.GoBin, c.Out, c.Exec) }},
	{Name: "cgo-free", Doc: "no Go file imports \"C\"",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.CgoFree(c.Root, c.Out, c.Cfg.CgoFree.Skip) }},
	{Name: "otel-client", Doc: "no outbound HTTP client without a tracing transport",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.OtelClient(c.Root, c.Out, c.Cfg.OtelClient.Skip) }},
	{Name: "container-labels", Doc: "every package that imports testcontainers-go imports reap, which labels its containers",
		// The subject is read from the tree, as spec-lint's is from git: a
		// repository whose first test starts a container is gated that day.
		Applies: func(c Ctx) (bool, string, error) {
			ok, err := gates.ImportsTestcontainers(c.Root)
			return ok, "no package imports testcontainers-go", err
		},
		Run: func(c Ctx) error { return gates.ContainerLabels(c.Root, c.Out) }},
	{Name: "license", Doc: "every source file carries the declared SPDX notice",
		Applies: tracksLicensed,
		Run:     func(c Ctx) error { return license.Run(c.Cfg.License, c.Root, c.Out) }},
	{Name: "spec-lint", Doc: "the spec tree agrees with itself and its index",
		Applies: tracksSpecs,
		Run:     func(c Ctx) error { return speclint.Run(c.Cfg.Spec, c.Root, c.Out) }},
	{Name: "depcheck", Doc: "no build reaches a dependency nobody admitted",
		Applies: func(c Ctx) (bool, string, error) {
			if len(c.Cfg.Depcheck.Packages) == 0 {
				return false, "depcheck.packages names no package", nil
			}
			return true, "", nil
		},
		Run: func(c Ctx) error { return depcheck.Run(c.Cfg.Depcheck, c.Out, depcheck.GoLister(c.GoBin, c.Root)) }},
	{Name: "registers", Doc: "no developer sentence in a string handed to a user surface",
		Applies: func(c Ctx) (bool, string, error) {
			if len(c.Cfg.Registers.UserSurfaces) == 0 {
				return false, "registers.user_surfaces names no function", nil
			}
			return true, "", nil
		},
		Run: func(c Ctx) error { return registers.Run(c.Cfg.Registers, c.Root, c.Out) }},
	{Name: "platform-via-sdk", Doc: "no platform route outside the Go SDK, " + platformsdk.SDK,
		// Opt-in: a client turns it on once its calls go through the SDK,
		// and a service, which serves the routes, never does.
		Applies: func(c Ctx) (bool, string, error) {
			return c.Cfg.PlatformViaSDK.Enabled, "platform_via_sdk.enabled is not set", nil
		},
		Run: func(c Ctx) error { return platformsdk.Run(c.Cfg.PlatformViaSDK, c.Root, c.Out) }},
	{Name: "words", Doc: "no do-not-use word in what a model or a person reads",
		// Opt-in: the list is lateregate's own, and what a model or a
		// person reads is the repository's to name.
		Applies: func(c Ctx) (bool, string, error) {
			return c.Cfg.Words.Enabled(), "words names nothing a model or a person reads", nil
		},
		Run: func(c Ctx) error { return words.Run(c.Cfg.Words, c.Cfg.Spec, c.Root, c.Out) }},
	{Name: "identity", Doc: "the repository declares its identity role and holds that role's rules",
		// No Applies: a repository with no block is precisely the gap, so
		// the absence fails inside the gate rather than skipping it.
		Run: func(c Ctx) error { return identity.Run(c.Cfg.Identity, c.Root, c.Out, c.Exec, c.Now) }},
	{Name: "postgres", Doc: "the repository's Postgres role holds: no client under none, a dated waiver under direct, the pooled and direct DSN names read under pooled",
		// No Applies: an absent role is decided from the imports inside the
		// gate, so an undeclared consumer is a finding and not a skip.
		//
		// The gate takes its own waiver because the direct role turns on one:
		// a live waiver is what makes that role pass, and this gate also runs
		// from `lateregate postgres`, which never builds a plan. A waiver the
		// plan has already expired reaches the gate too, which is what lets
		// the refusal be the postgres rule's own rather than a date.
		Run: func(c Ctx) error {
			return postgres.Run(c.Cfg.Postgres, c.Cfg.WaiverFor("postgres"), c.Root, c.Out, c.Now)
		}},
	{Name: "enum-go", Doc: "declared Go enums use named types, named members and exhaustive switches",
		Applies: func(c Ctx) (bool, string, error) {
			return len(c.Cfg.Enums.Go.Types) > 0, "enums.go.types names no domain", nil
		},
		Run: func(c Ctx) error {
			p := c.Cfg.Enums.Go
			return enumcheck.Run(enumcheck.Policy{Types: p.Types, Fields: p.Fields, Parsers: p.Parsers}, c.Root, c.Out)
		}},
	{Name: "enum-typescript", Doc: "declared TypeScript enums use named types, named members and exhaustive switches",
		Applies: func(c Ctx) (bool, string, error) {
			return len(c.Cfg.Enums.TypeScript) > 0, "enums.typescript names no project", nil
		},
		Run: func(c Ctx) error {
			projects := make([]tsenum.Project, 0, len(c.Cfg.Enums.TypeScript))
			for _, p := range c.Cfg.Enums.TypeScript {
				projects = append(projects, tsenum.Project{Project: p.Project, Types: p.Types, Fields: p.Fields, Parsers: p.Parsers})
			}
			return tsenum.Run(projects, c.Root, c.Out, c.Exec)
		}},
	{Name: "tidy", Doc: "go mod tidy would change no tracked module",
		Applies: func(c Ctx) (bool, string, error) {
			return tracks(gates.GoModules(c.Exec))("tracks no go.mod")
		},
		Run: func(c Ctx) error { return gates.Tidy(c.GoBin, c.Out, c.Exec) }},
	{Name: "actionlint", Doc: "actionlint " + gates.ActionlintVersion + " finds nothing in the workflows",
		Applies: func(c Ctx) (bool, string, error) {
			return tracks(gates.Workflows(c.Exec))("tracks no workflow under .github/workflows")
		},
		Run: func(c Ctx) error { return gates.Actionlint(c.GoBin, c.Out, c.Exec) }},
	// Like every gate here it takes the generic waiver; nothing is special-cased.
	{Name: "action-pins", Doc: "every third-party action is the organization's one commit",
		Applies: func(c Ctx) (bool, string, error) {
			return tracks(gates.ActionFiles(c.Exec))("tracks no workflow or action")
		},
		Run: func(c Ctx) error {
			files, err := gates.ActionFiles(c.Exec)
			if err != nil {
				return err
			}
			return actionpins.Run(c.Root, files, c.Out)
		}},
	{Name: "manifests", Doc: "every kustomization renders with kustomize " + gates.KustomizeVersion,
		Applies: func(c Ctx) (bool, string, error) {
			return tracks(gates.Kustomizations(c.Exec, readIn(c.Root)))("tracks no kustomization")
		},
		Run: func(c Ctx) error { return gates.Manifests(c.GoBin, c.Root, c.Out, c.Exec) }},
	{Name: "rules", Doc: "every PrometheusRule passes promtool " + gates.PromtoolVersion,
		Applies: func(c Ctx) (bool, string, error) {
			groups, err := gates.PrometheusRules(c.Exec, readIn(c.Root))
			if err != nil {
				return false, "", err
			}
			if len(groups) == 0 {
				return false, "tracks no PrometheusRule", nil
			}
			return true, "", nil
		},
		Run: func(c Ctx) error { return gates.Rules(gates.DefaultPromtool(), c.Root, c.Out, c.Exec) }},
	{Name: "frontend", Doc: "every named Bun package installs from its lockfile, then typechecks, tests, builds and runs its e2e script",
		// After the static scans: an install and a build are minutes.
		Applies: func(c Ctx) (bool, string, error) {
			if len(c.Cfg.Frontend.Projects) == 0 {
				return false, "frontend.projects names no package", nil
			}
			return true, "", nil
		},
		Run: func(c Ctx) error {
			return gates.Frontend(c.Cfg.Frontend.Projects, c.Root, c.Out, exec.LookPath, c.Exec, func(dir string) gates.Exec {
				return gates.OSExec(filepath.Join(c.Root, dir), c.Out)
			})
		}},
	{Name: "image", Doc: "every named image builds, reports its version, stays under its cap, holds no shell, and starts read-only and answers its probe",
		// After frontend: a container build is minutes too.
		Applies: func(c Ctx) (bool, string, error) {
			if len(c.Cfg.Image.Images) == 0 {
				return false, "image.images names no image", nil
			}
			return true, "", nil
		},
		Run: func(c Ctx) error {
			return gates.Image(c.Cfg.Image.Images, c.Out, exec.LookPath, c.Exec)
		}},
	{Name: "lean", Doc: "every Lake project git tracks builds with no warning, so no proof uses sorry",
		// Before lint and the suite: a machine without lake installs elan and
		// a toolchain first, which is a minute, not seconds.
		Applies: tracksLake,
		Run: func(c Ctx) error {
			return gates.Lean(gates.DefaultLeanToolchain(), c.Out, c.Exec, func(dir string) gates.Exec {
				return gates.OSExec(filepath.Join(c.Root, dir), c.Out)
			})
		}},
	{Name: "lint", Doc: "golangci-lint " + golangci.Version + " against the shared config",
		Applies: tracksGo,
		Run: func(c Ctx) error {
			return inModules(c, func() error {
				if c.Scope.Packages() {
					return golangci.LintPackages(c.Root, c.Cfg, c.GoBin, c.Out, c.Exec, patterns(c.Scope.Reached))
				}
				return golangci.Lint(c.Root, c.Cfg, c.GoBin, c.Out, c.Exec)
			}, func(dir string) error { return lintModule(c, dir) })
		}},
	{Name: "vuln", Doc: "govulncheck " + gates.VulnVersion + " finds no reachable vulnerability",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.Vuln(c.GoBin, c.Out, c.Exec) }},
	{Name: "suite", Doc: "go vet, then one run of the suite that is race-clean, over the coverage floor, leaves nothing under TMPDIR and no container behind, and needs only the toolchain on PATH",
		Applies: tracksGo,
		Run:     runSuite},
	{Name: E2E, Doc: "every e2e suite that runs on a push passes against the services and tools lateregate starts, and none skips a test",
		// After the suite: it starts containers and fetches tools, which is
		// minutes, and a failing suite is the cheaper answer.
		Applies: e2eApplies,
		Run:     runE2E},
	{Name: "test", Doc: "go vet and the suite",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.Test(c.GoBin, c.Out, c.Exec) }},
	{Name: "race", Doc: "the suite under the race detector",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.Race(c.Cfg.Race, c.GoBin, c.Out, c.Exec) }},
	{Name: "hermetic", Doc: "the suite with only the toolchain on PATH",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.Hermetic(c.Cfg.Hermetic, c.GoBin, c.Out, c.Exec) }},
	{Name: "tempdir", Doc: "the suite leaves nothing under TMPDIR",
		// A repository that names its own runner watches that runner, Go or
		// not; only the go test run it folds into needs Go to exist.
		Applies: func(c Ctx) (bool, string, error) {
			if len(c.Cfg.TempDir.Command) > 0 {
				return true, "", nil
			}
			return tracksGo(c)
		},
		Run: func(c Ctx) error { return gates.TempDir(c.Cfg.TempDir, c.Root, c.Args, c.Out, c.Exec) }},
	{Name: "containers", Doc: "the suite leaves no container behind",
		Applies: tracksGo,
		Run:     func(c Ctx) error { return gates.Containers(c.GoBin, c.Out, c.Exec) }},
	{Name: "cover", Doc: "every package clears the floor",
		Applies: tracksGo,
		Run:     runCover},
}

// Suite is the gate the suite gates fold into.
const Suite = "suite"

// folded names the gates whose property the suite run checks, so the plan
// runs the suite once instead of each of them. tempdir folds only while it
// watches go test: a repository whose tempdir.command names another runner
// has a suite the go test run is not, and keeps the gate to itself.
func folded(cfg *config.Config) map[string]bool {
	f := map[string]bool{"test": true, "race": true, "cover": true, "hermetic": true, "containers": true}
	if len(cfg.TempDir.Command) == 0 {
		f["tempdir"] = true
	}
	return f
}

// suiteRun is the suite run the configuration and the waivers ask for, and a
// note per gate whose waiver narrowed it or has run out.
//
// A live waiver on a folded gate turns its property off rather than skipping
// a job: a waived race drops -race, a waived hermetic keeps the full PATH, a
// waived tempdir runs outside the sandbox, and a waived cover keeps no floor.
// An expired one leaves the property on and says so, which is what an expired
// waiver does for a gate of its own.
func suiteRun(c Ctx) (gates.SuiteRun, []string) {
	f := folded(c.Cfg)
	run := gates.SuiteRun{
		Race:       true,
		Timeout:    c.Cfg.Race.Timeout,
		Hermetic:   true,
		Allow:      c.Cfg.Hermetic.Allow,
		Sandbox:    f["tempdir"],
		TempDir:    c.Cfg.TempDir,
		Containers: true,
		Profile:    cover.Profile,
	}
	list := cover.GoLister(c.GoBin, c.Root)
	run.Floor = func(profile string, _ gates.Tester) error {
		return cover.Run(c.Cfg.Cover, append([]string{profile}, c.Profiles...), c.Out, list)
	}
	if c.Scope.Packages() {
		// The run reads the packages the change reaches, and its profile
		// measures every package their test binaries link: the ones the
		// change touched, and the ones whose coverage only its tests can
		// have moved.
		run.Packages = importPaths(c.Scope.Reached)
		run.Floor = scopedFloor(c, list)
	}
	var notes []string
	for _, n := range []struct {
		gate, narrowed string
		off            func()
	}{
		{"race", "without -race", func() { run.Race = false }},
		{"hermetic", "with the full PATH", func() { run.Hermetic = false }},
		{"tempdir", "outside the TMPDIR sandbox", func() { run.Sandbox = false }},
		{"containers", "without watching its containers", func() { run.Containers = false }},
		{"cover", "without the coverage floor", func() { run.Profile, run.Floor = "", nil }},
	} {
		w, ok := c.Cfg.Waive[n.gate]
		if !ok || !f[n.gate] {
			continue
		}
		if w.Live(c.Now) {
			n.off()
			notes = append(notes, fmt.Sprintf("%s (%s waived until %s: %s)", n.narrowed, n.gate, w.Until, w.Reason))
			continue
		}
		notes = append(notes, fmt.Sprintf("%s waiver expired %s: %s", n.gate, w.Until, w.Reason))
	}
	return run, notes
}

// runSuite is the suite gate.
func runSuite(c Ctx) error {
	run, notes := suiteRun(c)
	for _, n := range notes {
		_, _ = fmt.Fprintln(c.Out, "suite runs "+n)
	}
	return inModules(c, func() error {
		return gates.Suite(run, c.Root, c.GoBin, c.Out, c.Exec)
	}, func(dir string) error { return suiteInModule(c, run, dir) })
}

// runCover collects a profile unless the caller supplied one.
func runCover(c Ctx) error {
	profiles := c.Profiles
	if len(profiles) == 0 {
		p, err := cover.Collect(c.GoBin, c.Root, c.Out, c.Exec)
		if err != nil {
			return err
		}
		profiles = []string{p}
	}
	return cover.Run(c.Cfg.Cover, profiles, c.Out, cover.GoLister(c.GoBin, c.Root))
}

// tracksGo asks git whether the repository has any Go to check. The gates
// that read Go code (formatting, fixers, imports, the linter, the
// vulnerability scan, the suite) have no subject in a repository whose code
// is TypeScript, Dockerfiles or shell, and a repository that carries only a
// go.mod to pin this tool has no Go either. The answer is the tracked set, as
// for fmt-check, so an untracked scratch file does not give a gate a subject.
func tracksGo(c Ctx) (bool, string, error) {
	out, err := c.Exec(nil, false, "git", "ls-files", "-z", "--", "*.go")
	if err != nil {
		// Applicability that cannot be determined must not resolve to "does
		// not apply": that is the vacuous pass this package exists to refuse.
		return false, "", fmt.Errorf("cannot list tracked Go files with git: %w\n"+
			"whether a repository has Go to check is asked of git, so this "+
			"needs to run inside a git repository", err)
	}
	if strings.TrimSpace(strings.ReplaceAll(string(out), "\x00", "")) == "" {
		return false, "tracks no Go file", nil
	}
	return true, "", nil
}

// tracksLicensed asks git whether any tracked file is of a type the license
// gate checks. The types are the repository's to declare and default to Go,
// so a repository that declares shell and Dockerfiles is gated on those
// whether or not it has Go, and one that declares nothing is gated on Go
// alone.
func tracksLicensed(c Ctx) (bool, string, error) {
	out, err := c.Exec(nil, false, "git", "ls-files", "-z")
	if err != nil {
		return false, "", fmt.Errorf("cannot list tracked files with git: %w\n"+
			"whether a repository has files to carry a notice is asked of git, so this "+
			"needs to run inside a git repository", err)
	}
	if !license.Tracks(c.Cfg.License, strings.Split(string(out), "\x00")) {
		return false, "tracks no " + strings.Join(c.Cfg.License.Checked(), "/") + " file", nil
	}
	return true, "", nil
}

// tracksSpecs asks git whether it tracks anything under the configured
// spec.dir (which defaults to specs). The answer comes from git and not from
// the presence of a spec section, so a repository cannot leave the gate by
// deleting a line of its configuration.
func tracksSpecs(c Ctx) (bool, string, error) {
	dir := c.Cfg.Spec.Dir
	if dir == "" {
		dir = config.DefaultSpecDir
	}
	out, err := c.Exec(nil, false, "git", "ls-files", "--", dir)
	if err != nil {
		// Applicability that cannot be determined must not resolve to "does
		// not apply": that is the vacuous pass this package exists to refuse.
		return false, "", fmt.Errorf("cannot list %s/ with git: %w\n"+
			"whether a repository has specs to lint is asked of git, so this "+
			"needs to run inside a git repository", dir, err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return false, "tracks no " + dir + "/ files", nil
	}
	return true, "", nil
}

// tracksLake asks git for Lake projects, as tracksSpecs asks it for specs: a
// repository whose proofs are checked in is gated the day it adds them.
func tracksLake(c Ctx) (bool, string, error) {
	dirs, err := gates.LakeProjects(c.Exec)
	if err != nil {
		return false, "", fmt.Errorf("%w\nwhether a repository has proofs to build is asked of git, "+
			"so this needs to run inside a git repository", err)
	}
	if len(dirs) == 0 {
		return false, "tracks no Lake project (lakefile.toml or lakefile.lean)", nil
	}
	return true, "", nil
}

// tracks turns a list git gave into an Applies answer: an error stays an
// error, since applicability that cannot be determined must not resolve to
// "does not apply", and an empty list skips with why.
func tracks(list []string, err error) func(why string) (bool, string, error) {
	return func(why string) (bool, string, error) {
		if err != nil {
			return false, "", err
		}
		if len(list) == 0 {
			return false, why, nil
		}
		return true, "", nil
	}
}

// readIn reads a tracked file by its path under root.
func readIn(root string) func(string) ([]byte, error) {
	return func(f string) ([]byte, error) { return os.ReadFile(filepath.Join(root, f)) }
}

// Find returns the gate by name.
func Find(name string) (Gate, bool) {
	for _, g := range Gates {
		if g.Name == name {
			return g, true
		}
	}
	return Gate{}, false
}

// Names lists the gate names in run order.
func Names() []string {
	n := make([]string, len(Gates))
	for i, g := range Gates {
		n[i] = g.Name
	}
	return n
}

// ValidateWaivers rejects a waiver keyed on a name that is not a gate. A
// waiver for a gate nobody runs hides a typo in the name of a gate somebody
// does.
func ValidateWaivers(cfg *config.Config) error {
	var bad []string
	for name := range cfg.Waive {
		if _, ok := Find(name); !ok {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s: waive names %s, which is not a gate\ngates: %s",
		config.Name, strings.Join(bad, ", "), strings.Join(Names(), ", "))
}

// Status is what the plan decided for one gate.
type Status string

const (
	// Run means the gate applies and no live waiver covers it.
	Run Status = "run"
	// Skip means the tree has no subject for the gate.
	Skip Status = "skip"
	// Waived means a dated waiver covers it, for now.
	Waived Status = "waived"
	// Folded means another gate's run checks this gate's property; Into names
	// that gate.
	Folded Status = "folded"
)

// Entry is one gate's place in the plan.
type Entry struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	// Reason is why a gate is skipped or waived, or the note that its waiver
	// has expired and it therefore runs.
	Reason string `json:"reason,omitempty"`
	Until  string `json:"until,omitempty"`
	// Into is the gate a folded gate's property runs in.
	Into string `json:"into,omitempty"`
	// Needs names the tools a running gate wants installed before it runs:
	// node, bun, and chromium:<dir> for a project whose tests drive Chromium.
	// The pipeline installs from this list rather than from its caller.
	Needs []string `json:"needs,omitempty"`
	// OS lists the runner labels the plain test also runs on, from test.os.
	// It is set on the test entry whatever its status.
	OS []string `json:"os,omitempty"`
}

// needs names the tools gate g wants when it runs.
func needs(cfg *config.Config, name string) []string {
	switch name {
	case E2E:
		return e2eNeeds(cfg)
	case "enum-typescript":
		return []string{"node", "bun"}
	case "frontend":
		n := []string{"node", "bun"}
		for _, p := range cfg.Frontend.Browser {
			n = append(n, "chromium:"+path.Clean(filepath.ToSlash(p)))
		}
		return n
	}
	return nil
}

// annotate adds what the pipeline installs and the systems the test runs on.
func annotate(cfg *config.Config, plan []Entry) {
	for i := range plan {
		e := &plan[i]
		if e.Status == Run {
			e.Needs = needs(cfg, e.Name)
		}
		if e.Name == "test" && len(cfg.Test.OS) > 0 {
			e.OS = slices.Clone(cfg.Test.OS)
		}
	}
}

// Plan decides, for every gate, whether it runs.
//
// An expired waiver does not skip the gate. It runs and fails on its own
// terms, so the reason for the work is in the gate's own output rather than
// in a date; the plan carries a note saying so.
func Plan(c Ctx) ([]Entry, error) {
	if err := ValidateWaivers(c.Cfg); err != nil {
		return nil, err
	}
	var plan []Entry
	fold := folded(c.Cfg)
	_, notes := suiteRun(c)
	for _, g := range Gates {
		if fold[g.Name] {
			e := Entry{Name: g.Name, Status: Folded, Into: Suite}
			// A property of a run that has no subject is not folded into
			// anything: it is skipped with the suite's reason.
			if g.Applies != nil {
				ok, why, err := g.Applies(c)
				if err != nil {
					return nil, err
				}
				if !ok {
					plan = append(plan, Entry{Name: g.Name, Status: Skip, Reason: why})
					continue
				}
			}
			if w, ok := c.Cfg.Waive[g.Name]; ok {
				e.Until, e.Reason = w.Until, "waived until "+w.Until+": "+w.Reason
				if !w.Live(c.Now) {
					e.Reason = "waiver expired " + w.Until + ": " + w.Reason
				}
			}
			plan = append(plan, e)
			continue
		}
		e := Entry{Name: g.Name, Status: Run}
		if g.Name == Suite {
			// A waived test is a waived suite: the suite is the test run.
			if w, ok := c.Cfg.Waive["test"]; ok && w.Live(c.Now) {
				if _, own := c.Cfg.Waive[Suite]; !own {
					e.Status, e.Reason, e.Until = Waived, "test is waived: "+w.Reason, w.Until
					plan = append(plan, e)
					continue
				}
			}
			e.Reason = strings.Join(notes, "; ")
		}
		if g.Applies != nil {
			ok, why, err := g.Applies(c)
			if err != nil {
				return nil, err
			}
			if !ok {
				e.Status, e.Reason = Skip, why
				plan = append(plan, e)
				continue
			}
		}
		if w, ok := c.Cfg.Waive[g.Name]; ok {
			// Validated at load, so it parses. It names a day and is
			// inclusive: the waiver dies when the day after it begins.
			e.Until = w.Until
			if w.Live(c.Now) {
				e.Status, e.Reason = Waived, w.Reason
			} else {
				expired := "waiver expired " + w.Until + ": " + w.Reason
				if e.Reason != "" {
					expired += "; " + e.Reason
				}
				e.Reason = expired
			}
		}
		if e.Status == Run {
			skip, note, err := scoped(c, g.Name)
			if err != nil {
				return nil, err
			}
			switch {
			case skip != "":
				e.Status, e.Reason = Skip, skip
			case note != "" && e.Reason != "":
				e.Reason += "; " + note
			case note != "":
				e.Reason = note
			}
		}
		plan = append(plan, e)
	}
	annotate(c.Cfg, plan)
	return plan, nil
}

// List prints the plan, as text or as JSON for the pipeline's matrix.
func List(c Ctx, asJSON bool) error {
	plan, err := Plan(c)
	if err != nil {
		return err
	}
	if asJSON {
		return json.MarshalEncode(jsontext.NewEncoder(c.Out), plan)
	}
	for _, e := range plan {
		_, _ = fmt.Fprintln(c.Out, line(e, "", ""))
	}
	return nil
}

// Check runs every gate the plan says to, and reports all of them.
//
// It does not stop at the first failure: a repository behind on four gates
// learns that in one run. Each gate's own output streams as it runs; the
// summary is last because it is the part a reader scrolls to.
func Check(c Ctx) error {
	plan, err := Plan(c)
	if err != nil {
		return err
	}
	if h := header(c.Scope); h != "" {
		_, _ = fmt.Fprintln(c.Out, h)
	}
	verdict := map[string]string{}
	var failed []string
	for _, e := range plan {
		if e.Status != Run {
			continue
		}
		g, _ := Find(e.Name)
		_, _ = fmt.Fprintf(c.Out, "== %s\n", g.Name)
		if err := g.Run(c); err != nil {
			// An error whose first line is empty still failed: the row says
			// so and points at the output above, where the whole error is.
			v := firstLine(err.Error())
			if strings.TrimSpace(v) == "" {
				v = "failed with no message; see the output above"
			}
			verdict[e.Name] = v
			failed = append(failed, e.Name)
			_, _ = fmt.Fprintf(c.Out, "FAIL %s: %s\n", g.Name, err)
			continue
		}
		verdict[e.Name] = ""
	}

	_, _ = fmt.Fprintln(c.Out)
	ran := 0
	for _, e := range plan {
		switch e.Status {
		case Run:
			ran++
			if v := verdict[e.Name]; v != "" {
				if e.Reason != "" {
					v += " (" + e.Reason + ")"
				}
				_, _ = fmt.Fprintln(c.Out, line(e, "FAIL", v))
			} else {
				_, _ = fmt.Fprintln(c.Out, line(e, "PASS", ""))
			}
		case Skip, Waived, Folded:
			_, _ = fmt.Fprintln(c.Out, line(e, "", ""))
		default:
			_, _ = fmt.Fprintln(c.Out, line(e, "", ""))
		}
	}
	on := ""
	if c.Scope.Narrows() {
		on = " on what changed since " + c.Scope.Since()
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d gates failed%s: %s", len(failed), ran, on, strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(c.Out, "%d gates passed%s\n", ran, on)
	return nil
}

// line renders one summary row. mark replaces the status word and note the
// reason; both default from the entry.
func line(e Entry, mark, note string) string {
	word := strings.ToUpper(string(e.Status))
	if mark != "" {
		word = mark
	}
	if note == "" {
		note = e.Reason
		switch e.Status {
		case Run, Skip:
			// The status word and the reason as they are.
		case Waived:
			word = "WAIV"
			note = "until " + e.Until + ": " + e.Reason
		case Folded:
			word = "FOLD"
			note = "into " + e.Into
			if e.Reason != "" {
				note += ", " + e.Reason
			}
		}
	}
	if note == "" {
		return fmt.Sprintf("%-4s %s", word, e.Name)
	}
	return fmt.Sprintf("%-4s %-12s %s", word, e.Name, note)
}

// firstLine keeps a multi-line error's first line for the summary; the
// whole of it was already printed where the gate ran.
func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// Known reports whether a name is a gate, for callers that map old target
// names onto the set.
func Known(name string) bool {
	return slices.Contains(Names(), name)
}
