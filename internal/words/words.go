// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package words holds what a model or a person reads to the organization's
// do-not-use list.
//
// The list is do-not-use.yaml in this package, embedded in the binary, so
// the gate reads the same words everywhere it runs; a repository may add a
// list of its own. Each item names a word, the forms the gate matches, the
// readers it is banned for (model-facing, person-facing, spec-overview),
// why, and the words to write instead. A repository names what each reader
// reads: the files a model reads, such as tool descriptions, whose names are
// read as the tools' names; the string files a person reads; and whether its
// specs' titles and Overview sections are read.
//
// Each form is matched as whole words, case-insensitively. A word is a run
// of letters and digits, and a lowercase letter followed by an uppercase one
// starts a new word, so spawn_agent and spawnAgent both hold spawn and
// respawn does not. In a source file (.go, .ts, .tsx, .js, .jsx, .mjs, .cjs,
// .json) only the strings are read, not the code, the comments or an
// object's keys, because a message catalog's key is an identifier nobody
// reads; a placeholder such as {name} inside a string is not read either.
// In every file a template action, {{...}}, is left out, and in a spec a
// link's target.
package words

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// Finding is a do-not-use word in what a reader reads: every hit of one
// item on one line.
type Finding struct {
	Rel string
	// Line is the line of the hits; 0 is the file's name.
	Line int
	// Where names what the text is: the tool name, the spec title, the spec
	// overview, or the reader's text.
	Where string
	// Found is each distinct text the hits are written as, in the order
	// they come.
	Found []string
	// Count is the number of hits.
	Count int
	Item  *Banned
}

func (f Finding) String() string {
	loc := f.Rel
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.Rel, f.Line)
	}
	quoted := make([]string, len(f.Found))
	own := true
	for i, w := range f.Found {
		quoted[i] = strconv.Quote(w)
		own = own && strings.EqualFold(w, f.Item.Word)
	}
	word := strings.Join(quoted, ", ")
	if !own {
		word += " (" + f.Item.Word + ")"
	}
	if f.Count > 1 {
		word += fmt.Sprintf(" %d times", f.Count)
	}
	return fmt.Sprintf("%s: %s in %s: %s Prefer: %s.", loc, word, f.Where, strings.TrimSpace(f.Item.Reason), strings.Join(f.Item.Prefer, ", "))
}

// read is one file as one reader reads it.
type read struct {
	rel   string
	scope string
	segs  []segment
}

// Run holds the files cfg names to the organization's list, and to the
// repository's own list when cfg names one. spec says where the specs are.
// It does not read whether the gate applies: that is the plan's question,
// and a gate run by name runs.
func Run(cfg config.Words, spec config.Spec, root string, out io.Writer) error {
	base, err := Default()
	if err != nil {
		return err
	}
	return run(cfg, spec, root, out, base)
}

// run is Run over a given organization's list.
func run(cfg config.Words, spec config.Spec, root string, out io.Writer, base []Banned) error {
	list := base
	source := fmt.Sprintf("the %d lateregate carries", len(base))
	if cfg.Vocabulary != "" {
		vocab := cfg.Vocabulary
		if !filepath.IsAbs(vocab) {
			vocab = filepath.Join(root, vocab)
		}
		extra, err := Extra(vocab, base)
		if err != nil {
			return err
		}
		list = append(slices.Clone(base), extra...)
		source += fmt.Sprintf(" and the %d of %s", len(extra), cfg.Vocabulary)
	}
	reads, skipped, err := gather(cfg, spec, root)
	if err != nil {
		return err
	}
	if len(reads) == 0 {
		return errors.New("words names nothing to read, so the gate would pass vacuously\n" +
			"name words.model_facing, words.person_facing or words.spec_overview")
	}

	forms := map[string][]form{}
	for _, s := range Scopes {
		forms[s] = compile(list, s)
	}
	needed := make([]bool, len(cfg.Allow))
	var kept []Finding
	allowed, hits := 0, 0
	counts := map[string]int{}
	for _, r := range reads {
		counts[r.scope]++
		for _, s := range r.segs {
			// at is the finding of each item on this line, so a word written
			// four times on one line is one finding.
			at := map[*Banned]int{}
			for _, h := range find(s.text, forms[r.scope]) {
				if i := allowance(cfg.Allow, r.rel, s.text, h); i >= 0 {
					needed[i] = true
					allowed++
					continue
				}
				hits++
				text := s.text[h.start:h.end]
				if i, ok := at[h.item]; ok {
					kept[i].Count++
					if !slices.Contains(kept[i].Found, text) {
						kept[i].Found = append(kept[i].Found, text)
					}
					continue
				}
				where := s.where
				if where == "" {
					where = r.scope + " text"
				}
				at[h.item] = len(kept)
				kept = append(kept, Finding{Rel: r.rel, Line: s.line, Where: where, Found: []string{text}, Count: 1, Item: h.item})
			}
		}
	}
	var unneeded []string
	for i, a := range cfg.Allow {
		if !needed[i] {
			unneeded = append(unneeded, describe(a))
		}
	}

	for _, f := range kept {
		_, _ = fmt.Fprintln(out, "  "+f.String())
	}
	var errs []error
	if len(kept) > 0 {
		errs = append(errs, fmt.Errorf("%d do-not-use word(s) in what a model or a person reads, in %d finding(s); "+
			"write the preferred word, or add a words.allow entry with the reason the word stays", hits, len(kept)))
	}
	if len(unneeded) > 0 {
		errs = append(errs, fmt.Errorf("words.allow names %s, which no hit needs; "+
			"an exception nothing uses is where the next real hit hides, so delete the entry",
			strings.Join(unneeded, ", ")))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	var parts []string
	for _, s := range Scopes {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	_, _ = fmt.Fprintf(out, "no do-not-use word, of %s, across %d file(s): %s",
		source, len(reads), strings.Join(parts, ", "))
	if allowed > 0 {
		_, _ = fmt.Fprintf(out, "; %d hit(s) allowed by words.allow", allowed)
	}
	if skipped > 0 {
		_, _ = fmt.Fprintf(out, "; %d older tool description version(s) not read", skipped)
	}
	_, _ = fmt.Fprintln(out)
	return nil
}

// allowance is the index of the first entry that covers a hit, or -1.
func allowance(allow []config.WordsAllow, rel, text string, h hit) int {
	for i, a := range allow {
		if a.Path != "" {
			if ok, err := path.Match(a.Path, rel); err != nil || !ok {
				continue
			}
		}
		if covers(text, h, a.Text) {
			return i
		}
	}
	return -1
}

// describe names an allowance in a message.
func describe(a config.WordsAllow) string {
	switch {
	case a.Text != "" && a.Path != "":
		return fmt.Sprintf("%q in %s", a.Text, a.Path)
	case a.Text != "":
		return fmt.Sprintf("%q", a.Text)
	default:
		return a.Path
	}
}

// gather reads every file cfg names, each as its reader reads it, and
// counts the older versions of model-facing files it left out. A glob that
// matches no file fails: it is a place the repository believes is checked
// and is not.
func gather(cfg config.Words, spec config.Spec, root string) ([]read, int, error) {
	var reads []read
	skipped := 0
	for _, src := range []struct {
		key   string
		globs []string
		scope string
	}{
		{"words.model_facing", cfg.ModelFacing, ModelFacing},
		{"words.person_facing", cfg.PersonFacing, PersonFacing},
	} {
		var rels []string
		for _, g := range src.globs {
			matched, err := files(root, g)
			if err != nil {
				return nil, 0, err
			}
			if len(matched) == 0 {
				return nil, 0, fmt.Errorf("%s names %s, which matches no file under %s; "+
					"a glob that matches nothing checks nothing, so fix it or delete it", src.key, g, root)
			}
			for _, rel := range matched {
				// A file two globs of one reader match is read once.
				if !slices.Contains(rels, rel) {
					rels = append(rels, rel)
				}
			}
		}
		if src.scope == ModelFacing {
			kept, n, err := latest(rels, cfg.OlderVersions)
			if err != nil {
				return nil, 0, err
			}
			rels, skipped = kept, n
		}
		for _, rel := range rels {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				return nil, 0, err
			}
			segs := body(rel, string(data))
			if src.scope == ModelFacing {
				segs = append([]segment{{line: 0, where: "the tool name", text: toolName(rel)}}, segs...)
			}
			reads = append(reads, read{rel: rel, scope: src.scope, segs: segs})
		}
	}
	if cfg.SpecOverview {
		rels, err := files(root, path.Join(spec.Dir, "*.md"))
		if err != nil {
			return nil, 0, err
		}
		n := 0
		for _, rel := range rels {
			if spec.IsExcluded(path.Base(rel)) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				return nil, 0, err
			}
			n++
			reads = append(reads, read{rel: rel, scope: SpecOverview, segs: overview(string(data))})
		}
		if n == 0 {
			return nil, 0, fmt.Errorf("words.spec_overview is set and %s/ holds no spec, so there is no title or overview to read", spec.Dir)
		}
	}
	return reads, skipped, nil
}

// latest drops the files a higher version of their tool supersedes, apart
// from those the older-versions globs keep, and counts what it dropped. A
// glob that keeps nothing fails, so the list holds only versions something
// still offers.
func latest(rels, keep []string) ([]string, int, error) {
	old := older(rels)
	used := make([]bool, len(keep))
	var out []string
	skipped := 0
	for _, rel := range rels {
		if !old[rel] {
			out = append(out, rel)
			continue
		}
		kept := false
		for i, g := range keep {
			if ok, err := path.Match(g, rel); err == nil && ok {
				used[i], kept = true, true
			}
		}
		if kept {
			out = append(out, rel)
			continue
		}
		skipped++
	}
	var unused []string
	for i, g := range keep {
		if !used[i] {
			unused = append(unused, g)
		}
	}
	if len(unused) > 0 {
		return nil, 0, fmt.Errorf("words.older_versions names %s, which matches no older version of a model-facing file; "+
			"the highest version of each tool is read already, so delete the entry", strings.Join(unused, ", "))
	}
	return out, skipped, nil
}

// files is every regular file the glob matches under root, as sorted
// slash-separated paths relative to it.
func files(root, glob string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
	if err != nil {
		return nil, fmt.Errorf("reading the glob %s: %w", glob, err)
	}
	var out []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		rel, err := filepath.Rel(root, m)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}
