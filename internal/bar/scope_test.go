// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/cover"
	"latere.ai/x/ci-gate/internal/gates"
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
		"vuln":  "no Go package changed since origin/main (222222222222)",
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
func TestAGoChangeNarrowsTheSuiteAndLintAndScans(t *testing.T) {
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
		!strings.HasSuffix(e.Reason, "; over the 3 packages the change reaches; the floor on the 1 package it changes and on any it reaches that loses coverage") ||
		!strings.HasPrefix(e.Reason, "without -race") {
		t.Errorf("suite: %s %q", e.Status, e.Reason)
	}
	// The linter reads what the change reaches: a changed signature leaves
	// its findings in the callers.
	if e := entry(plan, "lint"); e.Status != Run || e.Reason != "over the 3 packages the change reaches" {
		t.Errorf("lint: %s %q", e.Status, e.Reason)
	}
	// A Go change can call a vulnerable function no code called before,
	// so the scan runs, over the whole module.
	if e := entry(plan, "vuln"); e.Status != Run || e.Reason != "" {
		t.Errorf("vuln: %s %q", e.Status, e.Reason)
	}

	run, _ := suiteRun(c)
	if !slices.Equal(run.Packages, []string{"example.com/m/api", "example.com/m/store", "example.com/m"}) {
		t.Errorf("the suite reads %v", run.Packages)
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
		if run.Packages != nil {
			t.Errorf("the whole suite reads ./..., got %v", run.Packages)
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
// reaches, as directory patterns.
func TestLintReadsThePackagesTheChangeReaches(t *testing.T) {
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
	if len(ran) != 1 || !strings.HasSuffix(ran[0], " run --allow-parallel-runners ./api ./store .") {
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
	if isAre(1)+theyIt(1)+themIt(1) != "isitit" || isAre(2)+theyIt(2)+themIt(2) != "aretheythem" {
		t.Error("the words that agree with a count")
	}
	if got := lastLines([]byte("a\nb\nc\n"), 2); got != "b\nc" {
		t.Errorf("last lines %q", got)
	}
	if got := lastLines(nil, 2); got != "" {
		t.Errorf("no output, no lines: %q", got)
	}
	if header(nil) != "" {
		t.Error("no scope, no header")
	}
	if got := patterns([]scope.Package{{Dir: "."}, {Dir: "a/b"}}); !slices.Equal(got, []string{".", "./a/b"}) {
		t.Errorf("patterns %v", got)
	}
}

// repo is a module committed as the base, recorded as origin/main, with
// the edit a test makes after; the context checks that change.
func repo(t *testing.T, cfg *config.Config, files map[string]string, edit map[string]string) (Ctx, *strings.Builder) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	c, sb := ctx(t, cfg, nil)
	c.Exec = gates.OSExec(c.Root, sb)
	write := func(files map[string]string) {
		for name, body := range files {
			full := filepath.Join(c.Root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := c.Exec(nil, false, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(files)
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	write(edit)
	c.Scope = scope.Resolve(c.Root, c.GoBin, c.Exec)
	if !c.Scope.Narrows() {
		t.Fatalf("whole: %s", c.Scope.Whole)
	}
	return c, sb
}

// tester runs go test as the suite's Tester does, minus the race detector
// and the sandbox, and counts the runs.
type tester struct {
	c    Ctx
	runs [][]string
	fail func(args []string) bool
}

func (r *tester) test(stream bool, args ...string) ([]byte, error) {
	r.runs = append(r.runs, args)
	if r.fail != nil && r.fail(args) {
		return nil, errors.New("exit status 1")
	}
	return r.c.Exec(nil, stream, r.c.GoBin, append([]string{"test"}, args...)...)
}

// floor runs the scoped suite's go test and then its floor.
func floor(t *testing.T, c Ctx, r *tester) error {
	t.Helper()
	run, _ := suiteRun(c)
	profile := filepath.Join(c.Root, "coverage.out")
	args := slices.Concat([]string{"test", "-covermode=atomic", "-coverpkg=./...", "-coverprofile=" + profile}, run.Packages)
	if out, err := c.Exec(nil, false, c.GoBin, args...); err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
	r.c = c
	return run.Floor(profile, r.test)
}

// layers is a module where a's tests are what cover b.Two: b's own tests
// cover only b.One.
var layers = map[string]string{
	"go.mod":      "module example.com/m\n\ngo 1.24\n",
	"b/b.go":      "package b\n\nfunc One() int { return 1 }\n\nfunc Two() int { return 2 }\n",
	"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) { _ = One() }\n",
	"a/a.go":      "package a\n\nimport \"example.com/m/b\"\n\nfunc Sum() int { return b.One() + b.Two() }\n",
	"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) { _ = Sum() }\n",
}

// sum is a's source with Sum's body replaced.
func sum(body string) map[string]string {
	return map[string]string{"a/a.go": "package a\n\nimport \"example.com/m/b\"\n\nfunc Sum() int { return " + body + " }\n"}
}

// A change that stops a's tests reaching b.Two takes b under the floor,
// though b did not change: the floor measures b over every test that
// reaches it and fails it there.
func TestAnUntouchedPackageThatLostCoverageIsHeldToTheFloor(t *testing.T) {
	t.Parallel()
	c, sb := repo(t, nil, layers, sum("b.One()"))
	r := &tester{}
	err := floor(t, c, r)
	if err == nil || !strings.Contains(err.Error(), "b 50.0%") {
		t.Fatalf("b lost the half of it only a's tests covered: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "1 package the change did not touch lost coverage it had at origin/main") {
		t.Errorf("the floor says which package lost coverage:\n%s", sb.String())
	}
	if len(r.runs) != 2 || !slices.ContainsFunc(r.runs[0], func(a string) bool { return strings.HasPrefix(a, "-overlay=") }) ||
		!slices.Contains(r.runs[1], "example.com/m/b") {
		t.Errorf("want the base's run of a's tests, then b's own: %v", r.runs)
	}
	// go test compiles a package it measures from disk, past the overlay,
	// so the base's run measures only what the change left as it was.
	if !slices.Contains(r.runs[0], "-coverpkg=example.com/m/b") {
		t.Errorf("the base's run measures the untouched b alone: %v", r.runs[0])
	}
}

// A change that leaves every block its tests covered at the base covered
// holds the untouched packages to nothing more, and says so.
func TestAnUntouchedPackageThatKeptItsCoverageIsNotRemeasured(t *testing.T) {
	t.Parallel()
	c, sb := repo(t, nil, layers, sum("b.Two() + b.One()"))
	r := &tester{}
	if err := floor(t, c, r); err != nil {
		t.Fatalf("b kept its coverage: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "1 package the change reaches without touching kept the coverage the same tests gave it at origin/main") {
		t.Errorf("the floor says the untouched package kept its coverage:\n%s", sb.String())
	}
	if len(r.runs) != 1 {
		t.Errorf("want only the base's run: %v", r.runs)
	}
}

// A base that cannot be replayed for a package, or whose tests fail here,
// leaves nothing to compare, so what those tests reach is measured in full.
func TestWhatTheBaseCannotShowIsMeasuredInFull(t *testing.T) {
	t.Parallel()
	edit := sum("b.Two() + b.One()")
	edit["a/testdata/case.txt"] = "a case a's tests read\n"
	c, sb := repo(t, nil, layers, edit)
	r := &tester{}
	if err := floor(t, c, r); err != nil {
		t.Fatalf("b, measured in full, clears the floor: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "linked by tests that read a changed file at run time") || len(r.runs) != 1 {
		t.Errorf("a's tests read a changed file, so b is measured in full and a's base is not run: %v\n%s", r.runs, sb.String())
	}

	c, sb = repo(t, nil, layers, sum("b.Two() + b.One()"))
	r = &tester{fail: func(args []string) bool { return strings.HasPrefix(args[0], "-overlay=") }}
	if err := floor(t, c, r); err != nil {
		t.Fatalf("b, measured in full, clears the floor: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "did not pass here") || len(r.runs) != 2 {
		t.Errorf("a base that fails here leaves every untouched package to be measured in full: %v\n%s", r.runs, sb.String())
	}

	c, _ = repo(t, nil, layers, sum("b.One()"))
	r = &tester{fail: func(args []string) bool { return !strings.HasPrefix(args[0], "-overlay=") }}
	if err := floor(t, c, r); err == nil || !strings.Contains(err.Error(), "the tests of the 1 package that also reach it failed") {
		t.Errorf("a run that measures in full and fails fails the floor: %v", err)
	}
}

// An untouched package the run itself holds, because it imports the
// change, is measured from the run alone: no test outside it reaches it.
func TestALostPackageTheRunHoldsNeedsNoOtherRun(t *testing.T) {
	t.Parallel()
	c, sb := repo(t, nil, map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.24\n",
		"x/x.go":      "package x\n\nfunc X() int { return 1 }\n",
		"b/b.go":      "package b\n\nimport \"example.com/m/x\"\n\nfunc B() int {\n\tif x.X() > 0 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { _ = B() }\n",
	}, map[string]string{"x/x.go": "package x\n\nfunc X() int { return 0 }\n"})
	r := &tester{}
	err := floor(t, c, r)
	if err == nil || !strings.Contains(err.Error(), "b 66.7%") {
		t.Fatalf("b's figure is the run's: %v\n%s", err, sb.String())
	}
	if len(r.runs) != 1 {
		t.Errorf("want only the base's run: %v", r.runs)
	}
}

// A change confined to packages the floor cannot measure, ones with no
// statements or exempt ones, passes the floor and says why; one that
// changes a measurable package is held to it; and a floor that cannot
// list the module or rebuild the base fails.
func TestTheFloorMeasuresOnlyWhatTheChangeCanBeMeasuredOn(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Cover.Exempt = map[string]string{"m/conformance": "run against a live server"}
	files := map[string]string{
		"go.mod":                     "module example.com/m\n\ngo 1.24\n",
		"types/types.go":             "package types\n\ntype T int\n",
		"conformance/conformance.go": "package conformance\n\nfunc Run() int { return 1 }\n",
		"store/store.go":             "package store\n\nfunc Save() int { return 1 }\n",
	}
	c, sb := repo(t, cfg, files, map[string]string{
		"types/types.go":             "package types\n\ntype T int64\n",
		"conformance/conformance.go": "package conformance\n\nfunc Run() int { return 2 }\n",
	})
	if err := floor(t, c, &tester{}); err != nil {
		t.Fatalf("nothing measurable changed, so the floor has nothing to fail: %v", err)
	}
	if !strings.Contains(sb.String(), "the floor measures nothing here") {
		t.Errorf("the floor must say it measured nothing:\n%s", sb.String())
	}

	c, _ = repo(t, cfg, files, map[string]string{"store/store.go": "package store\n\nfunc Save() int { return 2 }\n"})
	if err := floor(t, c, &tester{}); err == nil || !strings.Contains(err.Error(), "store") {
		t.Errorf("a changed package under the floor must fail it: %v", err)
	}

	broken := c
	broken.Exec = func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		if name == "git" && args[0] == "ls-tree" {
			return nil, errors.New("exit status 128")
		}
		return c.Exec(env, stream, name, args...)
	}
	broken.Scope = scope.Resolve(c.Root, c.GoBin, c.Exec)
	if err := scopedFloor(broken, cover.GoLister(c.GoBin, c.Root))(filepath.Join(c.Root, "coverage.out"), (&tester{c: c}).test); err == nil ||
		!strings.Contains(err.Error(), "cannot be rebuilt to compare coverage against") {
		t.Errorf("a base that cannot be rebuilt fails the floor: %v", err)
	}

	c.GoBin = filepath.Join(t.TempDir(), "no-go")
	run, _ := suiteRun(c)
	if err := run.Floor(filepath.Join(c.Root, "coverage.out"), (&tester{c: c}).test); err == nil || !strings.Contains(err.Error(), "listing the module packages") {
		t.Errorf("a package list that fails must fail the floor: %v", err)
	}
}

// A floor with no directory for the base's overlay, or no profile of the
// run to compare against, fails rather than comparing nothing.
func TestAFloorWithNothingToCompareFails(t *testing.T) {
	c, _ := repo(t, nil, layers, sum("b.Two() + b.One()"))
	f := scopedFloor(c, cover.GoLister(c.GoBin, c.Root))
	if err := f(filepath.Join(c.Root, "none.out"), (&tester{c: c}).test); err == nil {
		t.Error("a run's profile that cannot be read must fail the floor")
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "none"))
	if err := f(filepath.Join(c.Root, "none.out"), (&tester{c: c}).test); err == nil || !strings.Contains(err.Error(), "making a directory for the base's overlay") {
		t.Errorf("no directory for the overlay must fail the floor: %v", err)
	}
}
