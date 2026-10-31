// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package changelog holds the rule that a tag is a release and a release has
// notes.
//
// The notes live in CHANGELOG.md, one level-two section per tag, and the
// section is the body of the GitHub release. The same reader serves the three
// places the rule is enforced: the pre-push refuses a release tag whose commit
// has no section, the release workflow refuses the same tag and otherwise
// publishes the section, and the cut command writes the section from what
// sits under Unreleased before it tags. One implementation, so the hook, the
// pipeline and the cut cannot disagree on what a note is.
package changelog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Name is the file at the repository root.
const Name = "CHANGELOG.md"

// Unreleased is the heading the next tag's notes accumulate under.
const Unreleased = "Unreleased"

// Seed is the changelog init writes when a repository has none: the rule in
// two paragraphs, and the heading the first note goes under.
const Seed = `# Changelog

Every tag has a section here, and the section is the body of the GitHub
release. A tag without one is refused at the pre-push and fails the release
workflow. Write under ` + "`Unreleased`" + ` as work lands; ` + "`lateregate release vX.Y.Z`" + `
turns that into the tag's section, commits, tags and pushes.

A section says what changed for whoever uses the release, not what was
committed: the commit log already holds that.

## Unreleased
`

// zeroSHA is what git passes for a ref that does not exist on one side.
const zeroSHA = "0000000000000000000000000000000000000000"

// releaseTag is vMAJOR.MINOR.PATCH with an optional prerelease and build
// suffix. A moving major tag such as v1 is not a release, so a pipeline that
// repoints one is not asked for a note.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsReleaseTag reports whether a tag name is one a release is cut from.
func IsReleaseTag(name string) bool { return releaseTag.MatchString(name) }

var (
	// ErrNoSection is a changelog with no heading for the tag.
	ErrNoSection = errors.New("no section")
	// ErrEmpty is a heading with nothing but whitespace under it.
	ErrEmpty = errors.New("empty section")
)

// Section returns the body under the level-two heading whose second word is
// tag, trimmed. The section runs to the next level-two heading; a deeper
// heading is part of it. ErrNoSection when no heading names the tag, ErrEmpty
// when one does and holds only whitespace.
func Section(body, tag string) (string, error) {
	var sb strings.Builder
	found := false
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			if found {
				break
			}
			if fields := strings.Fields(line); len(fields) >= 2 && fields[1] == tag {
				found = true
			}
			continue
		}
		if found {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	if !found {
		return "", ErrNoSection
	}
	section := strings.TrimSpace(sb.String())
	if section == "" {
		return "", ErrEmpty
	}
	return section + "\n", nil
}

// Notes returns the section for tag, read from the working tree under root
// when ref is empty and from `git show ref:CHANGELOG.md` otherwise. The
// second form is what the pre-push needs: the tag being pushed may point at
// a commit that is not checked out. Every failure names the fix.
func Notes(root, tag, ref string, run gates.Exec) (string, error) {
	var body string
	if ref == "" {
		b, err := os.ReadFile(filepath.Join(root, Name))
		if err != nil {
			return "", fmt.Errorf("no %s in the working tree; run `lateregate init` to write one", Name)
		}
		body = string(b)
	} else {
		b, err := run(nil, false, "git", "show", ref+":"+Name)
		if err != nil {
			return "", fmt.Errorf("no %s at %s; run `lateregate init` to write one and commit it", Name, ref)
		}
		body = string(b)
	}
	section, err := Section(body, tag)
	switch {
	case errors.Is(err, ErrNoSection):
		return "", fmt.Errorf("%s has no section for %s; add `## %s - %s` with the notes, or run `lateregate release %s`",
			Name, tag, tag, "YYYY-MM-DD", tag)
	case errors.Is(err, ErrEmpty):
		return "", fmt.Errorf("%s has a heading for %s and nothing under it; a release has notes", Name, tag)
	case err != nil:
		return "", err
	}
	return section, nil
}

// Prepush refuses a release tag whose commit has no section.
//
// in carries the lines git hands a pre-push hook on stdin, one per ref:
// local ref, local sha, remote ref, remote sha. A line whose remote ref is a
// release tag under refs/tags/ and whose local sha is not zero is checked at
// that sha, whatever the local ref is called: `git push origin
// HEAD:refs/tags/v1.2.3` names the local ref HEAD, and a sha pushed straight
// to the tag names the sha itself, and both create the tag on the remote as
// `git push origin v1.2.3` does. A deletion is not a release, a branch is the
// lint's concern, and a moving major tag is neither.
func Prepush(root string, in io.Reader, out io.Writer, run gates.Exec) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		localSHA, remoteRef := fields[1], fields[2]
		tag, ok := strings.CutPrefix(remoteRef, "refs/tags/")
		if !ok || !IsReleaseTag(tag) || localSHA == zeroSHA {
			continue
		}
		notes, err := Notes(root, tag, localSHA, run)
		if err != nil {
			return fmt.Errorf("refusing to push %s: %w", tag, err)
		}
		_, _ = fmt.Fprintf(out, "%s has its release note (%d lines)\n", tag, strings.Count(notes, "\n"))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading the pushed refs: %w", err)
	}
	return nil
}

// Cut turns what sits under Unreleased into the section for version, then
// commits, tags, and pushes the commit to the release branch and the tag in
// one atomic push, so the pre-push sees both, the release workflow runs once,
// and either both land or neither does.
//
// It refuses everything Ready refuses, and asks again right before it writes:
// the bar runs for minutes between the two, and another checkout can push to
// the release branch or tag in that time. A refusal writes nothing. A push
// the remote rejects is undone locally, tag and commit, so the checkout is
// back where it started and the next run starts from a pull.
func Cut(root, version string, now time.Time, out io.Writer, run gates.Exec, stamps []config.Stamp) error {
	rel, err := ready(root, version, run)
	if err != nil {
		return err
	}
	heading := fmt.Sprintf("## %s - %s", version, now.Format(time.DateOnly))
	var sb strings.Builder
	moved := false
	for line := range strings.SplitSeq(rel.body, "\n") {
		sb.WriteString(line)
		sb.WriteByte('\n')
		if !moved && strings.TrimSpace(line) == "## "+Unreleased {
			sb.WriteByte('\n')
			sb.WriteString(heading)
			sb.WriteByte('\n')
			moved = true
		}
	}
	rewritten := strings.TrimRight(sb.String(), "\n") + "\n"
	if _, err := Section(rewritten, version); err != nil {
		return fmt.Errorf("the rewritten %s has no usable section for %s: %w", Name, version, err)
	}
	// Plan the stamps before writing anything: a bad stamp config refuses the
	// cut with a clean tree, the way every other refusal above does, rather
	// than leaving a moved changelog and half-stamped files to unwind.
	plans, err := planStamps(root, version, stamps)
	if err != nil {
		return err
	}

	// From the first write on, a failure puts the checkout back at the base
	// commit: ready found the tree clean there, so a hard reset drops exactly
	// what this cut wrote and committed, and nothing of anyone else's.
	tagged := false
	fail := func(err error, next string) error {
		if undo := rel.undo(version, tagged, run); undo != nil {
			by := "`git reset --hard " + rel.base + "`"
			if tagged {
				by = "`git tag -d " + version + "` and " + by
			}
			return fmt.Errorf("%w; undoing the cut failed too: %w; run %s before trying again", err, undo, by)
		}
		msg := fmt.Sprintf("nothing was pushed, and the checkout is back at %s", short(rel.base))
		if next != "" {
			msg += "; " + next
		}
		return fmt.Errorf("%w; %s", err, msg)
	}
	if err := os.WriteFile(filepath.Join(root, Name), []byte(rewritten), 0o644); err != nil {
		return fail(err, "")
	}
	_, _ = fmt.Fprintf(out, "%s: moved %s under %q\n", Name, Unreleased, heading)
	for _, p := range plans {
		if err := os.WriteFile(filepath.Join(root, p.file), p.content, 0o644); err != nil {
			return fail(err, "")
		}
		_, _ = fmt.Fprintf(out, "%s: stamped %s\n", p.file, version)
	}

	steps := [][]string{{"add", Name}}
	for _, p := range plans {
		steps = append(steps, []string{"add", p.file})
	}
	steps = append(steps,
		[]string{"commit", "-m", "changelog: " + version},
		[]string{"tag", "-a", version, "-m", version},
	)
	for _, args := range steps {
		if _, err := run(nil, true, "git", args...); err != nil {
			return fail(fmt.Errorf("git %s: %w", args[0], err), "")
		}
		tagged = tagged || args[0] == "tag"
	}
	// The branch is named on the remote side, so the push can move the
	// release branch and nothing else, never a branch named after the
	// checkout's, and the tag is named as a ref, so the pre-push reads its
	// line as a tag push. --atomic is the lock: when
	// the branch update is not a fast-forward because another release
	// landed first, the remote takes neither ref, and no tag points at a
	// commit off the branch.
	push := []string{"push", "--atomic", "origin", "HEAD:refs/heads/" + rel.branch, "refs/tags/" + version}
	if _, err := run(nil, true, "git", push...); err != nil {
		return fail(fmt.Errorf("git push: %w", err), rel.rejected(run))
	}
	_, _ = fmt.Fprintf(out, "%s: committed, tagged, pushed to %s\n", version, rel.branch)
	return nil
}

// Ready refuses a release that could not land as one fast-forward of the
// release branch, and writes nothing.
//
// The release command calls it before the bar runs, so a checkout that is
// behind, ahead of, or beside the release branch is refused in seconds
// rather than after the whole suite, and Cut calls it again before it
// writes. Its refusals are the cut's: a version that is not a release tag,
// a dirty tree, a HEAD that is not origin's tip of the release branch, a
// version origin already has or a newer one, a tag left in this checkout,
// and an empty Unreleased.
func Ready(root, version string, run gates.Exec) error {
	_, err := ready(root, version, run)
	return err
}

// release is what a cut goes on top of, as ready found it.
type release struct {
	// branch is origin's default branch, the one the push moves.
	branch string
	// base is the commit HEAD and origin's branch both named when ready
	// looked, and the commit a failed cut resets to.
	base string
	// body is CHANGELOG.md as it stands at base.
	body string
}

func ready(root, version string, run gates.Exec) (release, error) {
	if !IsReleaseTag(version) {
		return release{}, fmt.Errorf("usage: lateregate release vX.Y.Z (got %q)", version)
	}
	status, err := run(nil, false, "git", "status", "--porcelain")
	if err != nil {
		return release{}, fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return release{}, errors.New("the working tree is not clean; commit or stash first, so the tag holds only the changelog commit")
	}
	rel := release{branch: releaseBranch(run)}
	if err := rel.synced(run); err != nil {
		return release{}, err
	}
	head, err := run(nil, false, "git", "rev-parse", "HEAD")
	if err != nil {
		return release{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	rel.base = strings.TrimSpace(string(head))
	if err := newest(version, run); err != nil {
		return release{}, err
	}
	// Origin does not have it, so a tag here is one a cut created and never
	// pushed, or one made by hand; either way the cut's own tag would fail
	// on it after the commit is made.
	if _, err := run(nil, false, "git", "rev-parse", "-q", "--verify", "refs/tags/"+version); err == nil {
		return release{}, fmt.Errorf("%s already exists in this checkout, though not on origin; delete it with `git tag -d %s` and run again", version, version)
	}
	b, err := os.ReadFile(filepath.Join(root, Name))
	if err != nil {
		return release{}, fmt.Errorf("no %s in the working tree; run `lateregate init` to write one", Name)
	}
	rel.body = string(b)
	if _, err := Section(rel.body, Unreleased); err != nil {
		return release{}, fmt.Errorf("nothing under `## %s` in %s; a release has notes", Unreleased, Name)
	}
	return rel, nil
}

// releaseBranch is origin's default branch, read from origin/HEAD, and main
// when the clone never recorded one.
func releaseBranch(run gates.Exec) string {
	out, err := run(nil, false, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "main"
	}
	if branch, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "origin/"); ok && branch != "" {
		return branch
	}
	return "main"
}

// synced refuses unless HEAD is exactly origin's tip of the release branch,
// read fresh. A release is the commit CI verified plus the changelog commit:
// behind, the bar tests a tree that is not the branch and the push is
// rejected after the gates; ahead, the tag ships commits CI has not seen.
// The branch the checkout is on does not matter: the push names the release
// branch on the remote side, so a worktree on a branch of its own, or a
// detached HEAD, releases the default branch and creates no other.
func (r release) synced(run gates.Exec) error {
	if err := r.fetch(run); err != nil {
		return err
	}
	counts, err := run(nil, false, "git", "rev-list", "--left-right", "--count", r.tracking()+"...HEAD")
	if err != nil {
		return fmt.Errorf("git rev-list %s...HEAD: %w", r.tracking(), err)
	}
	var behind, ahead int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(counts)), "%d %d", &behind, &ahead); err != nil {
		return fmt.Errorf("git rev-list %s...HEAD printed %q: %w", r.tracking(), counts, err)
	}
	switch {
	case behind > 0 && ahead > 0:
		return fmt.Errorf("this checkout and %s have diverged: %s has %s this checkout lacks, and this checkout has %s it lacks; "+
			"rebase onto %s, push and let CI verify the result, then run again",
			r.tracking(), r.branch, commits(behind), commits(ahead), r.tracking())
	case behind > 0:
		return fmt.Errorf("%s has %s this checkout lacks; run `git pull --ff-only` and run again", r.branch, commits(behind))
	case ahead > 0:
		return fmt.Errorf("this checkout has %s %s lacks; push them and let CI verify them first, then run again", commits(ahead), r.branch)
	}
	return nil
}

// tracking is the remote-tracking ref of the release branch.
func (r release) tracking() string { return "origin/" + r.branch }

// fetch brings origin's tip of the release branch into the remote-tracking
// ref. The refspec is explicit, so a clone whose fetch config does not cover
// the branch still compares against the tip and not against a stale ref.
// Tags are read from origin directly, so none are fetched here.
func (r release) fetch(run gates.Exec) error {
	refspec := "+refs/heads/" + r.branch + ":refs/remotes/" + r.tracking()
	if _, err := run(nil, true, "git", "fetch", "--quiet", "--no-tags", "origin", refspec); err != nil {
		return fmt.Errorf("git fetch origin %s: %w", r.branch, err)
	}
	return nil
}

// newest refuses a version origin already has, or one that is not newer by
// semver than every release tag origin holds, and names origin's newest. The
// read goes to origin, not to the local tags: a tag another checkout pushed
// is invisible here until a fetch brings it.
func newest(version string, run gates.Exec) error {
	out, err := run(nil, false, "git", "ls-remote", "--tags", "--refs", "origin", "refs/tags/v*")
	if err != nil {
		return fmt.Errorf("reading the tags on origin: git ls-remote: %w", err)
	}
	highest, taken := "", false
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		tag, ok := strings.CutPrefix(fields[1], "refs/tags/")
		if !ok || !IsReleaseTag(tag) {
			continue
		}
		taken = taken || tag == version
		if highest == "" || semver.Compare(tag, highest) > 0 {
			highest = tag
		}
	}
	switch {
	case taken:
		return fmt.Errorf("%s already exists on origin, and the newest release there is %s; pick a version above it", version, highest)
	case highest != "" && semver.Compare(highest, version) >= 0:
		return fmt.Errorf("origin already has %s, which is not older than %s; pick a version above %s", highest, version, highest)
	}
	return nil
}

// undo puts the checkout back at the base commit after a failed cut: the tag
// first, if the cut made it, then a hard reset that drops the changelog
// commit and the written files with it.
func (r release) undo(version string, tagged bool, run gates.Exec) error {
	if tagged {
		if _, err := run(nil, false, "git", "tag", "-d", version); err != nil {
			return fmt.Errorf("git tag -d %s: %w", version, err)
		}
	}
	if _, err := run(nil, false, "git", "reset", "--hard", "-q", r.base); err != nil {
		return fmt.Errorf("git reset --hard %s: %w", short(r.base), err)
	}
	return nil
}

// rejected says what to do after a failed push, reading origin once more to
// tell a release branch that moved, which is the race the push is atomic
// for, from a push that failed for its own reason, which git has already
// printed above.
func (r release) rejected(run gates.Exec) string {
	moved := fmt.Sprintf("%s moved since the check, so run `git pull --ff-only` and run again, "+
		"and if `## %s` is then empty, another release already shipped these changes", r.branch, Unreleased)
	if err := r.fetch(run); err != nil {
		return fmt.Sprintf("origin could not be read again to tell why (%v): if %s", err, moved)
	}
	tip, err := run(nil, false, "git", "rev-parse", r.tracking())
	if err != nil {
		return fmt.Sprintf("%s could not be read to tell why (%v): if %s", r.tracking(), err, moved)
	}
	if strings.TrimSpace(string(tip)) != r.base {
		return moved
	}
	return r.branch + " has not moved, so the reason is in git's output above: fix it and run again"
}

// commits counts commits in words.
func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// short is a commit's abbreviated name, for messages.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// versionInText finds a vX.Y.Z inside a stamp's matched span, unanchored,
// unlike releaseTag which matches a whole tag string.
var versionInText = regexp.MustCompile(`v\d+\.\d+\.\d+`)

// stampPlan is one file's stamped content, computed but not yet written.
type stampPlan struct {
	file    string
	content []byte
}

// planStamps rewrites the version inside each configured file's marker to the
// release version and returns the planned writes, one per file in the order
// the files first appear, without touching disk. Several stamps may name one
// file: they apply in order to the same buffer, so each pattern is matched
// against the file as the stamps before it left it, and every stamp lands in
// the one write. A pattern that does not match exactly once, or matches a
// span with no version and no declared placeholder, is a configuration
// error: better a refused release than one that ships a file naming the
// wrong version. A file already naming the release version plans a write to
// identical content, which git commits as nothing.
func planStamps(root, version string, stamps []config.Stamp) ([]stampPlan, error) {
	var files []string
	contents := map[string][]byte{}
	for _, s := range stamps {
		// Cleaned, so two spellings of one path share one buffer.
		file := filepath.Clean(s.File)
		b, ok := contents[file]
		if !ok {
			var err error
			b, err = os.ReadFile(filepath.Join(root, file))
			if err != nil {
				return nil, fmt.Errorf("release stamp %s: %w", s.File, err)
			}
			files = append(files, file)
		}
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			return nil, fmt.Errorf("release stamp %s: pattern %q: %w", s.File, s.Pattern, err)
		}
		locs := re.FindAllIndex(b, -1)
		if len(locs) != 1 {
			return nil, fmt.Errorf("release stamp %s: pattern %q matched %d times, want exactly one", s.File, s.Pattern, len(locs))
		}
		// A declared placeholder moves as a version does: the first release
		// replaces it, and every release after finds a version in its place.
		move := versionInText
		if s.Placeholder != "" {
			move = regexp.MustCompile(versionInText.String() + "|" + regexp.QuoteMeta(s.Placeholder))
		}
		lo, hi := locs[0][0], locs[0][1]
		if !move.Match(b[lo:hi]) {
			if s.Placeholder != "" {
				return nil, fmt.Errorf("release stamp %s: pattern %q holds neither a vX.Y.Z nor the placeholder %q to move", s.File, s.Pattern, s.Placeholder)
			}
			return nil, fmt.Errorf("release stamp %s: pattern %q holds no vX.Y.Z to move", s.File, s.Pattern)
		}
		content := make([]byte, 0, len(b))
		content = append(content, b[:lo]...)
		content = append(content, move.ReplaceAll(b[lo:hi], []byte(version))...)
		content = append(content, b[hi:]...)
		contents[file] = content
	}
	plans := make([]stampPlan, 0, len(files))
	for _, file := range files {
		plans = append(plans, stampPlan{file: file, content: contents[file]})
	}
	return plans, nil
}
