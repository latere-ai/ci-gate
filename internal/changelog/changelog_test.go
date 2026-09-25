// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package changelog

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestPrepushIgnoresWhatIsNotARelease(t *testing.T) {
	var calls []string
	run := show(t, &calls, nil)
	refs := "refs/tags/v1 " + sha1 + " refs/tags/v1 " + zeroSHA + "\n" +
		"refs/tags/v1.2.3 " + zeroSHA + " refs/tags/v1.2.3 " + sha1 + "\n" +
		"refs/heads/main " + sha1 + " refs/heads/main " + sha2 + "\n" +
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

func TestCutRefusesAStampThatDoesNotMatchExactlyOnceAndWritesNothing(t *testing.T) {
	cases := []struct {
		name, body, pattern, want string
	}{
		{"twice", "v0.0.1 here and v0.0.2 there\n", `v\d+\.\d+\.\d+`, "want exactly one"},
		{"never", "no version at all\n", `newTag: v\d+\.\d+\.\d+`, "want exactly one"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := clone(t)
			if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			git(t, dir, "add", "marker.txt")
			git(t, dir, "commit", "-q", "-m", "seed")
			before := mustRead(t, dir)
			var sb strings.Builder
			stamps := []config.Stamp{{File: "marker.txt", Pattern: c.pattern}}
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
		{"existing tag", "v0.0.9", "v0.0.9 already exists", func() {
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
	err = Cut(dir, "v0.1.0", time.Now(), &sb, run, nil)
	if err == nil || !strings.Contains(err.Error(), "no CHANGELOG.md") {
		t.Errorf("got %v", err)
	}
}

func TestCutReportsAFailingGitStep(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte("## Unreleased\n\nnote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fail := func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		switch args[0] {
		case "rev-parse":
			return nil, errors.New("no such tag")
		case "push":
			return nil, errors.New("rejected")
		}
		return nil, nil
	}
	var sb strings.Builder
	err := Cut(dir, "v0.1.0", time.Now(), &sb, fail, nil)
	if err == nil || !strings.Contains(err.Error(), "git push: rejected") {
		t.Errorf("got %v", err)
	}
	statusFails := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("not a repository")
	}
	if err := Cut(dir, "v0.1.0", time.Now(), &sb, statusFails, nil); err == nil || !strings.Contains(err.Error(), "git status") {
		t.Errorf("got %v", err)
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
