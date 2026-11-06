// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/ship"
)

const shipConfig = "identity:\n  role: none\nship:\n  image:\n    name: ghcr.io/latere-ai/x\n" +
	"  deploy:\n    kubeconfig: op://V/I/F\n    namespace: n\n    deployment: x\n    url: https://x.example\n"

const emptyShipConfig = "identity:\n  role: none\nship: {}\n"

// stubGH runs git for real and answers gh and the container engine from a
// record, so a command that reaches GitHub or a registry is seen, not run.
func stubGH(t *testing.T, calls *[]string) {
	t.Helper()
	old := shipDeps
	t.Cleanup(func() { shipDeps = old })
	shipDeps = func(root string, cfg *config.Config, w io.Writer) ship.Deps {
		d := ship.New(root, cfg, w)
		real := gates.OSExec(root, w)
		d.Exec = func(env []string, stream bool, name string, args ...string) ([]byte, error) {
			if name == "sh" && len(args) > 2 && args[2] == "gh" {
				*calls = append(*calls, strings.Join(args[2:], " "))
				if args[4] == "view" {
					return []byte("release not found"), os.ErrNotExist
				}
				return nil, nil
			}
			return real(env, stream, name, args...)
		}
		return d
	}
}

// A ship repository's release reads no CI: the API is never asked. The cut
// pushes the tag, and the GitHub release is published from the changelog.
func TestReleaseOfAShipRepositoryReadsNoCIAndPublishesTheRelease(t *testing.T) {
	for name, cfg := range map[string]string{"full": shipConfig, "empty ship block": emptyShipConfig} {
		t.Run(name, func(t *testing.T) { releaseReadsNoCI(t, cfg) })
	}
}

func releaseReadsNoCI(t *testing.T, config string) {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(k, v)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("CI was read: %s", r.URL)
		http.Error(w, "no", http.StatusTeapot)
	}))
	defer api.Close()
	t.Setenv("GITHUB_API_URL", api.URL)

	dir := published(t, config)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/pin\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "go.mod")
	gitIn(t, dir, "commit", "-q", "-m", "a module")
	gitIn(t, dir, "push", "-q", "origin", "main")

	var gh []string
	stubGH(t, &gh)
	s, err := out(t, "release", "-C", dir, "v1.0.0")
	if err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	if tags := gitIn(t, dir, "ls-remote", "--tags", "origin"); !strings.Contains(tags, "refs/tags/v1.0.0") {
		t.Errorf("the tag was not pushed: %q", tags)
	}
	if len(gh) != 2 || !strings.HasPrefix(gh[1], "gh release create v1.0.0 ") {
		t.Errorf("gh calls: %v", gh)
	}
}

func TestReleaseSaysTheTagStandsWhenTheGitHubReleaseFails(t *testing.T) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(k, v)
	}
	dir := published(t, shipConfig)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/pin\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "go.mod")
	gitIn(t, dir, "commit", "-q", "-m", "a module")
	gitIn(t, dir, "push", "-q", "origin", "main")
	t.Cleanup(func() { shipDeps = ship.New })
	shipDeps = func(root string, cfg *config.Config, w io.Writer) ship.Deps {
		d := ship.New(root, cfg, w)
		real := gates.OSExec(root, w)
		d.Exec = func(env []string, stream bool, name string, args ...string) ([]byte, error) {
			if name == "sh" && len(args) > 2 && args[2] == "gh" {
				return []byte("gh: not logged in"), os.ErrPermission
			}
			return real(env, stream, name, args...)
		}
		return d
	}
	_, err := out(t, "release", "-C", dir, "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "v1.0.0 is tagged and pushed, but its GitHub release was not published") {
		t.Fatalf("error: %v", err)
	}
	if tags := gitIn(t, dir, "ls-remote", "--tags", "origin"); !strings.Contains(tags, "refs/tags/v1.0.0") {
		t.Errorf("the tag was not pushed: %q", tags)
	}
}

func TestShipCommandsNeedAShipBlock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte("identity:\n  role: none\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"build"}, {"push"}, {"deploy"}, {"deploy-credential", "-context", "x"}} {
		_, err := out(t, append(append([]string{}, argv[0]), append([]string{"-C", dir}, argv[1:]...)...)...)
		if err == nil || !strings.Contains(err.Error(), "no ship block") {
			t.Errorf("%v: %v", argv, err)
		}
	}
}

func TestBuildRunsTheBarBeforeTheImage(t *testing.T) {
	dir := t.TempDir()
	body := shipConfig + "waive:\n  nosuch:\n    reason: r\n    until: 2099-01-01\n"
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := out(t, "build", "-C", dir)
	if err == nil || !strings.Contains(err.Error(), "not building") || !strings.Contains(err.Error(), "nosuch, which is not a gate") {
		t.Fatalf("a red bar built: %v", err)
	}
	if strings.Contains(s, "build --platform") {
		t.Errorf("an image was built:\n%s", s)
	}
}

func TestDeployTakesAtMostOneVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte(shipConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := out(t, "deploy", "-C", dir, "v1.0.0", "v1.0.1"); err == nil || !strings.Contains(err.Error(), "usage: lateregate deploy") {
		t.Errorf("error: %v", err)
	}
}

func TestShipCommandsReachTheirImplementation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte(shipConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	// Outside a git checkout the first question each asks fails, which is
	// all a dispatch test needs of them: nothing is built, pushed or applied.
	t.Cleanup(func() { shipDeps = ship.New })
	shipDeps = func(root string, cfg *config.Config, w io.Writer) ship.Deps {
		d := ship.New(root, cfg, w)
		d.Exec = func([]string, bool, string, ...string) ([]byte, error) { return nil, os.ErrNotExist }
		return d
	}
	for _, argv := range [][]string{{"push"}, {"deploy"}, {"deploy", "v1.0.0"}, {"deploy-credential", "-context", ""}} {
		if _, err := out(t, append([]string{argv[0], "-C", dir}, argv[1:]...)...); err == nil {
			t.Errorf("%v succeeded", argv)
		}
	}
}

// `ship: {}` is the workstation flow with nothing to publish: build runs the
// whole bar and says it builds no image, and the commands that act on an image
// or a deployment refuse.
func TestEmptyShipBlockBuildsNoImageAndRefusesPushAndDeploy(t *testing.T) {
	dir := published(t, emptyShipConfig)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/pin\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "go.mod")
	gitIn(t, dir, "commit", "-q", "-m", "a module")
	s, err := out(t, "build", "-C", dir)
	if err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	if !strings.Contains(s, "this repository builds no image") {
		t.Errorf("build does not say it builds nothing:\n%s", s)
	}
	for argv, want := range map[string]string{
		"push":              "publishes no image",
		"deploy":            "deploys nothing",
		"deploy-credential": "deploys nothing",
	} {
		args := []string{argv, "-C", dir}
		if argv == "deploy-credential" {
			args = append(args, "-context", "x")
		}
		_, err := out(t, args...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want it to say %q", argv, err, want)
		}
	}
}

// The bar still gates a build that has no image to build.
func TestBuildOfAnEmptyShipBlockRunsTheBar(t *testing.T) {
	dir := t.TempDir()
	body := emptyShipConfig + "waive:\n  nosuch:\n    reason: r\n    until: 2099-01-01\n"
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := out(t, "build", "-C", dir)
	if err == nil || !strings.Contains(err.Error(), "not building") {
		t.Fatalf("a red bar passed: %v", err)
	}
}
