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
  - internal/words/ (new)
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

The organization decided the words once, in its vocabulary
(`vocabulary.yaml` in `latere-ai/onboarding`): operating-system and
process jargon stays out of tool names, tool descriptions, screens and
spec overviews, and the words that name a screen's mechanism stay off
screens. Model-facing tools reuse the names models know from Claude Code
where one exists.

## The decision

A gate, `words`, that a repository turns on in `.lateregate.yaml` by
naming the vocabulary file and the places each reader reads:

```yaml
words:
  vocabulary: ../onboarding/vocabulary.yaml
  model_facing: [prompts/tools/*.md]
  person_facing: [frontend/src/i18n/en.ts]
  spec_overview: true
```

### The list

The vocabulary's `do_not_use` section is a list of items, each with:

- `word`, the word as the list names it;
- `forms`, the written forms matched, each a whole word or a run of whole
  words; empty means the word alone. A word banned in one sense lists the
  phrases that carry it: `look` as a noun is `a look`, `the look` and the
  like, and `share` of credit is `share of` and `'s share`;
- `scopes`, the readers it is banned for: `model-facing`,
  `person-facing`, `spec-overview`;
- `reason`, one line printed with every hit;
- `prefer`, the words to write instead.

The gate decodes that key alone, loosely: the vocabulary's own validator
holds the file to its schema, and a field it adds later is no reason for
every repository's gate to fail. An item the gate cannot use fails it: no
word, no scope, a scope it does not read, no reason, nothing to prefer,
or a form with no letter in it. Each would otherwise be a word silently
matched nowhere.

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
spec, and a block that names a vocabulary and no place fail, per
[[001-gate-principles]]. So does a missing vocabulary file: the failure
names the path it looked at and says to check `latere-ai/onboarding` out
there or point the key at the file.

### Where the list comes from

The vocabulary lives in a private repository and this gate is public, so
the gate neither embeds the file, which names internal hosts and
repositories, nor fetches it, which would break the rule that every gate
runs from the module cache with `GOPROXY=off`. The repository names the
file instead. On a laptop the workspace keeps the onboarding checkout
beside the repository, so `../onboarding/vocabulary.yaml` resolves. A
runner that checks out the repository alone has no such file, and the
gate fails there with the missing-file message; it is not skipped, since
a skip would be a pass over nothing.

### Alternatives considered

- **Embed the list in the gate.** It runs everywhere, and a list change
  waits for a gate release, but it is a second copy of the vocabulary's
  section in a public repository, and the copy drifts.
- **Fetch the vocabulary at run time.** Needs the network and a
  credential for a private repository on every run.
- **A copy in each repository.** Twenty copies of one list.
- **Inline markers.** No gate here has one, and the reason beside the
  exception is the part that matters.

## Acceptance criteria

- `lateregate list` on a repository without `words.vocabulary` shows
  `SKIP words words.vocabulary names no vocabulary file`; with it, the
  gate runs.
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
- A missing vocabulary file, one with no list, an item the gate cannot
  use, a glob that matches nothing and a spec directory with no spec each
  fail with what to do.
- `internal/words` clears the 90% floor.

## Outcome

Shipped as written. `internal/words` carries the list loader, the
readers and the matcher; `config.Words` the section and its load rules;
the bar entry applies when `words.vocabulary` is set. Coverage of the new
package is 96.5%.

No repository turns it on yet. Run by name on 2026-10-25 against copies
of each repository's main, without editing them:

| Repository | Read | Hits | Findings | Words |
|---|---|---|---|---|
| Topos | `prompts/tools/*.md`, spec titles and overviews | 83 | 67 | thread 46, kill 8, one-shot 7, fold 6, lease 5, spawn 4, writer 4, lock 1, inbox 1, held call 1 |
| Latere Chat | `frontend/src/i18n/*.ts`, spec titles and overviews | 12 | 12 | fold 8, offer 2, thread 1, lock 1 |
| Platform | spec titles and overviews | 12 | 12 | lease 4, writer 3, one-shot 2, daemon 1, thread 1, spawn 1 |

Topos's tool descriptions were being renamed to the Claude Code names the
same day, so its model-facing count is the one before that change.
