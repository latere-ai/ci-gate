// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// segment is a run of text a reader reads, with the line it is on. Line 0
// is the file's name.
type segment struct {
	line int
	// where names what the text is, for a hit outside a file's body: the
	// tool name, the spec title. Empty is the body.
	where string
	text  string
}

// sources are the file types whose strings are read rather than every line:
// in them the code, the comments and an object's keys are not what anyone
// reads.
var sources = []string{".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json"}

var (
	// action is a template action, which is replaced before anyone reads
	// the text.
	action = regexp.MustCompile(`\{\{.*?\}\}`)
	// placeholder is a message catalog's placeholder, such as {name}.
	placeholder = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_.]*\}`)
	// target is a Markdown link's target, which a reader follows rather
	// than reads.
	target = regexp.MustCompile(`\]\([^)]*\)`)
	// wikilink is a [[name]] link, or [[name|text]]: the name is a file's,
	// which the link resolves, and only the text after a bar is read.
	wikilink = regexp.MustCompile(`\[\[[^\]|]*\|?`)
	// version is the -vN a versioned tool description file carries.
	version = regexp.MustCompile(`-v([0-9]+)$`)
)

// blank replaces each match of re with spaces, keeping every offset.
func blank(s string, re *regexp.Regexp) string {
	return re.ReplaceAllStringFunc(s, func(m string) string { return strings.Repeat(" ", len(m)) })
}

// body is what a reader reads of a file: the strings of a source file, the
// lines of any other.
func body(name, src string) []segment {
	ext := filepath.Ext(name)
	if !slices.Contains(sources, ext) {
		var out []segment
		for i, l := range strings.Split(src, "\n") {
			out = append(out, segment{line: i + 1, text: blank(l, action)})
		}
		return out
	}
	segs := stringsOf(src, ext == ".go")
	for i := range segs {
		segs[i].text = blank(blank(segs[i].text, action), placeholder)
	}
	return segs
}

// toolName is the name of the tool a description file is for: its file
// name before a -vN suffix and the extension.
func toolName(name string) string {
	tool, _, _ := versioned(name)
	return tool
}

// versioned splits a description file's name into its tool's name and its
// version, the N of a -vN suffix, and reports whether it has one.
func versioned(name string) (string, int, bool) {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	m := version.FindStringSubmatchIndex(base)
	if m == nil {
		return base, 0, false
	}
	n, err := strconv.Atoi(base[m[2]:m[3]])
	if err != nil {
		return base, 0, false
	}
	return base[:m[0]], n, true
}

// older is the files of rels that a higher version of the same tool, in the
// same directory, supersedes. A released description is kept byte for byte
// so a replay sends it again, and no model is offered it after the next
// version ships, so only the highest is what a model reads.
func older(rels []string) map[string]bool {
	highest := map[string]int{}
	for _, rel := range rels {
		if tool, n, ok := versioned(rel); ok {
			key := path.Join(path.Dir(rel), tool)
			highest[key] = max(highest[key], n)
		}
	}
	out := map[string]bool{}
	for _, rel := range rels {
		if tool, n, ok := versioned(rel); ok && n < highest[path.Join(path.Dir(rel), tool)] {
			out[rel] = true
		}
	}
	return out
}

// overview is what is read of a spec: its frontmatter title and its
// Overview section, up to the next heading of the same or a higher level.
// A heading inside a fenced block is not one.
func overview(src string) []segment {
	lines := strings.Split(src, "\n")
	var out []segment
	i := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i = 1; i < len(lines); i++ {
			l := lines[i]
			if strings.TrimSpace(l) == "---" {
				i++
				break
			}
			if rest, ok := strings.CutPrefix(l, "title:"); ok {
				out = append(out, segment{line: i + 1, where: "the spec title", text: "title:" + rest})
			}
		}
	}
	in, fenced := false, false
	for ; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
		}
		if !fenced && (strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ")) {
			in = strings.TrimSpace(l) == "## Overview"
			continue
		}
		if in {
			out = append(out, segment{line: i + 1, where: "the spec overview", text: blank(blank(l, target), wikilink)})
		}
	}
	return out
}

// stringsOf reads the string literals of a source file, one segment per
// line of each, leaving out comments and the strings that are an object's
// keys: a string followed by a colon, where it opens the line or follows a
// brace or a comma. A ternary's first branch follows a question mark and is
// read. goRaw says a backquoted string is a Go raw string, with no escapes
// and no ${} inside it.
func stringsOf(src string, goRaw bool) []segment {
	var out []segment
	line := 1
	// before is the last character before a string that is not a space:
	// '\n' at the start of a line.
	before := byte('\n')
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			before = '\n'
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = len(src) - i - 2
			}
			line += strings.Count(src[i:i+2+end], "\n")
			i += end + 4
		case c == '"' || c == '\'' || c == '`':
			start := line
			text, next, lines := literal(src, i, goRaw)
			key := isKey(src, next, before)
			if !key {
				for k, l := range strings.Split(text, "\n") {
					out = append(out, segment{line: start + k, text: l})
				}
			}
			line += lines
			before = c
			i = next
		default:
			before = c
			i++
		}
	}
	return out
}

// literal reads the string that opens at src[i], returning its text with
// each escape blanked, the index after it, and the newlines it spans. A
// quoted string ends at a line's end if it is not closed. In a JavaScript
// template, ${...} is blanked: it is code.
func literal(src string, i int, goRaw bool) (string, int, int) {
	q := src[i]
	var b strings.Builder
	lines := 0
	raw := q == '`' && goRaw
	j := i + 1
	for j < len(src) {
		c := src[j]
		switch {
		case c == q:
			return b.String(), j + 1, lines
		case c == '\n' && q != '`':
			return b.String(), j, lines
		case c == '\n':
			lines++
			b.WriteByte('\n')
			j++
		case c == '\\' && !raw:
			// An escaped line end continues the string on the next line.
			if j+1 < len(src) && src[j+1] == '\n' {
				lines++
				b.WriteString(" \n")
			} else {
				b.WriteString("  ")
			}
			j += 2
		case c == '$' && q == '`' && !raw && j+1 < len(src) && src[j+1] == '{':
			b.WriteByte(' ')
			j++
			for depth := 0; j < len(src); {
				switch src[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
				if src[j] == '\n' {
					lines++
					b.WriteByte('\n')
				} else {
					b.WriteByte(' ')
				}
				j++
				if depth == 0 {
					break
				}
			}
		default:
			b.WriteByte(c)
			j++
		}
	}
	return b.String(), j, lines
}

// isKey reports whether the string that ended before src[next] is an
// object's key: a colon follows it, and it opens the line or follows a
// brace or a comma.
func isKey(src string, next int, before byte) bool {
	for next < len(src) && (src[next] == ' ' || src[next] == '\t') {
		next++
	}
	if next >= len(src) || src[next] != ':' {
		return false
	}
	return before == '\n' || before == '{' || before == ','
}
