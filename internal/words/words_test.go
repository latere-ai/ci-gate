// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package words

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// fixture is a small organization's list, so a case reads against words it
// can see; TestTheOrganizationsList reads the list lateregate carries.
const fixture = `do_not_use:
  - word: spawn
    forms: [spawn, spawned]
    scopes: [model-facing, person-facing, spec-overview]
    reason: "Process jargon."
    prefer: [Agent, start]
  - word: thread
    forms: [thread, threads]
    scopes: [model-facing, person-facing, spec-overview]
    reason: "A unit of execution."
    prefer: [agent, conversation]
  - word: lock
    scopes: [model-facing, spec-overview]
    reason: "Concurrency jargon."
    prefer: [claim]
  - word: held call
    forms: [held call, held calls]
    scopes: [model-facing]
    reason: "The harness's state."
    prefer: [call waiting for approval]
  - word: look
    forms: [a look]
    scopes: [person-facing]
    reason: "A mechanism as a noun."
    prefer: [check]
  - word: offer
    forms: [offer, offered]
    scopes: [person-facing]
    reason: "Narrates the mechanism."
    prefer: [suggest]
`

// tree writes files under a fresh root and returns the repository inside
// it, repo/, so a file can sit beside the repository too.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "repo")
}

// base is the fixture list, decoded and held to the rules of a list.
func base(t *testing.T) []Banned {
	t.Helper()
	items, err := decode([]byte(fixture), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := check(items, nil, "fixture"); err != nil {
		t.Fatal(err)
	}
	return items
}

// scan runs the gate over the fixture list.
func scan(t *testing.T, cfg config.Words, root string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := run(cfg, config.Spec{Dir: "specs", Exclude: []string{"README.md"}}, root, &sb, base(t))
	return sb.String(), err
}

// lines is the findings of a run, one per line, without the indent.
func lines(out string) []string {
	var got []string
	for l := range strings.SplitSeq(out, "\n") {
		if s, ok := strings.CutPrefix(l, "  "); ok {
			got = append(got, s)
		}
	}
	return got
}

// heads is each finding up to the reason: the place, the words and the reader.
func heads(out string) []string {
	var got []string
	for _, l := range lines(out) {
		i := strings.Index(l, " in ")
		j := strings.Index(l[i+4:], ":")
		got = append(got, l[:i+4+j])
	}
	return got
}

func expect(t *testing.T, out string, err error, want ...string) {
	t.Helper()
	got := heads(out)
	if len(want) == 0 {
		if err != nil {
			t.Fatalf("want a pass, got %v\n%s", err, out)
		}
		return
	}
	if err == nil {
		t.Fatalf("want a failure with %v, got a pass:\n%s", want, out)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("findings:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Each reader reads its own places: a tool description's name and body, a
// catalog's strings, a spec's title and Overview, and nothing else of them.
func TestEachReaderReadsItsPlaces(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/tools/spawn-v1.md": "Start a thread on a task.\n" +
			"Use {{.Tool}} to wait; a lock is held.\n",
		"repo/prompts/tools/read-v2.md": "Reads a file.\n",
		"repo/frontend/i18n/en.ts": `// The thread list: a comment nobody reads.
export const en = {
  'thread.title': 'Your conversations',
  'offer.ask': 'Latere offered a routine. Take a look.',
  'lock.note': 'A lock is not person-facing here.',
  'named': 'Hello, {thread}.',
}
`,
		"repo/specs/001-threads.md": `---
title: "Threads and spawn edges"
status: drafted
---

# Threads

## Overview

A thread holds a lock.

## Design

A thread in the design is not read.
`,
		"repo/specs/README.md":            "---\ntitle: Threads\n---\n",
		"repo/specs/.archive/000-old.md":  "---\ntitle: Old threads\n---\n",
		"repo/specs/002-no-overview.md":   "---\ntitle: Plain\n---\n\n## Design\n\nthread\n",
		"repo/frontend/i18n/README.md":    "a thread",
		"repo/prompts/tools/notes/x.md":   "a thread",
		"repo/prompts/tools/notes.txt.md": "fine",
	})
	out, err := scan(t, config.Words{
		ModelFacing:  []string{"prompts/tools/*.md"},
		PersonFacing: []string{"frontend/i18n/*.ts"},
		SpecOverview: true,
	}, root)
	expect(t, out, err,
		`prompts/tools/spawn-v1.md: "spawn" in the tool name`,
		`prompts/tools/spawn-v1.md:1: "thread" in model-facing text`,
		`prompts/tools/spawn-v1.md:2: "lock" in model-facing text`,
		`frontend/i18n/en.ts:4: "offered" (offer) in person-facing text`,
		`frontend/i18n/en.ts:4: "a look" (look) in person-facing text`,
		`specs/001-threads.md:2: "Threads" (thread) in the spec title`,
		`specs/001-threads.md:2: "spawn" in the spec title`,
		`specs/001-threads.md:10: "thread" in the spec overview`,
		`specs/001-threads.md:10: "lock" in the spec overview`,
	)
	if !strings.Contains(err.Error(), "9 do-not-use word(s) in what a model or a person reads, in 9 finding(s)") {
		t.Errorf("the failure counts the hits and the findings: %v", err)
	}
}

// A finding names the place, the word as written and the list's word, the
// reader, the reason and the words to write instead, and gathers every hit
// of one word on one line.
func TestAFindingSaysWhatToWrite(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/tools/run-v1.md": "One thread, two threads, a thread.\n",
	})
	out, _ := scan(t, config.Words{ModelFacing: []string{"prompts/tools/*.md"}}, root)
	want := `prompts/tools/run-v1.md:1: "thread", "threads" (thread) 3 times in model-facing text: A unit of execution. Prefer: agent, conversation.`
	if got := lines(out); len(got) != 1 || got[0] != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// A form matches whole words, case-insensitively. A word is a run of
// letters and digits, and a change from lower to upper case starts one, so
// an identifier's parts are words and a longer word holding a form is not
// the form.
func TestFormsMatchWholeWords(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/a.md": "respawn, spawning, unlock, locksmith, threadbare\n" +
			"SPAWN\n" +
			"spawn_agent\n" +
			"spawnAgent\n" +
			"thread.started\n" +
			"a held call; held-calls; held. Call\n",
	})
	out, err := scan(t, config.Words{ModelFacing: []string{"prompts/a.md"}}, root)
	expect(t, out, err,
		`prompts/a.md:2: "SPAWN" in model-facing text`,
		`prompts/a.md:3: "spawn" in model-facing text`,
		`prompts/a.md:4: "spawn" in model-facing text`,
		`prompts/a.md:5: "thread" in model-facing text`,
		`prompts/a.md:6: "held call", "held-calls" (held call) 2 times in model-facing text`,
	)
}

// An allowance with a list word covers every form of it; a longer text
// covers only the hits inside it; a path narrows either. A pass counts what
// the allowances covered.
func TestAllowancesCoverWhatTheyName(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/tools/a-v1.md": "Emits thread.started; threads are listed.\n",
		"repo/prompts/tools/b-v1.md": "A lock, and thread.started.\n",
	})
	cfg := config.Words{ModelFacing: []string{"prompts/tools/*.md"}}
	out, err := scan(t, cfg, root)
	expect(t, out, err,
		`prompts/tools/a-v1.md:1: "thread", "threads" (thread) 2 times in model-facing text`,
		`prompts/tools/b-v1.md:1: "lock" in model-facing text`,
		`prompts/tools/b-v1.md:1: "thread" in model-facing text`,
	)

	cfg.Allow = []config.WordsAllow{
		{Text: "thread.started", Reason: "the event type on the wire"},
		{Text: "lock", Path: "prompts/tools/b-*.md", Reason: "the tool's own field"},
	}
	out, err = scan(t, cfg, root)
	expect(t, out, err, `prompts/tools/a-v1.md:1: "threads" (thread) in model-facing text`)

	cfg.Allow = append(cfg.Allow, config.WordsAllow{Text: "THREAD", Path: "prompts/tools/a-v1.md", Reason: "renamed next"})
	out, err = scan(t, cfg, root)
	expect(t, out, err)
	if !strings.Contains(out, "4 hit(s) allowed by words.allow") {
		t.Errorf("a pass counts what the allowances covered:\n%s", out)
	}

	cfg.Allow = append(cfg.Allow, config.WordsAllow{Path: "prompts/other.md", Reason: "nothing there"},
		config.WordsAllow{Text: "spawn", Reason: "nothing spawns"})
	_, err = scan(t, cfg, root)
	if err == nil || !strings.Contains(err.Error(), `words.allow names prompts/other.md, "spawn", which no hit needs`) {
		t.Fatalf("an allowance no hit needs fails, naming it: %v", err)
	}
	if strings.Contains(err.Error(), "do-not-use word(s)") {
		t.Errorf("an allowed hit is not counted as a finding: %v", err)
	}
}

// An allowance's text covers a hit only where the text is written around
// it, as whole words.
func TestAnAllowedTextIsMatchedAsWholeWords(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/a.md": "thread.startedAt, the thread.started event\n",
	})
	cfg := config.Words{ModelFacing: []string{"prompts/a.md"},
		Allow: []config.WordsAllow{{Text: "thread.started", Path: "prompts/*.md", Reason: "the wire name"}}}
	out, err := scan(t, cfg, root)
	expect(t, out, err, `prompts/a.md:1: "thread" in model-facing text`)
}

// The list lateregate carries holds to the rules of a list, uses every
// reader, and is what Run reads.
func TestTheOrganizationsList(t *testing.T) {
	list, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, b := range list {
		for _, s := range b.Scopes {
			used[s] = true
		}
	}
	for _, s := range Scopes {
		if !used[s] {
			t.Errorf("no word is banned for %s", s)
		}
	}
	root := tree(t, map[string]string{"repo/prompts/tools/spawn-v1.md": "Starts an agent.\n"})
	var sb strings.Builder
	err = Run(config.Words{ModelFacing: []string{"prompts/tools/*.md"}}, config.Spec{}, root, &sb)
	expect(t, sb.String(), err, `prompts/tools/spawn-v1.md: "spawn" in the tool name`)
}

// A repository's own list adds its words to the organization's, held to the
// same rules and to that list: a word or a form it already carries fails.
func TestARepositorysOwnList(t *testing.T) {
	own := "version: 1\nentries: []\ndo_not_use:\n  - {word: ticket, forms: [ticket, tickets], scopes: [person-facing], reason: Names the tracker., prefer: [request]}\n"
	root := tree(t, map[string]string{
		"words.yaml":        own,
		"repo/i18n/en.json": `{"a": "Your tickets", "b": "Latere offered it"}`,
	})
	cfg := config.Words{Vocabulary: "../words.yaml", PersonFacing: []string{"i18n/*.json"}}
	out, err := scan(t, cfg, root)
	expect(t, out, err,
		`i18n/en.json:1: "tickets" (ticket) in person-facing text`,
		`i18n/en.json:1: "offered" (offer) in person-facing text`,
	)

	cfg.Vocabulary = filepath.Join(root, "..", "words.yaml")
	cfg.Allow = []config.WordsAllow{{Text: "ticket", Reason: "the support product's name"}, {Text: "offer", Reason: "renamed next"}}
	out, err = scan(t, cfg, root)
	expect(t, out, err)
	if !strings.Contains(out, "no do-not-use word, of the 6 lateregate carries and the 1 of "+cfg.Vocabulary+", across 1 file(s): 1 person-facing") {
		t.Errorf("an absolute path is read as it is, and the pass names both lists:\n%s", out)
	}
}

// A repository's list the gate cannot use fails with what to do: missing,
// unreadable as YAML, holding no list, or holding an item that breaks a
// rule of a list.
func TestARepositorysListTheGateCannotUseFails(t *testing.T) {
	cfg := config.Words{Vocabulary: "../words.yaml", ModelFacing: []string{"prompts/a.md"}}
	root := tree(t, map[string]string{"repo/prompts/a.md": "fine\n"})
	_, err := scan(t, cfg, root)
	if err == nil || !strings.Contains(err.Error(), "words.vocabulary names "+filepath.Join(root, "..", "words.yaml")+", which does not exist") ||
		!strings.Contains(err.Error(), "point it at the file, or delete the key") {
		t.Fatalf("a missing list fails, naming the file and the fix: %v", err)
	}

	item := func(fields string) string { return "do_not_use:\n  - {" + fields + "}\n" }
	for _, c := range []struct{ name, doc, want string }{
		{"not YAML", "do_not_use: [\n", "words.yaml"},
		{"no list", "version: 1\nentries: []\n", "holds no do_not_use list, so words.vocabulary adds nothing"},
		{"no word", item("scopes: [model-facing], reason: r, prefer: [p]"), `do_not_use "item 1": the word is empty or padded`},
		{"padded word", item("word: ' w', scopes: [model-facing], reason: r, prefer: [p]"), "the word is empty or padded"},
		{"a word the organization lists", item("word: Spawn, scopes: [model-facing], reason: r, prefer: [p]"), `do_not_use "Spawn": the word is listed already`},
		{"a word twice", "do_not_use:\n  - {word: w, scopes: [model-facing], reason: r, prefer: [p]}\n  - {word: W, scopes: [model-facing], reason: r, prefer: [p]}\n", `do_not_use "W": the word is listed already`},
		{"no scope", item("word: w, reason: r, prefer: [p]"), "names no scope"},
		{"unknown scope", item("word: w, scopes: [docs], reason: r, prefer: [p]"), `scope "docs" is not one this lateregate reads (model-facing, person-facing, spec-overview)`},
		{"scope twice", item("word: w, scopes: [model-facing, model-facing], reason: r, prefer: [p]"), `scope "model-facing" is listed twice`},
		{"no reason", item("word: w, scopes: [model-facing], prefer: [p]"), "has no reason"},
		{"a reason on two lines", item(`word: w, scopes: [model-facing], reason: "one\ntwo", prefer: [p]`), "the reason spans more than one line"},
		{"no prefer", item("word: w, scopes: [model-facing], reason: r"), "names no word to write instead"},
		{"an empty prefer", item("word: w, scopes: [model-facing], reason: r, prefer: [p, '']"), "a word to write instead is empty"},
		{"padded form", item("word: w, forms: [w, ' ws'], scopes: [model-facing], reason: r, prefer: [p]"), `form " ws" is padded`},
		{"empty form", item("word: w, forms: ['--'], scopes: [model-facing], reason: r, prefer: [p]"), `form "--" holds no letter or digit`},
		{"form twice", item("word: w, forms: [w, W], scopes: [model-facing], reason: r, prefer: [p]"), `form "W" is listed twice`},
		{"a form the organization's word owns", item("word: hold, forms: [held-call], scopes: [model-facing], reason: r, prefer: [p]"), `form "held-call" is already "held call"'s in scope "model-facing"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := tree(t, map[string]string{"words.yaml": c.doc, "repo/prompts/a.md": "fine\n"})
			_, err := scan(t, cfg, root)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, err)
			}
		})
	}

	// A form may be two words' in readers that do not overlap.
	root = tree(t, map[string]string{
		"words.yaml":        item("word: hold, forms: [held call], scopes: [person-facing], reason: r, prefer: [p]"),
		"repo/prompts/a.md": "fine\n",
	})
	if out, err := scan(t, cfg, root); err != nil {
		t.Fatalf("a form shared across readers that do not overlap: %v\n%s", err, out)
	}
}

// A place that reads nothing fails: a glob that matches no file, a spec
// directory with no spec, and a block that names no place at all.
func TestAPlaceThatReadsNothingFails(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/a.md":    "fine\n",
		"repo/specs/README.md": "---\ntitle: index\n---\n",
	})
	for _, c := range []struct {
		name string
		cfg  config.Words
		want string
	}{
		{"model glob", config.Words{ModelFacing: []string{"prompts/tools/*.md"}}, "words.model_facing names prompts/tools/*.md, which matches no file"},
		{"person glob", config.Words{PersonFacing: []string{"i18n/*.ts"}}, "words.person_facing names i18n/*.ts, which matches no file"},
		{"bad glob", config.Words{PersonFacing: []string{"i18n/["}}, "reading the glob i18n/["},
		{"no spec", config.Words{SpecOverview: true}, "specs/ holds no spec"},
		{"nothing named", config.Words{}, "words names nothing to read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := scan(t, c.cfg, root)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, err)
			}
		})
	}
}

// A file two globs of one reader match is read once, and a pass names what
// was read.
func TestAPassNamesWhatItRead(t *testing.T) {
	root := tree(t, map[string]string{
		"repo/prompts/a.md":   "a held call\n",
		"repo/i18n/en.json":   `{"thread": "Conversations"}`,
		"repo/specs/001-a.md": "---\ntitle: A\n---\n\n## Overview\n\nfine\n",
	})
	cfg := config.Words{
		ModelFacing:  []string{"prompts/*.md", "prompts/a.md"},
		PersonFacing: []string{"i18n/*.json"},
		SpecOverview: true,
		Allow:        []config.WordsAllow{{Text: "held call", Reason: "the tool's documented state"}},
	}
	out, err := scan(t, cfg, root)
	expect(t, out, err)
	if !strings.Contains(out, "no do-not-use word, of the 6 lateregate carries, across 3 file(s): 1 model-facing, 1 person-facing, 1 spec-overview; 1 hit(s) allowed by words.allow") {
		t.Errorf("the pass line:\n%s", out)
	}
}
