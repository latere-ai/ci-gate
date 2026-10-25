// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// span is a word's place in a text, as byte offsets.
type span struct{ start, end int }

// isWord reports whether r is part of a word: a letter or a digit.
func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// split cuts text into words: runs of letters and digits, with a new word
// where a lowercase letter meets an uppercase one, so that spawn_agent,
// spawn-agent and spawnAgent all hold the word spawn.
func split(text string) []span {
	var out []span
	start := -1
	var prev rune
	for i, r := range text {
		switch {
		case !isWord(r):
			if start >= 0 {
				out = append(out, span{start, i})
				start = -1
			}
		case start < 0:
			start = i
		case unicode.IsLower(prev) && unicode.IsUpper(r):
			out = append(out, span{start, i})
			start = i
		}
		prev = r
	}
	if start >= 0 {
		out = append(out, span{start, len(text)})
	}
	return out
}

// form is one written form of a list item, as the words it is made of.
type form struct {
	words []string
	item  *Banned
}

// compile is every form of the items banned for a reader.
func compile(list []Banned, scope string) []form {
	var out []form
	for i := range list {
		b := &list[i]
		if !b.bans(scope) {
			continue
		}
		for _, f := range b.forms() {
			out = append(out, form{words: spelled(f), item: b})
		}
	}
	return out
}

// hit is one form found in a text.
type hit struct {
	span
	item *Banned
}

// find is every hit in text. A form of several words matches where its
// words follow one another with only spaces, hyphens or underscores between
// them, or none at a change of case, so a form never reaches across a
// sentence's punctuation. An item is reported once at a place, even where
// two of its forms match there.
func find(text string, forms []form) []hit {
	words := split(text)
	var out []hit
	for i := range words {
		seen := map[*Banned]bool{}
		for _, f := range forms {
			if seen[f.item] || !matchAt(text, words, i, f.words) {
				continue
			}
			seen[f.item] = true
			out = append(out, hit{span{words[i].start, words[i+len(f.words)-1].end}, f.item})
		}
	}
	return out
}

// matchAt reports whether want's words start at words[i].
func matchAt(text string, words []span, i int, want []string) bool {
	if i+len(want) > len(words) {
		return false
	}
	for k, w := range want {
		s := words[i+k]
		if !strings.EqualFold(text[s.start:s.end], w) {
			return false
		}
		if k > 0 && !joins(text[words[i+k-1].end:s.start]) {
			return false
		}
	}
	return true
}

// joins reports whether the text between two words keeps them one phrase.
func joins(gap string) bool {
	for _, r := range gap {
		if !unicode.IsSpace(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// covers reports whether an allowance's text covers a hit: the item's own
// word covers every form of it, and a longer text covers the hits written
// inside it, as whole words.
func covers(text string, h hit, allowed string) bool {
	allowed = strings.TrimSpace(allowed)
	if allowed == "" || strings.EqualFold(allowed, h.item.Word) {
		return true
	}
	for p := 0; p+len(allowed) <= len(text); p++ {
		end := p + len(allowed)
		if !strings.EqualFold(text[p:end], allowed) {
			continue
		}
		if r, _ := utf8.DecodeLastRuneInString(text[:p]); p > 0 && isWord(r) {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(text[end:]); end < len(text) && isWord(r) {
			continue
		}
		if p < h.end && h.start < end {
			return true
		}
	}
	return false
}
