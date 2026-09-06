// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package registers

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// module writes a module at a fresh root with the given files and returns
// the root.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"go.mod": "module example.com/app\n\ngo 1.27\n"}
	maps.Copy(all, files)
	for rel, src := range all {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const apiPkg = "package api\n\nimport \"net/http\"\n\nfunc WriteError(w http.ResponseWriter, code, msg string) {}\n"

func run(t *testing.T, cfg config.Registers, root string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(cfg, root, &sb)
	return sb.String(), err
}

func surfaces(names ...string) config.Registers {
	return config.Registers{UserSurfaces: names}
}

// Each tell, as it appears in a string handed to the surface, and the
// sentences that look like one but are not.
func TestTellsOnAUserSurface(t *testing.T) {
	cases := []struct {
		name string
		lit  string
		want string // the tell name, or "" when the string passes
	}{
		{"import path", `"see latere.ai/x/pkg/httpjson"`, "Go import path"},
		{"github path", `"github.com/latere-ai/pkg failed"`, "Go import path"},
		{"a URL is a page, not a path", `"Open https://platform.latere.ai/docs/lux to fix it."`, ""},
		{"a host alone", `"Sign in at auth.latere.ai first."`, ""},
		{"package-qualified identifier", `"store.Deploy not found"`, "package-qualified identifier"},
		{"pgx error", `"query: pgx.ErrNoRows"`, "package-qualified identifier"},
		{"a sentence break is not a qualifier", `"Wait for it. Then retry."`, ""},
		{"kubernetes kind and name", `"apply Deployment insula-p-3f2a/api failed"`, "Kubernetes object"},
		{"kubernetes namespace", `"Namespace insula-p-3f2a is terminating"`, "Kubernetes object"},
		{"kubectl style plural", `"pods \"api-7d9c\" is forbidden"`, "Kubernetes object"},
		{"a kind in prose", `"Service unavailable. Try again in a few minutes."`, ""},
		{"absolute path", `"cannot read /etc/latere/config.yaml"`, "file path"},
		{"work path", `"manifest at /work/app/latere.yaml is invalid"`, "file path"},
		{"home path", `"token at ~/.config/latere/token expired"`, "file path"},
		{"go source position", `"panic at handler.go:42"`, "file path"},
		{"go file", `"see internal/api/deploy.go"`, "file path"},
		{"a file the user edits", `"Add runtime: go to latere.yaml, then deploy again."`, ""},
		{"the canonical user sentence", `"This project already has a deploy running. Wait for it or pass --supersede to replace it."`, ""},
		{"a format verb", `"Deploy %s is still running."`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := module(t, map[string]string{
				"internal/api/api.go": apiPkg,
				"internal/handler/h.go": "package handler\n\nimport (\n\t\"net/http\"\n\n\t\"example.com/app/internal/api\"\n)\n\n" +
					"func H(w http.ResponseWriter) { api.WriteError(w, \"c\", " + c.lit + ") }\n",
			})
			out, err := run(t, surfaces("internal/api.WriteError"), root)
			if c.want == "" {
				if err != nil {
					t.Fatalf("%s must pass: %v\n%s", c.lit, err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s must be flagged\n%s", c.lit, out)
			}
			if !strings.Contains(out, "internal/handler/h.go:9: "+c.want) {
				t.Errorf("finding must name the file, line and tell:\n%s", out)
			}
			if !strings.Contains(out, "passed to internal/api.WriteError") {
				t.Errorf("finding must name the surface:\n%s", out)
			}
		})
	}
}

// A literal reaches the surface through Sprintf, concatenation, or a
// renamed import, and each is read the same.
func TestLiteralsAreFoundThroughIndirection(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg,
		"internal/handler/h.go": `package handler

import (
	"fmt"
	"net/http"

	errs "example.com/app/internal/api"
)

func H(w http.ResponseWriter, name string) {
	errs.WriteError(w, "c", fmt.Sprintf("deploy %s: store.Deploy missing", name))
	errs.WriteError(w, "c", "cannot apply "+"Deployment insula-p-1/api")
}
`,
	})
	out, err := run(t, surfaces("internal/api.WriteError"), root)
	if err == nil || !strings.Contains(err.Error(), "2 developer sentence(s)") {
		t.Fatalf("both literals must be flagged: %v\n%s", err, out)
	}
	if !strings.Contains(out, "h.go:11:") || !strings.Contains(out, "h.go:12:") {
		t.Errorf("each finding carries its own line:\n%s", out)
	}
}

// An unqualified call from inside the surface's own package is the surface
// too, and a call to a same-named function in another package is not.
func TestUnqualifiedCallsMatchOnlyInTheSurfacePackage(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg + "\nfunc Deploy(w http.ResponseWriter) { WriteError(w, \"c\", \"see pgx.ErrNoRows\") }\n",
		"internal/other/o.go": "package other\n\nfunc WriteError(a, b, c string) {}\n\nfunc F() { WriteError(\"\", \"\", \"store.Deploy leaked\") }\n",
	})
	out, err := run(t, surfaces("internal/api.WriteError"), root)
	if err == nil {
		t.Fatalf("the unqualified call inside internal/api must be flagged\n%s", out)
	}
	if strings.Contains(out, "internal/other") {
		t.Errorf("a same-named function in another package is not the surface:\n%s", out)
	}
	if !strings.Contains(out, "internal/api/api.go:") {
		t.Errorf("the in-package call must be reported:\n%s", out)
	}
}

// A surface may be named by its full import path, including one outside the
// module.
func TestAFullImportPathNamesASurface(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg,
		"cmd/app/main.go": `package main

import (
	"net/http"

	"example.com/app/internal/api"
)

func main() { api.WriteError(nil, "c", "Deployment web-1/api is gone"); _ = http.StatusOK }
`,
	})
	out, err := run(t, surfaces("example.com/app/internal/api.WriteError"), root)
	if err == nil || !strings.Contains(out, "passed to example.com/app/internal/api.WriteError") {
		t.Fatalf("full path must resolve to the same calls: %v\n%s", err, out)
	}
}

// A clean tree passes and says how much it read, so a pass over nothing is
// distinguishable from a pass.
func TestACleanTreePassesAndCountsCalls(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go": apiPkg,
		"internal/handler/h.go": `package handler

import (
	"net/http"

	"example.com/app/internal/api"
)

func H(w http.ResponseWriter) {
	api.WriteError(w, "deploy_in_progress", "This project already has a deploy running. Wait for it or pass --supersede to replace it.")
	api.WriteError(w, "forbidden", "You do not have permission to delete this project.")
}
`,
		"internal/handler/h_test.go": "package handler\n\nimport \"example.com/app/internal/api\"\n\nfunc t() { api.WriteError(nil, \"c\", \"store.Deploy in a test is not a user surface\") }\n",
		"testdata/x.go":              "package x\n\nfunc f() { WriteError(\"store.Deploy\") }\n",
	})
	out, err := run(t, surfaces("internal/api.WriteError"), root)
	if err != nil {
		t.Fatalf("clean tree must pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "across 2 call(s)") {
		t.Errorf("the pass names how many calls it read:\n%s", out)
	}
}

// A surface nothing calls is a typo, and a typo that disables a check must
// fail rather than pass over nothing.
func TestASurfaceNothingCallsFails(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go":   apiPkg,
		"internal/handler/h.go": "package handler\n\nimport \"example.com/app/internal/api\"\n\nfunc H() { api.WriteError(nil, \"c\", \"Wait for it.\") }\n",
	})
	out, err := run(t, surfaces("internal/api.WriteError", "internal/api.WriteErorr"), root)
	if err == nil || !strings.Contains(err.Error(), "internal/api.WriteErorr, which no Go file calls") {
		t.Fatalf("an uncalled surface must fail naming it: %v\n%s", err, out)
	}
	if strings.Contains(err.Error(), "internal/api.WriteError,") {
		t.Errorf("only the uncalled surface is named: %v", err)
	}
}

func TestSkippedDirectoriesAreNotRead(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go":   apiPkg,
		"internal/handler/h.go": "package handler\n\nimport \"example.com/app/internal/api\"\n\nfunc H() { api.WriteError(nil, \"c\", \"Wait for it.\") }\n",
		"e2e/e.go":              "package e2e\n\nimport \"example.com/app/internal/api\"\n\nfunc E() { api.WriteError(nil, \"c\", \"store.Deploy\") }\n",
	})
	if out, err := run(t, surfaces("internal/api.WriteError"), root); err == nil {
		t.Fatalf("without the skip the e2e literal is flagged\n%s", out)
	}
	cfg := config.Registers{UserSurfaces: []string{"internal/api.WriteError"}, Skip: []string{"e2e"}}
	if out, err := run(t, cfg, root); err != nil {
		t.Fatalf("a skipped directory is not read: %v\n%s", err, out)
	}
}

func TestNoSurfaceMeansNothingToCheck(t *testing.T) {
	out, err := run(t, config.Registers{}, t.TempDir())
	if err != nil || !strings.Contains(out, "nothing to check") {
		t.Fatalf("no surface named: %v\n%s", err, out)
	}
}

func TestAModuleWithNoGoFilesFails(t *testing.T) {
	root := module(t, nil)
	if _, err := run(t, surfaces("internal/api.WriteError"), root); err == nil || !strings.Contains(err.Error(), "vacuously") {
		t.Fatalf("a scan that read nothing must fail: %v", err)
	}
}

func TestGoModIsRequiredToResolveSurfaces(t *testing.T) {
	root := t.TempDir()
	if _, err := run(t, surfaces("internal/api.WriteError"), root); err == nil || !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("no go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("go 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, surfaces("internal/api.WriteError"), root); err == nil || !strings.Contains(err.Error(), "module line") {
		t.Fatalf("go.mod without a module line: %v", err)
	}
}

// A file that does not parse is a build error, which another gate reports;
// the rest of the tree is still read.
func TestAnUnparseableFileIsLeftToTheBuild(t *testing.T) {
	root := module(t, map[string]string{
		"internal/api/api.go":   apiPkg,
		"internal/broken/b.go":  "package broken\n\nfunc {",
		"internal/handler/h.go": "package handler\n\nimport \"example.com/app/internal/api\"\n\nfunc H() { api.WriteError(nil, \"c\", \"Wait for it.\") }\n",
	})
	if out, err := run(t, surfaces("internal/api.WriteError"), root); err != nil {
		t.Fatalf("the broken file is skipped: %v\n%s", err, out)
	}
}

func TestAnUnreadableTreeIsAnError(t *testing.T) {
	root := module(t, map[string]string{"internal/api/api.go": apiPkg})
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	if _, err := run(t, surfaces("internal/api.WriteError"), root); err == nil || !strings.Contains(err.Error(), "scanning") {
		t.Fatalf("a directory the walk cannot enter: %v", err)
	}
}
