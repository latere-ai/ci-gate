// Copyright 2026 Latere AI.
// Licensed under the Apache License, Version 2.0.

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

func run(t *testing.T, c config.Cover, p string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(c, p, &sb)
	return sb.String(), err
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

// The behaviour learned the hard way: with -coverpkg=./... a block appears
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

// The shape tgo was in at M0 and the one a new adopter hits first: every
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
	if err := Run(cfg(), t.TempDir(), io.Discard); err == nil {
		t.Fatal("a directory in place of a profile must be an error")
	}
}
