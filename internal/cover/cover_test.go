// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

const mod = "mod/"

func profile(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "coverage.out")
	body := "mode: set\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cfg() config.Cover {
	return config.Cover{Threshold: 90, TrimPrefix: mod}
}

// run gates the given profiles with no package list, so these cases judge
// only what the profiles say. The list is exercised by the tests that pass
// one explicitly.
func run(t *testing.T, c config.Cover, profiles ...string) (string, error) {
	t.Helper()
	return runList(t, c, nil, profiles...)
}

func runList(t *testing.T, c config.Cover, list Lister, profiles ...string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(c, profiles, &sb, list)
	return sb.String(), err
}

// known is a Lister reporting a fixed package set.
func known(pkgs ...string) Lister {
	return func() ([]string, error) { return pkgs, nil }
}

func TestAPackageBelowTheFloorFails(t *testing.T) {
	p := profile(t,
		mod+"good/a.go:1.1,2.1 10 1",
		mod+"bad/b.go:1.1,2.1 8 1",
		mod+"bad/b.go:3.1,4.1 2 0",
	)
	out, err := run(t, cfg(), p)
	if err == nil {
		t.Fatal("80% must fail a 90% floor")
	}
	if !strings.Contains(err.Error(), "bad 80.0%") {
		t.Errorf("error should name the package and its number, got %q", err)
	}
	if !strings.Contains(out, "FAIL") || !strings.Contains(out, "(8/10 statements)") {
		t.Errorf("report should mark the failure with counts:\n%s", out)
	}
}

// -coverpkg=./... instruments the Go source a JavaScript package ships under
// node_modules, and no test of the repository runs it. The floor judges the
// module's own packages, so those rows are left out instead of failing the
// gate on every workstation with the frontend installed.
func TestAPackageUnderNodeModulesIsNotJudged(t *testing.T) {
	p := profile(t,
		mod+"good/a.go:1.1,2.1 10 1",
		mod+"frontend/node_modules/flatted/golang/pkg/flatted/flatted.go:1.1,2.1 164 0",
	)
	out, err := run(t, cfg(), p)
	if err != nil {
		t.Fatalf("a dependency's untested Go source must not fail the floor: %v", err)
	}
	if strings.Contains(out, "node_modules") {
		t.Errorf("the report lists the module's own packages only:\n%s", out)
	}
	if !strings.Contains(out, "(1 measured)") {
		t.Errorf("one package of the module was measured:\n%s", out)
	}
}

// A profile that holds a dependency's rows and nothing else measured none of
// the module's packages, which is the tests not having run.
func TestAProfileOfNodeModulesAloneCoversNothing(t *testing.T) {
	p := profile(t, mod+"frontend/node_modules/flatted/golang/pkg/flatted/flatted.go:1.1,2.1 164 164")
	_, err := run(t, cfg(), p)
	if err == nil || !strings.Contains(err.Error(), "covers no packages") {
		t.Fatalf("got %v", err)
	}
}

func TestEveryPackageClearingTheFloorPasses(t *testing.T) {
	p := profile(t, mod+"a/a.go:1.1,2.1 10 1", mod+"b/b.go:1.1,2.1 5 3")
	out, err := run(t, cfg(), p)
	if err != nil {
		t.Fatalf("full coverage should pass: %v", err)
	}
	if !strings.Contains(out, "(2 measured)") {
		t.Errorf("report should say how many packages were measured:\n%s", out)
	}
}

// The behavior learned the hard way: with -coverpkg=./... a block appears
// once per test binary that executed it. Counting each appearance inflates
// the totals, so a block is counted once and covered if any run covered it.
func TestARepeatedBlockIsCountedOnce(t *testing.T) {
	p := profile(t,
		mod+"a/a.go:1.1,2.1 10 0", // first binary never reached it
		mod+"a/a.go:1.1,2.1 10 1", // second one did
		mod+"a/a.go:1.1,2.1 10 0",
	)
	out, err := run(t, cfg(), p)
	if err != nil {
		t.Fatalf("the covered run should decide the block: %v", err)
	}
	if !strings.Contains(out, "(10/10 statements)") {
		t.Errorf("three appearances of one 10-statement block is still 10:\n%s", out)
	}
}

func TestAProfileCoveringNothingFails(t *testing.T) {
	if _, err := run(t, cfg(), profile(t)); err == nil {
		t.Fatal("an empty profile must fail rather than pass vacuously")
	} else if !strings.Contains(err.Error(), "did the tests run") {
		t.Errorf("error should point at the cause, got %q", err)
	}
}

// The shape forma was in at M0 and the one a new adopter hits first: every
// package exempt, nothing measured, and a gate reporting green forever.
func TestAProfileWhereEverythingIsExemptFails(t *testing.T) {
	c := cfg()
	c.Exempt = map[string]string{"a": "why", "b": "why"}
	out, err := run(t, c, profile(t, mod+"a/a.go:1.1,2.1 10 0", mod+"b/b.go:1.1,2.1 4 0"))
	if err == nil {
		t.Fatal("an all-exempt profile must fail rather than pass vacuously")
	}
	if !strings.Contains(err.Error(), "vacuously") {
		t.Errorf("error should name the failure mode, got %q", err)
	}
	if !strings.Contains(out, "exempt: why") {
		t.Errorf("the report should still show the exemptions and their reasons:\n%s", out)
	}
}

func TestAnExemptPackageBelowTheFloorPasses(t *testing.T) {
	c := cfg()
	c.Exempt = map[string]string{"gen": "generated code; the generator is tested"}
	out, err := run(t, c, profile(t, mod+"gen/g.go:1.1,2.1 10 0", mod+"a/a.go:1.1,2.1 10 1"))
	if err != nil {
		t.Fatalf("an exempt package should not fail the gate: %v", err)
	}
	if !strings.Contains(out, "exempt: generated code") {
		t.Errorf("the reason belongs in the report:\n%s", out)
	}
}

// A package with no statements is not a 0% package; dividing by zero there
// would fail every repo with a types-only or constants-only package.
func TestAPackageWithNoStatementsIsSkipped(t *testing.T) {
	if _, err := run(t, cfg(), profile(t, mod+"types/t.go:1.1,2.1 0 0", mod+"a/a.go:1.1,2.1 4 1")); err != nil {
		t.Fatalf("a statement-free package should not fail the gate: %v", err)
	}
}

func TestAMissingProfileIsAnError(t *testing.T) {
	if _, err := run(t, cfg(), filepath.Join(t.TempDir(), "nope.out")); err == nil {
		t.Fatal("a missing profile must be an error")
	}
}

// Silently skipping a malformed line lets a truncated profile look like a
// smaller but passing one.
func TestMalformedLinesAreErrors(t *testing.T) {
	for name, line := range map[string]string{
		"no separator":  "garbage",
		"missing field": mod + "a/a.go:1.1,2.1 10",
		"bad stmts":     mod + "a/a.go:1.1,2.1 x 1",
		"bad count":     mod + "a/a.go:1.1,2.1 10 x",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := run(t, cfg(), profile(t, line)); err == nil {
				t.Errorf("%q must be an error, not a skipped line", line)
			}
		})
	}
}

func TestAnUntrimmedPrefixIsLeftAlone(t *testing.T) {
	out, err := run(t, config.Cover{Threshold: 90}, profile(t, mod+"a/a.go:1.1,2.1 10 1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mod+"a") {
		t.Errorf("with no trim_prefix the full path should show:\n%s", out)
	}
}

func TestAnUnreadableProfileIsAnError(t *testing.T) {
	if err := Run(cfg(), []string{t.TempDir()}, io.Discard, nil); err == nil {
		t.Fatal("a directory in place of a profile must be an error")
	}
}

func TestNoProfileIsAnError(t *testing.T) {
	if err := Run(cfg(), nil, io.Discard, nil); err == nil {
		t.Fatal("a gate with nothing to read must not report a pass")
	}
}

// The tiers of a service split coverage: the unit run reaches the pure logic
// and the integration run reaches what sits behind the database. Gating on one
// of them alone fails code the other proves.
func TestTiersMergeAsAUnion(t *testing.T) {
	unit := profile(t,
		mod+"a/a.go:1.1,2.1 5 1",
		mod+"a/a.go:3.1,4.1 5 0",
	)
	integration := profile(t,
		mod+"a/a.go:1.1,2.1 5 0",
		mod+"a/a.go:3.1,4.1 5 1",
	)
	if _, err := run(t, cfg(), unit); err == nil {
		t.Fatal("the unit tier alone reaches half the statements and must fail")
	}
	out, err := run(t, cfg(), unit, integration)
	if err != nil {
		t.Fatalf("a block either tier covered is covered:\n%s\n%v", out, err)
	}
	if !strings.Contains(out, "(10/10 statements)") {
		t.Errorf("merging must union the blocks, not sum them:\n%s", out)
	}
}

// A package with no test file produces no profile records at all, so the floor
// never applies to it. Without the package list it passes by being absent,
// which is exactly the hole the per-package floor exists to close.
func TestAPackageWithNoCoverageDataFails(t *testing.T) {
	p := profile(t, mod+"a/a.go:1.1,2.1 10 1")
	out, err := runList(t, cfg(), known(mod+"a", mod+"untested"), p)
	if err == nil {
		t.Fatal("a package absent from the profile must not clear the floor by absence")
	}
	if !strings.Contains(err.Error(), "untested") {
		t.Errorf("the error must name the unmeasured package, got %q", err)
	}
	if !strings.Contains(out, "MISS") {
		t.Errorf("the report must show the unmeasured package:\n%s", out)
	}
}

func TestAnExemptPackageMayBeUnmeasured(t *testing.T) {
	c := cfg()
	c.Exempt = map[string]string{"harness": "shells out to a real binary"}
	p := profile(t, mod+"a/a.go:1.1,2.1 10 1")
	if out, err := runList(t, c, known(mod+"a", mod+"harness"), p); err != nil {
		t.Fatalf("an exemption carries a reason and covers being untested too:\n%s\n%v", out, err)
	}
}

func TestAFailingListerIsAnError(t *testing.T) {
	list := func() ([]string, error) { return nil, os.ErrPermission }
	p := profile(t, mod+"a/a.go:1.1,2.1 10 1")
	if _, err := runList(t, cfg(), list, p); err == nil {
		t.Fatal("a package list that cannot be read must fail rather than skip the rule")
	}
}

// A block the base's run covered that the change's run does not, or does
// not hold at all, is coverage lost; one covered by both, or by neither,
// is not, and a block only the change's run covers is coverage gained.
func TestLostNamesThePackagesThatLostABlock(t *testing.T) {
	base := profile(t,
		mod+"kept/k.go:1.1,2.1 4 1",
		mod+"kept/k.go:3.1,4.1 4 0",
		mod+"lost/l.go:1.1,2.1 4 3",
		mod+"gone/g.go:1.1,2.1 2 1",
		mod+"frontend/node_modules/x/x.go:1.1,2.1 9 1",
	)
	now := profile(t,
		mod+"kept/k.go:1.1,2.1 4 2",
		mod+"kept/k.go:3.1,4.1 4 1",
		mod+"lost/l.go:1.1,2.1 4 0",
	)
	got, err := Lost(base, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != mod+"gone "+mod+"lost" {
		t.Errorf("lost %v", got)
	}
	if _, err := Lost(filepath.Join(t.TempDir(), "none"), now); err == nil {
		t.Error("a base profile that cannot be read must fail")
	}
	if _, err := Lost(base, filepath.Join(t.TempDir(), "none")); err == nil {
		t.Error("a profile that cannot be read must fail")
	}
}

// RunOn judges only the packages it names, however many more the profile
// measured.
func TestRunOnJudgesOnlyThePackagesNamed(t *testing.T) {
	p := profile(t,
		mod+"changed/c.go:1.1,2.1 10 1",
		mod+"reached/r.go:1.1,2.1 10 0",
	)
	var sb strings.Builder
	if err := RunOn(cfg(), []string{p}, &sb, known(mod+"changed"), []string{mod + "changed"}); err != nil {
		t.Fatalf("a package the floor was not asked about cannot fail it: %v\n%s", err, sb.String())
	}
	if strings.Contains(sb.String(), "reached") {
		t.Errorf("an unjudged package is not reported:\n%s", sb.String())
	}
	sb.Reset()
	err := RunOn(cfg(), []string{p}, &sb, known(mod+"changed", mod+"missing"), []string{mod + "changed", mod + "missing"})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("a named package with no data is unmeasured: %v", err)
	}
}
