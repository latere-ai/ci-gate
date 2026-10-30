// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/scope"
)

const base = "2222222222222222222222222222222222222222"

// lake answers git as a repository with a Lake project in proofs/ and no
// specs.
func lake(_ []string, _ bool, name string, args ...string) ([]byte, error) {
	if name == "git" && len(args) > 0 && args[0] == "ls-files" && slices.Contains(args, "lakefile.toml") {
		return []byte("proofs/lakefile.toml\n"), nil
	}
	return nil, nil
}

func change(files ...string) *scope.Scope {
	return &scope.Scope{Ref: "origin/main", Base: base, Files: files}
}

func goChange() *scope.Scope {
	s := change("store/store.go", "frontend/app.ts")
	s.Changed = []scope.Package{{ImportPath: "example.com/m/store", Dir: "store"}}
	s.Reached = []scope.Package{
		{ImportPath: "example.com/m/api", Dir: "api"},
		{ImportPath: "example.com/m/store", Dir: "store"},
		{ImportPath: "example.com/m", Dir: "."},
	}
	return s
}

// A change no Go package holds leaves the minutes-long gates nothing to
// read; every other gate reads the whole tree as it always does.
func TestAChangeWithNoGoPackageSkipsTheLongGates(t *testing.T) {
	c, _ := ctx(t, nil, lake)
	c.Scope = change("frontend/app.ts", "proofs/README.md")
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"suite": "no Go package changed since origin/main (222222222222)",
		"lint":  "no Go package changed since origin/main (222222222222)",
		"vuln":  "no dependency changed since origin/main (222222222222)",
	} {
		if e := entry(plan, name); e.Status != Skip || e.Reason != want {
			t.Errorf("%s: %s %q, want skip %q", name, e.Status, e.Reason, want)
		}
	}
	// A file in a Lake project is that project's change.
	if e := entry(plan, "lean"); e.Status != Run {
		t.Errorf("lean: %s %q", e.Status, e.Reason)
	}
	for _, name := range []string{"fmt-check", "modernize", "license", "identity", "postgres"} {
		if e := entry(plan, name); e.Status != Run {
			t.Errorf("%s reads the whole tree in seconds and must run: %s %q", name, e.Status, e.Reason)
		}
	}
}

func TestLeanRunsOnlyForAChangedProject(t *testing.T) {
	c, _ := ctx(t, nil, lake)
	c.Scope = change("docs/README.md")
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "lean"); e.Status != Skip || e.Reason != "no Lake project changed since origin/main (222222222222)" {
		t.Errorf("lean: %s %q", e.Status, e.Reason)
	}
	asked := 0
	c.Exec = func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name == "git" && slices.Contains(args, "lakefile.toml") {
			// The plan asks once for whether the gate applies, and once for
			// what the change touches; the second answer fails.
			if asked++; asked > 1 {
				return nil, errors.New("git broke")
			}
			return []byte("proofs/lakefile.toml\n"), nil
		}
		return nil, nil
	}
	if _, err := Plan(c); err == nil || !strings.Contains(err.Error(), "git broke") {
		t.Errorf("a Lake list that fails must fail the plan, not skip the gate: %v", err)
	}
}

// A Go change runs the suite over what it reaches and lint over what it
// touches, and the plan says so.
func TestAGoChangeNarrowsTheSuiteAndLint(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Waive = map[string]config.Waiver{"race": {Reason: "flaky under -race", Until: "2026-12-01"}}
	c, _ := ctx(t, cfg, noSpecs)
	c.Scope = goChange()
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "suite"); e.Status != Run ||
		!strings.HasSuffix(e.Reason, "; over the 3 packages the change reaches; the floor on the 1 package it changes") ||
		!strings.HasPrefix(e.Reason, "without -race") {
		t.Errorf("suite: %s %q", e.Status, e.Reason)
	}
	if e := entry(plan, "lint"); e.Status != Run || e.Reason != "over the 1 package the change touches" {
		t.Errorf("lint: %s %q", e.Status, e.Reason)
	}
	if e := entry(plan, "vuln"); e.Status != Skip {
		t.Errorf("vuln: %s %q", e.Status, e.Reason)
	}

	run, _ := suiteRun(c)
	if !slices.Equal(run.Packages, []string{"example.com/m/api", "example.com/m/store", "example.com/m"}) {
		t.Errorf("the suite reads %v", run.Packages)
	}
	if !slices.Equal(run.CoverPkg, []string{"example.com/m/store"}) {
		t.Errorf("the profile measures %v", run.CoverPkg)
	}
}

// A waived cover has no floor to apply, so the note does not promise one.
func TestAWaivedCoverPromisesNoFloor(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Waive = map[string]config.Waiver{"cover": {Reason: "test work", Until: "2026-12-01"}}
	c, _ := ctx(t, cfg, noSpecs)
	c.Scope = goChange()
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "suite"); strings.Contains(e.Reason, "the floor on") || !strings.Contains(e.Reason, "over the 3 packages the change reaches") {
		t.Errorf("suite: %q", e.Reason)
	}
}

// A scope that fell back to the whole tree, and no scope at all, plan the
// whole bar.
func TestAWholeTreeScopeNarrowsNothing(t *testing.T) {
	for _, s := range []*scope.Scope{nil, {Whole: "go.mod changed since origin/main (222222222222)"}} {
		c, _ := ctx(t, nil, noSpecs)
		c.Scope = s
		plan, err := Plan(c)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"suite", "lint", "vuln"} {
			if e := entry(plan, name); e.Status != Run || e.Reason != "" {
				t.Errorf("%v: %s: %s %q", s, name, e.Status, e.Reason)
			}
		}
		run, _ := suiteRun(c)
		if run.Packages != nil || run.CoverPkg != nil {
			t.Errorf("the whole suite reads ./..., got %v and %v", run.Packages, run.CoverPkg)
		}
	}
}

// The check says what it read before the gates and on its verdict.
func TestCheckSaysWhatItRead(t *testing.T) {
	saved := Gates
	t.Cleanup(func() { Gates = saved })
	Gates = []Gate{
		{Name: "one", Run: func(Ctx) error { return nil }},
		{Name: "lint", Run: func(Ctx) error { return nil }},
	}
	c, sb := ctx(t, nil, noSpecs)
	c.Scope = change("frontend/app.ts")
	if err := Check(c); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.HasPrefix(out, "checking what changed since origin/main (222222222222): 1 file; -all checks the whole tree\n") {
		t.Errorf("the check must open on what it reads:\n%s", out)
	}
	if !strings.Contains(out, "SKIP lint         no Go package changed") || !strings.HasSuffix(out, "1 gates passed on what changed since origin/main (222222222222)\n") {
		t.Errorf("output:\n%s", out)
	}

	Gates = []Gate{{Name: "one", Run: func(Ctx) error { return errors.New("broken") }}}
	c, sb = ctx(t, nil, noSpecs)
	c.Scope = goChange()
	err := Check(c)
	if err == nil || err.Error() != "1 of 1 gates failed on what changed since origin/main (222222222222): one" {
		t.Errorf("verdict = %v", err)
	}
	if !strings.HasPrefix(sb.String(), "checking what changed since origin/main (222222222222): 2 files, 1 Go package, 3 packages with what imports them; -all checks the whole tree\n") {
		t.Errorf("output:\n%s", sb.String())
	}

	Gates = []Gate{{Name: "one", Run: func(Ctx) error { return nil }}}
	c, sb = ctx(t, nil, noSpecs)
	c.Scope = &scope.Scope{Whole: "go.mod changed since origin/main (222222222222)"}
	if err := Check(c); err != nil {
		t.Fatal(err)
	}
	if out := sb.String(); !strings.HasPrefix(out, "checking the whole tree: go.mod changed since origin/main (222222222222)\n") || !strings.HasSuffix(out, "1 gates passed\n") {
		t.Errorf("output:\n%s", out)
	}
}

// Scoped, the lint gate runs the linter over the packages the change
// touches, as directory patterns.
func TestLintReadsThePackagesTheChangeTouches(t *testing.T) {
	var ran []string
	c, _ := ctx(t, nil, func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, nil
	})
	if err := os.WriteFile(filepath.Join(c.Root, "go.mod"), []byte("module example.com/m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Scope = goChange()
	g, _ := Find("lint")
	if err := g.Run(c); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || !strings.HasSuffix(ran[0], " run --allow-parallel-runners ./store") {
		t.Errorf("ran %v", ran)
	}
	ran = nil
	c.Scope = nil
	if err := g.Run(c); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || !strings.HasSuffix(ran[0], " run --allow-parallel-runners ./...") {
		t.Errorf("ran %v", ran)
	}
}

func TestCountReadsAsWords(t *testing.T) {
	if count(1, "file") != "1 file" || count(0, "file") != "0 files" || count(2, "Go package") != "2 Go packages" {
		t.Error("count")
	}
	if header(nil) != "" {
		t.Error("no scope, no header")
	}
	if got := patterns([]scope.Package{{Dir: "."}, {Dir: "a/b"}}); !slices.Equal(got, []string{".", "./a/b"}) {
		t.Errorf("patterns %v", got)
	}
}
