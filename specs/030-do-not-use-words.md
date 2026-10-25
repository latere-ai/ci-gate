---
title: What a model or a person reads keeps to the do-not-use list
status: complete
depends_on:
  - 001-gate-principles.md
  - 013-registers-gate.md
  - 028-platform-via-sdk.md
affects:
  - internal/bar/
  - internal/config/
  - internal/words/ (new, with do-not-use.yaml)
  - README.md
  - docs/gates.md
  - docs/configuration.md
  - CHANGELOG.md
effort: medium
created: 2026-10-25
updated: 2026-10-25
author: changkun
dispatched_task_id: null
---

# What a model or a person reads keeps to the do-not-use list

## The problem

The names a model or a person reads drift toward the words their authors
think in. A tool a model calls is named `spawn`, its description says a
thread is killed, a screen says Latere offered a routine, and a spec's
overview, the paragraph the owner reads first, says a writer holds a
lease. Each word is accurate to the mechanism and opaque to the reader.
Review catches some of them on the day they are written and misses the
rest, because the word reads as correct to whoever wrote it.

The organization decided the words once: operating-system and process
jargon stays out of tool names, tool descriptions, screens and spec
overviews, and the words that name a screen's mechanism stay off
screens. Model-facing tools reuse the names models know from Claude Code
where one exists.

## The decision

A gate, `words`, that a repository turns on in `.lateregate.yaml` by
naming the places each reader reads:

```yaml
words:
  model_facing: [prompts/tools/*.md]
  person_facing: [frontend/src/i18n/en.ts]
  spec_overview: true
```

### The list

The list is `internal/words/do-not-use.yaml` in this repository,
embedded in the binary. It is the one copy: the organization's
vocabulary in `latere-ai/onboarding` points here rather than carrying the
words itself. Its `do_not_use` key is a list of items, each with:

- `word`, the word as the list names it;
- `forms`, the written forms matched, each a whole word or a run of whole
  words; empty means the word alone. A word banned in one sense lists the
  phrases that carry it: `look` as a noun is `a look`, `the look` and the
  like, and `share` of credit is `share of` and `'s share`;
- `scopes`, the readers it is banned for: `model-facing`,
  `person-facing`, `spec-overview`;
- `reason`, one line printed with every hit;
- `prefer`, the words to write instead.

A list is held to its rules when it is read, and this repository's tests
hold the embedded one to them: every item names a word, its readers, a
one-line reason and the words to write instead; no word is listed twice;
a form holds a letter, is not padded and is not listed twice; and one
written form belongs to one word per reader, so a hit has one reason.

A repository may add words of its own with `words.vocabulary`, a file
holding a `do_not_use` key in the same shape, relative to the repository
root or absolute. Only that key is read, so a file with other sections
can be named as it is. Its items are held to the same rules beside the
embedded list: a word or a form the list already carries fails, since a
second definition would give one hit two reasons.

### What is read

- **Model-facing**: every file `model_facing` matches, and the file's
  name, before a `-vN` suffix and the extension, read as the name of the
  tool the file describes.
- **Person-facing**: every file `person_facing` matches.
- **Spec overview**: each spec in `spec.dir` apart from `spec.exclude`,
  for its frontmatter `title` and its `## Overview` section, up to the
  next heading of the same level or higher. A heading in a fenced block is
  not one. The archive is a subdirectory and is not read.

In a source file (`.go`, `.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs`,
`.json`) only the strings are read. The code, the comments and a string
that is an object's key are not what anyone reads: a message catalog's
`'offer.ask'` is an identifier. A key is a string followed by a colon
that opens its line or follows a brace or a comma, so a ternary's first
branch is still read. Inside a string an escape, a placeholder such as
`{name}`, and a JavaScript template's `${...}` are blanked. In every file a
template action, `{{...}}`, is blanked, and in a spec a link's target.

### How a form matches

A word is a run of letters and digits, and a lowercase letter followed
by an uppercase one starts a new word. So `spawn_agent`, `spawn-agent`
and `spawnAgent` all hold `spawn`, and `respawn` does not. Matching is
case-insensitive. A form of several words matches where its words follow
one another with only spaces, hyphens or underscores between them, so
`held call` matches `held-calls` when that is a listed form and never
reaches across a sentence's punctuation. There is no stemming: the list
writes each inflection it bans.

### What is reported

Every hit of one item on one line is one finding:

```
  prompts/tools/spawn-v1.md: "spawn" in the tool name: Operating-system jargon for making a process. ... Prefer: Agent, start.
  prompts/tools/edit-v2.md:13: "thread" 4 times in model-facing text: As a product noun, ... Prefer: agent, conversation.
  frontend/src/i18n/en.ts:912: "offered" (offer) in person-facing text: Narrates the agent's mechanism. ... Prefer: suggest, you set this up.
lateregate: 3 do-not-use word(s) in what a model or a person reads, in 3 finding(s); write the preferred word, or add a words.allow entry with the reason the word stays
```

### Exceptions

`words.allow` is a list of entries, each with a `reason` and a `text`, a
`path` or both. A `text` that is a list word covers every form of it; a
longer text covers only the hits written inside it, as whole words, so a
wire name such as Topos's `thread.started` event stays without the list
losing `thread`. A `path` is a glob the entry is limited to. Load rejects
an entry with no reason, with neither text nor path, or with a path that
is not a glob, per [[001-gate-principles]]. An entry no hit needs fails
the gate, as an unneeded allowance fails `platform-via-sdk`
([[028-platform-via-sdk]]). There is no line marker: every exception
lives in `.lateregate.yaml` beside its reason, as everywhere else here.

### A gate that reads nothing fails

A glob that matches no file, `spec_overview` over a directory with no
spec, and a block that names a list or an allowance and no place fail,
per [[001-gate-principles]]. So does a `words.vocabulary` that names a
missing file or a file with no `do_not_use` list: the failure names the
path and says to fix it or delete the key.

### Where the list lives

The words are generic and not secret, so the public gate holds them. An
embedded list runs on a laptop and on a runner that checks out nothing
but the repository, with `GOPROXY=off`, per [[001-gate-principles]], and
a change to it reaches a repository the way every other rule of the bar
does: with the release a repository pins.

### Alternatives considered

- **Read the list from the vocabulary in `latere-ai/onboarding`.** The
  first version did, through a path each repository named. That
  repository is private, so a runner had no file and the gate failed on
  every push of a repository that turned it on.
- **Fetch the list at run time.** Needs the network, and a credential for
  a private repository, on every run.
- **A copy in each repository.** Twenty copies of one list.
- **Inline markers.** No gate here has one, and the reason beside the
  exception is the part that matters.

## Acceptance criteria

- `lateregate list` on a repository whose `words` names no place shows
  `SKIP words words names nothing a model or a person reads`; with a
  place, the gate runs, over the embedded list alone or with the
  repository's own.
- A banned form in a model-facing file's body or its name, in a
  person-facing catalog's string values, and in a spec's title or
  Overview is reported with the file, the line, the word as written and
  the list's word, the reader, the reason and the preferred words.
- Catalog keys, comments, placeholders and template actions are not read,
  and neither are a spec's other sections, its archive or an excluded
  file.
- Matching is whole-word and case-insensitive, splits identifiers at `_`,
  `-` and a change of case, and matches a phrase across spaces, hyphens
  and underscores only.
- An allowance by word covers every form, by text only the hits inside
  it, by path only those files; one with no reason or no place fails the
  load; one no hit needs fails the gate.
- The embedded list holds to the rules of a list. A repository's list
  that is missing, holds no list, breaks a rule, or names a word or a
  form the embedded list carries fails with what to do, as do a glob
  that matches nothing and a spec directory with no spec.
- `internal/words` clears the 90% floor.

## Outcome

Shipped as written. `internal/words` carries the embedded list, its
rules, the readers and the matcher; `config.Words` the section and its
load rules; the bar entry applies when `words` names a place to read.
Coverage of the new package is 96.2%.

No repository turns it on yet. Run by name on 2026-10-25 against copies
of each repository's main, without editing them, over the embedded list
alone:

| Repository | Read | Hits | Findings | Words |
|---|---|---|---|---|
| Topos | `prompts/tools/*.md`, spec titles and overviews | 83 | 67 | thread 46, kill 8, one-shot 7, fold 6, lease 5, spawn 4, writer 4, lock 1, inbox 1, held call 1 |
| Latere Chat | `frontend/src/i18n/*.ts`, spec titles and overviews | 12 | 12 | fold 8, offer 2, thread 1, lock 1 |
| Platform | spec titles and overviews | 12 | 12 | lease 4, writer 3, one-shot 2, daemon 1, thread 1, spawn 1 |

Topos's tool descriptions were being renamed to the Claude Code names the
same day, so its model-facing count is the one before that change.
