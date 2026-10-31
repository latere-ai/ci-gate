// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scope

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/gates"
)

// checkout is a git repository with a module committed as the base and
// recorded as origin/main, and the edits a test makes to it after.
type checkout struct {
	t    *testing.T
	root string
	run  gates.Exec
}

func newCheckout(t *testing.T, files map[string]string) *checkout {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	c := &checkout{t: t, root: t.TempDir()}
	c.run = gates.OSExec(c.root, os.Stderr)
	for p, body := range files {
		c.write(p, body)
	}
	c.git("init", "-q", "-b", "main")
	c.git("add", ".")
	c.git("commit", "-q", "-m", "base")
	c.git("update-ref", "refs/remotes/origin/main", "HEAD")
	return c
}

func (c *checkout) write(p, body string) {
	c.t.Helper()
	full := filepath.Join(c.root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *checkout) git(args ...string) {
	c.t.Helper()
	if out, err := c.run(nil, false, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...); err != nil {
		c.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// layered is a module whose package a, at the base, calls into b and d and
// embeds a file; e calls into b on its own; c stands apart.
var layered = map[string]string{
	"go.mod":      "module example.com/real\n\ngo 1.24\n",
	"b/b.go":      "package b\n\nfunc One() int { return 1 }\n\nfunc Two() int { return 2 }\n",
	"d/d.go":      "package d\n\nfunc D() int { return 4 }\n",
	"a/a.go":      "package a\n\nimport (\n\t_ \"embed\"\n\n\t\"example.com/real/b\"\n\t\"example.com/real/d\"\n)\n\n//go:embed data.txt\nvar Data string\n\nfunc Sum() int { return b.One() + b.Two() + d.D() }\n",
	"a/data.txt":  "base\n",
	"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) { _ = Sum() }\n",
	"e/e.go":      "package e\n\nimport \"example.com/real/b\"\n\nfunc E() int { return b.One() }\n",
	"e/e_test.go": "package e\n\nimport \"testing\"\n\nfunc TestE(t *testing.T) { _ = E() }\n",
	"c/c.go":      "package c\n\nfunc C() int { return 3 }\n",
	// A module of its own, which ./... does not read.
	"tools/go.mod":  "module example.com/tools\n\ngo 1.24\n",
	"tools/tool.go": "package tools\n",
}

// changeA drops a's import of d and its call to b.Two, changes the file it
// embeds and adds a file the base did not have.
func changeA(c *checkout) {
	c.write("a/a.go", "package a\n\nimport (\n\t_ \"embed\"\n\n\t\"example.com/real/b\"\n)\n\n//go:embed data.txt\nvar Data string\n\nfunc Sum() int { return b.One() }\n")
	c.write("a/data.txt", "now\n")
	c.write("a/extra.go", "package a\n\nfunc Extra() int { return 0 }\n")
}

// The overlay gives the go command each changed file as the base held it,
// or as absent where the change added it, and the packages the change did
// not touch include one only the base's tests linked.
func TestABaselineReplaysTheBase(t *testing.T) {
	c := newCheckout(t, layered)
	changeA(c)
	c.write("tools/tool.go", "package tools\n\nfunc T() {}\n")
	c.write("f/f.go", "package f\n\nfunc F() int { return 6 }\n")
	s := Resolve(c.root, "go", c.run)
	if !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
	dir := t.TempDir()
	b, err := s.Baseline(dir, "go", c.run)
	if err != nil {
		t.Fatal(err)
	}
	// f is new since the base, so the base has no test of it to run.
	if !slices.Equal(b.Tests, []string{"example.com/real/a"}) {
		t.Errorf("tests %v", b.Tests)
	}
	if !slices.Equal(b.Untouched, []string{"example.com/real/b", "example.com/real/d"}) {
		t.Errorf("untouched %v", b.Untouched)
	}
	if len(b.Unreplayed) != 0 {
		t.Errorf("an embedded file is replayed: %v", b.Unreplayed)
	}

	data, err := os.ReadFile(b.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(c.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := overlay.Replace[filepath.Join(c.root, "tools/tool.go")]; ok {
		t.Error("a nested module's file is not the module's to replay")
	}
	for name, want := range map[string]string{"a/a.go": layered["a/a.go"], "a/data.txt": "base\n", "a/extra.go": "", "f/f.go": ""} {
		backing, ok := overlay.Replace[filepath.Join(c.root, name)]
		if !ok {
			backing, ok = overlay.Replace[filepath.Join(root, name)]
		}
		if !ok {
			t.Errorf("%s is not in the overlay: %v", name, overlay.Replace)
			continue
		}
		if want == "" {
			if backing != "" {
				t.Errorf("%s, added since the base, must be absent there: %q", name, backing)
			}
			continue
		}
		got, err := os.ReadFile(backing)
		if err != nil || string(got) != want {
			t.Errorf("%s at the base: %q %v", name, got, err)
		}
	}

	// b's figure from the whole suite also takes e's tests, and b's own if
	// it had any.
	if got := s.Contributors([]string{"example.com/real/b"}); !slices.Equal(got, []string{"example.com/real/b", "example.com/real/e"}) {
		t.Errorf("contributors %v", got)
	}
}

// A file a package's tests open at run time is read from disk, which the
// overlay does not reach: that package's tests are left out of the base's
// run, and what they link is measured in full.
func TestARunTimeFileCannotBeReplayed(t *testing.T) {
	c := newCheckout(t, layered)
	changeA(c)
	c.write("a/testdata/case.txt", "new case\n")
	s := Resolve(c.root, "go", c.run)
	b, err := s.Baseline(t.TempDir(), "go", c.run)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Tests) != 0 {
		t.Errorf("a's tests read a changed file, so they cannot replay the base: %v", b.Tests)
	}
	if !slices.Equal(b.Unreplayed, []string{"example.com/real/b", "example.com/real/d"}) {
		t.Errorf("unreplayed %v", b.Unreplayed)
	}
}

// failing runs commands for real except the one that begins with prefix,
// which fails, or answers out when out is given.
func failing(run gates.Exec, prefix, out string) gates.Exec {
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		if strings.HasPrefix(name+" "+strings.Join(args, " "), prefix) {
			if out != "" {
				return []byte(out), nil
			}
			return nil, errors.New("exit status 1")
		}
		return run(env, stream, name, args...)
	}
}

// A base that cannot be rebuilt says what failed rather than comparing
// against nothing.
func TestABaselineThatCannotBeBuiltSaysWhy(t *testing.T) {
	c := newCheckout(t, layered)
	changeA(c)
	s := Resolve(c.root, "go", c.run)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The first changed file's base copy goes to 0/a.go; a directory there
	// leaves it nowhere to go.
	taken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(taken, "0", "a.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, dir string
		run       gates.Exec
		want      string
	}{
		{"ls-tree", t.TempDir(), failing(c.run, "git ls-tree", ""), "git cannot list the files"},
		{"show", t.TempDir(), failing(c.run, "git show", ""), "git cannot show a/a.go"},
		{"list", t.TempDir(), failing(c.run, "go list -overlay", ""), "go list cannot list the base's tests"},
		{"decode", t.TempDir(), failing(c.run, "go list -overlay", "{"), "reading go list's base packages"},
		{"dir", file, c.run, "not a directory"},
		{"taken", taken, c.run, "is a directory"},
	} {
		if _, err := s.Baseline(tc.dir, "go", tc.run); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}

	// With every changed file new since the base, nothing is written but
	// the overlay itself.
	c = newCheckout(t, layered)
	c.write("a/extra.go", "package a\n\nfunc Extra() int { return 0 }\n")
	s = Resolve(c.root, "go", c.run)
	if _, err := s.Baseline(file, "go", c.run); err == nil {
		t.Error("an overlay that cannot be written must fail")
	}

	if _, err := whole("go.mod changed").Baseline(t.TempDir(), "go", c.run); err == nil {
		t.Error("a scope over the whole tree has no base to rebuild")
	}
}

// A module below its repository's top level reads its change, and its
// base, relative to its own directory.
func TestABaselineOfAModuleBelowTheTop(t *testing.T) {
	files := map[string]string{"README.md": "top\n"}
	for p, body := range layered {
		files["svc/"+p] = body
	}
	c := newCheckout(t, files)
	changeA(&checkout{t: t, root: filepath.Join(c.root, "svc"), run: c.run})
	root := filepath.Join(c.root, "svc")
	run := gates.OSExec(root, os.Stderr)
	s := Resolve(root, "go", run)
	if !s.Narrows() || !slices.Equal(s.Files, []string{"a/a.go", "a/data.txt", "a/extra.go"}) {
		t.Fatalf("files %v, whole %q", s.Files, s.Whole)
	}
	b, err := s.Baseline(t.TempDir(), "go", run)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Tests, []string{"example.com/real/a"}) || !slices.Equal(b.Untouched, []string{"example.com/real/b", "example.com/real/d"}) {
		t.Errorf("tests %v, untouched %v", b.Tests, b.Untouched)
	}
	data, err := os.ReadFile(b.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(overlay.Replace[filepath.Join(root, "a", "a.go")])
	if err != nil || string(got) != layered["a/a.go"] {
		t.Errorf("a/a.go at the base: %q %v (overlay %v)", got, err, overlay.Replace)
	}
}
