// Copyright 2026 Latere AI.
// Licensed under the MIT License.

package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestACgoFreeTreePasses(t *testing.T) {
	var sb strings.Builder
	root := repo(t, map[string]string{
		"a.go":      "package a\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n",
		"b/b.go":    "package b\n\nimport \"strings\"\n",
		"c.txt":     "import \"C\"\n", // not a Go file
		"README.md": "```go\nimport \"C\"\n```\n",
	})
	if err := CgoFree(root, &sb, nil); err != nil {
		t.Fatalf("a cgo-free tree should pass: %v", err)
	}
	if !strings.Contains(sb.String(), "2 Go file(s)") {
		t.Errorf("the count should cover only Go files:\n%s", sb.String())
	}
}

func TestASingleImportOfCIsFound(t *testing.T) {
	var sb strings.Builder
	err := CgoFree(repo(t, map[string]string{"a.go": "package a\n\nimport \"C\"\n"}), &sb, nil)
	if err == nil {
		t.Fatal(`import "C" must fail`)
	}
	if !strings.Contains(sb.String(), "a.go:3") {
		t.Errorf("the file and line must be named:\n%s", sb.String())
	}
}

func TestAnImportOfCInsideABlockIsFound(t *testing.T) {
	var sb strings.Builder
	src := "package a\n\nimport (\n\t\"fmt\"\n\t\"C\"\n)\n"
	if err := CgoFree(repo(t, map[string]string{"a.go": src}), &sb, nil); err == nil {
		t.Fatal(`"C" inside an import block must fail`)
	}
}

func TestABlankImportOfCIsFound(t *testing.T) {
	var sb strings.Builder
	src := "package a\n\nimport (\n\t_ \"C\"\n)\n"
	if err := CgoFree(repo(t, map[string]string{"a.go": src}), &sb, nil); err == nil {
		t.Fatal(`a blank import of "C" must fail`)
	}
}

// A file can import "C" behind a build tag this platform does not select and
// still be a violation, so the scan reads source rather than trusting a build.
func TestAnImportBehindABuildTagIsStillFound(t *testing.T) {
	var sb strings.Builder
	src := "//go:build plan9\n\npackage a\n\nimport \"C\"\n"
	if err := CgoFree(repo(t, map[string]string{"a.go": src}), &sb, nil); err == nil {
		t.Fatal("a build tag this platform does not select does not excuse it")
	}
}

// A comment mentioning C is not an import.
func TestACommentIsNotAnImport(t *testing.T) {
	var sb strings.Builder
	src := "package a\n\n// import \"C\" is what we do not do\nimport \"fmt\"\n"
	if err := CgoFree(repo(t, map[string]string{"a.go": src}), &sb, nil); err != nil {
		t.Fatalf("a comment must not fail the gate: %v", err)
	}
}

func TestSkippedDirectoriesAreNotScanned(t *testing.T) {
	var sb strings.Builder
	files := map[string]string{
		"a.go":          "package a\n",
		"vendor/v.go":   "package v\n\nimport \"C\"\n",
		"testdata/t.go": "package t\n\nimport \"C\"\n",
	}
	if err := CgoFree(repo(t, files), &sb, []string{"vendor"}); err != nil {
		t.Fatalf("skipped directories must not fail the gate: %v", err)
	}
}

// A scan that read nothing proves nothing.
func TestATreeWithNoGoFilesFails(t *testing.T) {
	var sb strings.Builder
	err := CgoFree(repo(t, map[string]string{"README.md": "hi\n"}), &sb, nil)
	if err == nil || !strings.Contains(err.Error(), "vacuously") {
		t.Fatalf("an empty scan must fail rather than report green, got %v", err)
	}
}

func TestAnUnreadableRootIsAnError(t *testing.T) {
	if err := CgoFree(filepath.Join(t.TempDir(), "nope"), &strings.Builder{}, nil); err == nil {
		t.Fatal("a missing root must be an error")
	}
}
