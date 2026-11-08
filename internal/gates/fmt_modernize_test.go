// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// fmtRepo is a git checkout in a temp directory holding the given files, all
// added to the index.
func fmtRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fmtGit(t, root, "init", "-q")
	fmtGit(t, root, "add", ".")
	return root
}

func fmtGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// A fixture under testdata that is not valid Go on purpose must not fail the
// repository: go build ignores it.
func TestFmtCheckLeavesTestdataOut(t *testing.T) {
	root := fmtRepo(t, map[string]string{
		"a.go":               "package a\n",
		"testdata/bad.go":    "package a\nfunc (\n",
		"x/testdata/bad2.go": "package a\nfunc (\n",
	})
	if err := FmtCheck(io.Discard, OSExec(root, io.Discard)); err != nil {
		t.Fatalf("a syntax-error fixture under testdata failed the check: %v", err)
	}
}

// A tracked file that was removed from the working tree is not a formatting
// problem.
func TestFmtCheckLeavesDeletedFilesOut(t *testing.T) {
	root := fmtRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package a\n"})
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	if err := FmtCheck(io.Discard, OSExec(root, io.Discard)); err != nil {
		t.Fatalf("a tracked file deleted in the working tree failed the check: %v", err)
	}
}

// Deleting a file must not hide a real finding in the files that remain.
func TestFmtCheckStillListsUnformattedFilesBesideADeletedOne(t *testing.T) {
	root := fmtRepo(t, map[string]string{"a.go": "package   a\n", "b.go": "package a\n"})
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	err := FmtCheck(&sb, OSExec(root, io.Discard))
	if err == nil || !strings.Contains(err.Error(), "1 file(s)") || !strings.Contains(sb.String(), "a.go") {
		t.Fatalf("err = %v, report %q; want a.go reported", err, sb.String())
	}
}

// When gofmt itself fails, the person sees which file and why.
func TestFmtCheckShowsGofmtsOwnMessageWhenItFails(t *testing.T) {
	root := fmtRepo(t, map[string]string{"a.go": "package a\nfunc (\n"})
	err := FmtCheck(io.Discard, OSExec(root, io.Discard))
	if err == nil {
		t.Fatal("a syntax error outside testdata must fail the check")
	}
	if !strings.Contains(err.Error(), "a.go:") || !strings.Contains(err.Error(), "expected") {
		t.Errorf("error does not carry gofmt's message: %v", err)
	}
}

func TestFmtCheckPassesWhenOnlyTestdataIsTracked(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := FmtCheck(&sb, fake(t, &calls, "testdata/a.go\x00")); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !strings.Contains(sb.String(), "gofmt-formatted") {
		t.Errorf("calls %v, report %q", calls, sb.String())
	}
}

func TestFmtCheckPassesWhenEveryTrackedFileIsDeleted(t *testing.T) {
	var calls []call
	err := FmtCheck(io.Discard, fake(t, &calls, "a.go\x00", errors.New("exit status 2"), "a.go\x00"))
	if err != nil {
		t.Fatalf("a tree whose tracked Go files are all deleted: %v", err)
	}
}

func TestHookLeavesTestdataAndDeletedFilesOutOfGofmt(t *testing.T) {
	var calls []call
	err := Hook(&config.Config{}, t.TempDir(), "", "go", io.Discard, fake(t, &calls,
		"a/testdata/x.go\x00gone.go\x00",
		errors.New("exit status 2"), // gofmt -l: gone.go does not exist
		"gone.go\x00",               // git ls-files --deleted
	))
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	for _, c := range calls {
		if c.name == "gofmt" && strings.Contains(strings.Join(c.args, " "), "testdata") {
			t.Errorf("gofmt was handed a testdata path: %v", c.args)
		}
	}
}

func TestHookShowsGofmtsOwnMessageWhenItFails(t *testing.T) {
	root := fmtRepo(t, map[string]string{"a.go": "package a\nfunc (\n"})
	err := Hook(&config.Config{}, root, "", "go", io.Discard, OSExec(root, io.Discard))
	if err == nil || !strings.Contains(err.Error(), "a.go:") {
		t.Fatalf("want gofmt's message naming a.go, got %v", err)
	}
}

func TestStderrErrorKeepsTheWrappedMessage(t *testing.T) {
	inner := errors.New("exit status 2")
	err := error(&StderrError{Err: inner, Stderr: " boom\n"})
	if err.Error() != "exit status 2" || !errors.Is(err, inner) || stderrOf(err) != "boom" {
		t.Errorf("message %q, stderr %q", err, stderrOf(err))
	}
	if stderrOf(inner) != "" {
		t.Error("a plain error has no stderr")
	}
}
