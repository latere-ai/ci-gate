// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package golangci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func TestRenderCarriesTheModulePath(t *testing.T) {
	got := Render("github.com/latere-ai/tgo", nil, nil)
	if !strings.Contains(got, "- github.com/latere-ai/tgo") {
		t.Errorf("the module path is the one thing that differs per repo:\n%s", got)
	}
	if !strings.Contains(got, "Do not edit") {
		t.Error("a generated file must say so")
	}
	// A reader who finds the file wrong needs to know it is generated, that
	// committing it is a mistake, and where the real source is.
	for _, want := range []string{"do not commit", "gitignored", "ci-gate", ".lateregate.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("the header must mention %q:\n%s", want, got)
		}
	}
}

// The modernize linter and the toolchain's fixers are the same analyzers, so
// both read one list.
func TestRenderDisablesWhatTheConfigDisables(t *testing.T) {
	got := Render("m", []string{"newexpr", "errorsastype"}, nil)
	for _, f := range []string{"- newexpr", "- errorsastype"} {
		if !strings.Contains(got, f) {
			t.Errorf("%s missing:\n%s", f, got)
		}
	}
}

func TestRenderOmitsTheSettingsBlockWhenNothingIsDisabled(t *testing.T) {
	if got := Render("m", nil, nil); strings.Contains(got, "disable:") {
		t.Errorf("an empty list should produce no disable block:\n%s", got)
	}
}

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Regenerating on every run is what makes drift impossible: a hand edit does
// not survive the next Write, so there is nothing to detect.
func TestWriteOverwritesAHandEdit(t *testing.T) {
	root := repo(t, nil)
	cfg := &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}
	if _, err := Write(root, cfg, "go"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, Name)
	first, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte("version: \"2\"\n# hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, cfg, "go"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(first) {
		t.Error("a hand edit must not survive regeneration")
	}
	if strings.Contains(string(again), "hand edit") {
		t.Error("the edit is still there")
	}
}

// Changing the disabled list moves every repository on the next run, which is
// the point of one source.
func TestWriteFollowsTheDisabledList(t *testing.T) {
	root := repo(t, nil)
	if _, err := Write(root, &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}, "go"); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr", "errorsastype"}}}, "go"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, Name))
	if !strings.Contains(string(got), "errorsastype") {
		t.Errorf("the second render should carry the new list:\n%s", got)
	}
}

// A generated file that is also committed is two sources of truth that agree
// until one is edited, which is the shape this design exists to avoid.
func TestWriteRefusesATrackedFile(t *testing.T) {
	root := repo(t, nil)
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"add", ".golangci.yml"},
	} {
		if len(args) == 2 && args[0] == "add" {
			if err := os.WriteFile(filepath.Join(root, Name), []byte("version: \"2\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	_, err := Write(root, &config.Config{}, "go")
	if err == nil {
		t.Fatal("writing over a tracked file must fail")
	}
	for _, want := range []string{"tracked by git", "gitignored", "git rm --cached"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should say %q, got %v", want, err)
		}
	}
}

func TestAMissingModuleIsAnError(t *testing.T) {
	if _, err := ModulePath("go", t.TempDir()); err == nil {
		t.Fatal("a directory with no module must be an error")
	}
	if _, err := ModulePath("no-such-go-binary", "."); err == nil {
		t.Fatal("a missing toolchain must be an error")
	}
}

func TestWriteReportsAnUnwritablePath(t *testing.T) {
	root := repo(t, nil)
	if err := os.Mkdir(filepath.Join(root, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, &config.Config{}, "go"); err == nil {
		t.Fatal("an unwritable path must be an error")
	}
}

// The set is the point: errcheck across four repositories surfaced 452
// discarded errors and several real bugs, so a render that quietly loses a
// linter would undo that.
func TestRenderCarriesTheWholeLinterSet(t *testing.T) {
	got := Render("m", nil, nil)
	for _, linter := range []string{"errcheck", "govet", "ineffassign", "staticcheck", "unused", "modernize", "depguard"} {
		if !strings.Contains(got, "- "+linter) {
			t.Errorf("%s missing from the rendered set:\n%s", linter, got)
		}
	}
	if !strings.Contains(got, "default: none") {
		t.Error("the set must be explicit, not inherited from a golangci-lint default that can move")
	}
}

// depguard's settings must survive even when no modernize fixer is disabled,
// because the two share the settings block.
func TestTheTestifyBanSurvivesAnEmptyDisableList(t *testing.T) {
	got := Render("m", nil, nil)
	if !strings.Contains(got, "no-testify") || !strings.Contains(got, "stretchr/testify") {
		t.Errorf("the testify ban is org policy, not per repo:\n%s", got)
	}
}

// A repository with real policy of its own keeps its config, and says so. The
// alternative is an exception nobody declared, which reads the same as one
// nobody got around to.
func TestAnOwnedConfigIsReportedAndNotOverwritten(t *testing.T) {
	root := repo(t, map[string]string{Name: "version: \"2\"\n# hand written\n"})
	cfg := &config.Config{Golangci: config.Golangci{Own: "a 50-word spelling policy"}}
	reason, err := Own(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "a 50-word spelling policy" {
		t.Errorf("reason = %q", reason)
	}
	body, _ := os.ReadFile(filepath.Join(root, Name))
	if !strings.Contains(string(body), "hand written") {
		t.Error("an owned config must not be touched")
	}
}

// A declared exception pointing at nothing is a repository that lost its
// config and did not notice.
func TestAnOwnedConfigThatIsMissingFails(t *testing.T) {
	cfg := &config.Config{Golangci: config.Golangci{Own: "reasons"}}
	if _, err := Own(repo(t, nil), cfg); err == nil {
		t.Fatal("claiming to own a file that does not exist must fail")
	}
}

func TestNoClaimMeansGenerate(t *testing.T) {
	reason, err := Own(repo(t, nil), &config.Config{})
	if err != nil || reason != "" {
		t.Errorf("Own = %q, %v; want the generate path", reason, err)
	}
}

// The scope is the whole reason sloglint is config rather than template: every
// repository's request path is its own.
func TestSloglintIsRenderedWithItsScope(t *testing.T) {
	got := Render("m", nil, &config.Sloglint{
		Context: "all", RequestPaths: "internal/(handler|server)/",
	})
	for _, want := range []string{
		"- sloglint",
		"- path-except: internal/(handler|server)/",
		"context: all",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// sandbox exempts a sweeper and a store that live under its request path but
// do not serve requests, so a second exclusion has to survive.
func TestSloglintCarriesFurtherExemptPaths(t *testing.T) {
	got := Render("m", nil, &config.Sloglint{
		Context:      "scope",
		RequestPaths: "internal/http/(web|api)/",
		Exempt:       []string{`internal/http/api/(runs_sweeper|runs_store)\.go`},
	})
	if !strings.Contains(got, `- path: internal/http/api/(runs_sweeper|runs_store)\.go`) {
		t.Errorf("the exempt path is missing:\n%s", got)
	}
	if !strings.Contains(got, "context: scope") {
		t.Errorf("context not carried:\n%s", got)
	}
}

// A repository that does not serve requests gets no sloglint and no empty
// exclusions block, which golangci-lint would reject.
func TestNoSloglintMeansNoExclusionsBlock(t *testing.T) {
	got := Render("m", nil, nil)
	for _, unwanted := range []string{"sloglint", "exclusions:", "path-except:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unconfigured sloglint should emit no %q:\n%s", unwanted, got)
		}
	}
}
