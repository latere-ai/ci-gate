// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
)

// tree is a root module with a module beside it at tools/x and another at
// tools/y, as the fixture for every test of the modules key.
var tree = map[string]string{
	"go.mod":            "module example.com/m\n\ngo 1.24\n",
	"m.go":              "package m\n\nfunc M() int { return 1 }\n",
	"tools/x/go.mod":    "module example.com/x\n\ngo 1.24\n",
	"tools/x/x.go":      "package x\n\nfunc X() int { return 1 }\n",
	"tools/x/z/z.go":    "package z\n\nfunc Z() int { return 1 }\n",
	"tools/y/go.mod":    "module example.com/y\n\ngo 1.24\n",
	"tools/y/y.go":      "package y\n\nfunc Y() int { return 1 }\n",
	"tools/y/y_test.go": "package y\n\nimport \"testing\"\n\nfunc TestY(t *testing.T) { _ = Y() }\n",
}

// fixture writes tree under a new root.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range tree {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// toolchain stands in for the go command and git in each directory the
// gates run in. It records every command with the directory it ran in and
// writes the coverage profile each directory is given.
type toolchain struct {
	mu    sync.Mutex
	root  string
	calls []string
	// tracked is what git lists as tracked.
	tracked []string
	// fail makes the go test of a directory fail.
	fail map[string]bool
	// profile is the coverage profile a directory's go test writes.
	profile map[string]string
}

func (tc *toolchain) in(dir string) gates.Exec {
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		tc.mu.Lock()
		defer tc.mu.Unlock()
		tc.calls = append(tc.calls, dir+": "+name+" "+strings.Join(args, " "))
		switch {
		case name == "git" && args[0] == "ls-files":
			return []byte(strings.Join(tc.tracked, "\x00") + "\x00"), nil
		case name == "git":
			return nil, nil
		case args[0] == "list" && args[1] == "-m":
			return os.ReadFile(filepath.Join(tc.root, filepath.FromSlash(dir), "go.mod.name"))
		case args[0] == "test":
			for _, a := range args {
				if p, ok := strings.CutPrefix(a, "-coverprofile="); ok {
					full := filepath.Join(tc.root, filepath.FromSlash(dir), p)
					if err := os.WriteFile(full, []byte(tc.profile[dir]), 0o644); err != nil {
						return nil, err
					}
				}
			}
			if tc.fail[dir] {
				return []byte("FAIL\n"), errors.New("exit status 1")
			}
		}
		return nil, nil
	}
}

// ran is the commands a directory ran, without the directory.
func (tc *toolchain) ran(dir string) []string {
	var out []string
	for _, c := range tc.calls {
		if rest, ok := strings.CutPrefix(c, dir+": "); ok {
			out = append(out, rest)
		}
	}
	return out
}

// profile is a coverage profile for one file: total statements, covered of
// them.
func profile(file string, total, covered int) string {
	return fmt.Sprintf("mode: atomic\n%s:3.1,4.2 %d 1\n%s:5.1,6.2 %d 0\n", file, covered, file, total-covered)
}

// modulesCtx is a context over the fixture with the listed modules, the
// properties that need the machine's toolchain waived, and a toolchain that
// passes every run at the covering figure.
func modulesCtx(t *testing.T, modules ...string) (Ctx, *toolchain, *strings.Builder) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Modules = modules
	cfg.Waive = map[string]config.Waiver{}
	for _, g := range []string{"race", "hermetic", "tempdir", "containers"} {
		cfg.Waive[g] = config.Waiver{Reason: "the fixture has no machine toolchain", Until: "2026-12-01"}
	}
	c, sb := ctx(t, cfg, nil)
	c.Root = fixture(t)
	tc := &toolchain{
		root: c.Root, fail: map[string]bool{},
		tracked: []string{"tools/x/go.mod", "tools/y/go.mod"},
		profile: map[string]string{
			".":       profile("example.com/m/m.go", 4, 4),
			"tools/x": profile("example.com/x/x.go", 4, 4) + strings.TrimPrefix(profile("example.com/x/z/z.go", 4, 4), "mode: atomic\n"),
			"tools/y": profile("example.com/y/y.go", 4, 4),
		},
	}
	for dir, name := range map[string]string{".": "example.com/m", "tools/x": "example.com/x", "tools/y": "example.com/y"} {
		if err := os.WriteFile(filepath.Join(c.Root, filepath.FromSlash(dir), "go.mod.name"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c.Exec = tc.in(".")
	c.ExecIn = tc.in
	return c, tc, sb
}

// goCalls are the commands of the go tool among calls.
func goCalls(calls []string) []string {
	return slices.DeleteFunc(slices.Clone(calls), func(c string) bool { return !strings.HasPrefix(c, "go ") })
}

func suiteGate(t *testing.T) Gate {
	t.Helper()
	g, ok := Find(Suite)
	if !ok {
		t.Fatal("no suite gate")
	}
	return g
}

// After the root, the suite runs vet and the covering test in each listed
// module, from that module's directory and in the order the file lists them.
func TestTheSuiteRunsInEachModuleAfterTheRoot(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x", "tools/y")
	if err := suiteGate(t).Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	test := "test -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./..."
	for _, dir := range []string{".", "tools/x", "tools/y"} {
		got := tc.ran(dir)
		var gos []string
		for _, g := range got {
			if strings.HasPrefix(g, "go ") {
				gos = append(gos, g)
			}
		}
		if want := []string{"go vet ./...", "go " + test}; !slices.Equal(gos[len(gos)-2:], want) {
			t.Errorf("%s ran %q, want %q", dir, gos, want)
		}
	}
	order := ""
	for _, call := range tc.calls {
		if strings.Contains(call, "go test") || strings.Contains(call, "go vet") {
			order += strings.SplitN(call, ":", 2)[0] + " "
		}
	}
	if want := ". . tools/x tools/x tools/y tools/y "; order != want {
		t.Errorf("order %q, want %q", order, want)
	}
	for _, want := range []string{"root module\n", "module tools/x\n", "module tools/y\n"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("the report lacks %q:\n%s", want, sb)
		}
	}
	if n := strings.Count(sb.String(), "the suite passes"); n != 3 {
		t.Errorf("%d verdicts, want one per module:\n%s", n, sb)
	}
}

// With no module listed the suite is the root's alone and its output has no
// module headings.
func TestWithoutModulesTheSuiteIsTheRootsAlone(t *testing.T) {
	c, tc, sb := modulesCtx(t)
	if err := suiteGate(t).Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	if strings.Contains(sb.String(), "module") || len(tc.ran("tools/x")) != 0 {
		t.Errorf("no module to run in:\n%s\n%v", sb, tc.calls)
	}
	tc.fail["."] = true
	if err := suiteGate(t).Run(c); err == nil || strings.Contains(err.Error(), "root module") {
		t.Errorf("the root's own error must reach the caller as it is: %v", err)
	}
}

// A module that fails does not stop the one after it, and the gate fails.
func TestAFailingModuleDoesNotStopTheNext(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x", "tools/y")
	tc.fail["tools/x"] = true
	err := suiteGate(t).Run(c)
	if err == nil || !strings.Contains(err.Error(), "module tools/x: the test suite failed") {
		t.Fatalf("a failing module must fail the gate: %v", err)
	}
	if strings.Contains(err.Error(), "tools/y") || strings.Contains(err.Error(), "the root module") {
		t.Errorf("only the failing module is named: %v", err)
	}
	if got := tc.ran("tools/y"); !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "go test") }) {
		t.Errorf("tools/y did not run after tools/x failed: %v\n%s", got, sb)
	}

	// The root failing leaves the modules to run, and is named.
	c, tc, _ = modulesCtx(t, "tools/x", "tools/y")
	tc.fail["."] = true
	tc.fail["tools/y"] = true
	err = suiteGate(t).Run(c)
	if err == nil || !strings.Contains(err.Error(), "the root module: ") || !strings.Contains(err.Error(), "\nand module tools/y: ") {
		t.Fatalf("both failures must be named: %v", err)
	}
	if got := tc.ran("tools/x"); len(got) == 0 {
		t.Error("tools/x did not run after the root failed")
	}
}

// The floor reads the module's own packages and figures: a module under 90%
// fails on it while the root and the other module pass.
func TestTheFloorAppliesToEachModulesPackages(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x", "tools/y")
	tc.profile["tools/y"] = profile("example.com/y/y.go", 10, 5)
	err := suiteGate(t).Run(c)
	if err == nil || !strings.Contains(err.Error(), "module tools/y: ") ||
		!strings.Contains(err.Error(), "the suite's coverage is under the floor: below 90%: example.com/y 50.0%") {
		t.Fatalf("the module under the floor must fail: %v\n%s", err, sb)
	}
	if strings.Contains(err.Error(), "tools/x") || strings.Contains(err.Error(), "root module") {
		t.Errorf("a module over the floor must not fail: %v", err)
	}
	if !strings.Contains(sb.String(), "ok   example.com/x ") {
		t.Errorf("tools/x's packages are reported:\n%s", sb)
	}

	// A package of the module that no profile mentions has no data, and is
	// found by listing the module's own directory.
	c, tc, _ = modulesCtx(t, "tools/x")
	tc.profile["tools/x"] = profile("example.com/x/x.go", 4, 4)
	if err := suiteGate(t).Run(c); err == nil || !strings.Contains(err.Error(), "produced no coverage data") ||
		!strings.Contains(err.Error(), "example.com/x/z") {
		t.Errorf("a package with no data must fail the floor: %v", err)
	}
}

// An exemption names a module's package by a suffix of its import path, or
// by its place below the root.
func TestCoverExemptionsMatchTheModulesImportPaths(t *testing.T) {
	for _, key := range []string{"x/z", "tools/x/z"} {
		c, tc, sb := modulesCtx(t, "tools/x")
		tc.profile["tools/x"] = profile("example.com/x/x.go", 4, 4) + strings.TrimPrefix(profile("example.com/x/z/z.go", 10, 1), "mode: atomic\n")
		c.Cfg.Cover.Exempt = map[string]string{key: "an entry point"}
		if err := suiteGate(t).Run(c); err != nil {
			t.Errorf("%q: %v\n%s", key, err, sb)
		}
		if !strings.Contains(sb.String(), "exempt: an entry point") {
			t.Errorf("%q: the exemption is reported:\n%s", key, sb)
		}
	}
	// A key naming nothing in the module exempts nothing in it.
	c, tc, _ := modulesCtx(t, "tools/x")
	tc.profile["tools/x"] = profile("example.com/x/x.go", 4, 4) + strings.TrimPrefix(profile("example.com/x/z/z.go", 10, 1), "mode: atomic\n")
	c.Cfg.Cover.Exempt = map[string]string{"tools/y/z": "another module's"}
	if err := suiteGate(t).Run(c); err == nil || !strings.Contains(err.Error(), "example.com/x/z 10.0%") {
		t.Errorf("an exemption for another module must not apply: %v", err)
	}
}

// A waived cover drops the floor and the profile in every module.
func TestAWaivedCoverAppliesToEveryModule(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x", "tools/y")
	tc.profile["tools/y"] = profile("example.com/y/y.go", 10, 1)
	c.Cfg.Waive["cover"] = config.Waiver{Reason: "behind", Until: "2026-12-01"}
	if err := suiteGate(t).Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	for _, dir := range []string{".", "tools/x", "tools/y"} {
		for _, call := range tc.ran(dir) {
			if strings.Contains(call, "coverprofile") {
				t.Errorf("%s collected a profile: %s", dir, call)
			}
		}
	}
	// And a waived suite is the suite plan's decision, not this gate's: the
	// run of a module is the root's run with its floor swapped.
	run, _ := suiteRun(c)
	if got, err := moduleRun(c, run, "tools/x", c.execIn("tools/x")); err != nil || got.Floor != nil || got.Profile != "" {
		t.Errorf("a module run keeps the root's waiver: %+v %v", got, err)
	}
}

// A module's run is the root's, every property included, over the whole
// module and the repository's sandbox.
func TestAModulesRunHoldsEveryPropertyOfTheRoots(t *testing.T) {
	c, _, _ := modulesCtx(t, "tools/x")
	c.Cfg.Waive = nil
	c.Cfg.Race.Timeout = "5m"
	c.Cfg.Hermetic.Allow = []string{"/opt/tool"}
	c.Scope = goChange()
	root, _ := suiteRun(c)
	if len(root.Packages) == 0 {
		t.Fatal("the root run is narrowed")
	}
	got, err := moduleRun(c, root, "tools/x", c.execIn("tools/x"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Race || got.Timeout != "5m" || !got.Hermetic || !slices.Equal(got.Allow, []string{"/opt/tool"}) ||
		!got.Sandbox || !got.Containers || got.Profile != "coverage.out" || got.Floor == nil {
		t.Errorf("the module's run drops a property: %+v", got)
	}
	if got.Packages != nil {
		t.Errorf("a module is read whole, not over the root's packages: %v", got.Packages)
	}
	if got.Home != c.Root {
		t.Errorf("the sandbox is the repository's: %q", got.Home)
	}

	failing := c
	failing.ExecIn = func(string) gates.Exec {
		return func([]string, bool, string, ...string) ([]byte, error) { return nil, errors.New("no go") }
	}
	if _, err := moduleRun(failing, root, "tools/x", failing.execIn("tools/x")); err == nil || !strings.Contains(err.Error(), "cannot name the module at tools/x") {
		t.Errorf("a module the toolchain cannot name fails: %v", err)
	}
}

// A listed module that git does not track is a mistake in the file.
func TestAModuleWithoutATrackedGoModFails(t *testing.T) {
	c, tc, _ := modulesCtx(t, "tools/x", "tools/y")
	tc.tracked = []string{"tools/x/go.mod"}
	if err := suiteGate(t).Run(c); err == nil || !strings.Contains(err.Error(), "git tracks no tools/y/go.mod") {
		t.Errorf("an untracked module must fail: %v", err)
	}
	if len(tc.ran("tools/x")) != 0 {
		t.Error("nothing runs when the file is wrong")
	}
	c.Exec = func([]string, bool, string, ...string) ([]byte, error) { return nil, errors.New("not a repository") }
	if err := suiteGate(t).Run(c); err == nil || !strings.Contains(err.Error(), "cannot list the modules' go.mod files") {
		t.Errorf("a git that fails must fail the gate: %v", err)
	}
}

// Without an injected runner the commands run for real in the module.
func TestExecInDefaultsToTheModulesDirectory(t *testing.T) {
	c, _, _ := modulesCtx(t, "tools/x")
	c.ExecIn = nil
	out, err := c.execIn("tools/x")(nil, false, "go", "list", "-m")
	if err != nil || strings.TrimSpace(string(out)) != "example.com/x" {
		t.Errorf("go list -m in the module: %q %v", out, err)
	}
}

// On a laptop a module runs when a changed file lies inside it, and is
// skipped with the reason otherwise. The root is left alone when no package
// of it changed.
func TestOnALaptopAModuleRunsWhenAFileInsideItChanged(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x", "tools/y")
	c.Scope = change("tools/x/go.sum")
	if err := suiteGate(t).Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	if len(goCalls(tc.ran("."))) != 0 || len(tc.ran("tools/y")) != 0 || len(tc.ran("tools/x")) == 0 {
		t.Errorf("only tools/x runs:\n%v", tc.calls)
	}
	for _, want := range []string{
		"root module skipped: no Go package of the root module changed since origin/main (222222222222)",
		"module tools/y skipped: no file under it changed since origin/main (222222222222)",
	} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("the report lacks %q:\n%s", want, sb)
		}
	}

	// A root package's change leaves the modules out, and says why.
	c, tc, sb = modulesCtx(t, "tools/x")
	c.Scope = goChange()
	if err := suiteGate(t).Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	if len(tc.ran("tools/x")) != 0 || !strings.Contains(sb.String(), "module tools/x skipped: no file under it changed") {
		t.Errorf("tools/x must be skipped:\n%s", sb)
	}
}

// The plan says which modules a scoped check reaches.
func TestThePlanNamesTheModulesAChangeTouches(t *testing.T) {
	c, _, _ := modulesCtx(t, "tools/x", "tools/y")
	c.Cfg.Waive = nil
	c.Scope = change("tools/y/y.go")
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"suite", "lint"} {
		e := entry(plan, name)
		if e.Status != Run || !strings.Contains(e.Reason, "over the 1 module it touches: tools/y; no Go package of the root module changed") {
			t.Errorf("%s: %s %q", name, e.Status, e.Reason)
		}
	}

	c.Scope = goChange()
	c.Scope.Files = append(c.Scope.Files, "tools/x/go.mod")
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"suite", "lint"} {
		if e := entry(plan, name); e.Status != Run || !strings.HasSuffix(e.Reason, "; and the 1 module it touches: tools/x") {
			t.Errorf("%s: %s %q", name, e.Status, e.Reason)
		}
	}

	c.Scope = change("frontend/app.ts")
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "suite"); e.Status != Skip || !strings.HasPrefix(e.Reason, "no Go package changed since") {
		t.Errorf("a change outside every module and package skips the suite: %s %q", e.Status, e.Reason)
	}
}

// On CI with a base the modules follow the Go rule: the gate runs whole
// unless every changed file is inert, and a file in a module is not.
func TestOnCIAModulesFilesAreNotInertToGo(t *testing.T) {
	c, _, _ := modulesCtx(t, "tools/x", "tools/y")
	c.Scope = change("tools/x/go.mod")
	c.Scope.CI = true
	c.Scope.GoInert = false
	run, skipped := selectModules(c)
	if !slices.Equal(run, []string{"tools/x", "tools/y"}) || len(skipped) != 0 {
		t.Errorf("CI runs every module: %v %v", run, skipped)
	}
	c.Scope.GoInert = true
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "suite"); e.Status != Skip || !strings.Contains(e.Reason, "is inert to Go") {
		t.Errorf("an inert change skips the suite and with it the modules: %s %q", e.Status, e.Reason)
	}
	if run, _ := selectModules(Ctx{Cfg: c.Cfg}); len(run) != 2 {
		t.Errorf("no scope runs every module: %v", run)
	}
}

// A change to a module's go.mod or go.sum is that module's, and sends
// neither the root to its whole tree nor the others to a run. This reads
// the real scope, since wholeTreeFiles matches the root's paths only.
func TestAModulesGoModDoesNotMakeTheRootWhole(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Modules = []string{"tools/x", "tools/y"}
	c, _ := repo(t, cfg, tree, map[string]string{
		"tools/x/go.mod": "module example.com/x\n\ngo 1.24\n\n// changed\n",
		"tools/x/go.sum": "",
	})
	s := c.Scope
	if !s.Narrows() || len(s.Changed) != 0 || !s.Touches("tools/x") || s.Touches("tools/y") {
		t.Fatalf("scope %+v", s)
	}
	run, skipped := selectModules(c)
	if !slices.Equal(run, []string{"tools/x"}) || len(skipped) != 1 || skipped[0].Dir != "tools/y" {
		t.Errorf("modules %v skipped %v", run, skipped)
	}
	if why := rootSkipped(c, run); why == "" {
		t.Error("the root has nothing to run")
	}
	if why := rootSkipped(c, nil); why != "" {
		t.Errorf("with no module to run the root's own rule decides: %q", why)
	}
}

// lint runs in each module with the configuration rendered for its path.
func TestLintRunsInEachModule(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x", "tools/y")
	g, ok := Find("lint")
	if !ok {
		t.Fatal("no lint gate")
	}
	if err := g.Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	want := "run " + golangci.Module + "@" + golangci.Version + " run --allow-parallel-runners ./..."
	for _, dir := range []string{".", "tools/x", "tools/y"} {
		if !slices.Contains(tc.ran(dir), "go "+want) {
			t.Errorf("%s did not lint: %v", dir, tc.ran(dir))
		}
	}
	cfgFile, err := os.ReadFile(filepath.Join(c.Root, "tools", "x", golangci.Name))
	if err != nil || !strings.Contains(string(cfgFile), "example.com/x") {
		t.Errorf("the module's configuration carries its own path: %v\n%s", err, cfgFile)
	}

	// A module's findings fail the gate beside the root's.
	c.ExecIn = func(dir string) gates.Exec {
		inner := tc.in(dir)
		return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
			if dir == "tools/y" && len(args) > 0 && args[0] == "run" {
				return nil, errors.New("exit status 1")
			}
			return inner(env, stream, name, args...)
		}
	}
	if err := g.Run(c); err == nil || !strings.Contains(err.Error(), "module tools/y: golangci-lint") {
		t.Errorf("a finding in a module fails lint: %v", err)
	}
}

// A repository with its own linter configuration has one file, at the root.
func TestLintInModulesYieldsToARepositorysOwnConfiguration(t *testing.T) {
	c, tc, sb := modulesCtx(t, "tools/x")
	c.Cfg.Golangci.Own = "extra linters"
	if err := os.WriteFile(filepath.Join(c.Root, golangci.Name), []byte("version: \"2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, _ := Find("lint")
	if err := g.Run(c); err != nil {
		t.Fatalf("%v\n%s", err, sb)
	}
	if !strings.Contains(sb.String(), "lint reads the root module only") || len(tc.ran("tools/x")) != 0 {
		t.Errorf("the module is left to the root, and the output says so:\n%s\n%v", sb, tc.ran("tools/x"))
	}
}
