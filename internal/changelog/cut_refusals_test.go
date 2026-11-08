// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package changelog

import (
	"os"
	"path/filepath"
	"testing"

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
