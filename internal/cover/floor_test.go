// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
	"slices"
	"strings"
	"testing"
)

// A package just under the floor never prints as the floor itself: the
// figure is cut off at one decimal, not rounded up.
func TestAFailingFigureNeverReadsAsTheFloor(t *testing.T) {
	p := profile(t,
		mod+"p/a.go:1.1,2.1 8996 1",
		mod+"p/a.go:3.1,4.1 1004 0",
	)
	out, err := run(t, cfg(), p)
	if err == nil {
		t.Fatal("89.96% passed a 90% floor")
	}
	if strings.Contains(out, "90.0%") || !strings.Contains(out, "89.9%") {
		t.Errorf("a failing figure must print as 89.9%%:\n%s", out)
	}
	if !strings.Contains(err.Error(), "89.9%") {
		t.Errorf("the error repeats the figure: %v", err)
	}
}

func TestTruncatedKeepsExactTenths(t *testing.T) {
	for _, tc := range []struct {
		covered, total int
		want           string
	}{
		{897, 1000, "89.7"}, {9, 10, "90.0"}, {1, 1, "100.0"}, {0, 5, "0.0"}, {2, 3, "66.6"},
	} {
		if got := truncated(tc.covered, tc.total); got != tc.want {
			t.Errorf("%d/%d printed %s, want %s", tc.covered, tc.total, got, tc.want)
		}
	}
}

// A package whose only code is a function literal in a package-level var is
// instrumented, so it is listed and can be reported as unmeasured.
func TestGoListerKeepsAPackageWhoseCodeIsAFuncLiteral(t *testing.T) {
	root := module(t, map[string]string{
		"logic/logic.go": "package logic\n\nfunc Add(a, b int) int { return a + b }\n",
		"hooks/hooks.go": "package hooks\n\nvar Handler = func(a int) int {\n\tb := a * 2\n\treturn b\n}\n",
		"decl/decl.go":   "package decl\n\nvar Empty = func() {}\n\nconst C = 1\n",
	})
	got, err := GoLister("go", root)()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "example.com/m/hooks") {
		t.Errorf("listed %v: hooks has statements and was left out", got)
	}
	if slices.Contains(got, "example.com/m/decl") {
		t.Errorf("listed %v: decl has no statements", got)
	}
}
