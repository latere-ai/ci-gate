// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// A stamp over a prerelease marker replaces the whole old version, suffix
// included, in both the v form and the bare form.
func TestStampReplacesAPrereleaseVersionWhole(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, in, version, want string
	}{
		{"v form over an rc", `newTag: v\d+\.\d+\.\d+`, "newTag: v1.3.0-rc.1\n", "v1.3.0", "newTag: v1.3.0\n"},
		{"second rc over a first", `newTag: v\d+\.\d+\.\d+`, "newTag: v1.3.0-rc.1\n", "v1.3.0-rc.2", "newTag: v1.3.0-rc.2\n"},
		{"build metadata", `newTag: v\d+\.\d+\.\d+`, "newTag: v1.3.0+build.5\n", "v1.3.1", "newTag: v1.3.1\n"},
		{"bare form over an rc", `"version": "\d+\.\d+\.\d+`, "\"version\": \"1.3.0-rc.1\"\n", "v1.3.0", "\"version\": \"1.3.0\"\n"},
		{"a closing dot is not the suffix", `see v\d+\.\d+\.\d+\.`, "see v1.2.3.\n", "v1.2.4", "see v1.2.4.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "k.yaml"), []byte(tc.in), 0o644); err != nil {
				t.Fatal(err)
			}
			plans, err := planStamps(root, tc.version, []config.Stamp{{File: "k.yaml", Pattern: tc.pattern}})
			if err != nil {
				t.Fatal(err)
			}
			if got := string(plans[0].content); got != tc.want {
				t.Fatalf("stamping %s over %q gave %q, want %q", tc.version, tc.in, got, tc.want)
			}
		})
	}
}

// A version that is not valid semver is refused before anything is read from
// git, even when no tag on origin exists to compare it against.
func TestReleaseRefusesAVersionThatIsNotSemver(t *testing.T) {
	dir := clone(t)
	for _, version := range []string{"v01.02.03", "v1.02.3", "v1.2.03", "v1.2.3-01"} {
		err := Ready(dir, version, realExec(t, dir))
		if err == nil || !strings.Contains(err.Error(), "is not a valid semantic version") ||
			!strings.Contains(err.Error(), "no leading zeros") {
			t.Errorf("Ready(%s) = %v, want a refusal that says what a valid version looks like", version, err)
		}
	}
}

// A changelog that already has a section for the version is refused before
// anything is written, and the message names the line of that section.
func TestCutRefusesAVersionTheChangelogAlreadyHas(t *testing.T) {
	dir := clone(t)
	body := "# Changelog\n\n## Unreleased\n\n- new\n\n## v1.2.3 - 2026-01-01\n\n- older notes\n"
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-q", "-am", "notes ahead of the tag")
	publish(t, dir)

	err := Cut(dir, "v1.2.3", time.Now(), &strings.Builder{}, realExec(t, dir), nil)
	if err == nil || !strings.Contains(err.Error(), "CHANGELOG.md already has a section for v1.2.3 at line 7") {
		t.Fatalf("Cut() = %v, want the existing section named at line 7", err)
	}
	if got := mustRead(t, dir); got != body {
		t.Errorf("the changelog was written:\n%s", got)
	}
	if strings.Contains(git(t, dir, "tag"), "v1.2.3") {
		t.Error("a refused cut creates no tag")
	}
}
