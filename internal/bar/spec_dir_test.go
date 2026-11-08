// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func TestSpecLintAppliesWhereSpecDirPoints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte("spec:\n  dir: docs/specs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// git tracks docs/specs/001-a.md and nothing under specs/.
	c, _ := ctx(t, cfg, func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "ls-files" {
			if slices.Contains(args, "docs/specs") {
				return []byte("docs/specs/001-a.md\n"), nil
			}
		}
		return nil, nil
	})
	plan, err := Plan(c)
	if err != nil {
		t.Fatal(err)
	}
	if e := entry(plan, "spec-lint"); e.Status != Run {
		t.Fatalf("spec-lint with spec.dir docs/specs tracked by git: %+v", e)
	}
}

func TestAFailureWithNoFirstLineIsStillFAILInTheSummary(t *testing.T) {
	saved := Gates
	t.Cleanup(func() { Gates = saved })
	Gates = []Gate{
		{Name: "quiet", Run: func(Ctx) error { return errors.New("\nthe detail is on the second line") }},
		{Name: "blank", Run: func(Ctx) error { return errors.New("") }},
	}
	c, sb := ctx(t, nil, func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) { return nil, nil })
	err := Check(c)
	if err == nil || !strings.Contains(err.Error(), "2 of 2 gates failed") {
		t.Fatalf("verdict = %v", err)
	}
	out := sb.String()
	for _, name := range []string{"quiet", "blank"} {
		if !strings.Contains(out, "FAIL "+name) || strings.Contains(out, "PASS "+name) {
			t.Errorf("%s is not FAIL in the summary:\n%s", name, out)
		}
	}
}
