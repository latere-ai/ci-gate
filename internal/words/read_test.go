// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// texts is each segment as line:text, with the blanked runs collapsed, so a
// case reads as what a reader reads.
func texts(segs []segment) []string {
	var out []string
	for _, s := range segs {
		t := strings.Join(strings.Fields(s.text), " ")
		if t == "" {
			continue
		}
		out = append(out, strconv.Itoa(s.line)+":"+t)
	}
	return out
}

// A source file is read for its strings: keys, comments and code are not
// what anyone reads, and neither is a placeholder, a template action, an
// escape or a template literal's code.
func TestASourceFileIsReadForItsStrings(t *testing.T) {
	for _, c := range []struct {
		name, file, src string
		want            []string
	}{
		{"catalog", "en.ts", `// a comment: 'not read'
/* a block
   'not read' */ export const en = {
  'key.one': 'First {name} line.',
  "key.two": "Second\nline \"quoted\".",
  key3: 'Third',
  'key.four':
    'Fourth',
  'a': 'x', 'b': 'Fifth',
}`, []string{"4:First line.", "5:Second line quoted .", "6:Third", "8:Fourth", "9:x", "9:Fifth"}},
		{"ternary", "a.ts", `const s = ok ? 'Yes' : 'No'`, []string{"1:Yes", "1:No"}},
		{"switch", "a.go", "switch s {\ncase \"spawn\":\n}", []string{"2:spawn"}},
		{"template", "a.ts", "const s = `Hello ${user.name({a: 1})}\nand ${n} more`", []string{"1:Hello", "2:and more"}},
		{"raw Go string", "a.go", "const s = `C:\\path ${not code}\nsecond`", []string{`1:C:\path ${not code}`, "2:second"}},
		{"continued line", "a.js", "const s = 'one \\\ntwo'\nconst t = 'three'", []string{"1:one", "2:two", "3:three"}},
		{"unterminated", "a.ts", "const s = 'open\nconst t = 'closed'", []string{"1:open", "2:closed"}},
		{"unterminated comment", "a.ts", "'a' /* open", []string{"1:a"}},
		{"action", "a.go", `const s = "{{if .Thread}}shown{{end}}"`, []string{"1:shown"}},
		{"markdown", "a.md", "Line {name}\n{{.Hidden}} shown", []string{"1:Line {name}", "2:shown"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := texts(body(c.file, c.src)); !slices.Equal(got, c.want) {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// Only the highest -vN of each tool in a directory is a version a model is
// offered; a file with no version is always read.
func TestOlderVersions(t *testing.T) {
	got := older([]string{
		"tools/bash-v1.md", "tools/bash-v2.md", "tools/bash-v10.md",
		"tools/read-v4.md", "tools/read-v5.md",
		"tools/notes.md", "tools/spawn-v1.md",
		"other/bash-v2.md",
	})
	want := map[string]bool{"tools/bash-v1.md": true, "tools/bash-v2.md": true, "tools/read-v4.md": true}
	if len(got) != len(want) {
		t.Fatalf("older = %v, want %v", got, want)
	}
	for rel := range want {
		if !got[rel] {
			t.Errorf("%s is an older version", rel)
		}
	}
	if tool, n, ok := versioned("tools/web_fetch-v12.md"); !ok || tool != "web_fetch" || n != 12 {
		t.Errorf("versioned = %q %d %v", tool, n, ok)
	}
	if _, _, ok := versioned("tools/x-v.md"); ok {
		t.Error("-v with no number is no version")
	}
	if _, _, ok := versioned("tools/x-v99999999999999999999.md"); ok {
		t.Error("a version past an int is no version")
	}
}

// A tool description file is named for its tool: the name before a -vN
// suffix and the extension.
func TestToolName(t *testing.T) {
	for in, want := range map[string]string{
		"prompts/tools/spawn-v1.md":      "spawn",
		"prompts/tools/web_fetch-v12.md": "web_fetch",
		"tools/read.md":                  "read",
		"tools/v-v2-v3.txt":              "v-v2",
	} {
		if got := toolName(in); got != want {
			t.Errorf("toolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A spec is read for its frontmatter title and its Overview section, to the
// next heading of the same level or higher; a heading in a fenced block is
// not one, and neither a link's target nor a wikilink's file name is read,
// though a wikilink's text after a bar is.
func TestASpecIsReadForItsTitleAndOverview(t *testing.T) {
	src := "---\ntitle: The title\nstatus: x\n---\n\n# Heading\n\n## Overview\n\nOne [link](specs/thread.md).\n\n```\n## not a heading\n```\n\n### Sub\n\nTwo, as [[013-threads-and-subagents]] and [[033-x|the graph]] say.\n\n## Design\n\nNot read\n"
	want := []string{"2:title: The title", "10:One [link .", "13:## not a heading", "14:```", "12:```", "16:### Sub", "18:Two, as ]] and the graph]] say."}
	slices.Sort(want)
	got := texts(overview(src))
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	for _, s := range overview(src) {
		if s.line == 2 && s.where != "the spec title" || s.line != 2 && s.where != "the spec overview" {
			t.Errorf("line %d is %q", s.line, s.where)
		}
	}
	if got := texts(overview("No frontmatter\n## Overview\nRead\n")); !slices.Equal(got, []string{"3:Read"}) {
		t.Errorf("a spec with no frontmatter: %q", got)
	}
}

// split cuts at every character that is not a letter or a digit, and where
// a lowercase letter meets an uppercase one.
func TestSplit(t *testing.T) {
	text := "spawnAgent web_fetch HTTPServer naïve 2fa Watching’s"
	var got []string
	for _, s := range split(text) {
		got = append(got, text[s.start:s.end])
	}
	want := []string{"spawn", "Agent", "web", "fetch", "HTTPServer", "naïve", "2fa", "Watching", "s"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
