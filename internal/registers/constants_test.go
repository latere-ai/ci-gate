// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package registers

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitTrack makes root a git repository and stages everything in it, so the
// gate, which reads what git tracks, sees the files a test wrote.
func gitTrack(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		gitIn(t, root, "init", "-q")
	}
	gitIn(t, root, "add", "-A")
}

func gitIn(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestOnlyTrackedFilesAreRead(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg,
		"cmd/app/main.go": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
			"func h(w http.ResponseWriter) { api.WriteError(w, \"ok\", \"try again\") }\n",
		".gitignore": "scratch/\n",
	})
	bad := "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
		"func g(w http.ResponseWriter) { api.WriteError(w, \"x\", \"store.Deploy not found\") }\n"
	for _, rel := range []string{"scratch/x.go", "cmd/app/untracked.go"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run(t, surfaces("internal/api.WriteError"), root); err != nil {
		t.Fatalf("an untracked or ignored file was read: %v\n%s", err, out)
	}
}

func TestAnUntrackableTreeFailsWithTheReason(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	_, err := run(t, surfaces("internal/api.WriteError"), root)
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v, want git named", err)
	}
}
