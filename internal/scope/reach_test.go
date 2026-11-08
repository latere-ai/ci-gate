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
