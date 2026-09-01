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

	c, _ = ctx(t, nil, withSpecs)
	c.Cfg.Depcheck.Packages = map[string]config.Gated{"example.com/m": {Decision: "d"}}
	plan, err = Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"spec-lint", "depcheck"} {
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

func TestALiveWaiverSkipsAndAnExpiredOneRuns(t *testing.T) {
	c, _ := ctx(t, nil, withSpecs)
	c.Cfg.Waive = map[string]config.Waiver{
		"cover": {Reason: "the gap is in handler", Until: "2026-09-01"},
		"race":  {Reason: "runner is not race-clean", Until: "2026-08-31"},
	}
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	// until is inclusive: the waiver covers all of the day it names.
	if e := entry(plan, "cover"); e.Status != Waived || e.Until != "2026-09-01" {
		t.Errorf("cover on the day named: %+v", e)
	}
	// The day after, the gate runs and the plan says why.
	if e := entry(plan, "race"); e.Status != Run || !strings.Contains(e.Reason, "waiver expired 2026-08-31") {
		t.Errorf("race the day after: %+v", e)
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
	c.Cfg.Waive = map[string]config.Waiver{"cover": {Reason: "later", Until: "2026-12-01"}}
	if err := List(c, false); err != nil {
		t.Fatal(err)
	}
	text := sb.String()
	for _, want := range []string{"RUN  fmt-check", "SKIP spec-lint", "WAIV cover", "until 2026-12-01: later"} {
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
	if e := entry(plan, "cover"); e.Status != Waived || e.Until != "2026-12-01" {
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
	} {
		if got := line(tc.e, tc.mark, ""); got != tc.want {
			t.Errorf("line(%+v, %q) = %q, want %q", tc.e, tc.mark, got, tc.want)
		}
	}
}
