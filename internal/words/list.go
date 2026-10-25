// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// The readers a do-not-use word is banned for.
const (
	// ModelFacing is what a model reads: tool names and tool descriptions.
	ModelFacing = "model-facing"
	// PersonFacing is what a person reads on a screen.
	PersonFacing = "person-facing"
	// SpecOverview is a spec's title and its Overview section.
	SpecOverview = "spec-overview"
)

// Scopes are the readers the gate knows, in the order it reads them.
var Scopes = []string{ModelFacing, PersonFacing, SpecOverview}

// list is the organization's do-not-use list. It is embedded so the gate
// reads the same words on a laptop and on a runner that checks out nothing
// but the repository, and a change to it reaches a repository with the
// release that carries it, like any other rule of the bar.
//
//go:embed do-not-use.yaml
var list []byte

// ListFile is where the organization's list lives in this repository, for
// the messages that name it.
const ListFile = "internal/words/do-not-use.yaml"

// Banned is one item of a do_not_use list.
type Banned struct {
	Word string `yaml:"word"`
	// Forms are the written forms matched; empty means the word alone.
	Forms  []string `yaml:"forms"`
	Scopes []string `yaml:"scopes"`
	Reason string   `yaml:"reason"`
	Prefer []string `yaml:"prefer"`
}

// Default is the organization's list, as lateregate carries it.
func Default() ([]Banned, error) {
	items, err := decode(list, "lateregate's "+ListFile)
	if err != nil {
		return nil, err
	}
	if err := check(items, nil, "lateregate's "+ListFile); err != nil {
		return nil, err
	}
	return items, nil
}

// Extra reads a repository's own do_not_use list from the file at path,
// held to the rules of the organization's list and to that list itself: a
// word it already carries is not a word to add.
func Extra(path string, base []Banned) ([]Banned, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("words.vocabulary names %s, which does not exist\n"+
			"the key adds a do_not_use list of the repository's own to the one lateregate carries: "+
			"point it at the file, or delete the key", path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading words.vocabulary: %w", err)
	}
	items, err := decode(data, path)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s holds no do_not_use list, so words.vocabulary adds nothing\n"+
			"write the words under a do_not_use key, or delete words.vocabulary", path)
	}
	if err := check(items, base, path); err != nil {
		return nil, err
	}
	return items, nil
}

// decode reads the do_not_use key of a YAML document. Only that key is read,
// so a vocabulary file that holds other sections can be named as it is.
func decode(data []byte, name string) ([]Banned, error) {
	var doc struct {
		DoNotUse []Banned `yaml:"do_not_use"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return doc.DoNotUse, nil
}

// check holds items to the rules of a list, beside the items of base: every
// item names a word, its readers, a reason and the words to write instead;
// no word is listed twice; and a written form belongs to one word per
// reader, so a hit has one reason. Every problem is reported, each naming
// the file and the word.
func check(items, base []Banned, name string) error {
	var errs []error
	add := func(word, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: do_not_use %q: %s", name, word, fmt.Sprintf(format, args...)))
	}
	words := map[string]bool{}
	// owner[scope][form's words] is the word whose item matches the form there.
	owner := map[string]map[string]string{}
	claim := func(b Banned, report bool) {
		for _, f := range b.forms() {
			key := strings.ToLower(strings.Join(spelled(f), " "))
			if key == "" {
				continue
			}
			for _, s := range b.Scopes {
				if owner[s] == nil {
					owner[s] = map[string]string{}
				}
				if other, ok := owner[s][key]; ok && !strings.EqualFold(other, b.Word) && report {
					add(b.Word, "form %q is already %q's in scope %q", f, other, s)
					continue
				}
				owner[s][key] = b.Word
			}
		}
	}
	for _, b := range base {
		words[strings.ToLower(b.Word)] = true
		claim(b, false)
	}
	for i, b := range items {
		word := b.Word
		if strings.TrimSpace(word) == "" {
			word = fmt.Sprintf("item %d", i+1)
		}
		switch {
		case strings.TrimSpace(b.Word) == "" || b.Word != strings.TrimSpace(b.Word):
			add(word, "the word is empty or padded")
		case words[strings.ToLower(b.Word)]:
			add(word, "the word is listed already")
		}
		words[strings.ToLower(b.Word)] = true
		if len(b.Scopes) == 0 {
			add(word, "names no scope, so the word is matched nowhere")
		}
		for j, s := range b.Scopes {
			switch {
			case !slices.Contains(Scopes, s):
				add(word, "scope %q is not one this lateregate reads (%s)", s, strings.Join(Scopes, ", "))
			case slices.Contains(b.Scopes[:j], s):
				add(word, "scope %q is listed twice", s)
			}
		}
		switch r := strings.TrimSpace(b.Reason); {
		case r == "":
			add(word, "has no reason, which every hit prints")
		case strings.Contains(r, "\n"):
			add(word, "the reason spans more than one line")
		}
		if len(b.Prefer) == 0 {
			add(word, "names no word to write instead")
		}
		if slices.ContainsFunc(b.Prefer, func(p string) bool { return strings.TrimSpace(p) == "" }) {
			add(word, "a word to write instead is empty")
		}
		for j, f := range b.Forms {
			switch {
			case f != strings.TrimSpace(f):
				add(word, "form %q is padded", f)
			case len(split(f)) == 0:
				add(word, "form %q holds no letter or digit, so it matches nothing", f)
			case slices.ContainsFunc(b.Forms[:j], func(g string) bool { return strings.EqualFold(g, f) }):
				add(word, "form %q is listed twice", f)
			}
		}
		claim(b, true)
	}
	return errors.Join(errs...)
}

// spelled is the words a form is written with, as the matcher splits text.
func spelled(form string) []string {
	var out []string
	for _, s := range split(form) {
		out = append(out, form[s.start:s.end])
	}
	return out
}

// forms is what the item matches: its forms, or the word alone.
func (b Banned) forms() []string {
	if len(b.Forms) == 0 {
		return []string{b.Word}
	}
	return b.Forms
}

// bans reports whether the item is banned for a reader.
func (b Banned) bans(scope string) bool { return slices.Contains(b.Scopes, scope) }
