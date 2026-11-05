// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package actionpins

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	checkoutSHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	otherSHA    = "0123456789abcdef0123456789abcdef01234567"
)

var testList = map[string]Pin{
	"actions/checkout":     {Commit: checkoutSHA, Version: "v7.0.1"},
	"github/codeql-action": {Commit: otherSHA, Version: "v4.39.3"},
}

func TestEmbeddedList(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 29 {
		t.Fatalf("the list holds %d actions, want 29", len(list))
	}
	for k, p := range list {
		if strings.Count(k, "/") != 1 {
			t.Errorf("key %q is not owner/repo", k)
		}
		if !commitRE.MatchString(p.Commit) {
			t.Errorf("%s: commit %q is not 40 hex", k, p.Commit)
		}
		if !versionRE.MatchString(p.Version) {
			t.Errorf("%s: version %q is not vX.Y.Z", k, p.Version)
		}
	}
	if list["github/codeql-action"].Commit == "" {
		t.Error("a sub-path row must key by owner/repo")
	}
}

func TestParseListRejects(t *testing.T) {
	head := "name: x\ndescription: y\nruns:\n  using: composite\n  steps:\n"
	for name, body := range map[string]string{
		"empty":     head[:len(head)-len("  steps:\n")],
		"duplicate": head + "    - uses: a/b@" + checkoutSHA + " # v1.0.0\n    - uses: a/b/c@" + checkoutSHA + " # v1.0.0\n",
		"short":     head + "    - uses: a/b@abc123 # v1.0.0\n",
		"version":   head + "    - uses: a/b@" + checkoutSHA + " # main\n",
	} {
		if _, err := parseList([]byte(body)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func lines(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

func TestCheck(t *testing.T) {
	ok := "actions/checkout@" + checkoutSHA + " # v7.0.1"
	tests := []struct {
		name, file, body string
		want             []string
	}{
		{"listed and right", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: " + ok + "\n", nil},
		{"uses without a dash", "w.yml", "jobs:\n  a:\n    steps:\n      - name: x\n        uses: " + ok + "\n", nil},
		{"quoted", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: \"actions/checkout@" + checkoutSHA + "\" # v7.0.1\n", nil},
		{"sub-path shares the repository", "w.yml",
			"jobs:\n  a:\n    steps:\n      - uses: github/codeql-action/analyze@" + otherSHA + " # v4.39.3\n", nil},
		{"unlisted", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: foo/bar@v1\n",
			[]string{"w.yml:4: foo/bar is not in the organization's list; add it to internal/actionpins/action.yml in latere-ai/ci-gate first"}},
		{"tag", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v4\n",
			[]string{"w.yml:4: actions/checkout is pinned to the tag v4, the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"branch", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@main\n",
			[]string{"w.yml:4: actions/checkout is pinned to the branch or tag main, the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"other commit", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@" + otherSHA + " # v7.0.1\n",
			[]string{"w.yml:4: actions/checkout is pinned to commit " + otherSHA + ", the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"short commit", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@3d3c42e\n",
			[]string{"w.yml:4: actions/checkout is pinned to the short commit 3d3c42e, the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"no ref", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout\n",
			[]string{"w.yml:4: actions/checkout is pinned to no ref, the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"wrong comment", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@" + checkoutSHA + " # v7\n",
			[]string{"w.yml:4: actions/checkout has \"# v7\" after the commit, the list has \"# v7.0.1\""}},
		{"missing comment", "w.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@" + checkoutSHA + "\n",
			[]string{"w.yml:4: actions/checkout has no comment after the commit, the list has \"# v7.0.1\""}},
		{"reusable workflow job", "w.yml", "jobs:\n  a:\n    uses: actions/checkout/.github/workflows/x.yml@v1\n",
			[]string{"w.yml:3: actions/checkout is pinned to the tag v1, the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"composite action", "action.yml", "name: x\nruns:\n  using: composite\n  steps:\n    - uses: actions/checkout@v4\n",
			[]string{"action.yml:5: actions/checkout is pinned to the tag v4, the list has commit " + checkoutSHA + " (v7.0.1)"}},
		{"local docker and latere-ai skipped", "w.yml",
			"jobs:\n  a:\n    uses: Latere-AI/ci/.github/workflows/x.yml@v1\n    steps:\n      - uses: ./local\n      - uses: docker://alpine:3\n      - uses: latere-ai/ci/actions/x@v1\n", nil},
		{"owner and repository in any case", "w.yml",
			"jobs:\n  a:\n    steps:\n      - uses: Actions/Checkout@" + checkoutSHA + " # v7.0.1\n", nil},
		{"with keys are not uses", "w.yml",
			"jobs:\n  a:\n    steps:\n      - run: x\n        with:\n          uses: foo/bar@v1\n", nil},
		{"several in one run", "w.yml",
			"jobs:\n  a:\n    steps:\n      - uses: foo/bar@v1\n      - uses: actions/checkout@v4\n",
			[]string{
				"w.yml:4: foo/bar is not in the organization's list; add it to internal/actionpins/action.yml in latere-ai/ci-gate first",
				"w.yml:5: actions/checkout is pinned to the tag v4, the list has commit " + checkoutSHA + " (v7.0.1)"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Check(testList, tc.file, []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(lines(got), tc.want) {
				t.Errorf("findings\n got %q\nwant %q", lines(got), tc.want)
			}
		})
	}
}

func TestCheckRejectsBrokenYAML(t *testing.T) {
	if _, err := Check(testList, "w.yml", []byte("jobs: [\n")); err == nil {
		t.Fatal("a file that does not parse passed")
	}
}

func TestRunReportsAndNamesTheFix(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".github/workflows/a.yml", "jobs:\n  a:\n    steps:\n      - uses: foo/bar@v1\n")
	var out bytes.Buffer
	err := Run(dir, []string{".github/workflows/a.yml", "gone.yml"}, &out)
	if err == nil || !strings.Contains(err.Error(), ".github/workflows/a.yml:4: foo/bar is not in the organization's list") ||
		!strings.Contains(err.Error(), FixCommand) || !strings.Contains(err.Error(), "needs it added") {
		t.Fatalf("Run = %v", err)
	}
}

func TestWriteKeepsFormatting(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	co := list["actions/checkout"]
	body := `name: ci
jobs:
  a:
    steps:
      - uses: actions/checkout@v4
      - name: go
        uses: "actions/setup-go@v5"   # latest
      - uses: 'actions/setup-node@abc1234'
        with:
          node-version: 24
      - uses: foo/bar@v1 # keep
      - uses: actions/upload-artifact@` + strings.Repeat("a", 40) + ` # old note
      - uses: ./local
      - uses: actions/download-artifact/restore@main
`
	dir := t.TempDir()
	p := filepath.Join(dir, "w.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Write(dir, []string{"w.yml"}, &out)
	if err == nil || !strings.Contains(err.Error(), "w.yml:11: foo/bar is not in the organization's list") {
		t.Fatalf("Write = %v, want the unlisted action reported", err)
	}
	if strings.Contains(err.Error(), "w.yml:5") || strings.Contains(err.Error(), "w.yml:12") {
		t.Errorf("a fixed action is still reported: %v", err)
	}
	if out.String() != "wrote w.yml\n" {
		t.Errorf("printed %q", out.String())
	}
	got, _ := os.ReadFile(p)
	sg, sn, ua, dl := list["actions/setup-go"], list["actions/setup-node"], list["actions/upload-artifact"], list["actions/download-artifact"]
	want := `name: ci
jobs:
  a:
    steps:
      - uses: actions/checkout@` + co.Commit + ` # ` + co.Version + `
      - name: go
        uses: "actions/setup-go@` + sg.Commit + `" # ` + sg.Version + `
      - uses: 'actions/setup-node@` + sn.Commit + `' # ` + sn.Version + `
        with:
          node-version: 24
      - uses: foo/bar@v1 # keep
      - uses: actions/upload-artifact@` + ua.Commit + ` # ` + ua.Version + `
      - uses: ./local
      - uses: actions/download-artifact/restore@` + dl.Commit + ` # ` + dl.Version + `
`
	if string(got) != want {
		t.Errorf("rewritten file\n%s\nwant\n%s", got, want)
	}
	// A second run changes nothing.
	out.Reset()
	_ = Write(dir, []string{"w.yml"}, &out)
	if out.String() != "" {
		t.Errorf("second run printed %q", out.String())
	}
}

func TestWriteCleanTreeSucceeds(t *testing.T) {
	list, _ := List()
	co := list["actions/checkout"]
	dir := t.TempDir()
	body := "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v1\n"
	if err := os.WriteFile(filepath.Join(dir, "w.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Write(dir, []string{"w.yml"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := Run(dir, []string{"w.yml"}, &out); err != nil {
		t.Fatalf("after -w the gate still fails: %v (%s)", err, co.Commit)
	}
}
