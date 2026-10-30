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

const base = "1111111111111111111111111111111111111111"

// pkg is one package go list reports, as the fake toolchain prints it.
type pkg struct {
	ImportPath   string
	Deps         []string `json:",omitempty"`
	TestImports  []string `json:",omitempty"`
	XTestImports []string `json:",omitempty"`
}

// module is the fixture graph: b imports a, c's tests import b, d stands
// alone, and a/inner is a package inside a's directory that imports nothing.
var module = []pkg{
	{ImportPath: "example.com/m/a"},
	{ImportPath: "example.com/m/a/inner"},
	{ImportPath: "example.com/m/b", Deps: []string{"example.com/m/a", "fmt"}},
	{ImportPath: "example.com/m/c", TestImports: []string{"example.com/m/b", "testing"}},
	{ImportPath: "example.com/m/d", XTestImports: []string{"example.com/m/d", "testing"}},
	// A package outside the module, as go list -e can report one, is not
	// the module's to scope.
	{ImportPath: "other.org/x"},
}

// repo stands in for git and the toolchain. Each answer can be replaced, and
// a field holding an error makes that command fail.
type repo struct {
	head, verify, mergeBase, diff, untracked, mod, list error
	originHEAD                                          string
	changed, others                                     []string
	modulePath                                          string
	pkgs                                                []pkg
	listOut                                             string
}

func newRepo(changed ...string) *repo {
	return &repo{originHEAD: "origin/main", changed: changed, modulePath: "example.com/m", pkgs: module}
}

func (r *repo) exec(_ []string, _ bool, name string, args ...string) ([]byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	switch {
	case cmd == "git rev-parse --abbrev-ref origin/HEAD":
		return []byte(r.originHEAD + "\n"), r.head
	case cmd == "git rev-parse --verify --quiet origin/main^{commit}":
		return []byte(base + "\n"), r.verify
	case strings.HasPrefix(cmd, "git merge-base HEAD "):
		return []byte(base + "\n"), r.mergeBase
	case cmd == "git diff --name-only --no-renames -z "+base:
		return []byte(strings.Join(r.changed, "\x00")), r.diff
	case cmd == "git ls-files --others --exclude-standard -z":
		return []byte(strings.Join(r.others, "\x00")), r.untracked
	case cmd == "go list -m":
		return []byte(r.modulePath + "\n"), r.mod
	case cmd == "go list -e -json=ImportPath,Deps,TestImports,XTestImports ./...":
		if r.listOut != "" {
			return []byte(r.listOut), r.list
		}
		var b strings.Builder
		for _, p := range r.pkgs {
			j, _ := json.MarshalIndent(p, "", "\t")
			b.Write(j)
			b.WriteByte('\n')
		}
		return []byte(b.String()), r.list
	}
	return nil, errors.New("unexpected command: " + cmd)
}

// tree writes each path under a new root, so the files a change names exist
// on disk unless a test leaves one out to stand for a deletion.
func tree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func paths(pkgs []Package) []string {
	var out []string
	for _, p := range pkgs {
		out = append(out, p.ImportPath)
	}
	return out
}

func TestAChangeReachesItsPackageAndWhatImportsIt(t *testing.T) {
	files := []string{"a/a.go", "a/testdata/golden.json", "frontend/app.ts", "CHANGELOG.md"}
	r := newRepo(files...)
	s := Resolve(tree(t, files...), "go", r.exec)
	if !s.Narrows() {
		t.Fatalf("the scope must hold, it checks the whole tree: %s", s.Whole)
	}
	if s.Ref != "origin/main" || s.Base != base {
		t.Errorf("measured from %s at %s", s.Ref, s.Base)
	}
	if !slices.Equal(s.Files, []string{"CHANGELOG.md", "a/a.go", "a/testdata/golden.json", "frontend/app.ts"}) {
		t.Errorf("files %v", s.Files)
	}
	// testdata belongs to the package its directory sits in; a frontend
	// and the changelog belong to none.
	if got := paths(s.Changed); !slices.Equal(got, []string{"example.com/m/a"}) {
		t.Errorf("changed %v", got)
	}
	// b imports a; c's tests import b; a/inner and d reach neither.
	if got := paths(s.Reached); !slices.Equal(got, []string{"example.com/m/a", "example.com/m/b", "example.com/m/c"}) {
		t.Errorf("reached %v", got)
	}
	if s.Changed[0].Pattern() != "./a" {
		t.Errorf("pattern %q", s.Changed[0].Pattern())
	}
}

// A file belongs to the nearest package: a package inside another's
// directory is its own.
func TestAFileBelongsToItsNearestPackage(t *testing.T) {
	files := []string{"a/inner/x.go"}
	s := Resolve(tree(t, files...), "go", newRepo(files...).exec)
	if got := paths(s.Changed); !slices.Equal(got, []string{"example.com/m/a/inner"}) {
		t.Errorf("changed %v", got)
	}
	if got := paths(s.Reached); !slices.Equal(got, []string{"example.com/m/a/inner"}) {
		t.Errorf("reached %v", got)
	}
}

// A package whose tests import it from outside, as an external test
// package does, is reached through those imports too.
func TestAnExternalTestReachesItsPackage(t *testing.T) {
	files := []string{"d/d.go"}
	s := Resolve(tree(t, files...), "go", newRepo(files...).exec)
	if got := paths(s.Reached); !slices.Equal(got, []string{"example.com/m/d"}) {
		t.Errorf("reached %v", got)
	}
}

// At the root package, every file in the checkout is an input of the root.
func TestARootPackageOwnsWhatNoOtherDoes(t *testing.T) {
	files := []string{"frontend/app.ts", "b/b.go"}
	r := newRepo(files...)
	r.pkgs = append([]pkg{{ImportPath: "example.com/m"}}, module...)
	s := Resolve(tree(t, files...), "go", r.exec)
	if got := paths(s.Changed); !slices.Equal(got, []string{"example.com/m", "example.com/m/b"}) {
		t.Errorf("changed %v", got)
	}
	for _, p := range s.Changed {
		if p.ImportPath == "example.com/m" && p.Pattern() != "." {
			t.Errorf("the root's pattern is %q", p.Pattern())
		}
	}
}

func TestUntrackedFilesAreTheChangeToo(t *testing.T) {
	r := newRepo("b/b.go")
	r.others = []string{"b/new.go", "b/b.go"}
	s := Resolve(tree(t, "b/b.go", "b/new.go"), "go", r.exec)
	if !slices.Equal(s.Files, []string{"b/b.go", "b/new.go"}) {
		t.Errorf("each path once: %v", s.Files)
	}
	if got := paths(s.Changed); !slices.Equal(got, []string{"example.com/m/b"}) {
		t.Errorf("changed %v", got)
	}
}

func TestNoChangeNarrowsToNothing(t *testing.T) {
	s := Resolve(tree(t), "go", newRepo().exec)
	if !s.Narrows() || len(s.Files) != 0 || len(s.Changed) != 0 || len(s.Reached) != 0 {
		t.Errorf("an unchanged checkout changes no package: %+v", s)
	}
}

// A file a nested module holds is that module's, which ./... never reads.
func TestANestedModuleIsNotTheChange(t *testing.T) {
	files := []string{"tools/gen/main.go", "tools/go.mod"}
	s := Resolve(tree(t, files...), "go", newRepo("tools/gen/main.go").exec)
	if !s.Narrows() || len(s.Changed) != 0 {
		t.Errorf("a nested module's file must name no package: %+v", s)
	}
}

// A deleted file in a package that is still there is that package's change.
func TestADeletedFileChangesItsPackage(t *testing.T) {
	r := newRepo("a/old.go", "a/testdata/gen.go")
	s := Resolve(tree(t, "a/a.go"), "go", r.exec)
	if !s.Narrows() {
		t.Fatalf("a file deleted from a package still there must not widen the scope: %s", s.Whole)
	}
	if got := paths(s.Changed); !slices.Equal(got, []string{"example.com/m/a"}) {
		t.Errorf("changed %v", got)
	}
}

// A package removed or moved leaves whatever imported it outside the graph,
// so nothing the graph says can be trusted.
func TestARemovedPackageChecksTheWholeTree(t *testing.T) {
	r := newRepo("gone/gone.go", "a/a.go")
	s := Resolve(tree(t, "a/a.go"), "go", r.exec)
	if s.Narrows() || !strings.Contains(s.Whole, "the package in gone was removed or moved") {
		t.Errorf("whole = %q", s.Whole)
	}
}

// What the whole tree depends on, the module's dependencies and the bar's
// own configuration, sends the check back to the whole tree.
func TestADependencyOrTheBarsConfigChecksTheWholeTree(t *testing.T) {
	for _, f := range []string{"go.mod", "go.sum", "go.work", ".lateregate.yaml", ".golangci.yml"} {
		s := Resolve(tree(t, f), "go", newRepo(f, "a/a.go").exec)
		if s.Narrows() || s.Whole != f+" changed since origin/main (111111111111)" {
			t.Errorf("%s: whole = %q", f, s.Whole)
		}
	}
	// A go.mod below the root is a nested module's, not this one's.
	if s := Resolve(tree(t, "tools/go.mod"), "go", newRepo("tools/go.mod").exec); !s.Narrows() {
		t.Errorf("a nested module's go.mod must not widen the scope: %s", s.Whole)
	}
}

func TestTheDefaultBranchIsTheRemotes(t *testing.T) {
	r := newRepo()
	r.originHEAD = "origin/trunk"
	if s := Resolve(tree(t), "go", r.exec); s.Ref != "origin/trunk" {
		t.Errorf("ref %q, want origin/HEAD's branch", s.Ref)
	}
	// A clone that never recorded origin/HEAD falls back to origin/main.
	r = newRepo()
	r.head = errors.New("ambiguous argument")
	if s := Resolve(tree(t), "go", r.exec); s.Ref != "origin/main" || !s.Narrows() {
		t.Errorf("ref %q, whole %q", s.Ref, s.Whole)
	}
	r = newRepo()
	r.originHEAD = "origin/HEAD"
	if s := Resolve(tree(t), "go", r.exec); s.Ref != "origin/main" {
		t.Errorf("ref %q", s.Ref)
	}
}

// Every way the change cannot be measured checks the whole tree and says
// why; none of them narrows on a guess.
func TestWhatCannotBeMeasuredChecksTheWholeTree(t *testing.T) {
	broken := errors.New("exit status 128")
	for _, tc := range []struct {
		name string
		fail func(*repo)
		want string
	}{
		{"no default branch", func(r *repo) { r.head, r.verify = broken, broken }, "there is no origin/main to measure a change from"},
		{"no merge base", func(r *repo) { r.mergeBase = broken }, "HEAD shares no commit with origin/main"},
		{"no diff", func(r *repo) { r.diff = broken }, "git cannot list what changed since 111111111111"},
		{"no untracked list", func(r *repo) { r.untracked = broken }, "git cannot list the untracked files"},
		{"no module", func(r *repo) { r.mod = broken }, "go list cannot name the module"},
		{"outside a module", func(r *repo) { r.modulePath = "command-line-arguments" }, "is not one Go module"},
		{"a workspace", func(r *repo) { r.modulePath = "example.com/m\nexample.com/n" }, "is not one Go module"},
		{"no package list", func(r *repo) { r.list = broken }, "go list cannot list the module's packages"},
		{"an unreadable list", func(r *repo) { r.listOut = "{not json" }, "reading go list's packages"},
		{"no package", func(r *repo) { r.pkgs = []pkg{{ImportPath: "other.org/x"}} }, "go list found no package in the module"},
	} {
		r := newRepo("a/a.go")
		tc.fail(r)
		s := Resolve(tree(t, "a/a.go"), "go", r.exec)
		if s.Narrows() || !strings.Contains(s.Whole, tc.want) {
			t.Errorf("%s: whole = %q, want it to say %q", tc.name, s.Whole, tc.want)
		}
	}
}

func TestTouchesAndSince(t *testing.T) {
	s := &Scope{Ref: "origin/main", Base: base, Files: []string{"proofs/Basic.lean", "README.md"}}
	if !s.Touches("proofs") || !s.Touches(".") || s.Touches("proof") || s.Touches("docs") {
		t.Error("touches reads directories, not prefixes")
	}
	if s.Since() != "origin/main (111111111111)" {
		t.Errorf("since %q", s.Since())
	}
	if (&Scope{Base: "abc"}).Since() != " (abc)" {
		t.Error("a short base is printed whole")
	}
	var none *Scope
	if none.Narrows() || (&Scope{Whole: "go.mod changed"}).Narrows() {
		t.Error("a nil scope and a whole-tree scope narrow nothing")
	}
}

// Against a real repository and toolchain: a commit on top of origin/main
// and an edit beside it are the change, measured the way a laptop runs it.
func TestResolveAgainstARealCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := gates.OSExec(root, os.Stderr)
	git := func(args ...string) {
		t.Helper()
		if out, err := run(nil, false, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("go.mod", "module example.com/real\n\ngo 1.24\n")
	write("a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	write("b/b.go", "package b\n\nimport \"example.com/real/a\"\n\nfunc B() int { return a.A() }\n")
	write("c/c.go", "package c\n\nfunc C() int { return 3 }\n")
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	write("a/a.go", "package a\n\nfunc A() int { return 2 }\n")
	git("commit", "-q", "-am", "change a")
	write("notes.md", "untracked\n")

	s := Resolve(root, "go", run)
	if !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
	if !slices.Equal(s.Files, []string{"a/a.go", "notes.md"}) {
		t.Errorf("files %v", s.Files)
	}
	if got := paths(s.Reached); !slices.Equal(got, []string{"example.com/real/a", "example.com/real/b"}) {
		t.Errorf("reached %v", got)
	}
}
