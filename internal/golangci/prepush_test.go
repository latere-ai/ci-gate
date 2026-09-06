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

const (
	sha1 = "1111111111111111111111111111111111111111"
	sha2 = "2222222222222222222222222222222222222222"
	sha3 = "3333333333333333333333333333333333333333"
)

// replay records calls and answers each git call from outputs in order; the
// linter call is recorded and answered with lintErr.
func replay(calls *[]call, lintErr error, outputs ...string) func([]string, bool, string, ...string) ([]byte, error) {
	i := 0
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{name, args})
		if name == "git" {
			if i < len(outputs) {
				o := outputs[i]
				i++
				return []byte(o), nil
			}
			return nil, nil
		}
		return nil, lintErr
	}
}

func joined(c call) string { return c.name + " " + strings.Join(c.args, " ") }

func prepush(t *testing.T, refs string, run func([]string, bool, string, ...string) ([]byte, error)) (string, error) {
	t.Helper()
	dir := module(t)
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	err = Prepush(dir, cfg, "go", strings.NewReader(refs), &sb, run)
	return sb.String(), err
}

// A branch push diffs against the remote's commit, and the linter runs on
// exactly the packages that diff names, with the config rendered first.
func TestPrepushLintsThePackagesTheBranchChanges(t *testing.T) {
	var calls []call
	out, err := prepush(t, "refs/heads/main "+sha2+" refs/heads/main "+sha1+"\n",
		replay(&calls, nil, "internal/a/a.go\x00internal/b/b_test.go\x00main.go\x00tok/testdata/gen/g.go\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("ran %v", calls)
	}
	if got := joined(calls[0]); got != "git diff --name-only --diff-filter=ACMR -z "+sha1+" "+sha2+" -- *.go" {
		t.Errorf("diff ran as %q", got)
	}
	if got, want := joined(calls[1]), "go run "+Module+"@"+Version+" run --allow-parallel-runners . ./internal/a ./internal/b"; got != want {
		t.Errorf("linter ran as %q, want %q", got, want)
	}
	if !strings.Contains(out, "wrote ") || !strings.Contains(out, Name) || !strings.Contains(out, "linting 3 package(s)") {
		t.Errorf("output:\n%s", out)
	}
}

// A branch the remote does not have yet is diffed against its merge base
// with origin/main, not against nothing.
func TestPrepushOfANewBranchDiffsAgainstTheMergeBase(t *testing.T) {
	var calls []call
	_, err := prepush(t, "refs/heads/feature "+sha2+" refs/heads/feature "+zeroSHA+"\n",
		replay(&calls, nil, sha3+"\n", "x/x.go\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if got := joined(calls[0]); got != "git merge-base origin/main "+sha2 {
		t.Errorf("merge-base ran as %q", got)
	}
	if got := joined(calls[1]); !strings.Contains(got, " "+sha3+" "+sha2+" ") {
		t.Errorf("diff must use the merge base: %q", got)
	}
	if got := joined(calls[2]); !strings.HasSuffix(got, " run --allow-parallel-runners ./x") {
		t.Errorf("linter ran as %q", got)
	}
}

// A tag points at a commit already pushed on a branch; a branch deletion
// pushes nothing. Neither runs the linter.
func TestPrepushIgnoresTagsAndDeletions(t *testing.T) {
	var calls []call
	out, err := prepush(t, "refs/tags/v1.2.3 "+sha2+" refs/tags/v1.2.3 "+zeroSHA+"\n"+
		"refs/heads/old "+zeroSHA+" refs/heads/old "+sha1+"\n", replay(&calls, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Errorf("nothing runs for a tag or a deletion; ran %v", calls)
	}
	if !strings.Contains(out, "no branch pushed") {
		t.Errorf("output:\n%s", out)
	}
}

func TestPrepushWithNoGoChangesRunsNothing(t *testing.T) {
	var calls []call
	out, err := prepush(t, "refs/heads/main "+sha2+" refs/heads/main "+sha1+"\n", replay(&calls, nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Errorf("only the diff runs; ran %v", calls)
	}
	if !strings.Contains(out, "changes no Go file") {
		t.Errorf("output:\n%s", out)
	}
}

func TestPrepushReportsLintFindings(t *testing.T) {
	var calls []call
	_, err := prepush(t, "refs/heads/main "+sha2+" refs/heads/main "+sha1+"\n",
		replay(&calls, errors.New("exit 1"), "a.go\x00"))
	if err == nil || !strings.Contains(err.Error(), "golangci-lint "+Version+" reported findings") {
		t.Fatalf("got %v", err)
	}
}

func TestPrepushReportsGitFailing(t *testing.T) {
	failing := func(_ []string, _ bool, name string, _ ...string) ([]byte, error) {
		return nil, errors.New("not a git repository")
	}
	_, err := prepush(t, "refs/heads/main "+sha2+" refs/heads/main "+sha1+"\n", failing)
	if err == nil || !strings.Contains(err.Error(), "listing the Go files") {
		t.Fatalf("got %v", err)
	}
	_, err = prepush(t, "refs/heads/new "+sha2+" refs/heads/new "+zeroSHA+"\n", failing)
	if err == nil || !strings.Contains(err.Error(), "merge base") {
		t.Fatalf("got %v", err)
	}
}

// A changed file under a nested module is not a package of the main module:
// the hook skips it, the way it skips testdata, instead of failing the push
// with "main module does not contain package".
func TestPrepushSkipsPackagesInNestedModules(t *testing.T) {
	var calls []call
	dir := module(t)
	if err := os.MkdirAll(filepath.Join(dir, "tools", "spike", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "spike", "go.mod"), []byte("module example.com/spike\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	err = Prepush(dir, cfg, "go", strings.NewReader("refs/heads/main "+sha2+" refs/heads/main "+sha1+"\n"), &sb,
		replay(&calls, nil, "tools/spike/main.go\x00tools/spike/sub/x.go\x00internal/a/a.go\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("ran %v", calls)
	}
	if got, want := joined(calls[1]), "go run "+Module+"@"+Version+" run --allow-parallel-runners ./internal/a"; got != want {
		t.Errorf("linter ran as %q, want %q", got, want)
	}
	if !strings.Contains(sb.String(), "linting 1 package(s)") {
		t.Errorf("output:\n%s", sb.String())
	}
}

func TestInNestedModuleWalksUpToTheRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{"a/b/c": true, "a/b": true, "a": false, ".": false, "other": false} {
		if got := inNestedModule(root, dir); got != want {
			t.Errorf("inNestedModule(%q) = %v, want %v", dir, got, want)
		}
	}
}
