// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// db renders a make rule database holding exactly the named targets.
func db(targets ...string) string {
	var b strings.Builder
	b.WriteString("# GNU Make 3.81\n# Files\n\n")
	for _, t := range targets {
		b.WriteString(t + ":\n#  Phony target.\n\n")
	}
	return b.String()
}

func canned(out string) gates.Exec {
	return func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
		return []byte(out), nil
	}
}

var day = time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)

func TestAllHeld(t *testing.T) {
	var b strings.Builder
	if err := Run(config.Contract{}, &b, canned(db(Required...)), day); err != nil {
		t.Fatalf("a repository holding every gate must pass: %v", err)
	}
	if !strings.Contains(b.String(), "9 required gates held, 0 waived") {
		t.Errorf("want the held count, got %q", b.String())
	}
}

func TestMissingReportsEveryGateAtOnce(t *testing.T) {
	held := []string{"fmt-check", "test", "lint", "lint-config", "lint-modernize"}
	err := Run(config.Contract{}, &strings.Builder{}, canned(db(held...)), day)
	if err == nil {
		t.Fatal("a repository missing four gates must fail")
	}
	// One run has to name all of them: a gate per push is four pushes.
	for _, want := range []string{"test-hermetic", "test-race", "cover", "spec-lint"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q named in one run, got %v", want, err)
		}
	}
}

func TestWaiverAdmitsAndExpiryFails(t *testing.T) {
	held := []string{"fmt-check", "test", "test-hermetic", "test-race", "lint", "lint-config", "lint-modernize", "spec-lint"}
	cfg := config.Contract{Exempt: map[string]config.Waiver{
		"cover": {Reason: "the suite needs Postgres beside it", Until: "2026-11-01"},
	}}

	var b strings.Builder
	if err := Run(cfg, &b, canned(db(held...)), day); err != nil {
		t.Fatalf("a live waiver must admit the gate: %v", err)
	}
	if !strings.Contains(b.String(), "waived: cover until 2026-11-01") {
		t.Errorf("a waiver must be visible in the log, got %q", b.String())
	}

	// The same config, one day past the date.
	past := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	err := Run(cfg, &strings.Builder{}, canned(db(held...)), past)
	if err == nil {
		t.Fatal("an expired waiver must fail: a warning is a reason to write the next one")
	}
	// Expiry and absence call for different work, so they read differently.
	if !strings.Contains(err.Error(), "exemption ran out") {
		t.Errorf("want an expiry, not an absence, got %v", err)
	}
}

// until names a day and covers all of it. An exemption that died at
// midnight on the date it names would not cover the day it names, which is
// not what anybody writing one means.
func TestExpiryIsInclusiveOfTheDayItNames(t *testing.T) {
	held := []string{"fmt-check", "test", "test-hermetic", "test-race", "lint", "lint-config", "lint-modernize", "spec-lint"}
	cfg := config.Contract{Exempt: map[string]config.Waiver{
		"cover": {Reason: "the suite needs Postgres beside it", Until: "2026-11-01"},
	}}
	e := canned(db(held...))

	for _, tc := range []struct {
		name string
		now  time.Time
		live bool
	}{
		{"the morning of the day named", time.Date(2026, 11, 1, 0, 0, 1, 0, time.UTC), true},
		{"the last second of it", time.Date(2026, 11, 1, 23, 59, 59, 0, time.UTC), true},
		{"the day after", time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(cfg, &strings.Builder{}, e, tc.now)
			if tc.live && err != nil {
				t.Fatalf("the exemption must still cover %s: %v", tc.now.Format(time.DateOnly), err)
			}
			if !tc.live && err == nil {
				t.Fatalf("the exemption must be dead on %s", tc.now.Format(time.DateOnly))
			}
		})
	}
}

func TestExemptionForUnknownGateFails(t *testing.T) {
	cfg := config.Contract{Exempt: map[string]config.Waiver{
		"convr": {Reason: "typo for cover", Until: "2026-11-01"},
	}}
	err := Run(cfg, &strings.Builder{}, canned(db(Required...)), day)
	if err == nil || !strings.Contains(err.Error(), "not a required gate") {
		t.Fatalf("a misspelt gate name must fail rather than silently do nothing, got %v", err)
	}
}

func TestEmptyDatabaseFails(t *testing.T) {
	err := Run(config.Contract{}, &strings.Builder{}, canned(""), day)
	if err == nil || !strings.Contains(err.Error(), "vacuously") {
		t.Fatalf("make failing to run must not read as a repository with no targets, got %v", err)
	}
}

// A file named like a required gate must not answer for it. `make -n test`
// succeeds when a test/ directory exists, and `make -n cover` when a cover
// file does -- both are ordinary things to have in a Go repository. The same
// shape was live in this organisation with a LICENSE file answering for a
// `license` target on a case-insensitive filesystem.
func TestFileDoesNotSatisfyATarget(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	dir := t.TempDir()
	write(t, dir, "Makefile", "fmt-check:\n\t@true\n")
	// Two required gate names, neither defined as a target: one a directory,
	// one a plain file.
	if err := os.Mkdir(filepath.Join(dir, "test"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "cover", "not a gate\n")

	held, err := targets(gates.OSExec(dir, &strings.Builder{}))
	if err != nil {
		t.Fatal(err)
	}
	if !held["fmt-check"] {
		t.Error("the one real target must be found")
	}
	for _, shadow := range []string{"test", "cover"} {
		if held[shadow] {
			t.Errorf("%q is a file or directory, not a make target", shadow)
		}
	}

	// And the gate built on it must fail, naming both.
	err = Run(config.Contract{}, &strings.Builder{}, gates.OSExec(dir, &strings.Builder{}), day)
	if err == nil {
		t.Fatal("a repository whose only target is fmt-check must fail")
	}
	for _, want := range []string{"test", "cover"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q reported missing, got %v", want, err)
		}
	}
}

// The rule database of a real Makefile is large. Matching it through a pipe
// that closes early kills make with SIGPIPE, which exits 141 and reads as a
// repository with no targets at all.
func TestLargeDatabaseIsReadWhole(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	dir := t.TempDir()
	var mk strings.Builder
	for _, g := range Required {
		mk.WriteString(g + ":\n\t@true\n")
	}
	// Enough rules that the database does not fit one pipe buffer.
	for i := range 4000 {
		mk.WriteString("filler" + itoa(i) + ":\n\t@true\n")
	}
	write(t, dir, "Makefile", mk.String())

	var b strings.Builder
	if err := Run(config.Contract{}, &b, gates.OSExec(dir, &b), day); err != nil {
		t.Fatalf("every required gate is defined, so this must pass: %v", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
