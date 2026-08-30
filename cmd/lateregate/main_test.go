// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func out(t *testing.T, argv ...string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := run(argv, &sb)
	return sb.String(), err
}

func TestNoCommandPrintsUsageAndFails(t *testing.T) {
	s, err := out(t)
	if err == nil {
		t.Fatal("no command must fail")
	}
	if !strings.Contains(s, "Usage:") {
		t.Errorf("usage should be printed:\n%s", s)
	}
}

func TestHelpSucceeds(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		s, err := out(t, arg)
		if err != nil {
			t.Errorf("%s should succeed: %v", arg, err)
		}
		if !strings.Contains(s, "spec-lint") {
			t.Errorf("%s should list the commands:\n%s", arg, s)
		}
	}
}

func TestAnUnknownCommandFails(t *testing.T) {
	s, err := out(t, "cover-everything")
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("want an unknown-command error, got %v", err)
	}
	if !strings.Contains(s, "Usage:") {
		t.Errorf("usage should be printed:\n%s", s)
	}
}

func TestABadFlagFails(t *testing.T) {
	if _, err := out(t, "cover", "-nope"); err == nil {
		t.Fatal("an unknown flag must fail")
	}
}

// A broken config must stop every command, not just the one that reads that
// section: a repository with an unparseable gate config has no gates.
func TestABrokenConfigStopsEveryCommand(t *testing.T) {
	dir := t.TempDir()
	body := "cover:\n  exempt:\n    internal/a: \"\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".lateregate.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"cover", "spec-lint", "fmt-check", "modernize", "hermetic"} {
		if _, err := out(t, cmd, "-C", dir); err == nil {
			t.Errorf("%s should refuse to run with an invalid config", cmd)
		}
	}
}

func TestCoverReadsTheProfileAndTheConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := "cover:\n  threshold: 90\n  trim_prefix: mod/\n"
	if err := os.WriteFile(filepath.Join(dir, ".lateregate.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(profile, []byte("mode: set\nmod/a/a.go:1.1,2.1 10 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := out(t, "cover", "-C", dir, "-profile", profile)
	if err == nil {
		t.Fatal("0% must fail a 90% floor")
	}
	if !strings.Contains(s, "FAIL") || !strings.Contains(s, "a ") {
		t.Errorf("the report should name the package:\n%s", s)
	}
}

func TestSpecLintIsANoopWithoutConfig(t *testing.T) {
	s, err := out(t, "spec-lint", "-C", t.TempDir())
	if err != nil {
		t.Fatalf("an unconfigured spec tree should not fail: %v", err)
	}
	if !strings.Contains(s, "nothing to check") {
		t.Errorf("report:\n%s", s)
	}
}

// The gates that shell out are wired to a real toolchain here, which is what
// makes this a check of the wiring rather than of the logic.
func TestFmtCheckRunsAgainstThisRepository(t *testing.T) {
	// The repository root, not a temp directory: the file list comes from git
	// now, and an empty directory proves nothing. It used to pass here by
	// scanning nothing at all.
	if _, err := out(t, "fmt-check", "-C", "../.."); err != nil {
		t.Errorf("this repository is gofmt-clean, so the gate should pass: %v", err)
	}
}

// A directory git knows nothing about is not a formatted repository.
func TestFmtCheckFailsOutsideACheckout(t *testing.T) {
	if _, err := out(t, "fmt-check", "-C", t.TempDir()); err == nil {
		t.Error("a non-checkout must fail rather than report a clean tree")
	}
}

func TestHermeticReportsAMissingToolchain(t *testing.T) {
	if _, err := out(t, "hermetic", "-C", t.TempDir(), "-go", "no-such-go-binary"); err == nil {
		t.Fatal("a missing toolchain must fail")
	}
}

// tempdir takes its command after --, so a repo that is not a Go module still
// reaches the gate. `true` touches nothing, which is the vacuous-pass refusal
// and proves the argv reached the gate.
func TestTempDirTakesItsCommandAfterTheSeparator(t *testing.T) {
	_, err := out(t, "tempdir", "-C", t.TempDir(), "--", "true")
	if err == nil || !strings.Contains(err.Error(), "did not use it") {
		t.Fatalf("want the unused-sandbox refusal, got %v", err)
	}
}

// The gate reads this repository's own config and its own tree, which is the
// only way the two stay true to each other.
func TestLicenseRunsAgainstThisRepository(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	s, err := out(t, "license", "-C", root)
	if err != nil {
		t.Fatalf("this repository declares MIT on every Go file: %v\n%s", err, s)
	}
	if !strings.Contains(s, "MIT declared on") {
		t.Errorf("report:\n%s", s)
	}
}
