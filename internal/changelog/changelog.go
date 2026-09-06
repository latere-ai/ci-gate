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
// in carries the lines git hands a pre-push hook on stdin, one per ref. A
// local ref under refs/tags/ whose name is a release tag and whose local sha
// is not zero is checked at that sha. A deletion is not a release, a branch
// is the lint's concern, and a moving major tag is neither.
func Prepush(root string, in io.Reader, out io.Writer, run gates.Exec) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		localRef, localSHA := fields[0], fields[1]
		tag, ok := strings.CutPrefix(localRef, "refs/tags/")
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
// commits, tags, and pushes HEAD and the tag in one push, so the pre-push
// sees both and the release workflow runs once.
//
// It refuses a version that is not a release tag, a dirty tree, an existing
// tag, and an empty Unreleased, and writes nothing in any of those cases: a
// tag is a release, and a release has notes.
func Cut(root, version string, now time.Time, out io.Writer, run gates.Exec) error {
	if !IsReleaseTag(version) {
		return fmt.Errorf("usage: lateregate release vX.Y.Z (got %q)", version)
	}
	status, err := run(nil, false, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return errors.New("the working tree is not clean; commit or stash first, so the tag holds only the changelog commit")
	}
	if _, err := run(nil, false, "git", "rev-parse", "-q", "--verify", "refs/tags/"+version); err == nil {
		return fmt.Errorf("%s already exists", version)
	}
	path := filepath.Join(root, Name)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no %s in the working tree; run `lateregate init` to write one", Name)
	}
	body := string(b)
	if _, err := Section(body, Unreleased); err != nil {
		return fmt.Errorf("nothing under `## %s` in %s; a release has notes", Unreleased, Name)
	}

	heading := fmt.Sprintf("## %s - %s", version, now.Format(time.DateOnly))
	var sb strings.Builder
	moved := false
	for line := range strings.SplitSeq(body, "\n") {
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
	if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "%s: moved %s under %q\n", Name, Unreleased, heading)

	steps := [][]string{
		{"add", Name},
		{"commit", "-m", "changelog: " + version},
		{"tag", "-a", version, "-m", version},
		{"push", "origin", "HEAD", version},
	}
	for _, args := range steps {
		if _, err := run(nil, true, "git", args...); err != nil {
			return fmt.Errorf("git %s: %w", args[0], err)
		}
	}
	_, _ = fmt.Fprintf(out, "%s: committed, tagged, pushed\n", version)
	return nil
}
