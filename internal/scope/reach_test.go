// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scope

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

// otherGOOS is a GOOS this machine does not build.
func otherGOOS() string {
	if runtime.GOOS == "linux" {
		return "windows"
	}
	return "linux"
}

const importsA = "package b\n\nimport \"example.com/m/a\"\n\nvar _ = a.F\n"

// A package that imports the changed one only from a file built for another
// GOOS is reached, and so is whatever imports that package.
func TestAnImporterBuiltOnAnotherGOOSIsReached(t *testing.T) {
	c := newCheckout(t, map[string]string{
		"go.mod":                     "module example.com/m\n\ngo 1.22\n",
		"a/a.go":                     "package a\n\nfunc F() int { return 1 }\n",
		"b/b.go":                     "package b\n",
		"b/b_" + otherGOOS() + ".go": importsA,
		"d/d.go":                     "package d\n\nimport _ \"example.com/m/b\"\n",
		"e/e.go":                     "package e\n",
	})
	c.write("a/a.go", "package a\n\nfunc F() string { return \"\" }\n")
	s := Resolve(c.root, "go", c.run)
	if !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
	want := []string{"example.com/m/a", "example.com/m/b", "example.com/m/d"}
	if got := paths(s.Reached); !slices.Equal(got, want) {
		t.Fatalf("reached %v, want %v", got, want)
	}
}

// A file behind a build tag, and a test file behind one, make their
// packages importers.
func TestAnImporterBehindABuildTagIsReached(t *testing.T) {
	c := newCheckout(t, map[string]string{
		"go.mod":           "module example.com/m\n\ngo 1.22\n",
		"a/a.go":           "package a\n\nfunc F() int { return 1 }\n",
		"b/b.go":           "package b\n",
		"b/tagged.go":      "//go:build integration\n\n" + importsA,
		"t/t.go":           "package t\n",
		"t/t_test.go":      "package t\n",
		"t/tagged_test.go": "//go:build integration\n\npackage t\n\nimport _ \"example.com/m/a\"\n",
		"other/o.go":       "package other\n",
	})
	c.write("a/a.go", "package a\n\nfunc F() string { return \"\" }\n")
	s := Resolve(c.root, "go", c.run)
	if !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
	want := []string{"example.com/m/a", "example.com/m/b", "example.com/m/t"}
	if got := paths(s.Reached); !slices.Equal(got, want) {
		t.Fatalf("reached %v, want %v", got, want)
	}
}

// What a tagged test file imports reaches that package alone: a package
// importing it is not reached through a file only its tests build.
func TestATaggedTestImportDoesNotReachThePackageImportingIt(t *testing.T) {
	c := newCheckout(t, map[string]string{
		"go.mod":           "module example.com/m\n\ngo 1.22\n",
		"a/a.go":           "package a\n\nfunc F() int { return 1 }\n",
		"t/t.go":           "package t\n",
		"t/tagged_test.go": "//go:build integration\n\npackage t\n\nimport _ \"example.com/m/a\"\n",
		"u/u.go":           "package u\n\nimport _ \"example.com/m/t\"\n",
	})
	c.write("a/a.go", "package a\n\nfunc F() string { return \"\" }\n")
	s := Resolve(c.root, "go", c.run)
	if !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
	want := []string{"example.com/m/a", "example.com/m/t"}
	if got := paths(s.Reached); !slices.Equal(got, want) {
		t.Fatalf("reached %v, want %v", got, want)
	}
}

// A file the build excludes whose imports cannot be read leaves the graph
// unknown, so the whole tree is checked and the file is named.
func TestAnUnreadableExcludedFileChecksTheWholeTree(t *testing.T) {
	c := newCheckout(t, map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.22\n",
		"a/a.go":      "package a\n",
		"b/b.go":      "package b\n",
		"b/broken.go": "//go:build integration\n\npackage b\n\nimport (\n",
	})
	c.write("a/a.go", "package a\n\nfunc F() {}\n")
	s := Resolve(c.root, "go", c.run)
	if s.Narrows() {
		t.Fatalf("scope held: reached %v", paths(s.Reached))
	}
	if !strings.Contains(s.Whole, "b/broken.go") || !strings.Contains(s.Whole, "cannot be read") {
		t.Fatalf("reason %q does not name the file and why", s.Whole)
	}
}

var nestedReplace = map[string]string{
	"go.mod":       "module example.com/m\n\ngo 1.22\n\nrequire example.com/sub v0.0.0\n\nreplace example.com/sub => ./sub\n",
	"sub/go.mod":   "module example.com/sub\n\ngo 1.22\n",
	"sub/sub.go":   "package sub\n\nfunc F() int { return 1 }\n",
	"other/go.mod": "module example.com/other\n\ngo 1.22\n",
	"other/o.go":   "package other\n",
	"c/c.go":       "package c\n\nimport \"example.com/sub\"\n\nvar _ = sub.F\n",
}

// A change inside a nested module the root module replaces to checks the
// whole tree, and the reason names the module and the directive.
func TestAChangeInAReplacedNestedModuleChecksTheWholeTree(t *testing.T) {
	c := newCheckout(t, nestedReplace)
	c.write("sub/deep/x.txt", "x\n")
	c.write("sub/sub.go", "package sub\n\nfunc F() string { return \"\" }\n")
	s := Resolve(c.root, "go", c.run)
	if s.Narrows() {
		t.Fatalf("scope held: reached %v", paths(s.Reached))
	}
	for _, w := range []string{"sub", "replace directive in go.mod", "./sub"} {
		if !strings.Contains(s.Whole, w) {
			t.Errorf("reason %q lacks %q", s.Whole, w)
		}
	}
}

// A nested module the root does not reach stays out of the change, even
// beside one it does.
func TestAChangeInAnUnreachedNestedModuleIsNotTheChange(t *testing.T) {
	c := newCheckout(t, nestedReplace)
	c.write("other/o.go", "package other\n\nfunc O() {}\n")
	s := Resolve(c.root, "go", c.run)
	if !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
	if len(s.Reached) != 0 {
		t.Fatalf("reached %v", paths(s.Reached))
	}
}

// A replace to a version, not a directory, reaches no nested module.
func TestAReplaceToAVersionReachesNoNestedModule(t *testing.T) {
	files := map[string]string{
		"go.mod":     "module example.com/m\n\ngo 1.22\n\nreplace example.com/sub => example.com/fork v1.0.0\n",
		"sub/go.mod": "module example.com/sub\n\ngo 1.22\n",
		"sub/sub.go": "package sub\n",
		"c/c.go":     "package c\n",
	}
	c := newCheckout(t, files)
	c.write("sub/sub.go", "package sub\n\nfunc F() {}\n")
	if s := Resolve(c.root, "go", c.run); !s.Narrows() {
		t.Fatalf("whole: %s", s.Whole)
	}
}

// A go.work use line reaches a nested module as a replace does.
func TestAChangeInAWorkspaceNestedModuleChecksTheWholeTree(t *testing.T) {
	c := newCheckout(t, map[string]string{
		"go.mod":     "module example.com/m\n\ngo 1.22\n",
		"go.work":    "go 1.22\n\nuse (\n\t.\n\t./sub\n)\n",
		"sub/go.mod": "module example.com/sub\n\ngo 1.22\n",
		"sub/sub.go": "package sub\n",
		"c/c.go":     "package c\n",
	})
	c.write("sub/sub.go", "package sub\n\nfunc F() {}\n")
	s := Resolve(c.root, "go", c.run)
	if s.Narrows() || !strings.Contains(s.Whole, "use line in go.work") {
		t.Fatalf("whole = %q", s.Whole)
	}
}

// A go.mod that cannot be read leaves the nested module's reach unknown.
func TestAnUnreadableGoModWithANestedChangeChecksTheWholeTree(t *testing.T) {
	c := newCheckout(t, map[string]string{
		"go.mod":     "module example.com/m\n\ngo 1.22\n",
		"sub/go.mod": "module example.com/sub\n\ngo 1.22\n",
		"sub/sub.go": "package sub\n",
	})
	c.write("go.mod", "not a go.mod\n")
	c.git("commit", "-q", "-am", "break")
	c.git("update-ref", "refs/remotes/origin/main", "HEAD")
	c.write("sub/sub.go", "package sub\n\nfunc F() {}\n")
	s := Resolve(c.root, "go", c.run)
	if s.Narrows() || !strings.Contains(s.Whole, "cannot be told") {
		t.Fatalf("whole = %q", s.Whole)
	}
}
