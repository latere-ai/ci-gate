// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package golangci

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

type call struct {
	name string
	args []string
}

func record(calls *[]call, err error) func([]string, bool, string, ...string) ([]byte, error) {
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{name, args})
		return nil, err
	}
}

// A Go module in a git checkout, so Write can read the module path and see
// the file is untracked.
func module(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The render happens before the linter, every time: golangci-lint with no
// config lints its own defaults, and a stale file lints last month's.
func TestLintRendersThenRunsThePinnedLinter(t *testing.T) {
	dir := module(t)
	var calls []call
	var sb strings.Builder
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Lint(dir, cfg, "go", &sb, record(&calls, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, Name)); err != nil {
		t.Fatalf("%s must be rendered before the linter runs: %v", Name, err)
	}
	if len(calls) != 1 {
		t.Fatalf("ran %v", calls)
	}
	want := "go run " + Module + "@" + Version + " run ./..."
	if got := calls[0].name + " " + strings.Join(calls[0].args, " "); got != want {
		t.Errorf("ran %q, want %q", got, want)
	}
	if !strings.Contains(sb.String(), "wrote") || !strings.Contains(sb.String(), Version) {
		t.Errorf("report:\n%s", sb.String())
	}
}

func TestLintReportsFindings(t *testing.T) {
	dir := module(t)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var calls []call
	err = Lint(dir, cfg, "go", &strings.Builder{}, record(&calls, errors.New("exit 1")))
	if err == nil || !strings.Contains(err.Error(), "reported findings") {
		t.Fatalf("got %v", err)
	}
}

// A repository that keeps its own config is linted against it, and the
// reason is printed so the exception stays visible.
func TestLintLeavesADeclaredOwnConfigAlone(t *testing.T) {
	dir := module(t)
	own := "linters:\n  enable: [govet]\n"
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Golangci.Own = "a vendored tree with its own lint history"
	var calls []call
	var sb strings.Builder
	if err := Lint(dir, cfg, "go", &sb, record(&calls, nil)); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, Name))
	if string(body) != own {
		t.Error("a declared own config must not be overwritten")
	}
	if !strings.Contains(sb.String(), "own lint history") {
		t.Errorf("the reason must be printed:\n%s", sb.String())
	}
	if len(calls) != 1 {
		t.Errorf("the linter still runs; ran %v", calls)
	}
}

func TestLintFailsWhenTheDeclaredOwnConfigIsMissing(t *testing.T) {
	dir := module(t)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Golangci.Own = "a reason"
	var calls []call
	if err := Lint(dir, cfg, "go", &strings.Builder{}, record(&calls, nil)); err == nil {
		t.Fatal("golangci.own pointing at no file is a repository that lost its config")
	}
	if len(calls) != 0 {
		t.Error("nothing should be linted against a config that is not there")
	}
}
