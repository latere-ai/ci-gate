// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package contract

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// repo is a tree in shape: caller, hook, gitignore, pin, and no Makefile.
// Each test breaks one thing.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte(Caller), 0o644))
	must(t, os.MkdirAll(filepath.Join(dir, ".githooks"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".githooks", "pre-commit"), []byte(gates.Staged), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("coverage.out\n.golangci.yml\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.27\n\ntool latere.ai/x/ci-gate/cmd/lateregate\n"), 0o644))
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
}

// untracked answers git ls-files as "not tracked", and replays a make
// database when asked for one.
func untracked(db string) gates.Exec {
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		switch name {
		case "git":
			if len(args) > 0 && args[0] == "ls-files" {
				return nil, errors.New("exit 1")
			}
			return nil, nil
		case "make":
			return []byte(db), nil
		}
		return nil, nil
	}
}

func tracked(db string) gates.Exec {
	return func(_ []string, _ bool, name string, _ ...string) ([]byte, error) {
		if name == "make" {
			return []byte(db), nil
		}
		return nil, nil
	}
}

func load(t *testing.T, dir string) *config.Config {
	t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func check(t *testing.T, dir string, exec gates.Exec) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(dir, load(t, dir), &sb, exec)
	return sb.String(), err
}

func TestInShapePasses(t *testing.T) {
	dir := repo(t)
	out, err := check(t, dir, untracked(""))
	if err != nil {
		t.Fatalf("a tree in shape must pass: %v", err)
	}
	if !strings.Contains(out, "in shape") {
		t.Errorf("output:\n%s", out)
	}
}

// Every drift in one run, not one per push.
func TestEveryDriftIsReportedAtOnce(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Makefile", "cover:\n\tgo tool cover -func=coverage.out\n")
	db := "cover:\n#  Phony target.\n\tgo tool cover -func=coverage.out\n\n"
	_, err := check(t, dir, untracked(db))
	if err == nil {
		t.Fatal("an empty tree drifts in every way")
	}
	for _, want := range []string{
		"no workflow calls", "pre-commit is missing", ".gitignore is missing",
		"hand-rolls a gate: cover", "go.mod is missing",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in one run, got:\n%v", want, err)
		}
	}
}

func TestACallerStillOnGoVerifyIsNamedAsSuch(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".github/workflows/ci.yml", "on: {push: {branches: [main]}, pull_request: {}}\njobs:\n  v:\n    uses: latere-ai/ci/.github/workflows/go-verify.yml@v1\n")
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "still calls go-verify.yml") {
		t.Fatalf("the fix for an old caller differs from the fix for none, got %v", err)
	}
}

func TestTwoCallersFail(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".github/workflows/other.yml", Caller)
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "2 workflows call") {
		t.Fatalf("got %v", err)
	}
}

func TestTheCallerMustRunOnPushToMainAndPullRequest(t *testing.T) {
	for _, tc := range []struct{ name, on, want string }{
		{"no on block", "jobs:\n  g:\n    uses: " + Workflow + "\n", "no `on:` block"},
		{"push only", "on:\n  push:\n    branches: [main]\njobs:\n  g:\n    uses: " + Workflow + "\n", "must trigger on push to main and on pull_request"},
		{"wrong branch", "on:\n  push:\n    branches: [release]\n  pull_request:\njobs:\n  g:\n    uses: " + Workflow + "\n", "does not include main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := repo(t)
			write(t, dir, ".github/workflows/ci.yml", tc.on)
			_, err := check(t, dir, untracked(""))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	// `on` may parse as the boolean true; the flow style is accepted too.
	dir := repo(t)
	write(t, dir, ".github/workflows/ci.yml", "on: {push: {branches: [main]}, pull_request: {}}\njobs:\n  g:\n    uses: "+Workflow+"\n")
	if _, err := check(t, dir, untracked("")); err != nil {
		t.Fatalf("flow-style triggers are triggers: %v", err)
	}
}

func TestTheHookMustDelegate(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".githooks/pre-commit", "#!/bin/sh\ngofmt -l .\n")
	must(t, os.Chmod(filepath.Join(dir, ".githooks", "pre-commit"), 0o755))
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "does not run `lateregate hook`") {
		t.Fatalf("got %v", err)
	}

	// Extra lines are the repository's own.
	write(t, dir, ".githooks/pre-commit", "#!/bin/sh\n./own.sh\nexec go tool lateregate hook\n")
	must(t, os.Chmod(filepath.Join(dir, ".githooks", "pre-commit"), 0o755))
	if _, err := check(t, dir, untracked("")); err != nil {
		t.Fatalf("a hook that delegates and does more is in shape: %v", err)
	}

	must(t, os.Chmod(filepath.Join(dir, ".githooks", "pre-commit"), 0o644))
	_, err = check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("a hook git cannot run is no hook, got %v", err)
	}
}

func TestATrackedGolangciConfigFailsUnlessDeclared(t *testing.T) {
	dir := repo(t)
	_, err := check(t, dir, tracked(""))
	if err == nil || !strings.Contains(err.Error(), ".golangci.yml is tracked") {
		t.Fatalf("got %v", err)
	}

	write(t, dir, ".lateregate.yaml", "golangci:\n  own: a vendored tree with its own lint history\n")
	_, err = check(t, dir, tracked(""))
	if err == nil || !strings.Contains(err.Error(), "and there is none") {
		t.Fatalf("a declared own config that is not there is a lost config, got %v", err)
	}

	write(t, dir, ".golangci.yml", "linters:\n  enable: [govet]\n")
	if _, err := check(t, dir, tracked("")); err != nil {
		t.Fatalf("declared and present is in shape: %v", err)
	}
}

func TestGitignoreMustListTheGeneratedFiles(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".gitignore", "/coverage.out\n")
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "does not list .golangci.yml") {
		t.Fatalf("got %v", err)
	}
	write(t, dir, ".gitignore", "/coverage.out\n/.golangci.yml\n")
	if _, err := check(t, dir, untracked("")); err != nil {
		t.Fatalf("a leading slash still ignores the file: %v", err)
	}
}

func TestARestatedDefaultIsNamed(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".lateregate.yaml", "modernize:\n  disable: [newexpr, errorsastype]\n")
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "restates a default: modernize.disable") {
		t.Fatalf("got %v", err)
	}
}

// A target named for a gate delegates or is deleted. One that runs its own
// tiers and then delegates is delegating.
func TestAGateNamedTargetMustDelegate(t *testing.T) {
	dir := repo(t)
	write(t, dir, "Makefile", "x:\n")
	for _, tc := range []struct {
		name string
		db   string
		want string
	}{
		{"hand-rolled cover", "cover:\n#  Phony target.\n\tgo test -coverprofile=c.out ./...\n\tgo tool cover -func=c.out\n\n", "hand-rolls a gate: cover (runs the cover gate itself)"},
		{"hand-rolled race under the old name", "test-race:\n\tCGO_ENABLED=1 go test -race ./...\n\n", "test-race (runs the race gate itself)"},
		{"lint via a system binary", "lint: lint-config\n\tgolangci-lint run ./...\n\nlint-config:\n\t@go tool lateregate golangci\n\n", "lint (runs the lint gate itself)"},
		{"tiers then delegate", "cover:\n\tgo test -tags=integration -coverprofile=i.out ./...\n\t@go tool lateregate cover -profile=i.out\n\n", ""},
		{"delegating everything", "check:\n\t@go tool lateregate\n\ntest:\n\t@go tool lateregate test\n\n", ""},
		{"no gate targets", "build:\n\tgo build ./...\n\nvalidate: lint-otel\n\n", ""},
		{"this repository's own form", "cover:\n\t@$(GO) run ./cmd/lateregate cover\n\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := check(t, dir, untracked(tc.db))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("must pass: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAnEmptyMakeDatabaseIsAnError(t *testing.T) {
	dir := repo(t)
	write(t, dir, "Makefile", "x:\n")
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "no rule database") {
		t.Fatalf("make failing to run must not read as a Makefile with no targets, got %v", err)
	}
}

func TestTheToolMustBePinned(t *testing.T) {
	dir := repo(t)
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.27\n\nrequire latere.ai/x/ci-gate v0.25.2\n")
	_, err := check(t, dir, untracked(""))
	if err == nil || !strings.Contains(err.Error(), "does not pin the tool") {
		t.Fatalf("a require is not a tool line, got %v", err)
	}
	// Inside a block.
	write(t, dir, "go.mod", "module example.com/m\n\ngo 1.27\n\ntool (\n\tlatere.ai/x/ci-gate/cmd/lateregate\n\tgolang.org/x/tools/cmd/stringer\n)\n")
	if _, err := check(t, dir, untracked("")); err != nil {
		t.Fatalf("a tool block pins it: %v", err)
	}
}

// init writes the wiring, and never a file already in shape.
func TestInitWritesTheWiringOnce(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".gitignore", "coverage.out")
	var configured []string
	exec := func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "config" {
			configured = append(configured, strings.Join(args, " "))
		}
		if name == "git" && len(args) > 0 && args[0] == "ls-files" {
			return nil, errors.New("exit 1")
		}
		return nil, nil
	}
	var sb strings.Builder
	if err := Init(dir, &sb, exec); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wrote .github/workflows/ci.yml", "wrote .githooks/pre-commit", "added .golangci.yml to .gitignore"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("want %q:\n%s", want, sb.String())
		}
	}
	if len(configured) != 1 || configured[0] != "config core.hooksPath .githooks" {
		t.Errorf("hooksPath must be set: %v", configured)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.HasPrefix(string(body), "coverage.out\n") || !ignores(string(body), ".golangci.yml") {
		t.Errorf("gitignore after init:\n%s", body)
	}
	hook, _ := os.Stat(filepath.Join(dir, ".githooks", "pre-commit"))
	if hook.Mode()&0o111 == 0 {
		t.Error("the hook must be executable")
	}

	// The pin is the one thing init does not write, so it says so.
	if !strings.Contains(sb.String(), "go get -tool") {
		t.Errorf("init must name the next step:\n%s", sb.String())
	}

	// Now in shape apart from go.mod: nothing to write.
	write(t, dir, "go.mod", "module m\n\ntool latere.ai/x/ci-gate/cmd/lateregate\n")
	sb.Reset()
	if err := Init(dir, &sb, exec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "nothing to write") {
		t.Errorf("a second init must write nothing:\n%s", sb.String())
	}
	if _, err := check(t, dir, exec); err != nil {
		t.Fatalf("after init the tree is in shape: %v", err)
	}
}

// An old caller carries inputs somebody chose, so init leaves it to be
// edited by hand and Run keeps reporting it.
func TestInitDoesNotRewriteAnOldCaller(t *testing.T) {
	dir := repo(t)
	old := "on: {push: {branches: [main]}, pull_request: {}}\njobs:\n  v:\n    uses: latere-ai/ci/.github/workflows/go-verify.yml@v1\n    with:\n      test_os: '[\"ubuntu-latest\"]'\n"
	write(t, dir, ".github/workflows/ci.yml", old)
	if err := Init(dir, &strings.Builder{}, untracked("")); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".github", "workflows", "ci.yml"))
	if string(body) != old {
		t.Error("init must not rewrite a caller that carries chosen inputs")
	}
}

func TestInitReplacesAHandRolledHook(t *testing.T) {
	dir := repo(t)
	write(t, dir, ".githooks/pre-commit", "#!/bin/sh\ngofmt -l .\n")
	if err := Init(dir, &strings.Builder{}, untracked("")); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".githooks", "pre-commit"))
	if string(body) != gates.Staged {
		t.Error("a hook holding its own copy of the checks is replaced by the delegating one")
	}
}

func TestInitReportsAFailingHooksPath(t *testing.T) {
	dir := repo(t)
	exec := func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "config" {
			return nil, errors.New("not a git repository")
		}
		return nil, errors.New("exit 1")
	}
	if err := Init(dir, &strings.Builder{}, exec); err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
		t.Fatalf("got %v", err)
	}
}

func TestRequiredListsTheGates(t *testing.T) {
	if len(Required()) == 0 {
		t.Error("the required set is the gate set")
	}
}
