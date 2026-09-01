// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
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

// No command is the whole bar. The plan is validated before any gate runs,
// so a bad waiver stops it there; that is what makes this testable without
// running fourteen gates against a temp directory.
func TestNoCommandRunsCheck(t *testing.T) {
	dir := t.TempDir()
	body := "waive:\n  test-race:\n    reason: r\n    until: 2026-12-01\n"
	if err := os.WriteFile(filepath.Join(dir, ".lateregate.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := out(t, "-C", dir)
	if err == nil || !strings.Contains(err.Error(), "is not a gate") {
		t.Fatalf("no command must run check, which validates the plan first: %v", err)
	}
	if _, err := out(t, "check", "-C", dir); err == nil || !strings.Contains(err.Error(), "is not a gate") {
		t.Fatalf("check by name is the same: %v", err)
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

// Whether spec-lint applies is decided by check and list, which ask git. A
// direct call on a tree with no specs measures nothing, and nothing is not
// a pass.
func TestSpecLintFailsOnATreeWithNoSpecs(t *testing.T) {
	_, err := out(t, "spec-lint", "-C", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "holds no specs") {
		t.Fatalf("got %v", err)
	}
}

// A fresh checkout: git answers, so the plan can be made, and it says
// spec-lint has no subject here.
func TestListPlansAgainstACheckout(t *testing.T) {
	dir := checkout(t)
	s, err := out(t, "list", "-C", dir, "-json")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, s)
	}
	if !strings.Contains(s, `"name":"spec-lint","status":"skip"`) {
		t.Errorf("plan:\n%s", s)
	}
	s, err = out(t, "list", "-C", dir)
	if err != nil || !strings.Contains(s, "RUN  fmt-check") {
		t.Errorf("text plan: %v\n%s", err, s)
	}
}

// init then contract: the wiring init writes is the wiring contract wants,
// apart from the pin, which is a `go get`.
func TestInitThenContract(t *testing.T) {
	dir := checkout(t)
	if _, err := out(t, "init", "-C", dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.27\n\ntool latere.ai/x/ci-gate/cmd/lateregate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := out(t, "contract", "-C", dir); err != nil {
		t.Fatalf("contract after init: %v\n%s", err, s)
	}
}

func TestHookFailsOutsideACheckout(t *testing.T) {
	if _, err := out(t, "hook", "-C", t.TempDir()); err == nil {
		t.Error("git cannot list staged files outside a checkout")
	}
}

// The repository gates itself with its own binary. contract is the wiring
// check, and this tree is the reference for the shape it checks.
func TestContractPassesOnThisRepository(t *testing.T) {
	if s, err := out(t, "contract", "-C", "../.."); err != nil {
		t.Errorf("this repository must be in shape: %v\n%s", err, s)
	}
}

func checkout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if outb, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, outb)
		}
	}
	return dir
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
