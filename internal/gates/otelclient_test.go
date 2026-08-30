// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGo puts src at rel under a fresh root and returns the root.
func writeGo(t *testing.T, rel, src string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestOtelClient covers every shape the previous text-matching checks
// disagreed about. Each case names what the gate must decide and why.
func TestOtelClient(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool // true when the gate must flag the file
	}{{
		name: "bare literal on one line",
		src:  "package p\nimport \"net/http\"\nvar c = &http.Client{}\n",
		want: true,
	}, {
		name: "bare literal spanning lines",
		src:  "package p\nimport (\n\t\"net/http\"\n\t\"time\"\n)\n\nvar c = &http.Client{\n\tTimeout: 5 * time.Second,\n}\n",
		want: true,
	}, {
		name: "instrumented on one line",
		src:  "package p\nimport \"net/http\"\nfunc t() http.RoundTripper { return nil }\nvar c = &http.Client{Transport: t()}\n",
		want: false,
	}, {
		// The defect in the line-oriented check: a Transport field below the
		// opening brace was reported as missing.
		name: "transport several lines below the brace",
		src:  "package p\nimport (\n\t\"net/http\"\n\t\"time\"\n)\n\nfunc t() http.RoundTripper { return nil }\n\nvar c = &http.Client{\n\t// a comment in between\n\tTimeout: 5 * time.Second,\n\n\tTransport: t(),\n}\n",
		want: false,
	}, {
		// The defect a substring test cannot see: Transport appears, but as
		// part of a value, so no transport is actually set.
		name: "transport named only in a value",
		src:  "package p\nimport \"net/http\"\ntype cfg struct{ Transport struct{ Timeout int } }\nvar c1 cfg\nvar c = &http.Client{Timeout: 0}\nvar _ = c1\n",
		want: true,
	}, {
		name: "http.DefaultClient used",
		src:  "package p\nimport \"net/http\"\nfunc f(r *http.Request) { _, _ = http.DefaultClient.Do(r) }\n",
		want: true,
	}, {
		name: "comment mentioning both shapes",
		src:  "package p\n\n// Never write &http.Client{} and never reach for http.DefaultClient.\n/* &http.Client{} and http.DefaultClient again */\nimport \"net/http\"\n\nvar _ = http.MethodGet\n",
		want: false,
	}, {
		name: "string literal mentioning both shapes",
		src:  "package p\nimport \"net/http\"\nvar msg = \"do not use &http.Client{} or http.DefaultClient\"\nvar _ = http.MethodGet\n",
		want: false,
	}, {
		name: "renamed net/http import is still checked",
		src:  "package p\nimport nethttp \"net/http\"\nvar c = &nethttp.Client{}\n",
		want: true,
	}, {
		// A different package exposing a Client type is not net/http.
		name: "unrelated package Client literal",
		src:  "package p\ntype thing struct{ Timeout int }\nvar http = struct{ Client func() thing }{}\nvar _ = http\n",
		want: false,
	}, {
		name: "no net/http import at all",
		src:  "package p\nvar x = 1\n",
		want: false,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeGo(t, "a.go", tc.src)
			var buf bytes.Buffer
			err := OtelClient(root, &buf, nil)
			if tc.want && err == nil {
				t.Errorf("gate passed; want a violation.\noutput: %s", buf.String())
			}
			if !tc.want && err != nil {
				t.Errorf("gate failed: %v\noutput: %s", err, buf.String())
			}
		})
	}
}

// TestOtelClient_IgnoresTestFiles keeps the gate off code that dials an
// httptest server, where there is no trace to continue.
func TestOtelClient_IgnoresTestFiles(t *testing.T) {
	root := writeGo(t, "a_test.go", "package p\nimport \"net/http\"\nvar c = &http.Client{}\nvar _ = http.DefaultClient\n")
	// A non-test file must exist or the scan is vacuous by design.
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := OtelClient(root, &buf, nil); err != nil {
		t.Errorf("gate flagged a _test.go file: %v", err)
	}
}

// TestOtelClient_VacuousScanFails is the property that makes every other case
// meaningful. A gate that reads nothing must not report success.
func TestOtelClient_VacuousScanFails(t *testing.T) {
	var buf bytes.Buffer
	err := OtelClient(t.TempDir(), &buf, nil)
	if err == nil {
		t.Fatal("gate passed on a tree with no Go files")
	}
	if !strings.Contains(err.Error(), "vacuously") {
		t.Errorf("err = %v, want it to name the vacuous scan", err)
	}
}

// TestOtelClient_SkipsNamedDirectories covers the escape hatch a build-tagged
// e2e harness needs.
func TestOtelClient_SkipsNamedDirectories(t *testing.T) {
	root := writeGo(t, "e2e/a.go", "package e2e\nimport \"net/http\"\nvar c = &http.Client{}\n")
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := OtelClient(root, &buf, nil); err == nil {
		t.Fatal("gate passed without the skip; the fixture is wrong")
	}

	buf.Reset()
	if err := OtelClient(root, &buf, []string{"e2e"}); err != nil {
		t.Errorf("gate flagged a skipped directory: %v", err)
	}
}

// TestOtelClient_ReportsFileAndLine keeps the output actionable.
func TestOtelClient_ReportsFileAndLine(t *testing.T) {
	root := writeGo(t, "pkg/a.go", "package p\nimport \"net/http\"\n\nvar c = &http.Client{}\n")
	var buf bytes.Buffer
	if err := OtelClient(root, &buf, nil); err == nil {
		t.Fatal("gate passed; want a violation")
	}
	got := buf.String()
	if !strings.Contains(got, filepath.Join("pkg", "a.go")+":4") {
		t.Errorf("output %q does not name pkg/a.go:4", got)
	}
}

// TestOtelClient_UnparseableFileDoesNotFailTheGate keeps the error the
// compiler reports better from surfacing here as a telemetry complaint.
func TestOtelClient_UnparseableFileDoesNotFailTheGate(t *testing.T) {
	root := writeGo(t, "a.go", "package p\nthis is not go\n")
	var buf bytes.Buffer
	if err := OtelClient(root, &buf, nil); err != nil {
		t.Errorf("gate failed on a file the parser rejected: %v", err)
	}
}

// TestOtelClient_SkipsAgentWorktrees covers .claude, which holds whole scratch
// copies of the repository. Walking one reports every file twice and can
// resurrect a violation already fixed on the branch being scanned.
func TestOtelClient_SkipsAgentWorktrees(t *testing.T) {
	root := writeGo(t, ".claude/worktrees/wip/a.go", "package p\nimport \"net/http\"\nvar c = &http.Client{}\n")
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := OtelClient(root, &buf, nil); err != nil {
		t.Errorf("gate walked into .claude: %v\n%s", err, buf.String())
	}
}
