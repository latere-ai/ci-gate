// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package speclint

import (
	"strings"
	"testing"
)

// An alias or a heading on a wikilink names the same spec, so the link
// resolves; one to a spec that is not there still does not.
func TestWikilinksWithAnAliasOrHeadingResolve(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md": spec("draft") + "\nSee [[002-b|the b spec]], [[002-b#design]], [[002-b.md#design|b]]," +
			" [[#local]] and [[003-missing|gone]].\n",
		"002-b.md":  spec("draft"),
		"README.md": index("| 1 | [a](001-a.md) | draft | x |", "| 2 | [b](002-b.md) | draft | x |"),
	})
	c := cfg()
	c.Wikilinks = true
	out, err := run(t, c, root)
	if err == nil {
		t.Fatal("a wikilink to a missing spec passed")
	}
	if !strings.Contains(out, "[[003-missing]] resolves to nothing") || strings.Count(out, "resolves to nothing") != 1 {
		t.Errorf("only the missing target is reported:\n%s", out)
	}
}

// With no status vocabulary the index cell is still compared with the spec's
// status, ignoring case: a different word is drift, a different spelling is not.
func TestWithoutAVocabularyTheIndexStatusIsComparedIgnoringCase(t *testing.T) {
	c := cfg()
	c.Status = nil
	same := tree(t, map[string]string{
		"001-a.md":  spec("complete"),
		"README.md": index("| 1 | [a](001-a.md) | Complete | x |"),
	})
	if out, err := run(t, c, same); err != nil {
		t.Fatalf("Complete over complete should agree: %v\n%s", err, out)
	}
	differ := tree(t, map[string]string{
		"001-a.md":  spec("complete"),
		"README.md": index("| 1 | [a](001-a.md) | draft | x |"),
	})
	out, err := run(t, c, differ)
	if err == nil || !strings.Contains(out, `row says 001-a.md is "draft"; the spec says "complete"`) {
		t.Fatalf("draft over complete must fail (%v):\n%s", err, out)
	}
}

// With a vocabulary the comparison stays exact.
func TestWithAVocabularyTheIndexStatusIsComparedExactly(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("complete"),
		"README.md": index("| 1 | [a](001-a.md) | Complete | x |"),
	})
	if _, err := run(t, cfg(), root); err == nil {
		t.Fatal("Complete over complete passed with a vocabulary")
	}
}
