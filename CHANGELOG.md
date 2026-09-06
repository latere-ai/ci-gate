# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under `Unreleased` as work lands; `lateregate release vX.Y.Z`
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased

## v0.30.0 - 2026-09-06

- The licence gate accepts `license.spdx: LicenseRef-Proprietary`, the SPDX
  form for terms not on the licence list. The root `LICENSE` must then reserve
  all rights and grant no licence, and every file carries the identifier the
  same way an open source repository does, so a proprietary repository is
  checked instead of waived.

### Added

- `license` checks the root `LICENSE` text against `license.spdx` through a
  fingerprint per identifier, so a declaration that names one licence over a
  root file carrying another fails with both named. An identifier the table
  does not know fails closed. This is the mismatch pkg carried for four
  days: `Apache-2.0` on 264 files above an MIT `LICENSE`.

## v0.29.2 - 2026-09-06

- The pre-push hook no longer fails a push that changes a Go file under a
  nested module (a directory with its own `go.mod`, such as a spike tool or
  an example). Such a file is not a package of the main module, so the hook
  skips it the way it skips `testdata`, and the full gate remains the bar.

## v0.29.1 - 2026-09-06

This repository now publishes its own GitHub releases from this file,
through `notes-release.yml` in latere-ai/ci. v0.29.0 was tagged before that
pipeline existed and has no release object; its section below is the note.

## v0.29.0 - 2026-09-06

A tag is a release, and a release has notes. The changelog rule pkg kept in
three shell scripts is now in the binary, for every repository.

### Added

- `lateregate release-notes TAG [REF]` prints the `CHANGELOG.md` section
  for a tag, read from the working tree or at a git ref, and fails naming
  the fix when the file, the heading, or the notes are missing. The
  release workflows in latere-ai/ci call it and fail closed.
- `lateregate release vX.Y.Z` moves what sits under `## Unreleased` into a
  dated section for the version, commits, creates an annotated tag, and
  pushes `HEAD` and the tag in one push. It refuses a dirty tree, an
  existing tag, and an empty `Unreleased`.

### Changed

- `lateregate prepush` refuses a release tag (`vMAJOR.MINOR.PATCH`, with an
  optional prerelease or build suffix) whose commit has no changelog
  section, before it lints. A moving major tag such as `v1`, a tag
  deletion, and a branch are unaffected.
- `lateregate contract` wants `CHANGELOG.md` tracked at the root with a
  `## Unreleased` heading, and reports a `Makefile` target named `release`
  or `release-notes` that does not delegate. `lateregate init` writes the
  seed changelog when there is none.
