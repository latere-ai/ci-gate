// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitTrack makes root a git repository and stages everything in it, so the
// gates that read what git tracks see the files a test wrote.
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

func TestCgoFreeFindsAnImportFollowedByAComment(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": "package a\n\n// #include <stdio.h>\nimport \"C\" // nolint\n",
		"b.go": "package a\n\nimport (\n\t\"C\" // cgo\n)\n",
		"c.go": "package a\n\nimport    c \"C\"\n",
	})
	var sb strings.Builder
	if err := CgoFree(root, &sb, nil); err == nil {
		t.Fatal("cgo-free passed files that import \"C\" with a comment or spacing")
	}
	for _, want := range []string{"a.go:4", "b.go:4", "c.go:3"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("output does not name %s:\n%s", want, sb.String())
		}
	}
}

func TestCgoFreeFailsOnAFileItCannotParse(t *testing.T) {
	root := repo(t, map[string]string{"a.go": "package a\n", "bad.go": "package a\n\nimport (\n"})
	err := CgoFree(root, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "bad.go") {
		t.Fatalf("err = %v, want the unparsable file named", err)
	}
}
func TestCgoFreeLeavesNodeModulesOut(t *testing.T) {
	root := repo(t, map[string]string{
		"ok.go":                     "package a\n",
		"web/node_modules/dep/x.go": "package x\n\nimport \"C\"\n",
	})
	if err := CgoFree(root, io.Discard, nil); err != nil {
		t.Fatalf("cgo-free read a dependency's Go source under node_modules: %v", err)
	}
}

func TestSourceScansReadOnlyWhatGitTracks(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go":       "package a\n",
		".gitignore": "scratch/\n",
	})
	const bad = "package x\n\nimport \"C\"\n\nimport \"net/http\"\n\nvar _ = &http.Client{}\n"
	for _, rel := range []string{"scratch/x.go", "untracked.go"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := CgoFree(root, io.Discard, nil); err != nil {
		t.Errorf("cgo-free read an untracked or ignored file: %v", err)
	}
	if err := OtelClient(root, io.Discard, nil); err != nil {
		t.Errorf("otel-client read an untracked or ignored file: %v", err)
	}
}

func TestSourceScansFailWhenGitCannotListFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	for name, run := range map[string]func() error{
		"cgo-free":    func() error { return CgoFree(root, io.Discard, nil) },
		"otel-client": func() error { return OtelClient(root, io.Discard, nil) },
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), "git") {
			t.Errorf("%s: err = %v, want the failure to name git", name, err)
		}
	}
}

func TestOtelClientReportsDefaultClientHelpersAndZeroClients(t *testing.T) {
	cases := map[string]string{
		"get":       "func f() { _, _ = http.Get(\"https://x\") }\n",
		"post":      "func f() { _, _ = http.Post(\"https://x\", \"text/plain\", nil) }\n",
		"head":      "func f() { _, _ = http.Head(\"https://x\") }\n",
		"postform":  "func f() { _, _ = http.PostForm(\"https://x\", nil) }\n",
		"new":       "var c = new(http.Client)\n",
		"zero var":  "var c http.Client\n",
		"local var": "func f() { var c http.Client; _ = c }\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := repo(t, map[string]string{"a.go": "package a\n\nimport \"net/http\"\n\n" + body})
			var sb strings.Builder
			err := OtelClient(root, &sb, nil)
			if err == nil {
				t.Fatalf("otel-client passed %q", body)
			}
			if !strings.Contains(sb.String(), "a.go:5") {
				t.Errorf("output does not name a.go:5:\n%s", sb.String())
			}
		})
	}
}

func TestOtelClientLeavesInstrumentedAndUnrelatedShapesAlone(t *testing.T) {
	src := "package a\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/other\"\n)\n\n" +
		"var c http.Client = *other.Client()\n" +
		"var d = other.Get(\"x\")\n" +
		"var e = http.NewRequest\n" +
		"var f = new(other.Client)\n"
	root := repo(t, map[string]string{"a.go": src})
	if err := OtelClient(root, io.Discard, nil); err != nil {
		t.Fatalf("otel-client reported an unrelated shape: %v", err)
	}
}
