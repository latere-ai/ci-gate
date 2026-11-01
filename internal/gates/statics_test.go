// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The modules git lists: one per go.mod, none from fixtures or vendored
// trees, and a git that cannot answer is an error rather than no modules.
func TestGoModulesAreTheTrackedGoMods(t *testing.T) {
	var calls []call
	listed := "go.mod\ntools/gen/go.mod\ninternal/x/testdata/mod/go.mod\nvendor/a/go.mod\ndocs/go.mod.md\n"
	dirs, err := GoModules(fake(t, &calls, listed))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirs, []string{".", "tools/gen"}) {
		t.Fatalf("modules %v", dirs)
	}
	if calls[0].name != "git" || !slices.Contains(calls[0].args, "*/go.mod") {
		t.Fatalf("asked %v", calls)
	}
	if _, err := GoModules(fake(t, &calls, errors.New("not a git repository"))); err == nil ||
		!strings.Contains(err.Error(), "cannot list Go modules") {
		t.Fatalf("a git that cannot answer: %v", err)
	}
}

// Every module is checked even after one fails, and the error names the
// untidy ones.
func TestTidyChecksEveryModuleAndNamesTheUntidy(t *testing.T) {
	var calls []call
	var out strings.Builder
	// git's list sorts to a, b, then the root.
	err := Tidy("go", &out, fake(t, &calls, "go.mod\na/go.mod\nb/go.mod\n", errors.New("diff"), nil, nil))
	if err == nil || !strings.Contains(err.Error(), "1 of 3 modules: a") {
		t.Fatalf("Tidy = %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("calls %v", calls)
	}
	if got := strings.Join(calls[1].args, " "); got != "-C a mod tidy -diff" || !calls[1].stream {
		t.Fatalf("tidy call %q stream=%v", got, calls[1].stream)
	}
	calls = nil
	out.Reset()
	if err := Tidy("go", &out, fake(t, &calls, "go.mod\n")); err != nil || !strings.Contains(out.String(), "1 module(s) are tidy") {
		t.Fatalf("a tidy module: %v %q", err, out.String())
	}
	if err := Tidy("go", &out, fake(t, &calls, "")); err == nil {
		t.Fatal("no module passed vacuously")
	}
}

// Only files directly under .github/workflows are workflows, and actionlint
// runs at the pinned version over exactly them, with its machine-dependent
// passes off.
func TestActionlintLintsTheTrackedWorkflowsAtThePin(t *testing.T) {
	var calls []call
	var out strings.Builder
	listed := ".github/workflows/verify.yml\n.github/workflows/release.yaml\n.github/workflows/sub/x.yml\n"
	if err := Actionlint("go", &out, fake(t, &calls, listed, nil)); err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "github.com/rhysd/actionlint/cmd/actionlint@" + ActionlintVersion,
		"-shellcheck=", "-pyflakes=", ".github/workflows/release.yaml", ".github/workflows/verify.yml"}
	if !slices.Equal(calls[1].args, want) {
		t.Fatalf("actionlint args %v", calls[1].args)
	}
	calls = nil
	if err := Actionlint("go", &out, fake(t, &calls, listed, errors.New("exit 1"))); err == nil ||
		!strings.Contains(err.Error(), "found a problem") {
		t.Fatalf("a finding: %v", err)
	}
	if err := Actionlint("go", &out, fake(t, &calls, "")); err == nil {
		t.Fatal("no workflow passed vacuously")
	}
}

// A kustomization is a directory with one of the three file names; a
// Component is rendered only through its includer, so it is left out.
func TestKustomizationsLeaveComponentsOut(t *testing.T) {
	var calls []call
	listed := "deploy/base/kustomization.yaml\ndeploy/prod/kustomization.yml\ndeploy/c/kustomization.yaml\n" +
		"deploy/old/Kustomization\nx/testdata/k/kustomization.yaml\ndocs/kustomization.yaml.md\n"
	files := map[string]string{
		"deploy/base/kustomization.yaml": "resources:\n- a.yaml\n",
		"deploy/prod/kustomization.yml":  "kind: Kustomization\nresources:\n- ../base\n",
		"deploy/c/kustomization.yaml":    "apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: \"Component\"\n",
		"deploy/old/Kustomization":       "resources: []\n",
	}
	read := func(f string) ([]byte, error) {
		b, ok := files[f]
		if !ok {
			return nil, errors.New("unreadable: " + f)
		}
		return []byte(b), nil
	}
	dirs, err := Kustomizations(fake(t, &calls, listed), read)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirs, []string{"deploy/base", "deploy/old", "deploy/prod"}) {
		t.Fatalf("kustomizations %v", dirs)
	}
	if _, err := Kustomizations(fake(t, &calls, "a/kustomization.yaml\n"), read); err == nil {
		t.Fatal("an unreadable kustomization was skipped")
	}
	gone := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if dirs, err := Kustomizations(fake(t, &calls, "a/kustomization.yaml\n"), gone); err != nil || len(dirs) != 0 {
		t.Fatalf("a kustomization deleted in the working tree: %v %v", dirs, err)
	}
}

// Every kustomization is rendered at the pinned kustomize, the output
// thrown away and the errors streamed, and the error names the broken ones.
func TestManifestsRenderEveryKustomization(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/kustomization.yaml", "resources: []\n")
	writeFile(t, root, "b/kustomization.yaml", "resources: []\n")
	var calls []call
	var out strings.Builder
	err := Manifests("go", root, &out, fake(t, &calls, "a/kustomization.yaml\nb/kustomization.yaml\n", errors.New("bad patch"), nil))
	if err == nil || !strings.Contains(err.Error(), "1 of 2 kustomizations: a") {
		t.Fatalf("Manifests = %v", err)
	}
	if got := calls[1].args; got[1] != "sigs.k8s.io/kustomize/kustomize/v5@"+KustomizeVersion || got[2] != "build" || got[3] != "a" || !calls[1].stream {
		t.Fatalf("kustomize call %v", got)
	}
	calls = nil
	if err := Manifests("go", root, &out, fake(t, &calls, "")); err == nil {
		t.Fatal("no kustomization passed vacuously")
	}
}

// writeFile writes body to rel under root, making its directories.
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
