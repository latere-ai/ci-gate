// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scope

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixture writes files under a new root and returns it with the exec that
// lists them as git's tracked files. A value of "" writes an empty file.
func fixture(t *testing.T, files map[string]string) (string, func([]string, bool, string, ...string) ([]byte, error)) {
	t.Helper()
	root := t.TempDir()
	var tracked []string
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		tracked = append(tracked, p)
	}
	slices.Sort(tracked)
	return root, func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name == "git" && strings.Join(args, " ") == "ls-files -z" {
			return []byte(strings.Join(tracked, "\x00") + "\x00"), nil
		}
		return nil, errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
	}
}

// The shapes a hand-written path filter encoded, each held by a fixture.
func TestInertToGo(t *testing.T) {
	repo := map[string]string{
		"go.mod":                  "module example.com/m\n",
		"api/api.go":              "package api\n",
		"api/api_test.go":         "package api\n\nimport \"os\"\n\nfunc read() { _, _ = os.ReadFile(\"../specs/003-api.md\") }\n",
		"api/notes.md":            "a Markdown file in a package directory\n",
		"web/web.go":              "package web\n\nimport (\n\t\"embed\"\n\t\"path/filepath\"\n)\n\n//go:embed static\nvar static embed.FS\n\nvar docs = filepath.Join(\"docs\", \"x\")\n",
		"web/static/app.css":      "",
		"web/static/img/a.png":    "",
		"specs/003-api.md":        "",
		"specs/004-other.md":      "",
		"docs/x/api-text.yaml":    "",
		"docs/y/other.yaml":       "",
		"CHANGELOG.md":            "",
		"scripts/release.sh":      "",
		".github/workflows/r.yml": "",
		"raw/raw.go":              "package raw\n\nvar p = `raw/rawdir/data.txt`\n\nvar q = \"./quoted//\"\n",
		"raw/rawdir/data.txt":     "",
		"tool/go.mod":             "module example.com/tool\n",
		"tool/README.md":          "",
		"vendor/v/v.go":           "package v\n\nvar x = \"scripts\"\n",
	}
	root, run := fixture(t, repo)
	for _, c := range []struct {
		name  string
		files []string
		inert bool
	}{
		{"a spec a test reads by relative path", []string{"specs/003-api.md"}, false},
		{"a file beside the spec the same literal names", []string{"specs/004-other.md"}, false},
		{"a directory joined with filepath.Join", []string{"docs/x/api-text.yaml"}, false},
		{"a file under the directory only its parent literal names", []string{"docs/y/other.yaml"}, false},
		{"an embedded directory", []string{"web/static/app.css"}, false},
		{"a file below the embedded directory", []string{"web/static/img/a.png"}, false},
		{"Markdown inside a package directory", []string{"api/notes.md"}, false},
		{"a raw string literal", []string{"raw/rawdir/data.txt"}, false},
		{"a file in a nested module", []string{"tool/README.md"}, false},
		{"Go source", []string{"api/api.go"}, false},
		{"a file nothing names", []string{"CHANGELOG.md"}, true},
		{"a workflow nothing names", []string{".github/workflows/r.yml"}, true},
		{"a script only vendor names", []string{"scripts/release.sh"}, true},
		{"several files nothing names", []string{"CHANGELOG.md", ".github/workflows/r.yml"}, true},
		{"one named among several", []string{"CHANGELOG.md", "specs/003-api.md"}, false},
		{"nothing changed", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, why := inert(root, c.files, run)
			if got != c.inert || got != (why == "") {
				t.Errorf("inert(%v) = %v, %q, want %v", c.files, got, why, c.inert)
			}
		})
	}
}

// The root package does not make every file of the repository its own: what
// it reads, an embed pattern or a literal names.
func TestInertWithARootPackage(t *testing.T) {
	root, run := fixture(t, map[string]string{
		"main.go":     "package main\n\n//go:embed all:assets \"two words\" *.txt\nvar x int\n",
		"README.md":   "",
		"notes/n.md":  "",
		"assets/.a":   "",
		"two words/w": "",
		"x.txt":       "",
	})
	for f, inert := range map[string]bool{
		"README.md":   true,
		"notes/n.md":  true,
		"assets/.a":   false,
		"two words/w": false,
		"x.txt":       false,
	} {
		if got := inertFiles(root, run, f); got != inert {
			t.Errorf("%s: inert %v, want %v", f, got, inert)
		}
	}
}

func inertFiles(root string, run func([]string, bool, string, ...string) ([]byte, error), f ...string) bool {
	got, _ := inert(root, f, run)
	return got
}

// A repository that cannot be read is never inert: the scope does not
// narrow on a guess.
func TestInertFailsClosed(t *testing.T) {
	root, run := fixture(t, map[string]string{"docs/a.md": ""})
	broken := func([]string, bool, string, ...string) ([]byte, error) { return nil, errors.New("no git") }
	if got, why := inert(root, []string{"docs/a.md"}, broken); got || !strings.Contains(why, "cannot list") {
		t.Error("git that cannot list the tracked files must not make a change inert")
	}
	// A tracked file that cannot be read is not ruled out; one deleted in
	// the working tree is simply gone.
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	listed := func(e []string, s bool, name string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "ls-files -z" {
			return []byte("x.go\x00gone.go\x00"), nil
		}
		return run(e, s, name, args...)
	}
	if got, _ := inert(root, []string{"docs/a.md"}, listed); os.Getuid() != 0 && got {
		t.Error("an unreadable tracked Go file must not make a change inert")
	}
	if err := os.Chmod(filepath.Join(root, "x.go"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, why := inert(root, []string{"docs/a.md"}, listed); !got {
		t.Log(why)
		t.Error("a tracked file deleted from the tree names nothing")
	}
}

func TestEmbedPatterns(t *testing.T) {
	got := embedPatterns(` a.txt  "b c" ` + "`d`" + ` all:e *.go`)
	want := []string{"a.txt", "b c", "d", "all:e", "*.go"}
	if !slices.Equal(got, want) {
		t.Errorf("patterns %q, want %q", got, want)
	}
	// A pattern the matcher rejects counts as a match.
	n := newNeedles([]string{"p/x"})
	if !n.embeds(".", "[") {
		t.Error("a malformed pattern must count as matching")
	}
	if n.embeds("q", "x") || n.embeds(".", "y") {
		t.Error("a pattern in another directory or of another name matches nothing")
	}
}

func TestScanSurvivesBrokenSource(t *testing.T) {
	n := newNeedles([]string{"specs/a.md"})
	if n.scan(".", []byte("package p\n\nvar a = \"specs\" \x00 \"unterminated")) == "" {
		t.Error("a literal before the break still counts")
	}
	if n.scan(".", []byte("package p\n\nvar a = \"\\q\"\n")) != "" {
		t.Error("a literal that does not unquote and names nothing")
	}
	if n.scan(".", []byte("package p\n\nvar a = \"\\q/specs\"\n")) == "" {
		t.Error("a literal that does not unquote is read as written")
	}
}

func TestResolveSinceInCI(t *testing.T) {
	root, run := fixture(t, map[string]string{"CHANGELOG.md": "", "go.mod": "module m\n"})
	r := newRepo("CHANGELOG.md")
	exec := func(e []string, s bool, name string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "ls-files -z" {
			return run(e, s, name, args...)
		}
		return r.exec(e, s, name, args...)
	}
	s := ResolveSince(root, "go", exec, base, true)
	if !s.Narrows() || s.Packages() || !s.CI || !s.GoInert {
		t.Fatalf("a CI scope of an inert change: %+v", s)
	}
	if s.Since() != base[:12] {
		t.Errorf("since %q", s.Since())
	}
	r.changed = []string{"go.mod"}
	if s := ResolveSince(root, "go", exec, base, true); s.Whole == "" {
		t.Error("a whole-tree file checks everything on CI too")
	}
	r.changed = []string{"CHANGELOG.md"}
	r.diff = errors.New("boom")
	if s := ResolveSince(root, "go", exec, base, true); s.Whole == "" {
		t.Error("a diff that fails checks the whole tree")
	}
	r.diff = nil
	r.changed = []string{"a/a.go"}
	if s := ResolveSince(root, "go", exec, base, true); s.GoInert {
		t.Error("Go source is never inert")
	}
	// Off CI the same base narrows to packages.
	r.changed = []string{"a/a.go"}
	if s := ResolveSince(tree(t, "a/a.go"), "go", r.exec, base, false); !s.Packages() || len(s.Changed) != 1 {
		t.Errorf("a laptop scope from a named base: %+v", s)
	}
	if s := WholeTree("why"); s.Narrows() || s.Whole != "why" {
		t.Errorf("WholeTree: %+v", s)
	}
}
