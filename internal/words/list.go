// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// The readers a do-not-use word is banned for. They are the vocabulary's
// own names, and the gate reads each from the places a repository names.
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

// Banned is one item of the vocabulary's do_not_use list.
type Banned struct {
	Word string `yaml:"word"`
	// Forms are the written forms matched; empty means the word alone.
	Forms  []string `yaml:"forms"`
	Scopes []string `yaml:"scopes"`
	Reason string   `yaml:"reason"`
	Prefer []string `yaml:"prefer"`
}

// Load reads the do_not_use list from the vocabulary file at path.
//
// Only that key is decoded, and loosely: the vocabulary's own validator in
// latere-ai/onboarding holds the file to its schema, and a field it adds
// later is not a reason for every repository's gate to fail. What the gate
// needs from each item it checks here, because an item it cannot use would
// otherwise be a word it silently never matches.
func Load(path string) ([]Banned, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the vocabulary file %s does not exist\n"+
			"words.vocabulary names the vocabulary.yaml of latere-ai/onboarding, whose do_not_use list the gate reads: "+
			"check that repository out where the key points, or point the key at the file", path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the vocabulary file: %w", err)
	}
	var doc struct {
		DoNotUse []Banned `yaml:"do_not_use"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.DoNotUse) == 0 {
		return nil, fmt.Errorf("%s holds no do_not_use list, so the gate would pass vacuously\n"+
			"point words.vocabulary at the vocabulary file that carries the list", path)
	}
	var errs []error
	for i, b := range doc.DoNotUse {
		if err := b.check(); err != nil {
			name := strings.TrimSpace(b.Word)
			if name == "" {
				name = fmt.Sprintf("item %d", i+1)
			}
			errs = append(errs, fmt.Errorf("%s: do_not_use %q: %w", path, name, err))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return doc.DoNotUse, nil
}

// check reports the first thing that keeps the gate from using an item.
func (b Banned) check() error {
	switch {
	case strings.TrimSpace(b.Word) == "":
		return errors.New("the item names no word")
	case len(b.Scopes) == 0:
		return errors.New("the item names no scope, so the word is matched nowhere")
	case strings.TrimSpace(b.Reason) == "":
		return errors.New("the item has no reason, which every hit prints")
	case len(b.Prefer) == 0:
		return errors.New("the item names no word to write instead")
	}
	for _, s := range b.Scopes {
		if !slices.Contains(Scopes, s) {
			return fmt.Errorf("scope %q is not one this lateregate reads (%s); "+
				"a newer list may need a newer lateregate", s, strings.Join(Scopes, ", "))
		}
	}
	for _, f := range b.forms() {
		if len(split(f)) == 0 {
			return fmt.Errorf("form %q holds no letter or digit, so it matches nothing", f)
		}
	}
	return nil
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
