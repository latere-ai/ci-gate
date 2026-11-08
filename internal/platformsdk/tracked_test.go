// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package platformsdk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
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
		"main.go":    "package main\n\nfunc main() {}\n",
		".gitignore": "scratch/\n",
	})
	const bad = "package x\n\nconst route = \"/v1/agents\"\n\nvar _ = route\n"
	for _, rel := range []string{"scratch/x.go", "untracked.go"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err)
}

func TestAnUntrackableTreeFailsWithTheReason(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	_, err := run(t, config.PlatformViaSDK{}, root)
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v, want git named", err)
	}
}
