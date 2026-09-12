// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package enumsetup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func fixture(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPrepareFrozenInstallAndDeduplicateWorkspace(t *testing.T) {
	for _, lock := range []string{"package-lock.json", "npm-shrinkwrap.json", "bun.lock", "bun.lockb"} {
		t.Run(lock, func(t *testing.T) {
			root := fixture(t, "frontend/package.json", "frontend/"+lock, "frontend/tsconfig.json", "frontend/nested/tsconfig.json")
			var calls []string
			run := func(_ []string, stream bool, name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				if stream != (name != "git") {
					t.Errorf("wrong streaming for %s", name)
				}
				return nil, nil
			}
			projects := []config.TypeScriptEnums{{Project: "frontend/tsconfig.json"}, {Project: "frontend/nested/tsconfig.json"}}
			if err := Prepare(projects, root, io.Discard, run); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || !strings.Contains(calls[0], "--error-unmatch -- frontend/package.json frontend/"+lock) {
				t.Fatalf("not one tracked, frozen installation: %v", calls)
			}
			want := "npm --prefix " + filepath.Join(root, "frontend") + " ci"
			if strings.HasPrefix(lock, "bun.") {
				want = "bun install --frozen-lockfile --cwd " + filepath.Join(root, "frontend")
			}
			if calls[1] != want {
				t.Fatalf("got %s; want %s", calls[1], want)
			}
		})
	}
}

func TestPrepareRejectsMissingOrAmbiguousInputs(t *testing.T) {
	for _, tc := range []struct {
		name, project, want string
		files               []string
	}{
		{"missing project", "tsconfig.json", "tsconfig file", nil},
		{"directory project", "frontend", "tsconfig file", []string{"frontend/a"}},
		{"outside", "../tsconfig.json", "inside", nil},
		{"no lock", "tsconfig.json", "no package.json", []string{"tsconfig.json", "package.json"}},
		{"multiple locks", "tsconfig.json", "multiple lockfiles", []string{"tsconfig.json", "package.json", "package-lock.json", "bun.lock"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t, tc.files...)
			run := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
				t.Fatal("invalid input reached installation")
				return nil, nil
			}
			err := Prepare([]config.TypeScriptEnums{{Project: tc.project}}, root, io.Discard, run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %s", err, tc.want)
			}
		})
	}
	if err := Prepare(nil, t.TempDir(), io.Discard, nil); err == nil {
		t.Fatal("empty projects passed")
	}
	if _, err := owner(t.TempDir(), string(filepath.Separator)); err == nil {
		t.Fatal("owner search escaped repository")
	}
}

func TestPrepareStopsOnUntrackedLockOrInstallFailure(t *testing.T) {
	for _, broken := range []string{"git", "npm"} {
		t.Run(broken, func(t *testing.T) {
			root := fixture(t, "package.json", "package-lock.json", "tsconfig.json")
			var calls []string
			run := func(_ []string, _ bool, name string, _ ...string) ([]byte, error) {
				calls = append(calls, name)
				if name == broken {
					return nil, errors.New("fixture failure")
				}
				return nil, nil
			}
			err := Prepare([]config.TypeScriptEnums{{Project: "tsconfig.json"}}, root, io.Discard, run)
			if err == nil || !strings.Contains(err.Error(), "fixture failure") {
				t.Fatalf("lost failure: %v", err)
			}
			if broken == "git" && len(calls) != 1 {
				t.Fatalf("installed without tracked lock: %v", calls)
			}
		})
	}
}

func TestPrepareChecksAllProjectsBeforeInstalling(t *testing.T) {
	root := fixture(t, "package.json", "package-lock.json", "tsconfig.json")
	called := false
	run := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	err := Prepare([]config.TypeScriptEnums{{Project: "tsconfig.json"}, {Project: "missing.json"}}, root, io.Discard, run)
	if err == nil || called {
		t.Fatalf("prepared partial project set: called=%v err=%v", called, err)
	}
}
