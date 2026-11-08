// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadBody(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

func TestAnExemptionMatchesWholePathElementsOnly(t *testing.T) {
	c := Cover{Exempt: map[string]string{"internal/api": "talks to a real server"}}
	for _, other := range []string{
		"example.com/m/internal/otherapi",
		"example.com/m/internal/legacyapi",
	} {
		if _, ok := c.ExemptFor(other); ok {
			t.Errorf("exempt \"internal/api\" also exempts %s", other)
		}
	}
	for _, same := range []string{"internal/api", "example.com/m/internal/api"} {
		if _, ok := c.ExemptFor(same); !ok {
			t.Errorf("exempt \"internal/api\" does not exempt %s", same)
		}
	}
	c = Cover{Exempt: map[string]string{"api": "x"}}
	if _, ok := c.ExemptFor("example.com/m/internal/grapi"); ok {
		t.Error("exempt \"api\" also exempts internal/grapi")
	}
	if _, ok := c.ExemptFor("example.com/m/internal/api"); !ok {
		t.Error("exempt \"api\" does not exempt internal/api")
	}
}

func TestANestedModulePackageIsExemptedByItsPlaceBelowTheRoot(t *testing.T) {
	c := Cover{Module: "example.com/gen", Dir: "tools/gen", Exempt: map[string]string{"tools/gen/cmd/x": "generator"}}
	if _, ok := c.ExemptFor("example.com/gen/cmd/x"); !ok {
		t.Error("a nested module's package is not exempted by its path below the root")
	}
}

func TestABlankExemptionOrAllowanceKeyIsRefused(t *testing.T) {
	for _, tc := range []struct{ body, field string }{
		{"cover:\n  exempt:\n    '': because\n", "cover.exempt"},
		{"cover:\n  exempt:\n    '  ': because\n", "cover.exempt"},
		{"tempdir:\n  allow:\n    '': because\n", "tempdir.allow"},
		{"tempdir:\n  allow:\n    ' ': because\n", "tempdir.allow"},
	} {
		_, err := loadBody(t, "identity:\n  role: none\n"+tc.body)
		if err == nil || !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), "empty key") {
			t.Errorf("%q: error %v does not name %s and the empty key", tc.body, err, tc.field)
		}
	}
}

func TestATempdirAllowanceStillMatchesByPrefix(t *testing.T) {
	td := TempDir{Allow: map[string]string{"go-build": "kept on purpose"}}
	if _, ok := td.AllowedFor("go-build123456"); !ok {
		t.Error("a prefix entry no longer admits a name with a random suffix")
	}
	if _, ok := td.AllowedFor("other"); ok {
		t.Error("an unrelated name is admitted")
	}
}

func TestABlankTempdirKeyBuiltInMemoryAdmitsNothing(t *testing.T) {
	td := TempDir{Allow: map[string]string{"": "why"}}
	if _, ok := td.AllowedFor("leaked-123"); ok {
		t.Error("an empty tempdir.allow key admits every survivor")
	}
}
