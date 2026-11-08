// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package golangci

import (
	"strings"
	"testing"
)

func renderExtra(t *testing.T, extra map[string]any) (string, error) {
	t.Helper()
	return Render("example.com/m", nil, nil, extra)
}

func TestExtraThatRemovesFromTheSharedBarIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		names []string
	}{
		{"disabled linters", map[string]any{"linters": map[string]any{
			"disable": []any{"errcheck", "staticcheck"},
		}}, []string{"linters.disable", "errcheck", "staticcheck"}},
		{"an emptied deny list", map[string]any{"linters": map[string]any{
			"settings": map[string]any{"depguard": map[string]any{"rules": map[string]any{
				"stdlib": map[string]any{"deny": []any{}},
			}}},
		}}, []string{"depguard.rules.stdlib.deny", "io/ioutil", "encoding/json"}},
		{"replaced exclusion rules", map[string]any{"linters": map[string]any{
			"exclusions": map[string]any{"rules": []any{
				map[string]any{"path": `gen\.go`, "linters": []any{"errcheck"}},
			}},
		}}, []string{"linters.exclusions.rules", `_test\.go`}},
		{"a check switched off", map[string]any{"linters": map[string]any{
			"settings": map[string]any{"errcheck": map[string]any{"check-type-assertions": false}},
		}}, []string{"errcheck.check-type-assertions"}},
		{"a staticcheck check dropped", map[string]any{"linters": map[string]any{
			"settings": map[string]any{"staticcheck": map[string]any{"checks": []any{"all", "-SA1000"}}},
		}}, []string{"staticcheck.checks", "-SA1000"}},
		{"another govet analyzer switched off", map[string]any{"linters": map[string]any{
			"settings": map[string]any{"govet": map[string]any{"disable": []any{"fieldalignment", "shadow", "printf"}}},
		}}, []string{"govet.disable", "printf"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := renderExtra(t, tc.extra)
			if err == nil {
				t.Fatal("extra that takes from the shared bar renders")
			}
			for _, want := range tc.names {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not name %q:\n%v", want, err)
				}
			}
		})
	}
}

func TestExtraThatOnlyAddsIsAccepted(t *testing.T) {
	got, err := renderExtra(t, map[string]any{"linters": map[string]any{
		"enable": []any{"gosec"},
		"settings": map[string]any{
			"errcheck": map[string]any{"check-type-assertions": true},
			"staticcheck": map[string]any{"checks": []any{
				"all", "-ST1000", "-ST1003", "-ST1016", "-ST1020", "-ST1021", "-ST1022",
			}},
			"depguard": map[string]any{"rules": map[string]any{
				"below-transport": map[string]any{"files": []any{"**/internal/store/**"},
					"deny": []any{map[string]any{"pkg": "net/http", "desc": "below transport"}}},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "below-transport") || !strings.Contains(got, "io/ioutil") {
		t.Fatalf("the addition or the shared bar is missing:\n%s", got)
	}
}

func TestExtraMayMakeTheBarStricter(t *testing.T) {
	_, err := renderExtra(t, map[string]any{"linters": map[string]any{
		"settings": map[string]any{
			"staticcheck": map[string]any{"checks": []any{"all"}},
			"govet":       map[string]any{"disable": []any{"shadow", "fieldalignment"}},
		},
	}})
	if err != nil {
		t.Fatalf("a stricter extra is refused: %v", err)
	}
}
