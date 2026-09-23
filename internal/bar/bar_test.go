// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

var day = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// withSpecs answers git as a repository that tracks specs.
func withSpecs(_ []string, _ bool, name string, _ ...string) ([]byte, error) {
	if name == "git" {
		return []byte("specs/001-a.md\n"), nil
	}
	return nil, nil
}

func noSpecs(_ []string, _ bool, name string, _ ...string) ([]byte, error) {
	return nil, nil
}

func ctx(t *testing.T, cfg *config.Config, exec gates.Exec) (Ctx, *strings.Builder) {
	t.Helper()
	if cfg == nil {
		var err error
		cfg, err = config.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
	}
	var sb strings.Builder
	return Ctx{Cfg: cfg, Root: t.TempDir(), GoBin: "go", Out: &sb, Exec: exec, Now: day}, &sb
}

func entry(plan []Entry, name string) Entry {
	for _, e := range plan {
		if e.Name == name {
			return e
		}
	}
	return Entry{}
}

// Every gate is in the plan, and the plan is in run order.
func TestPlanNamesEveryGateInOrder(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != len(Gates) {
		t.Fatalf("plan has %d entries for %d gates", len(plan), len(Gates))
	}
	for i, g := range Gates {
		if plan[i].Name != g.Name {
			t.Errorf("plan[%d] = %s, want %s", i, plan[i].Name, g.Name)
		}
	}
	if e := entry(plan, "fmt-check"); e.Status != Run {
		t.Errorf("fmt-check always applies, got %+v", e)
	}
}

// Applicability is asked of the tree. A gate with no subject is skipped with
// the reason, and the reason is not a waiver.
func TestPlanSkipsAGateWithNoSubject(t *testing.T) {
	c, _ := ctx(t, nil, noSpecs)
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "spec-lint"); e.Status != Skip || !strings.Contains(e.Reason, "tracks no specs/") {
		t.Errorf("spec-lint on a tree with no specs: %+v", e)
	}
	if e := entry(plan, "depcheck"); e.Status != Skip || !strings.Contains(e.Reason, "names no package") {
		t.Errorf("depcheck with no packages: %+v", e)
	}
	if e := entry(plan, "registers"); e.Status != Skip || !strings.Contains(e.Reason, "names no function") {
		t.Errorf("registers with no surface: %+v", e)
	}

	c, _ = ctx(t, nil, withSpecs)
	c.Cfg.Depcheck.Packages = map[string]config.Gated{"example.com/m": {Decision: "d"}}
	c.Cfg.Registers.UserSurfaces = []string{"internal/api.WriteError"}
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spec-lint", "depcheck", "registers"} {
		if e := entry(plan, name); e.Status != Run {
			t.Errorf("%s has a subject and must run: %+v", name, e)
		}
	}
}

// Applicability that cannot be determined must not resolve to "does not
// apply": that is the vacuous pass this package refuses.
func TestPlanFailsWhenGitCannotAnswer(t *testing.T) {
	broken := func(_ []string, _ bool, name string, _ ...string) ([]byte, error) {
		return nil, errors.New("not a git repository")
	}
	c, _ := ctx(t, nil, broken)
	if _, err := Plan(c); err == nil || !strings.Contains(err.Error(), "cannot list specs/") {
		t.Fatalf("got %v", err)
	}
}

func TestEnumGatesFollowDeclaredDomainsAndWaivers(t *testing.T) {
	c, _ := ctx(t, nil, noSpecs)
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"enum-go", "enum-typescript"} {
		if e := entry(plan, name); e.Status != Skip || e.Reason == "" {
			t.Fatalf("unconfigured %s: %+v", name, e)
		}
	}
	c.Cfg.Enums.Go.Types = []string{"internal/state.Status"}
	c.Cfg.Enums.TypeScript = []config.TypeScriptEnums{{Project: "frontend/tsconfig.json", Types: []string{"src/state.ts#Status"}}}
	c.Cfg.Waive = map[string]config.Waiver{"enum-go": {Reason: "migration", Until: "2026-09-01"}}
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if entry(plan, "enum-go").Status != Waived || entry(plan, "enum-typescript").Status != Run {
		t.Fatalf("wrong enum plan: %+v", plan)
	}
	c.Now = day.AddDate(0, 0, 1)
	plan, err = Plan(c)
	if err != nil || entry(plan, "enum-go").Status != Run {
		t.Fatalf("expired enum waiver: %+v %v", plan, err)
	}
}

func TestTypeScriptGatePassesPolicyToEmbeddedAnalyzer(t *testing.T) {
	called := false
	c, _ := ctx(t, nil, func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		called = true
		if name != "node" || !strings.Contains(strings.Join(args, " "), "src/state.ts#Status") {
			t.Fatalf("wrong analyzer invocation: %s", name)
		}
		return nil, nil
	})
	c.Cfg.Enums.TypeScript = []config.TypeScriptEnums{{Project: "tsconfig.json", Types: []string{"src/state.ts#Status"}}}
	g, _ := Find("enum-typescript")
	if err := g.Run(c); err != nil || !called {
		t.Fatalf("TypeScript gate: %v called=%v", err, called)
	}
	g, _ = Find("enum-go")
	if err := g.Run(c); err == nil {
		t.Fatal("direct Go gate with no domain passed")
	}
}

func TestALiveWaiverSkipsAndAnExpiredOneRuns(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	c.Cfg.Waive = map[string]config.Waiver{
		"lint": {Reason: "the config is being rewritten", Until: "2026-09-01"},
		"vuln": {Reason: "the fix is not released", Until: "2026-08-31"},
	}
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	// until is inclusive: the waiver covers all of the day it names.
	if e := entry(plan, "lint"); e.Status != Waived || e.Until != "2026-09-01" {
		t.Errorf("lint on the day named: %+v", e)
	}
	// The day after, the gate runs and the plan says why.
	if e := entry(plan, "vuln"); e.Status != Run || !strings.Contains(e.Reason, "waiver expired 2026-08-31") {
		t.Errorf("vuln the day after: %+v", e)
	}
}

// A waiver for a gate nobody runs hides a typo in the name of one somebody
// does.
func TestAWaiverForAnUnknownGateFails(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	c.Cfg.Waive = map[string]config.Waiver{"test-race": {Reason: "r", Until: "2026-12-01"}}
	_, err := Plan(c)
	if err == nil || !strings.Contains(err.Error(), "test-race") || !strings.Contains(err.Error(), "race") {
		t.Fatalf("the error must name the bad key and list the gates, got %v", err)
	}
}

func TestListPrintsThePlanAsTextAndJSON(t *testing.T) {
	c, sb := ctx(t, nil, noSpecs)
	c.Cfg.Waive = map[string]config.Waiver{"lint": {Reason: "later", Until: "2026-12-01"}}
	if err := List(c, false); err != nil {
		t.Fatal(err)
	}
	text := sb.String()
	for _, want := range []string{"RUN  fmt-check", "SKIP spec-lint", "WAIV lint", "until 2026-12-01: later", "FOLD race         into suite"} {
		if !strings.Contains(text, want) {
			t.Errorf("text plan lacks %q:\n%s", want, text)
		}
	}

	sb.Reset()
	if err := List(c, true); err != nil {
		t.Fatal(err)
	}
	var plan []Entry
	if err := json.Unmarshal([]byte(sb.String()), &plan); err != nil {
		t.Fatalf("list -json must be JSON: %v\n%s", err, sb.String())
	}
	if e := entry(plan, "lint"); e.Status != Waived || e.Until != "2026-12-01" {
		t.Errorf("JSON plan: %+v", e)
	}
	if e := entry(plan, "spec-lint"); e.Status != Skip {
		t.Errorf("JSON plan: %+v", e)
	}
}

// Check runs every gate the plan says to, keeps going past a failure, and
// puts the summary last. The gates here are stubbed by name.
func TestCheckRunsAllAndReportsAllAtOnce(t *testing.T) {
	saved := Gates
	t.Cleanup(func() { Gates = saved })
	Gates = []Gate{
		{Name: "one", Run: func(Ctx) error { return nil }},
		{Name: "two", Run: func(Ctx) error { return errors.New("two is broken\nsecond line") }},
		{Name: "three", Applies: func(Ctx) (bool, string, error) { return false, "no subject", nil }},
		{Name: "four", Run: func(Ctx) error { return errors.New("four too") }},
		{Name: "five", Run: func(Ctx) error { return nil }},
	}
	c, sb := ctx(t, nil, noSpecs)
	c.Cfg.Waive = map[string]config.Waiver{"five": {Reason: "not yet", Until: "2026-12-01"}}
	err := Check(c)
	if err == nil {
		t.Fatal("a failing gate must fail the run")
	}
	if !strings.Contains(err.Error(), "2 of 3 gates failed: two, four") {
		t.Errorf("verdict = %v", err)
	}
	out := sb.String()
	for _, want := range []string{
		"== one", "== two", "== four",
		"PASS one", "FAIL two          two is broken", "SKIP three        no subject", "FAIL four         four too", "WAIV five         until 2026-12-01: not yet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "== five") || strings.Contains(out, "== three") {
		t.Error("a waived or skipped gate must not run")
	}
	// The summary is last.
	if strings.LastIndex(out, "== ") > strings.Index(out, "PASS one") {
		t.Errorf("the summary must follow every gate's output:\n%s", out)
	}
}

func TestCheckPassesWhenEveryGateDoes(t *testing.T) {
	saved := Gates
	t.Cleanup(func() { Gates = saved })
	Gates = []Gate{{Name: "one", Run: func(Ctx) error { return nil }}}
	c, sb := ctx(t, nil, noSpecs)
	if err := Check(c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "1 gates passed") {
		t.Errorf("output:\n%s", sb.String())
	}
}

func TestCheckReportsAnExpiredWaiverBesideTheVerdict(t *testing.T) {
	saved := Gates
	t.Cleanup(func() { Gates = saved })
	Gates = []Gate{{Name: "one", Run: func(Ctx) error { return errors.New("still broken") }}}
	c, sb := ctx(t, nil, noSpecs)
	c.Cfg.Waive = map[string]config.Waiver{"one": {Reason: "soon", Until: "2026-01-01"}}
	if err := Check(c); err == nil {
		t.Fatal("an expired waiver does not cover a failing gate")
	}
	if !strings.Contains(sb.String(), "waiver expired 2026-01-01") {
		t.Errorf("the summary must say the waiver ran out:\n%s", sb.String())
	}
}

func TestFindAndNames(t *testing.T) {
	if _, ok := Find("cover"); !ok {
		t.Error("cover is a gate")
	}
	if _, ok := Find("test-race"); ok {
		t.Error("test-race is a make target name, not a gate")
	}
	if !Known("race") || Known("nope") {
		t.Error("Known must follow the set")
	}
	if len(Names()) != len(Gates) {
		t.Error("Names must list every gate")
	}
}

// runCover collects when no profile is given and reads the given ones
// otherwise; both paths are visible in the commands it runs.
func TestCoverCollectsUnlessProfilesAreGiven(t *testing.T) {
	var ran []string
	exec := func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	c, _ := ctx(t, nil, exec)
	c.Profiles = []string{"/nonexistent/unit.out"}
	if err := runCover(c); err == nil {
		t.Fatal("a profile that does not exist must fail rather than be collected over")
	}
	if len(ran) != 0 {
		t.Errorf("with profiles given nothing is collected; ran %v", ran)
	}

	c.Profiles = nil
	_ = runCover(c) // the collected profile is not there in this stub, so Run fails
	if len(ran) != 1 || !strings.HasPrefix(ran[0], "go test ./... -covermode=atomic") {
		t.Errorf("without profiles the suite is collected; ran %v", ran)
	}
}

func TestLineFormatsEachStatus(t *testing.T) {
	for _, tc := range []struct {
		e    Entry
		mark string
		want string
	}{
		{Entry{Name: "a", Status: Run}, "PASS", "PASS a"},
		{Entry{Name: "a", Status: Run, Reason: "waiver expired x"}, "PASS", "PASS a            waiver expired x"},
		{Entry{Name: "b", Status: Skip, Reason: "why"}, "", "SKIP b            why"},
		{Entry{Name: "c", Status: Waived, Reason: "why", Until: "2026-12-01"}, "", "WAIV c            until 2026-12-01: why"},
		{Entry{Name: "d", Status: Folded, Into: "suite"}, "", "FOLD d            into suite"},
		{Entry{Name: "e", Status: Folded, Into: "suite", Reason: "waived until x: y"}, "", "FOLD e            into suite, waived until x: y"},
	} {
		if got := line(tc.e, tc.mark, ""); got != tc.want {
			t.Errorf("line(%+v, %q) = %q, want %q", tc.e, tc.mark, got, tc.want)
		}
	}
}

// The five suite gates fold into one run: the plan says so per gate, and the
// JSON is what the pipeline reads to leave them out of its job matrix.
func TestTheSuiteGatesFoldIntoSuite(t *testing.T) {
	c, sb := ctx(t, nil, withSpecs)
	if err := List(c, true); err != nil {
		t.Fatal(err)
	}
	var plan []Entry
	if err := json.Unmarshal([]byte(sb.String()), &plan); err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, Suite); e.Status != Run || e.Reason != "" {
		t.Errorf("suite: %+v", e)
	}
	for _, name := range []string{"test", "race", "cover", "tempdir", "hermetic"} {
		if e := entry(plan, name); e.Status != Folded || e.Into != Suite {
			t.Errorf("%s: %+v, want folded into suite", name, e)
		}
	}
	for _, want := range []string{`{"name":"race","status":"folded","into":"suite"}`, `{"name":"suite","status":"run"}`} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("list -json lacks %s:\n%s", want, sb.String())
		}
	}
}

// A live waiver on a folded gate turns its property off in the one run; an
// expired one leaves it on and says so. Both show on the suite's plan line.
func TestAWaiverNarrowsTheSuite(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	c.Cfg.Waive = map[string]config.Waiver{
		"race":     {Reason: "flaky under load", Until: "2026-09-30"},
		"hermetic": {Reason: "needs git", Until: "2026-09-30"},
		"tempdir":  {Reason: "fixtures leak", Until: "2026-09-30"},
		"cover":    {Reason: "was behind", Until: "2026-08-01"},
	}
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	s := entry(plan, Suite)
	for _, want := range []string{
		"without -race (race waived until 2026-09-30: flaky under load)",
		"with the full PATH (hermetic waived until 2026-09-30: needs git)",
		"outside the TMPDIR sandbox (tempdir waived until 2026-09-30: fixtures leak)",
		"cover waiver expired 2026-08-01: was behind",
	} {
		if s.Status != Run || !strings.Contains(s.Reason, want) {
			t.Errorf("suite %+v lacks %q", s, want)
		}
	}
	if e := entry(plan, "race"); e.Status != Folded || e.Reason != "waived until 2026-09-30: flaky under load" {
		t.Errorf("race: %+v", e)
	}
	if e := entry(plan, "cover"); e.Status != Folded || !strings.HasPrefix(e.Reason, "waiver expired") {
		t.Errorf("cover: %+v", e)
	}

	run, _ := suiteRun(c)
	if run.Race || run.Hermetic || run.Sandbox {
		t.Errorf("waived properties must be off: %+v", run)
	}
	if run.Floor == nil || run.Profile == "" {
		t.Error("an expired cover waiver keeps the floor")
	}
	c.Cfg.Waive["cover"] = config.Waiver{Reason: "behind", Until: "2026-12-01"}
	if run, _ := suiteRun(c); run.Floor != nil || run.Profile != "" {
		t.Error("a live cover waiver drops the floor and the profile")
	}
}

// The suite is the test run, so waiving test waives the suite, unless the
// suite carries a waiver of its own.
func TestAWaivedTestWaivesTheSuite(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	c.Cfg.Waive = map[string]config.Waiver{"test": {Reason: "no Go yet", Until: "2026-12-01"}}
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, Suite); e.Status != Waived || e.Reason != "test is waived: no Go yet" || e.Until != "2026-12-01" {
		t.Errorf("suite: %+v", e)
	}
	c.Cfg.Waive[Suite] = config.Waiver{Reason: "the suite's own", Until: "2026-10-01"}
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, Suite); e.Status != Waived || e.Reason != "the suite's own" {
		t.Errorf("suite with its own waiver: %+v", e)
	}
	c.Cfg.Waive[Suite] = config.Waiver{Reason: "ran out", Until: "2026-08-01"}
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, Suite); e.Status != Run || !strings.HasPrefix(e.Reason, "waiver expired 2026-08-01: ran out") {
		t.Errorf("suite with an expired waiver of its own: %+v", e)
	}
}

// A tempdir that watches another runner watches a suite the go test run is
// not, so it stays a gate of its own and the suite runs unsandboxed.
func TestATempdirWithItsOwnCommandStaysAGate(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	c.Cfg.TempDir.Command = []string{"pytest", "-q"}
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "tempdir"); e.Status != Run {
		t.Errorf("tempdir: %+v", e)
	}
	if run, _ := suiteRun(c); run.Sandbox {
		t.Error("the suite must not sandbox a run tempdir does not watch")
	}
}

// Check runs the suite in place of the five: one go test, whatever the gate
// count.
func TestCheckRunsTheSuiteOnceInPlaceOfTheFive(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	tests := 0
	exec := func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name == "git" {
			return []byte("specs/001-a.md\n"), nil
		}
		if len(args) > 0 && args[0] == "test" {
			tests++
		}
		return nil, nil
	}
	c, sb := ctx(t, nil, exec)
	c.GoBin = "/toolchain/bin/go"
	c.Cfg.Waive = map[string]config.Waiver{"race": {Reason: "r", Until: "2026-12-01"}}
	_ = Check(c) // the stubbed gates fail; what matters is what ran
	if tests != 1 {
		t.Errorf("go test ran %d times, want once", tests)
	}
	out := sb.String()
	for _, want := range []string{"== suite", "suite runs without -race (race waived until 2026-12-01: r)", "FOLD race         into suite, waived until 2026-12-01: r"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	for _, name := range []string{"test", "race", "cover", "tempdir", "hermetic"} {
		if strings.Contains(out, "== "+name+"\n") {
			t.Errorf("%s ran on its own", name)
		}
	}
}
