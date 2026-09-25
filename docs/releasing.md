# Releasing with `lateregate`

A tag is a release, and a release has notes. `CHANGELOG.md` holds one
level-two section per tag, and that section is the body of the GitHub
release. Three places enforce it with one implementation: the pre-push
refuses a release tag whose commit has no section, the release workflows
in [`latere-ai/ci`](https://github.com/latere-ai/ci) read the section at
the tag and fail without it, and `lateregate release` writes the section
as it cuts the tag.

## The changelog

A heading's second word names the tag, so `## v0.29.0 - 2026-09-06` and
`## v0.29.0` both name `v0.29.0`, and the section runs to the next
level-two heading. `## Unreleased` holds what the next tag will say; write
under it as work lands. A section says what changed for whoever uses the
release, not what was committed: the binary writes no draft from the
commit log.

`lateregate release-notes TAG [REF]` prints the section for `TAG`, read
from the working tree or from `REF:CHANGELOG.md`, and fails naming the fix
when the file, the heading, or the notes are missing.

## Cutting a release: `release`

```sh
go tool lateregate release v1.4.0
```

In order, it:

1. reads CI through the GitHub API and refuses while it is red (below);
2. runs the whole bar on the tree about to be tagged;
3. refuses a version that is not `vMAJOR.MINOR.PATCH` (with an optional
   prerelease or build suffix), a dirty working tree, a tag that already
   exists, and an empty `## Unreleased`;
4. writes `## v1.4.0 - <today>` under a fresh `## Unreleased`, and
   rewrites each `release.stamp` file's version marker to `v1.4.0`;
5. commits `changelog: v1.4.0`, creates an annotated tag, and pushes
   `HEAD` and the tag in one push, so the release workflow runs once.

A `Makefile` target is a convenience:

```make
release:
	@go tool lateregate release $(VERSION)
```

### Stamps

A file that names the current release (a pinned image tag in a production
overlay, a "vX.Y.Z is the current release" line in `SECURITY.md`) goes
stale at every cut unless someone edits it by hand. `release.stamp` moves
it in the release's own commit:

```yaml
release:
  stamp:
    - file: deploy/prod/kustomization.yaml
      pattern: 'newTag: v\d+\.\d+\.\d+'
```

`pattern` is a regular expression that must match the file exactly once
and hold exactly one `vX.Y.Z`; the cut replaces that version and nothing
else. The pattern is the marker's context, not the bare version, which
would match every version the file mentions. A stamp that matches zero or
several times refuses the cut before anything is written.

Several entries may name one file, one per marker, such as two image lines
in a compose file. They apply in the order listed, each to the file as the
entries before it left it, and the file is written once with every marker
moved.

A marker no release has pinned yet, such as `newTag: unreleased` in a
deploy overlay, names that literal as the stamp's `placeholder`. The cut
then replaces the placeholder inside the match as it replaces a version, so
the first release pins itself, and every release after moves the version
that took its place. The pattern has to match the marker in both states:

```yaml
release:
  stamp:
    - file: deploy/prod/kustomization.yaml
      pattern: 'newTag: (v\d+\.\d+\.\d+|unreleased)'
      placeholder: unreleased
```

Without `placeholder`, a match that holds no `vX.Y.Z` refuses the cut. With
it, a match that holds neither a `vX.Y.Z` nor the placeholder refuses it.

### The CI guard

The bar a cut runs says the tree about to be tagged is sound. It says
nothing about what the last push to the default branch did, or whether
the previous release published anything. A failing release run reaches a
person only if a person reads CI, and the cut is the moment someone is
already waiting. So before it runs the bar, `release` reads GitHub and
refuses on three things:

- **A red run in this tag's window.** For every workflow with a completed
  run on the default branch at `HEAD` or an ancestor back to the previous
  release tag, the latest such run must have ended on an answer.
  `failure`, `cancelled`, `timed_out`, `startup_failure` and
  `action_required` are not answers. A run cancelled because a newer run
  of the same workflow replaced it is read past, the way the newer run is
  while it is still in progress, so a concurrency group that cancels
  superseded runs changes nothing here.
- **A red release run on the previous tag.** The run the last tag started.
- **A previous tag with no release to show for it.** If that run went
  green, a GitHub Release must exist for the tag. A workflow can finish and
  publish nothing, and this is the check that catches it.

A window where nothing has completed yet is not green either. It is
unknown, and the guard says so rather than passing. The guard runs before
the bar because a red default branch is three API reads and the bar is a
full instrumented suite.

Every refusal names the run, the failing job and its URL, and ends with
one line saying who acts:

```
lateregate: not releasing v0.39.0: ci is red
  ci #1284 failure, job "gate (cover)"
  https://github.com/latere-ai/ci-gate/actions/runs/1284
CODE: fix and push, then cut again
```

The last line is read from the failing job's log:

| Line | What the log named |
|---|---|
| `BUDGET: the maintainer must act (…)` | Actions minutes, billing, a usage or spending limit, runner capacity, an org policy refusal. Only the account owner can move it; the phrase that matched is in the parenthesis so it can be forwarded as evidence. |
| `INFRA: re-run the job, then cut again` | A registry refusal, a reset connection, a certificate or a name that did not resolve. |
| `CODE: fix and push, then cut again` | Anything else, including a log that could not be read. |

A `timed_out` run is `INFRA` unless its log names a test (`--- FAIL:`,
`panic: test timed out` and the rest), because a suite that ran out of
time is the suite's problem and re-running it spends the clock twice. A
`startup_failure` has no job log of its own, so its message classifies it:
one naming an org policy refusal or an exhausted allowance is `BUDGET`,
one naming nothing is `CODE`.

A tag whose run went green and published nothing reads:

```
lateregate: not releasing v0.39.0: v0.38.0 deployed but published nothing
  release #1201 success, no GitHub Release exists for v0.38.0
  https://github.com/latere-ai/ci-gate/actions/runs/1201
CODE: fix and push, then cut again
```

`lateregate release -force-red vX.Y.Z` cuts anyway. It does not skip the
check: it overrides the result and prints every finding it is overriding.
It is the maintainer's escape hatch and belongs in no pipeline.

The reads go to the GitHub REST API. The token comes from `GH_TOKEN`, then
`GITHUB_TOKEN`, then `gh auth token`, so an authenticated `gh` on a laptop
and a runner's own token both work with nothing to configure.
`GITHUB_API_URL` points the reads at an enterprise host.

```yaml
release:
  require_green: false
```

turns the guard off. Without it a tag still deploys, its notes may still
not publish, and the first person to find out is a reader who wanted to
know what changed. A repository that sets it should say in a comment
beside the key who checks CI instead.
