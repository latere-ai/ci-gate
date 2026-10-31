// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package changelog

import (
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

const sample = `# Changelog

## Unreleased

## v1.2.3 - 2026-09-06

One line about the release.

### Added

- a thing

## v1.2.2 - 2026-09-01

Older.
`

func TestSectionRunsToTheNextLevelTwoHeading(t *testing.T) {
	got, err := Section(sample, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	want := "One line about the release.\n\n### Added\n\n- a thing\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "Older") {
		t.Error("the section must stop at the next tag's heading")
	}
}

func TestSectionNamesTheTagByTheSecondWord(t *testing.T) {
	if _, err := Section("## v9.9.9\ntext\n", "v9.9.9"); err != nil {
		t.Errorf("a heading with no date still names the tag: %v", err)
	}
	if _, err := Section("## Release v9.9.9\ntext\n", "v9.9.9"); !errors.Is(err, ErrNoSection) {
		t.Errorf("a heading whose second word is not the tag does not name it, got %v", err)
	}
}

func TestSectionMissingAndEmpty(t *testing.T) {
	if _, err := Section(sample, "v0.0.1"); !errors.Is(err, ErrNoSection) {
		t.Errorf("got %v", err)
	}
	if _, err := Section(sample, "Unreleased"); !errors.Is(err, ErrEmpty) {
		t.Errorf("a heading with only whitespace under it is empty, got %v", err)
	}
	if _, err := Section("", "v1.0.0"); !errors.Is(err, ErrNoSection) {
		t.Errorf("got %v", err)
	}
}

func TestIsReleaseTag(t *testing.T) {
	for _, ok := range []string{"v0.1.0", "v10.20.30", "v1.2.3-rc1", "v1.2.3-rc.1+build.5", "v1.2.3+sha.abc"} {
		if !IsReleaseTag(ok) {
			t.Errorf("%s is a release tag", ok)
		}
	}
	for _, no := range []string{"v1", "v1.2", "1.2.3", "V1.2.3", "v1.2.3.4", "latest", "v1.2.3 "} {
		if IsReleaseTag(no) {
			t.Errorf("%s is not a release tag", no)
		}
	}
}

func TestNotesReadsTheWorkingTree(t *testing.T) {
	dir := t.TempDir()
	if _, err := Notes(dir, "v1.2.3", "", nil); err == nil || !strings.Contains(err.Error(), "no CHANGELOG.md in the working tree") {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Notes(dir, "v1.2.3", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "One line about the release.") {
		t.Errorf("got %q", got)
	}
	_, err = Notes(dir, "v0.0.1", "", nil)
	if err == nil || !strings.Contains(err.Error(), "no section for v0.0.1") || !strings.Contains(err.Error(), "lateregate release v0.0.1") {
		t.Errorf("a missing section names the tag and the cut command, got %v", err)
	}
	_, err = Notes(dir, "Unreleased", "", nil)
	if err == nil || !strings.Contains(err.Error(), "nothing under it") {
		t.Errorf("an empty section says so, got %v", err)
	}
}

// show answers git show <ref>:CHANGELOG.md from a map of ref to body.
func show(t *testing.T, calls *[]string, bodies map[string]string) gates.Exec {
	t.Helper()
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if name != "git" || len(args) != 2 || args[0] != "show" {
			t.Fatalf("unexpected call %s %v", name, args)
		}
		ref, _, _ := strings.Cut(args[1], ":")
		body, ok := bodies[ref]
		if !ok {
			return nil, errors.New("exit 128")
		}
		return []byte(body), nil
	}
}

func TestNotesReadsTheFileAtARef(t *testing.T) {
	var calls []string
	run := show(t, &calls, map[string]string{"abc": sample})
	got, err := Notes(t.TempDir(), "v1.2.3", "abc", run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "One line") {
		t.Errorf("got %q", got)
	}
	if len(calls) != 1 || calls[0] != "git show abc:CHANGELOG.md" {
		t.Errorf("reads the file at the ref, got %v", calls)
	}
	_, err = Notes(t.TempDir(), "v1.2.3", "nope", run)
	if err == nil || !strings.Contains(err.Error(), "no CHANGELOG.md at nope") {
		t.Errorf("got %v", err)
	}
}

const (
	sha1 = "1111111111111111111111111111111111111111"
	sha2 = "2222222222222222222222222222222222222222"
)

func TestPrepushRefusesAReleaseTagWithoutASection(t *testing.T) {
	var calls []string
	run := show(t, &calls, map[string]string{sha1: sample, sha2: "# Changelog\n\n## Unreleased\n"})
	var sb strings.Builder
	err := Prepush(t.TempDir(), strings.NewReader("refs/tags/v1.2.3 "+sha1+" refs/tags/v1.2.3 "+zeroSHA+"\n"), &sb, run)
	if err != nil {
		t.Fatalf("a tag with a section passes: %v", err)
	}
	if !strings.Contains(sb.String(), "v1.2.3 has its release note") {
		t.Errorf("got %q", sb.String())
	}
	err = Prepush(t.TempDir(), strings.NewReader("refs/tags/v1.2.4 "+sha2+" refs/tags/v1.2.4 "+zeroSHA+"\n"), &sb, run)
	if err == nil || !strings.Contains(err.Error(), "refusing to push v1.2.4") || !strings.Contains(err.Error(), "no section for v1.2.4") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "lateregate release v1.2.4") {
		t.Errorf("the refusal names the fix, got %v", err)
	}
}

// What decides a check is the ref the remote receives, not the local ref's
// name. `git push origin v1.2.3` names the local ref refs/tags/v1.2.3,
// `git push origin HEAD:refs/tags/v1.2.3` names it HEAD, and a sha pushed
// straight to the tag names the sha itself; all three create the release tag
// on the remote, so all three are checked at the pushed sha. The lines are
// the ones git hands the hook.
func TestPrepushChecksATagPushWhateverTheLocalRef(t *testing.T) {
	for _, tc := range []struct{ name, local string }{
		{"a tag", "refs/tags/v1.2.3"},
		{"HEAD", "HEAD"},
		{"a sha", sha1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := tc.local + " " + sha1 + " refs/tags/v1.2.3 " + zeroSHA + "\n"

			var calls []string
			var sb strings.Builder
			err := Prepush(t.TempDir(), strings.NewReader(line), &sb, show(t, &calls, map[string]string{sha1: sample}))
			if err != nil {
				t.Fatalf("a tag with a section passes: %v", err)
			}
			if len(calls) != 1 || calls[0] != "git show "+sha1+":"+Name {
				t.Errorf("the changelog is read at the pushed sha, got %v", calls)
			}
			if !strings.Contains(sb.String(), "v1.2.3 has its release note") {
				t.Errorf("got %q", sb.String())
			}

			calls = nil
			err = Prepush(t.TempDir(), strings.NewReader(line), &sb,
				show(t, &calls, map[string]string{sha1: "# Changelog\n\n## Unreleased\n"}))
			if err == nil || !strings.Contains(err.Error(), "refusing to push v1.2.3") || !strings.Contains(err.Error(), "no section for v1.2.3") {
				t.Fatalf("a tag without a section is refused, got %v", err)
			}
		})
	}
}

// A moving major tag is not a release, a deletion pushes nothing, and a push
// to a branch creates no tag on the remote, whatever the local ref is called:
// a local tag pushed to refs/heads/main is a branch push. None of them reads
// the changelog.
func TestPrepushIgnoresWhatIsNotARelease(t *testing.T) {
	var calls []string
	run := show(t, &calls, nil)
	refs := "refs/tags/v1 " + sha1 + " refs/tags/v1 " + zeroSHA + "\n" +
		"HEAD " + sha1 + " refs/tags/v1 " + zeroSHA + "\n" +
		"refs/tags/v1.2.3 " + zeroSHA + " refs/tags/v1.2.3 " + sha1 + "\n" +
		"(delete) " + zeroSHA + " refs/tags/v1.2.3 " + sha1 + "\n" +
		"refs/heads/main " + sha1 + " refs/heads/main " + sha2 + "\n" +
		"HEAD " + sha1 + " refs/heads/main " + sha2 + "\n" +
		"refs/tags/v1.2.3 " + sha1 + " refs/heads/main " + sha2 + "\n" +
		"garbage line\n"
	var sb strings.Builder
	if err := Prepush(t.TempDir(), strings.NewReader(refs), &sb, run); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Errorf("a moving major tag, a deletion and a branch read nothing, got %v", calls)
	}
}

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"HOME="+dir, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// clone is a repository with a bare origin, one commit, and a changelog with
// notes under Unreleased.
func clone(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	git(t, base, "init", "--bare", "-q", "-b", "main", origin)
	dir := filepath.Join(base, "work")
	git(t, base, "clone", "-q", origin, dir)
	git(t, dir, "checkout", "-q", "-b", "main")
	body := "# Changelog\n\n## Unreleased\n\nA note for the next tag.\n\n## v0.0.9 - 2026-01-01\n\nOld.\n"
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", Name)
	git(t, dir, "commit", "-q", "-m", "seed")
	git(t, dir, "push", "-q", "-u", "origin", "main")
	return dir
}

// publish pushes the clone's main to origin, so a commit a test makes is one
// CI would have seen before the cut.
func publish(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "push", "-q", "origin", "main")
}

// another is a second clone of dir's origin, on main: the other session.
func another(t *testing.T, dir string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	git(t, dir, "clone", "-q", "-b", "main", filepath.Join(filepath.Dir(dir), "origin.git"), other)
	return other
}

// land commits one file in dir and pushes it to origin's main.
func land(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", file)
	git(t, dir, "commit", "-q", "-m", "land "+file)
	publish(t, dir)
}

func realExec(t *testing.T, dir string) gates.Exec {
	t.Helper()
	var sink strings.Builder
	inner := gates.OSExec(dir, &sink)
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		if name == "git" {
			env = append(os.Environ(),
				"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
				"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
				"HOME="+dir, "GIT_CONFIG_NOSYSTEM=1")
		}
		return inner(env, stream, name, args...)
	}
}

func TestCutMovesUnreleasedCommitsTagsAndPushes(t *testing.T) {
	dir := clone(t)
	var sb strings.Builder
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	if err := Cut(dir, "v0.1.0", now, &sb, realExec(t, dir), nil); err != nil {
		t.Fatalf("%v\n%s", err, sb.String())
	}
	got, err := Notes(dir, "v0.1.0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "A note for the next tag.\n" {
		t.Errorf("the section is what sat under Unreleased, got %q", got)
	}
	if _, err := Section(mustRead(t, dir), Unreleased); !errors.Is(err, ErrEmpty) {
		t.Errorf("Unreleased is empty after the cut, got %v", err)
	}
	if !strings.Contains(mustRead(t, dir), "## v0.1.0 - 2026-09-06") {
		t.Error("the heading carries the date of the cut")
	}
	if old, err := Notes(dir, "v0.0.9", "", nil); err != nil || old != "Old.\n" {
		t.Errorf("older sections are untouched, got %q %v", old, err)
	}
	if strings.TrimSpace(git(t, dir, "status", "--porcelain")) != "" {
		t.Error("the cut leaves a clean tree")
	}
	if subject := strings.TrimSpace(git(t, dir, "log", "-1", "--format=%s")); subject != "changelog: v0.1.0" {
		t.Errorf("commit subject %q", subject)
	}
	if kind := strings.TrimSpace(git(t, dir, "cat-file", "-t", "v0.1.0")); kind != "tag" {
		t.Errorf("the tag is annotated, got %s", kind)
	}
	remote := git(t, dir, "ls-remote", "origin")
	if !strings.Contains(remote, "refs/tags/v0.1.0") || !strings.Contains(remote, "refs/heads/main") {
		t.Errorf("HEAD and the tag are pushed, remote has:\n%s", remote)
	}
	// The pushed tag's commit has the section, which is what the pre-push
	// and the workflow read.
	if _, err := Notes(dir, "v0.1.0", "v0.1.0", realExec(t, dir)); err != nil {
		t.Errorf("the section is at the tag: %v", err)
	}
}

func TestCutStampsTheConfiguredFilesIntoTheReleaseCommit(t *testing.T) {
	dir := clone(t)
	readf := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// Two files that name the previous version, the marker wrapping across a
	// line in one and inline in the other, both seeded and committed.
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SECURITY.md", "Fixes go to the latest series. v0.0.9 is the\ncurrent release.\n")
	write("kustomization.yaml", "images:\n  - name: app\n    newTag: v0.0.9\n")
	git(t, dir, "add", "SECURITY.md", "kustomization.yaml")
	git(t, dir, "commit", "-q", "-m", "seed the stamped files")
	publish(t, dir)

	stamps := []config.Stamp{
		{File: "SECURITY.md", Pattern: `v\d+\.\d+\.\d+ is the\s+current release`},
		{File: "kustomization.yaml", Pattern: `newTag: v\d+\.\d+\.\d+`},
	}
	var sb strings.Builder
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	if err := Cut(dir, "v0.1.0", now, &sb, realExec(t, dir), stamps); err != nil {
		t.Fatalf("%v\n%s", err, sb.String())
	}
	if got := readf("SECURITY.md"); !strings.Contains(got, "v0.1.0 is the") || strings.Contains(got, "v0.0.9") {
		t.Errorf("SECURITY.md was not stamped to v0.1.0: %q", got)
	}
	if got := readf("kustomization.yaml"); !strings.Contains(got, "newTag: v0.1.0") || strings.Contains(got, "v0.0.9") {
		t.Errorf("the overlay was not stamped to v0.1.0: %q", got)
	}
	if strings.TrimSpace(git(t, dir, "status", "--porcelain")) != "" {
		t.Error("the stamped files are in the release commit, not left dirty in the tree")
	}
	names := git(t, dir, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{Name, "SECURITY.md", "kustomization.yaml"} {
		if !strings.Contains(names, want) {
			t.Errorf("the release commit does not carry %s; it touched:\n%s", want, names)
		}
	}
	if remote := git(t, dir, "ls-remote", "origin"); !strings.Contains(remote, "refs/tags/v0.1.0") {
		t.Errorf("the tag with the stamped commit is not pushed:\n%s", remote)
	}
}

// Several stamps may name one file, each marking its own line. Every one of
// them lands: a cut that planned each entry from the file as committed would
// write the file once per entry, and the last write would drop the rest.
func TestCutAppliesEveryStampOnOneFile(t *testing.T) {
	dir := clone(t)
	compose := "services:\n  stubs:\n    image: ghcr.io/x/stubs:v0.0.9\n  app:\n    image: ghcr.io/x/app:v0.0.9\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "compose.yaml")
	git(t, dir, "commit", "-q", "-m", "seed the stamped file")
	publish(t, dir)

	stamps := []config.Stamp{
		{File: "compose.yaml", Pattern: `stubs:v\d+\.\d+\.\d+`},
		{File: "compose.yaml", Pattern: `app:v\d+\.\d+\.\d+`},
	}
	var sb strings.Builder
	if err := Cut(dir, "v0.1.0", time.Now(), &sb, realExec(t, dir), stamps); err != nil {
		t.Fatalf("%v\n%s", err, sb.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "services:\n  stubs:\n    image: ghcr.io/x/stubs:v0.1.0\n  app:\n    image: ghcr.io/x/app:v0.1.0\n"
	if string(b) != want {
		t.Errorf("both stamps on compose.yaml move to v0.1.0:\ngot:\n%s\nwant:\n%s", b, want)
	}
	if strings.TrimSpace(git(t, dir, "status", "--porcelain")) != "" {
		t.Error("the stamped file is in the release commit, not left dirty in the tree")
	}
	if n := strings.Count(sb.String(), "compose.yaml: stamped"); n != 1 {
		t.Errorf("one file is stamped once, reported %d times:\n%s", n, sb.String())
	}
}

// A deploy overlay can name a placeholder no tag has pinned. A stamp that
// declares it moves it on the first release, and the same stamp moves the
// version every release after.
func TestCutStampsADeclaredPlaceholder(t *testing.T) {
	dir := clone(t)
	path := filepath.Join(dir, "kustomization.yaml")
	if err := os.WriteFile(path, []byte("images:\n  - name: app\n    newTag: unreleased\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "kustomization.yaml")
	git(t, dir, "commit", "-q", "-m", "seed the placeholder")
	publish(t, dir)

	stamps := []config.Stamp{{
		File:        "kustomization.yaml",
		Pattern:     `newTag: (v\d+\.\d+\.\d+|unreleased)`,
		Placeholder: "unreleased",
	}}
	for _, version := range []string{"v0.1.0", "v0.2.0"} {
		if version != "v0.1.0" {
			// The next cut needs notes of its own under Unreleased.
			body := strings.Replace(mustRead(t, dir), "## Unreleased\n", "## Unreleased\n\nAnother note.\n", 1)
			if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, dir, "commit", "-q", "-am", "notes")
			publish(t, dir)
		}
		var sb strings.Builder
		if err := Cut(dir, version, time.Now(), &sb, realExec(t, dir), stamps); err != nil {
			t.Fatalf("%s: %v\n%s", version, err, sb.String())
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if want := "    newTag: " + version + "\n"; !strings.HasSuffix(string(b), want) {
			t.Errorf("%s: the overlay was not stamped: %q", version, b)
		}
	}
}

// A stamp that does not match exactly once, or whose match holds nothing to
// move, refuses the cut. A placeholder moves only where the stamp declares
// it, so a match holding an undeclared one still refuses.
func TestCutRefusesABadStampAndWritesNothing(t *testing.T) {
	cases := []struct {
		name, body, pattern, placeholder, want string
	}{
		{"twice", "v0.0.1 here and v0.0.2 there\n", `v\d+\.\d+\.\d+`, "", "want exactly one"},
		{"never", "no version at all\n", `newTag: v\d+\.\d+\.\d+`, "", "want exactly one"},
		{"no version", "newTag: unreleased\n", `newTag: \S+`, "", "holds no vX.Y.Z to move"},
		{"neither version nor placeholder", "newTag: latest\n", `newTag: \S+`, "unreleased",
			`holds neither a vX.Y.Z nor the placeholder "unreleased"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := clone(t)
			if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, dir, "add", "marker.txt")
			git(t, dir, "commit", "-q", "-m", "seed")
			publish(t, dir)
			before := mustRead(t, dir)
			var sb strings.Builder
			stamps := []config.Stamp{{File: "marker.txt", Pattern: c.pattern, Placeholder: c.placeholder}}
			err := Cut(dir, "v0.1.0", time.Now(), &sb, realExec(t, dir), stamps)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal mentioning %q, got %v", c.want, err)
			}
			if mustRead(t, dir) != before {
				t.Error("a refused cut leaves the changelog untouched")
			}
			if strings.TrimSpace(git(t, dir, "status", "--porcelain")) != "" {
				t.Error("a refused cut writes nothing: the tree stays clean")
			}
			if out := git(t, dir, "tag", "--list", "v0.1.0"); strings.TrimSpace(out) != "" {
				t.Error("a refused cut creates no tag")
			}
		})
	}
}

func TestCutRefusesAndWritesNothing(t *testing.T) {
	dir := clone(t)
	before := mustRead(t, dir)
	run := realExec(t, dir)
	var sb strings.Builder
	cases := []struct {
		name, version, want string
		prep                func()
	}{
		{"not a release tag", "1.0.0", "usage: lateregate release vX.Y.Z", nil},
		{"dirty tree", "v0.1.0", "not clean", func() {
			if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"a tag only this checkout has", "v0.0.9", "v0.0.9 already exists in this checkout", func() {
			_ = os.Remove(filepath.Join(dir, "stray.txt"))
			git(t, dir, "tag", "v0.0.9")
		}},
	}
	for _, c := range cases {
		if c.prep != nil {
			c.prep()
		}
		err := Cut(dir, c.version, time.Now(), &sb, run, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
		if mustRead(t, dir) != before {
			t.Errorf("%s: the changelog was written", c.name)
		}
	}
	// An empty Unreleased.
	if err := os.WriteFile(filepath.Join(dir, Name), []byte("# Changelog\n\n## Unreleased\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-q", "-am", "empty")
	publish(t, dir)
	err := Cut(dir, "v0.1.0", time.Now(), &sb, run, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing under `## Unreleased`") {
		t.Errorf("got %v", err)
	}
	if strings.Contains(git(t, dir, "tag"), "v0.1.0") {
		t.Error("no tag on refusal")
	}
	// No changelog at all.
	git(t, dir, "rm", "-q", Name)
	git(t, dir, "commit", "-q", "-m", "rm")
	publish(t, dir)
	err = Cut(dir, "v0.1.0", time.Now(), &sb, run, nil)
	if err == nil || !strings.Contains(err.Error(), "no CHANGELOG.md") {
		t.Errorf("got %v", err)
	}
}

// base is the commit the fake checkout and its origin both sit at.
const base = "abcabcabcabcabcabcabcabcabcabcabcabcabca"

// fakeGit answers each git call from a table keyed on its arguments joined
// by spaces, records every call in order, and answers anything not in the
// table with no output and no error.
type fakeGit struct {
	calls   []string
	answers map[string]answer
}

type answer struct {
	out string
	err error
}

// synced is a fake checkout on main at origin's tip, with v0.0.9 on origin
// and v0.1.0 nowhere, which Cut releases unless a test changes an answer.
func synced() *fakeGit {
	return &fakeGit{answers: map[string]answer{
		"symbolic-ref --short refs/remotes/origin/HEAD":    {out: "origin/main\n"},
		"rev-list --left-right --count origin/main...HEAD": {out: "0\t0\n"},
		"rev-parse HEAD":                              {out: base + "\n"},
		"rev-parse origin/main":                       {out: base + "\n"},
		"ls-remote --tags --refs origin refs/tags/v*": {out: sha1 + "\trefs/tags/v0.0.9\n"},
		"rev-parse -q --verify refs/tags/v0.1.0":      {err: errors.New("exit 1")},
	}}
}

func (f *fakeGit) run(_ []string, _ bool, name string, args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, name+" "+call)
	a := f.answers[call]
	return []byte(a.out), a.err
}

// since is every call from the first one that starts with prefix.
func (f *fakeGit) since(prefix string) []string {
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return f.calls[i:]
		}
	}
	return nil
}

// notes is a directory holding a changelog with a note under Unreleased.
func notes(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte("## Unreleased\n\nnote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The push names the release branch on the remote side and the tag as a
// ref, atomically, and it is the last thing the cut does.
func TestCutPushesTheBranchAndTheTagAtomically(t *testing.T) {
	f := synced()
	var sb strings.Builder
	if err := Cut(notes(t), "v0.1.0", time.Now(), &sb, f.run, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"git add CHANGELOG.md",
		"git commit -m changelog: v0.1.0",
		"git tag -a v0.1.0 -m v0.1.0",
		"git push --atomic origin HEAD:refs/heads/main refs/tags/v0.1.0",
	}
	if got := f.since("git add"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the cut's writes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(sb.String(), "v0.1.0: committed, tagged, pushed to main") {
		t.Errorf("got %q", sb.String())
	}
}

// Before it writes, the cut reads origin's default branch, fetches it, and
// compares HEAD with it, then reads origin's tags. A clone whose origin/HEAD
// names another branch releases that branch.
func TestCutSyncsWithOriginsDefaultBranchFirst(t *testing.T) {
	f := synced()
	f.answers["symbolic-ref --short refs/remotes/origin/HEAD"] = answer{out: "origin/trunk\n"}
	f.answers["rev-list --left-right --count origin/trunk...HEAD"] = answer{out: "0\t0\n"}
	var sb strings.Builder
	if err := Cut(notes(t), "v0.1.0", time.Now(), &sb, f.run, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"git status --porcelain",
		"git symbolic-ref --short refs/remotes/origin/HEAD",
		"git fetch --quiet --no-tags origin +refs/heads/trunk:refs/remotes/origin/trunk",
		"git rev-list --left-right --count origin/trunk...HEAD",
		"git rev-parse HEAD",
		"git ls-remote --tags --refs origin refs/tags/v*",
		"git rev-parse -q --verify refs/tags/v0.1.0",
		"git add CHANGELOG.md",
	}
	if got := f.calls[:len(want)]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the cut's reads:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if last := f.calls[len(f.calls)-1]; last != "git push --atomic origin HEAD:refs/heads/trunk refs/tags/v0.1.0" {
		t.Errorf("the push moves trunk, got %q", last)
	}
	// With no origin/HEAD recorded, the release branch is main.
	f = synced()
	f.answers["symbolic-ref --short refs/remotes/origin/HEAD"] = answer{err: errors.New("not a symbolic ref")}
	if err := Ready(notes(t), "v0.1.0", f.run); err != nil {
		t.Fatalf("a clone with no origin/HEAD releases main: %v", err)
	}
}

// Every refusal comes before the first write, so the fake records no add,
// commit, tag or push, and the message says what to do next.
func TestCutRefusesACheckoutThatIsNotOriginsTip(t *testing.T) {
	cases := []struct {
		name   string
		answer map[string]answer
		want   string
	}{
		{"behind", map[string]answer{"rev-list --left-right --count origin/main...HEAD": {out: "3\t0\n"}},
			"main has 3 commits this checkout lacks; run `git pull --ff-only` and run again"},
		{"behind by one", map[string]answer{"rev-list --left-right --count origin/main...HEAD": {out: "1\t0\n"}},
			"main has 1 commit this checkout lacks"},
		{"ahead", map[string]answer{"rev-list --left-right --count origin/main...HEAD": {out: "0\t2\n"}},
			"this checkout has 2 commits main lacks; push them and let CI verify them first, then run again"},
		{"diverged", map[string]answer{"rev-list --left-right --count origin/main...HEAD": {out: "1\t2\n"}},
			"this checkout and origin/main have diverged: main has 1 commit this checkout lacks, and this checkout has 2 commits it lacks"},
		{"fetch fails", map[string]answer{"fetch --quiet --no-tags origin +refs/heads/main:refs/remotes/origin/main": {err: errors.New("exit 128")}},
			"git fetch origin main: exit 128"},
		{"tag on origin", map[string]answer{"ls-remote --tags --refs origin refs/tags/v*": {out: sha1 + "\trefs/tags/v0.1.0\n" + sha2 + "\trefs/tags/v0.0.9\n"}},
			"v0.1.0 already exists on origin, and the newest release there is v0.1.0; pick a version above it"},
		{"newer tag on origin", map[string]answer{"ls-remote --tags --refs origin refs/tags/v*": {out: sha1 + "\trefs/tags/v0.0.9\n" + sha2 + "\trefs/tags/v0.2.0\n"}},
			"origin already has v0.2.0, which is not older than v0.1.0; pick a version above v0.2.0"},
		{"origin unreadable", map[string]answer{"ls-remote --tags --refs origin refs/tags/v*": {err: errors.New("exit 128")}},
			"reading the tags on origin: git ls-remote: exit 128"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := synced()
			maps.Copy(f.answers, c.answer)
			var sb strings.Builder
			err := Cut(notes(t), "v0.1.0", time.Now(), &sb, f.run, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal mentioning %q, got %v", c.want, err)
			}
			if w := f.since("git add"); len(w) != 0 {
				t.Errorf("a refusal writes nothing, got %v", w)
			}
		})
	}
}

// The order is semver's: a numeric part compares as a number, a release is
// newer than its own prerelease, and a moving major tag is not a release.
func TestCutComparesOriginsTagsBySemver(t *testing.T) {
	cases := []struct {
		version, tags, want string
	}{
		{"v0.10.0", "v0.9.0 v0.9.1", ""},
		{"v0.2.0", "v0.1.0 v0.2.0-rc1", ""},
		{"v0.2.0-rc2", "v0.2.0", "origin already has v0.2.0, which is not older than v0.2.0-rc2"},
		{"v0.2.0", "v0.1.0 v9", ""},
		{"v1.0.0", "", ""},
		{"v0.9.0", "v0.10.0 v0.8.0", "origin already has v0.10.0"},
	}
	for _, c := range cases {
		var refs strings.Builder
		for tag := range strings.FieldsSeq(c.tags) {
			refs.WriteString(sha1 + "\trefs/tags/" + tag + "\n")
		}
		f := synced()
		f.answers["ls-remote --tags --refs origin refs/tags/v*"] = answer{out: refs.String()}
		f.answers["rev-parse -q --verify refs/tags/"+c.version] = answer{err: errors.New("exit 1")}
		err := Ready(notes(t), c.version, f.run)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s over %q: %v", c.version, c.tags, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s over %q: want %q, got %v", c.version, c.tags, c.want, err)
		}
	}
}

// A rejected push is undone: the tag goes, then a hard reset to the base
// drops the changelog commit. Origin is read again to tell a branch that
// moved from a push that failed for its own reason.
func TestCutUndoesARejectedPush(t *testing.T) {
	push := "push --atomic origin HEAD:refs/heads/main refs/tags/v0.1.0"
	moved := synced()
	moved.answers[push] = answer{err: errors.New("exit 1")}
	moved.answers["rev-parse origin/main"] = answer{out: sha2 + "\n"}
	var sb strings.Builder
	err := Cut(notes(t), "v0.1.0", time.Now(), &sb, moved.run, nil)
	want := "git push: exit 1; nothing was pushed, and the checkout is back at abcabcabcabc; " +
		"main moved since the check, so run `git pull --ff-only` and run again, " +
		"and if `## Unreleased` is then empty, another release already shipped these changes"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	undo := []string{
		"git push --atomic origin HEAD:refs/heads/main refs/tags/v0.1.0",
		"git fetch --quiet --no-tags origin +refs/heads/main:refs/remotes/origin/main",
		"git rev-parse origin/main",
		"git tag -d v0.1.0",
		"git reset --hard -q " + base,
	}
	if got := moved.since("git push"); strings.Join(got, "\n") != strings.Join(undo, "\n") {
		t.Errorf("after the push:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(undo, "\n"))
	}

	// The branch did not move: the reason is the one git printed.
	still := synced()
	still.answers[push] = answer{err: errors.New("exit 1")}
	err = Cut(notes(t), "v0.1.0", time.Now(), &sb, still.run, nil)
	if err == nil || !strings.Contains(err.Error(), "main has not moved, so the reason is in git's output above") {
		t.Errorf("got %v", err)
	}

	// Origin cannot be read again: both readings are offered.
	gone := synced()
	gone.answers[push] = answer{err: errors.New("exit 1")}
	run := func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		if args[0] == "fetch" && slices.Contains(gone.calls, "git "+push) {
			gone.calls = append(gone.calls, "git "+strings.Join(args, " "))
			return nil, errors.New("exit 128")
		}
		return gone.run(env, stream, name, args...)
	}
	err = Cut(notes(t), "v0.1.0", time.Now(), &sb, run, nil)
	if err == nil || !strings.Contains(err.Error(), "origin could not be read again to tell why (git fetch origin main: exit 128): if main moved since the check") {
		t.Errorf("got %v", err)
	}

	// The undo itself fails: the message names both and the commands to run.
	stuck := synced()
	stuck.answers[push] = answer{err: errors.New("exit 1")}
	stuck.answers["reset --hard -q "+base] = answer{err: errors.New("exit 128")}
	err = Cut(notes(t), "v0.1.0", time.Now(), &sb, stuck.run, nil)
	if err == nil || !strings.Contains(err.Error(), "git push: exit 1; undoing the cut failed too: git reset --hard abcabcabcabc: exit 128; "+
		"run `git tag -d v0.1.0` and `git reset --hard "+base+"` before trying again") {
		t.Errorf("got %v", err)
	}
}

// A step before the push that fails is undone the same way, and the tag is
// deleted only when the cut made it.
func TestCutReportsAFailingGitStep(t *testing.T) {
	f := synced()
	f.answers["commit -m changelog: v0.1.0"] = answer{err: errors.New("hook refused")}
	var sb strings.Builder
	err := Cut(notes(t), "v0.1.0", time.Now(), &sb, f.run, nil)
	if err == nil || !strings.Contains(err.Error(), "git commit: hook refused; nothing was pushed, and the checkout is back at abcabcabcabc") {
		t.Errorf("got %v", err)
	}
	if got := f.since("git commit"); strings.Join(got, "\n") != "git commit -m changelog: v0.1.0\ngit reset --hard -q "+base {
		t.Errorf("an untagged cut resets and deletes no tag, got %v", got)
	}
	statusFails := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("not a repository")
	}
	if err := Cut(notes(t), "v0.1.0", time.Now(), &sb, statusFails, nil); err == nil || !strings.Contains(err.Error(), "git status") {
		t.Errorf("got %v", err)
	}
}

// The same refusals over real clones of one bare origin: another session
// pushed to main, this checkout has a commit nobody pushed, and the tag or
// a newer one is already on origin though not here.
func TestCutRefusesAStaleCheckoutOverRealGit(t *testing.T) {
	t.Run("behind", func(t *testing.T) {
		dir := clone(t)
		land(t, another(t, dir), "theirs.txt")
		err := Cut(dir, "v0.1.0", time.Now(), io.Discard, realExec(t, dir), nil)
		if err == nil || !strings.Contains(err.Error(), "main has 1 commit this checkout lacks; run `git pull --ff-only` and run again") {
			t.Fatalf("got %v", err)
		}
		git(t, dir, "pull", "-q", "--ff-only")
		if err := Cut(dir, "v0.1.0", time.Now(), io.Discard, realExec(t, dir), nil); err != nil {
			t.Errorf("after the pull the cut lands: %v", err)
		}
	})
	t.Run("ahead", func(t *testing.T) {
		dir := clone(t)
		if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "mine.txt")
		git(t, dir, "commit", "-q", "-m", "unpushed")
		err := Cut(dir, "v0.1.0", time.Now(), io.Discard, realExec(t, dir), nil)
		if err == nil || !strings.Contains(err.Error(), "this checkout has 1 commit main lacks; push them and let CI verify them first") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("tag on origin", func(t *testing.T) {
		dir := clone(t)
		other := another(t, dir)
		git(t, other, "tag", "v0.1.0")
		git(t, other, "push", "-q", "origin", "v0.1.0")
		err := Cut(dir, "v0.1.0", time.Now(), io.Discard, realExec(t, dir), nil)
		if err == nil || !strings.Contains(err.Error(), "v0.1.0 already exists on origin") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("newer tag on origin", func(t *testing.T) {
		dir := clone(t)
		other := another(t, dir)
		git(t, other, "tag", "v0.2.0")
		git(t, other, "push", "-q", "origin", "v0.2.0")
		err := Cut(dir, "v0.1.0", time.Now(), io.Discard, realExec(t, dir), nil)
		if err == nil || !strings.Contains(err.Error(), "origin already has v0.2.0, which is not older than v0.1.0; pick a version above v0.2.0") {
			t.Fatalf("got %v", err)
		}
	})
}

// Two sessions release from the same commit. The first lands. The second is
// refused before it writes, because its fetch sees main moved, and once it
// pulls there is nothing under Unreleased: the first release shipped it.
func TestTwoReleasesFromOneCommitLandOnce(t *testing.T) {
	first := clone(t)
	second := another(t, first)
	if err := Cut(first, "v0.1.0", time.Now(), io.Discard, realExec(t, first), nil); err != nil {
		t.Fatal(err)
	}
	err := Cut(second, "v0.2.0", time.Now(), io.Discard, realExec(t, second), nil)
	if err == nil || !strings.Contains(err.Error(), "main has 1 commit this checkout lacks") {
		t.Fatalf("got %v", err)
	}
	if remote := git(t, second, "ls-remote", "--tags", "origin"); strings.Contains(remote, "v0.2.0") {
		t.Errorf("the second release created a tag on origin:\n%s", remote)
	}
	git(t, second, "pull", "-q", "--ff-only")
	err = Cut(second, "v0.2.0", time.Now(), io.Discard, realExec(t, second), nil)
	if err == nil || !strings.Contains(err.Error(), "nothing under `## Unreleased`") {
		t.Errorf("after the pull the notes are already released, got %v", err)
	}
}

// A release is cut from wherever HEAD is origin's tip: a worktree on a
// branch of its own, beside the checkout that holds main, or a detached
// HEAD. The push moves main and the tag, and leaves on origin no branch
// named after the checkout's.
func TestCutReleasesMainFromAWorktreeOffMain(t *testing.T) {
	for _, c := range []struct {
		name string
		at   []string
	}{
		{"own branch", []string{"-b", "release-v0.1.0"}},
		{"detached", []string{"--detach"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := clone(t)
			tree := filepath.Join(t.TempDir(), "tree")
			git(t, dir, append(append([]string{"worktree", "add", "-q"}, c.at...), tree, "main")...)
			if err := Cut(tree, "v0.1.0", time.Now(), io.Discard, realExec(t, tree), nil); err != nil {
				t.Fatal(err)
			}
			head := strings.TrimSpace(git(t, tree, "rev-parse", "HEAD"))
			remote := git(t, dir, "ls-remote", "origin")
			if !strings.Contains(remote, head+"\trefs/heads/main") {
				t.Errorf("origin's main is the changelog commit %s:\n%s", head, remote)
			}
			if !strings.Contains(remote, "refs/tags/v0.1.0") || strings.Contains(remote, "refs/heads/release-v0.1.0") {
				t.Errorf("the push sends main and the tag, and nothing else:\n%s", remote)
			}
		})
	}
}

// The race the atomic push closes: another session lands on main after the
// cut checked and before it pushed. The remote takes neither ref, and the
// cut leaves no tag and no commit behind.
func TestARaceLostAtThePushLeavesNothing(t *testing.T) {
	dir := clone(t)
	other := another(t, dir)
	before := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	changelog := mustRead(t, dir)
	inner := realExec(t, dir)
	raced := false
	run := func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		if name == "git" && args[0] == "push" && !raced {
			raced = true
			land(t, other, "theirs.txt")
		}
		return inner(env, stream, name, args...)
	}
	var sb strings.Builder
	err := Cut(dir, "v0.1.0", time.Now(), &sb, run, nil)
	if !raced {
		t.Fatalf("the cut never pushed: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "nothing was pushed") || !strings.Contains(err.Error(), "main moved since the check, so run `git pull --ff-only`") {
		t.Fatalf("got %v\n%s", err, sb.String())
	}
	if remote := git(t, dir, "ls-remote", "--tags", "origin"); strings.Contains(remote, "v0.1.0") {
		t.Errorf("a rejected branch update still created the tag on origin:\n%s", remote)
	}
	if tags := strings.TrimSpace(git(t, dir, "tag", "--list")); tags != "" {
		t.Errorf("the local tag is left: %q", tags)
	}
	if head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD")); head != before {
		t.Errorf("the changelog commit is left: HEAD %s, was %s", head, before)
	}
	if strings.TrimSpace(git(t, dir, "status", "--porcelain")) != "" || mustRead(t, dir) != changelog {
		t.Error("the tree is not back where it was")
	}
}

// The pre-push still sees the tag: git hands the hook one line per ref, and
// the explicit refspecs name refs/tags/v0.1.0 on both sides.
func TestCutsPushHandsTheHookTheTag(t *testing.T) {
	dir := clone(t)
	seen := filepath.Join(t.TempDir(), "seen")
	// Shell builtins only: the hermetic gate runs this suite with a PATH that
	// holds the toolchain and /usr/bin.
	hook := "#!/bin/sh\nwhile read -r line; do echo \"$line\" >> '" + seen + "'; done\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-push"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Cut(dir, "v0.1.0", time.Now(), io.Discard, realExec(t, dir), nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	tag := strings.TrimSpace(git(t, dir, "rev-parse", "refs/tags/v0.1.0"))
	head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	var sb strings.Builder
	if err := Prepush(dir, strings.NewReader(string(b)), &sb, realExec(t, dir)); err != nil {
		t.Errorf("the pre-push refuses what the cut pushes: %v", err)
	}
	for _, want := range []string{
		"refs/tags/v0.1.0 " + tag + " refs/tags/v0.1.0 " + zeroSHA,
		"HEAD " + head + " refs/heads/main ",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the hook did not see %q; it saw:\n%s", want, b)
		}
	}
	if !strings.Contains(sb.String(), "v0.1.0 has its release note") {
		t.Errorf("the pre-push did not check the tag, got %q", sb.String())
	}
}

func TestPrepushReportsAReadFailure(t *testing.T) {
	var sb strings.Builder
	err := Prepush(t.TempDir(), failingReader{}, &sb, nil)
	if err == nil || !strings.Contains(err.Error(), "reading the pushed refs") {
		t.Errorf("got %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("closed") }

func mustRead(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, Name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
