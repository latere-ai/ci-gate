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

func TestAMessageHeldInAConstantIsRead(t *testing.T) {
	callers := map[string]string{
		"same package": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
			"const msg = \"apply Deployment insula-p-3f2a/api: store.Deploy not found\"\n\n" +
			"func h(w http.ResponseWriter) { api.WriteError(w, \"deploy_failed\", msg) }\n",
		"concatenated": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
			"const prefix = \"deploy failed: \"\nconst msg = prefix + \"store.Deploy not found\"\n\n" +
			"func h(w http.ResponseWriter) { api.WriteError(w, \"deploy_failed\", msg) }\n",
		"another package": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n\t\"example.com/app/internal/text\"\n)\n\n" +
			"func h(w http.ResponseWriter) { api.WriteError(w, \"deploy_failed\", text.Failed) }\n",
	}
	for name, main := range callers {
		t.Run(name, func(t *testing.T) {
			root := module(t, map[string]string{
				"internal/api/api.go":   apiPkg,
				"internal/text/text.go": "package text\n\nconst Failed = \"apply Deployment insula-p-3f2a/api: store.Deploy not found\"\n",
				"cmd/app/main.go":       main,
			})
			out, err := run(t, surfaces("internal/api.WriteError"), root)
			if err == nil || !strings.Contains(err.Error(), "developer sentence") {
				t.Fatalf("a developer sentence held in a constant was not found (err %v):\n%s", err, out)
			}
			if !strings.Contains(out, "main.go:") {
				t.Errorf("the finding should name the call site:\n%s", out)
			}
		})
	}
}

func TestAConstantInUserRegisterPasses(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg,
		"cmd/app/main.go": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
			"const a = \"This deploy is still running.\"\nconst b = b2\nconst b2 = a\n\n" +
			"func h(w http.ResponseWriter, err error) { api.WriteError(w, \"c\", b); api.WriteError(w, \"c\", err.Error()) }\n",
	})
	if out, err := run(t, surfaces("internal/api.WriteError"), root); err != nil {
		t.Fatalf("a constant in the user's register failed: %v\n%s", err, out)
	}
}

func TestACallThroughAVersionedImportIsMatched(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg,
		"cmd/a/main.go": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
			"func h(w http.ResponseWriter) { api.WriteError(w, \"ok\", \"try again\") }\n",
		"cmd/b/main.go": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api/v2\"\n)\n\n" +
			"func h(w http.ResponseWriter) { api.WriteError(w, \"x\", \"store.Deploy not found\") }\n",
		"internal/api/v2/api.go": "package api\n\nimport \"net/http\"\n\nfunc WriteError(w http.ResponseWriter, code, msg string) {}\n",
	})
	out, err := run(t, surfaces("internal/api.WriteError", "internal/api/v2.WriteError"), root)
	if err == nil || !strings.Contains(err.Error(), "developer sentence") {
		t.Fatalf("a call through a /v2 import was not read (err %v):\n%s", err, out)
	}
}

func TestACallThroughAnExternalVersionedImportIsMatched(t *testing.T) {
	root := module(t, map[string]string{
		"cmd/b/main.go": "package main\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/ext/render/v3\"\n)\n\n" +
			"func h(w http.ResponseWriter) { render.Error(w, \"store.Deploy not found\") }\n",
	})
	out, err := run(t, surfaces("example.com/ext/render/v3.Error"), root)
	if err == nil || !strings.Contains(err.Error(), "developer sentence") {
		t.Fatalf("a call through an external /v3 import was not read (err %v):\n%s", err, out)
	}
}

func TestLocalNameOfAnImportPath(t *testing.T) {
	ix := &index{names: map[string]string{"example.com/app/internal/odd": "different"}}
	for ip, want := range map[string]string{
		"net/http":                     "http",
		"example.com/lib/v2":           "lib",
		"example.com/lib/v10":          "lib",
		"example.com/app/internal/odd": "different",
		"gopkg.in/yaml.v3":             "yaml",
		"gopkg.in/check":               "check",
		"example.com/v2":               "example.com",
	} {
		if got := ix.localName(ip); got != want {
			t.Errorf("localName(%q) = %q, want %q", ip, got, want)
		}
	}
}
