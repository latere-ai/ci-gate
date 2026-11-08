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
