// Copyright 2026 Latere AI.
// Licensed under the MIT License.

package golangci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func TestRenderCarriesTheModulePath(t *testing.T) {
	got := Render("github.com/latere-ai/tgo", nil)
	if !strings.Contains(got, "- github.com/latere-ai/tgo") {
		t.Errorf("the module path is the one thing that differs per repo:\n%s", got)
	}
	if !strings.Contains(got, "Do not edit") {
		t.Error("a generated file must say so")
	}
	// A reader who finds the file wrong needs the command, not a hunt.
	if !strings.Contains(got, "lateregate golangci -write") {
		t.Error("the header must say how to regenerate it")
	}
}

// The modernize linter and the toolchain's fixers are the same analyzers, so
// both read one list.
func TestRenderDisablesWhatTheConfigDisables(t *testing.T) {
	got := Render("m", []string{"newexpr", "errorsastype"})
	for _, f := range []string{"- newexpr", "- errorsastype"} {
		if !strings.Contains(got, f) {
			t.Errorf("%s missing:\n%s", f, got)
		}
	}
}

func TestRenderOmitsTheSettingsBlockWhenNothingIsDisabled(t *testing.T) {
	if got := Render("m", nil); strings.Contains(got, "disable:") {
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

func TestWriteThenCheckAgrees(t *testing.T) {
	root := repo(t, nil)
	cfg := &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}
	if _, err := Write(root, cfg, "go"); err != nil {
		t.Fatal(err)
	}
	if err := Check(root, cfg, "go"); err != nil {
		t.Fatalf("a file just written must pass its own check: %v", err)
	}
}

// The failure the whole design exists to make impossible: a config that drifts
// from the shared one and nobody notices.
func TestCheckFailsOnAnEditedFile(t *testing.T) {
	root := repo(t, nil)
	cfg := &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}
	if _, err := Write(root, cfg, "go"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, Name)
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(body, []byte("\n# hand edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Check(root, cfg, "go")
	if err == nil {
		t.Fatal("an edited file must fail the check")
	}
	if !strings.Contains(err.Error(), "-write") {
		t.Errorf("the error must say how to fix it, got %v", err)
	}
}

// Changing the disabled list must move every repository, which is the point of
// a single source.
func TestCheckFailsWhenTheDisabledListChanges(t *testing.T) {
	root := repo(t, nil)
	if _, err := Write(root, &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}, "go"); err != nil {
		t.Fatal(err)
	}
	changed := &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr", "errorsastype"}}}
	if err := Check(root, changed, "go"); err == nil {
		t.Fatal("a changed disable list must fail the check until the file is regenerated")
	}
}

func TestCheckReportsAMissingFile(t *testing.T) {
	err := Check(repo(t, nil), &config.Config{}, "go")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a missing file must say so, got %v", err)
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

func TestCheckReportsAnUnreadableFile(t *testing.T) {
	root := repo(t, nil)
	if err := os.Mkdir(filepath.Join(root, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Check(root, &config.Config{}, "go"); err == nil {
		t.Fatal("a directory in place of the file must be an error")
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
