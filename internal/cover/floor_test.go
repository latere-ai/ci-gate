// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
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
