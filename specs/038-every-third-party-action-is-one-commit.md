---
title: Every third-party action is the organization's one commit
status: complete
depends_on:
  - 001-gate-principles.md
  - 033-what-a-release-reads-is-checked-on-push.md
affects:
  - internal/actionpins/
  - internal/gates/
  - internal/bar/
  - cmd/lateregate/
  - docs/gates.md
  - docs/commands.md
  - CHANGELOG.md
  - .github/dependabot.yml
effort: medium
trigger: every Latere repository pins each third-party action to the same commit, and nothing holds them there, so one repository's Dependabot or a hand edit can move one action in one repository
created: 2026-11-05
updated: 2026-11-05
author: changkun
dispatched_task_id: null
---

# Every third-party action is the organization's one commit

## The problem

Every repository pins each third-party GitHub Action to a commit, and the
commit is the same everywhere (`actions/checkout` at one SHA, commented
`# v7.0.1`). Nothing keeps it so. One repository's Dependabot, or a hand
edit, can move one action in one repository, and the drift is found when an
action misbehaves in only some of them.

GitHub's job token cannot push changes to `.github/workflows`, so no
workflow can realign the pins by itself.

## The decision

**The list is in the binary.** `internal/actionpins/action.yml` is a
composite action that is never run: a `uses: owner/repo@commit # vX.Y.Z`
step per action. Written that way, Dependabot's github-actions updater keeps
it current, and `go:embed` carries it into `lateregate`, which every
repository pins and runs on every push and on a laptop, offline.

**The `action-pins` gate reads the tree against it.** It applies when git
tracks a workflow under `.github/workflows` or an `action.yml` or
`action.yaml`. Each `uses:` of a job, a job's step and a composite action's
step, outside `testdata/`, is keyed by owner and repository. Local
references, `docker://` images and `latere-ai` repositories are left to other
rules. The rest fail, one `file:line` each, when the action is not listed,
the ref is not the list's commit, or the trailing comment is not
`# <version>`. The gate takes the generic dated waiver every gate has and no
special handling.

**`lateregate action-pins -w` fixes the tree.** It rewrites each listed
action's ref and comment by line, so indentation, a leading `- ` and quotes
survive, and leaves an unlisted action as it is, still reported.

**A change to the list ships with its sweep.** The release that changes it is
followed the same day by one commit per repository that pins ci-gate, moving
the gate pin and running `action-pins -w`. Otherwise each repository's daily
gate bump fails on the old pins and opens an issue. Dependabot watches the
list and ci-gate's own workflows in one group, so one pull request moves
both and ci-gate's own gate passes on it.

## What was considered

- **A list in latere-ai/ci, read over the network.** Rejected: the gate runs
  offline on a laptop, and a check that needs a fetch is skipped or flaky
  exactly where it is wanted.
- **Realigning in the daily gate bump.** Rejected: the job token cannot write
  workflow files, so the bump could only open an issue, which is what the
  gate's failure already does.

## Acceptance criteria

- The embedded list parses, keys each action once by owner and repository,
  and holds 40-character commits and `vX.Y.Z` versions.
- Each of an unlisted action, a tag, a branch, a short commit, another
  commit and a wrong or missing comment is a finding naming the file and
  line; a reusable-workflow job, a composite action, quoted refs and both
  `- uses:` and `uses:` forms are read; local, `docker://` and `latere-ai`
  references and `testdata/` are not.
- `-w` keeps indentation, `- ` and quotes, replaces another comment, leaves
  an unlisted action alone and a second run changes nothing.
- This repository's own tree passes the gate.

## Outcome

Implemented as specified. A reference whose commit is wrong gets the one
finding for the ref, not a second for its comment, since the same command
sets both.
