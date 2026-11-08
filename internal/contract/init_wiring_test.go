// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package contract

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// gitRepo is an empty directory that is a git repository, with git's own
// configuration out of the way so core.hooksPath starts unset.
func gitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// runInit runs init over real git in dir and returns what it printed.
func runInit(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := Init(dir, &config.Config{}, &out, gates.OSExec(dir, &out))
	return out.String(), err
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func putMode(t *testing.T, dir, rel, body string, mode os.FileMode) {
	t.Helper()
	write(t, dir, rel, body)
	must(t, os.Chmod(filepath.Join(dir, filepath.FromSlash(rel)), mode))
}

func hooksPath(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "config", "--get", "core.hooksPath")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// A ci.yml that is the repository's own is not the shared caller, and init
// is not the one to replace it.
func TestInitLeavesAnExistingWorkflowAlone(t *testing.T) {
	dir := gitRepo(t)
	own := "name: ci\non: [push]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make ci\n"
	write(t, dir, ".github/workflows/ci.yml", own)
	out, err := runInit(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, ".github/workflows/ci.yml"); got != own {
		t.Fatalf("init replaced the repository's own ci.yml:\n%s", got)
	}
	if strings.Contains(out, "wrote .github/workflows/ci.yml") {
		t.Errorf("init must not claim to have written it:\n%s", out)
	}
	want := ".github/workflows/ci.yml exists and does not call " + Workflow
	if !strings.Contains(out, want) {
		t.Errorf("want a line saying %q:\n%s", want, out)
	}
	if _, err := check(t, dir, untracked("")); err == nil || !strings.Contains(err.Error(), "no workflow calls") {
		t.Errorf("contract keeps reporting a repository with no caller, got %v", err)
	}
}

// A caller at a ref the contract does not accept carries inputs somebody
// chose; init leaves it and contract reports the ref.
func TestInitLeavesACallerAtAnotherRefAlone(t *testing.T) {
	dir := gitRepo(t)
	own := "name: ci\non:\n  push:\n    branches: [main]\n  pull_request:\njobs:\n  gate:\n    uses: latere-ai/ci/.github/workflows/lateregate.yml@main\n    with:\n      gate_module: tools\n"
	write(t, dir, ".github/workflows/ci.yml", own)
	out, err := runInit(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, ".github/workflows/ci.yml"); got != own {
		t.Fatalf("init replaced a caller that names gate_module:\n%s", got)
	}
	if strings.Contains(out, "does not call") {
		t.Errorf("a caller at another ref is a caller:\n%s", out)
	}
	if _, err := check(t, dir, untracked("")); err == nil || !strings.Contains(err.Error(), "no workflow calls") {
		t.Errorf("contract keeps reporting the ref, got %v", err)
	}
}

// A caller in another file counts, so ci.yml is not written beside it.
func TestInitWritesNoSecondCaller(t *testing.T) {
	dir := gitRepo(t)
	write(t, dir, ".github/workflows/gate.yml", Caller)
	if _, err := runInit(t, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "ci.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("another file already calls the shared workflow, got %v", err)
	}
}

// A hook git cannot run is no hook; init gives it the bit and says so.
func TestInitMakesAnExistingHookExecutable(t *testing.T) {
	dir := gitRepo(t)
	precommit := "#!/bin/sh\nexec go tool lateregate hook\n"
	prepush := "#!/bin/sh\nrefs=$(cat)\nprintf '%s\\n' \"$refs\" | go tool lateregate prepush || exit 1\n"
	putMode(t, dir, ".githooks/pre-commit", precommit, 0o644)
	putMode(t, dir, ".githooks/pre-push", prepush, 0o644)
	out, err := runInit(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for hook, body := range map[string]string{".githooks/pre-commit": precommit, ".githooks/pre-push": prepush} {
		info, err := os.Stat(filepath.Join(dir, hook))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s is still not executable", hook)
		}
		if got := readFile(t, dir, hook); got != body {
			t.Errorf("%s changed:\n%s", hook, got)
		}
		if !strings.Contains(out, "made "+hook+" executable") {
			t.Errorf("want a line saying %s was made executable:\n%s", hook, out)
		}
	}
}

// A pre-commit that holds its own copy of the checks is replaced, and the
// replacement runs.
func TestInitReplacesANonExecutableHandRolledHook(t *testing.T) {
	dir := gitRepo(t)
	putMode(t, dir, ".githooks/pre-commit", "#!/bin/sh\ngofmt -l .\n", 0o644)
	if _, err := runInit(t, dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".githooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 || readFile(t, dir, ".githooks/pre-commit") != gates.Staged {
		t.Errorf("want the delegating hook, executable; mode %v", info.Mode())
	}
}

// A pre-push that holds the repository's own check keeps its bytes and gets
// the bit.
func TestInitKeepsAnOwnPrepushWhenItMakesItExecutable(t *testing.T) {
	dir := gitRepo(t)
	own := "#!/bin/sh\n./scripts/changelog-for-tag.sh\n"
	putMode(t, dir, ".githooks/pre-push", own, 0o644)
	if _, err := runInit(t, dir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dir, ".githooks/pre-push"); got != own {
		t.Errorf("pre-push changed:\n%s", got)
	}
	if info, _ := os.Stat(filepath.Join(dir, ".githooks", "pre-push")); info.Mode()&0o111 == 0 {
		t.Error("the pre-push stays unexecutable")
	}
}
