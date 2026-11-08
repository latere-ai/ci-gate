// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// gitTrack makes dir a git repository and stages everything in it, so the
// gate, which reads what git tracks, sees the files a test wrote.
func gitTrack(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		gitIn(t, dir, "init", "-q")
	}
	gitIn(t, dir, "add", "-A")
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestADoubleStarMatchesAnyNumberOfDirectories(t *testing.T) {
	tracked := []string{
		"prompts/a.md", "prompts/x/b.md", "prompts/x/y/c.md", "prompts/x/y/c.txt",
		"other/d.md", "e.md",
	}
	cases := []struct {
		glob string
		want []string
	}{
		{"prompts/**/*.md", []string{"prompts/a.md", "prompts/x/b.md", "prompts/x/y/c.md"}},
		{"prompts/*.md", []string{"prompts/a.md"}},
		{"**/*.md", []string{"e.md", "other/d.md", "prompts/a.md", "prompts/x/b.md", "prompts/x/y/c.md"}},
		{"prompts/x/**", []string{"prompts/x/b.md", "prompts/x/y/c.md", "prompts/x/y/c.txt"}},
		{"prompts/x/y/c.md", []string{"prompts/x/y/c.md"}},
	}
	root := t.TempDir()
	for _, rel := range tracked {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		got, err := files(root, tracked, c.glob)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s matched %v, want %v", c.glob, got, c.want)
		}
	}
}

func TestADeeperFileIsReadByADoubleStarGlob(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/a.md":     "Hello.\n",
		"repo/prompts/x/y/b.md": "We spawn a worker.\n",
	})
	out, err := scan(t, config.Words{PersonFacing: []string{"prompts/**/*.md"}}, root)
	expect(t, out, err, "prompts/x/y/b.md:1: \"spawn\" in person-facing text")
}

func TestOnlyTrackedFilesAreRead(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/a.md": "Hello.\n",
		"repo/.gitignore":   "scratch/\n",
	})
	for _, rel := range []string{"scratch/b.md", "prompts/untracked.md"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("We spawn a worker.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := scan(t, config.Words{PersonFacing: []string{"prompts/**/*.md", "scratch/*.md"}}, root)
	if err == nil || !strings.Contains(err.Error(), "scratch/*.md, which matches no file") {
		t.Fatalf("an ignored file satisfied a glob (err %v):\n%s", err, out)
	}
	out, err = scan(t, config.Words{PersonFacing: []string{"prompts/**/*.md"}}, root)
	expect(t, out, err)
}

func TestAnUntrackableTreeFailsWithTheReason(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("Hello.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	_, err := scan(t, config.Words{PersonFacing: []string{"*.md"}}, root)
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v, want git named", err)
	}
}
