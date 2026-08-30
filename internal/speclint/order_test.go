// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package speclint

import (
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// orderCfg turns on both rules with the vocabulary the other tests use.
func orderCfg() config.Spec {
	c := cfg()
	c.Numbered = true
	c.Started = []string{"complete"}
	c.Settled = []string{"complete"}
	return c
}

func TestAnUnnumberedSpecIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"notes.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| - | [Notes](notes.md) | draft | x |"),
	})
	out, err := run(t, orderCfg(), root)
	if err == nil {
		t.Fatal("a spec with no number cannot be cited and must be reported")
	}
	if !strings.Contains(out, "notes.md: the file name carries no number") {
		t.Errorf("the report must name the file:\n%s", out)
	}
}

// The number is the identifier every citation resolves through. Handing a
// deleted spec's number to a new one silently repoints every citation that
// already exists, and nothing else in the tree notices.
func TestAReusedNumberIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md": spec("draft"),
		"001-b.md": spec("draft"),
		"README.md": index(
			"| 001 | [A](001-a.md) | draft | x |",
			"| 001 | [B](001-b.md) | draft | x |",
		),
	})
	out, err := run(t, orderCfg(), root)
	if err == nil {
		t.Fatal("two specs sharing a number must be reported")
	}
	if !strings.Contains(out, "number 001 is used by 001-a.md, 001-b.md") {
		t.Errorf("the report must name both specs:\n%s", out)
	}
}

func TestNumberingIsOffByDefault(t *testing.T) {
	root := tree(t, map[string]string{
		"notes.md":  spec("draft"),
		"README.md": index("| - | [Notes](notes.md) | draft | x |"),
	})
	if out, err := run(t, cfg(), root); err != nil {
		t.Fatalf("a tree that does not number its specs must pass by default:\n%s", out)
	}
}

// depends_on states an ordering and nothing else checks anyone kept to it. A
// spec built ahead of what it depends on was built against a design that was
// still moving.
func TestASpecBuiltAheadOfItsDependencyIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-base.md": spec("draft"),
		"002-on-top.md": spec("complete",
			"depends_on:", "  - 001-base.md"),
		"README.md": index(
			"| 001 | [Base](001-base.md) | draft | x |",
			"| 002 | [On top](002-on-top.md) | complete | x |",
		),
	})
	out, err := run(t, orderCfg(), root)
	if err == nil {
		t.Fatal("a complete spec depending on a draft one must be reported")
	}
	if !strings.Contains(out, `002-on-top.md: status is complete while its dependency 001-base.md is "draft"`) {
		t.Errorf("the report must name both specs and both statuses:\n%s", out)
	}
}

func TestASettledDependencyDoesNotBlock(t *testing.T) {
	c := orderCfg()
	c.Status = append(c.Status, "superseded")
	c.Settled = []string{"complete", "superseded"}
	root := tree(t, map[string]string{
		"001-base.md": spec("superseded"),
		"002-on-top.md": spec("complete",
			"depends_on:", "  - 001-base.md"),
		"README.md": index(
			"| 001 | [Base](001-base.md) | superseded | x |",
			"| 002 | [On top](002-on-top.md) | complete | x |",
		),
	})
	if out, err := run(t, c, root); err != nil {
		t.Fatalf("a superseded dependency moved its work elsewhere and must not block:\n%s", out)
	}
}

// A spec that has not started may depend on anything: that is what a draft is.
func TestAnUnstartedSpecMayDependOnAnOpenOne(t *testing.T) {
	root := tree(t, map[string]string{
		"001-base.md": spec("draft"),
		"002-on-top.md": spec("draft",
			"depends_on:", "  - 001-base.md"),
		"README.md": index(
			"| 001 | [Base](001-base.md) | draft | x |",
			"| 002 | [On top](002-on-top.md) | draft | x |",
		),
	})
	if out, err := run(t, orderCfg(), root); err != nil {
		t.Fatalf("a draft may depend on a draft:\n%s", out)
	}
}

// The dangling edge is CheckDependencies' finding. Reporting it twice, once
// as a missing spec and once as an unsettled one, buries the actual cause.
func TestACrossRepoDependencyIsNotAReadinessFinding(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md": spec("complete",
			"depends_on:", "  - other/003-elsewhere.md"),
		"README.md": index("| 001 | [A](001-a.md) | complete | x |"),
	})
	if out, err := run(t, orderCfg(), root); err != nil {
		t.Fatalf("an edge into another repository cannot be judged from here:\n%s", out)
	}
}

func TestReadinessIsOffByDefault(t *testing.T) {
	root := tree(t, map[string]string{
		"001-base.md": spec("draft"),
		"002-on-top.md": spec("complete",
			"depends_on:", "  - 001-base.md"),
		"README.md": index(
			"| 001 | [Base](001-base.md) | draft | x |",
			"| 002 | [On top](002-on-top.md) | complete | x |",
		),
	})
	if out, err := run(t, cfg(), root); err != nil {
		t.Fatalf("the point at which work starts is the repository's own decision:\n%s", out)
	}
}
