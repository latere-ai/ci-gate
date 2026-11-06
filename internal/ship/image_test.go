// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildTagsLocallyForTheClusterPlatform(t *testing.T) {
	r := newRig(t, "")
	if err := Build(r.d); err != nil {
		t.Fatal(err)
	}
	c, ok := r.find("podman build")
	if !ok {
		t.Fatalf("no build: %v", r.calls)
	}
	want := "build --platform linux/amd64 -f Dockerfile -t ghcr.io/latere-ai/auth:dev-abc1234 --build-arg B=c --build-arg VERSION=v1.2.3-4-gabc1234-dirty ."
	if strings.Join(c.args, " ") != want {
		t.Errorf("build:\n%s\nwant\n%s", strings.Join(c.args, " "), want)
	}
	if _, ok := r.find("podman push"); ok {
		t.Error("build must not push")
	}
}

func TestBuildPrefersDockerAndNeedsAnEngine(t *testing.T) {
	r := newRig(t, "")
	r.have["docker"] = true
	if err := Build(r.d); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.find("docker build"); !ok {
		t.Errorf("docker is first: %v", r.calls)
	}
	r = newRig(t, "")
	r.have = map[string]bool{}
	if err := Build(r.d); err == nil || !strings.Contains(err.Error(), "docker nor podman") {
		t.Errorf("no engine: %v", err)
	}
}

func TestBuildRunsPrepareWithTheVersionEnvironment(t *testing.T) {
	r := newRig(t, strings.Replace(goodShip, "    version_arg: VERSION\n", "    platform: linux/arm64\n    prepare:\n      - make bin\n", 1))
	if err := Build(r.d); err != nil {
		t.Fatal(err)
	}
	c, ok := r.find("bash")
	if !ok {
		t.Fatalf("prepare did not run: %v", r.calls)
	}
	for k, v := range map[string]string{"VERSION": "v1.2.3-4-gabc1234-dirty", "COMMIT": testCommit, "GOOS": "linux", "GOARCH": "arm64"} {
		if got := envOf(c.env, k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !strings.HasSuffix(c.args[1], "make bin") {
		t.Errorf("command: %q", c.args[1])
	}
	// Prepare comes before the build.
	if r.calls[len(r.calls)-1].name != "podman" {
		t.Errorf("the build is last: %v", r.calls)
	}
}

func TestBuildStopsWhenPrepareFails(t *testing.T) {
	r := newRig(t, strings.Replace(goodShip, "    version_arg: VERSION\n", "    prepare: [make bin]\n", 1))
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "bash" {
			return nil, errors.New("exit 2"), true
		}
		return nil, nil, false
	}
	err := Build(r.d)
	if err == nil || !strings.Contains(err.Error(), "ship.image.prepare `make bin`") {
		t.Fatalf("error: %v", err)
	}
	if _, ok := r.find("podman build"); ok {
		t.Error("an image was built after a failed prepare")
	}
}

func TestBuildNeedsAShipBlock(t *testing.T) {
	r := newRig(t, "")
	r.d.Cfg.Ship = nil
	if err := Build(r.d); err == nil || !strings.Contains(err.Error(), "no ship block") {
		t.Errorf("error: %v", err)
	}
}

func TestPushRefusesOffTagAndDirty(t *testing.T) {
	r := newRig(t, "")
	r.tags = nil
	err := Push(r.d)
	if err == nil || !strings.Contains(err.Error(), "HEAD is not at a release tag") {
		t.Fatalf("off tag: %v", err)
	}
	r = newRig(t, "")
	r.tags = []string{"v1.2.3+meta", "latest", "v2"}
	if err := Push(r.d); err == nil || !strings.Contains(err.Error(), "not at a release tag") {
		t.Fatalf("only non-release tags: %v", err)
	}
	r = newRig(t, "")
	r.dirty = " M main.go"
	err = Push(r.d)
	if err == nil || !strings.Contains(err.Error(), "not clean") || !strings.Contains(err.Error(), "M main.go") {
		t.Fatalf("dirty: %v", err)
	}
	if _, ok := r.find("podman build"); ok {
		t.Error("built from a dirty tree")
	}
}

func TestPushBuildsOnceTagsThreeAndPrintsTheDigest(t *testing.T) {
	r := newRig(t, "")
	r.tags = []string{"v1.1.0", "v1.2.3"}
	if err := Push(r.d); err != nil {
		t.Fatal(err)
	}
	b, _ := r.find("podman build")
	line := strings.Join(b.args, " ")
	for _, want := range []string{"-t ghcr.io/latere-ai/auth:v1.2.3", "-t ghcr.io/latere-ai/auth:sha-abc1234", "-t ghcr.io/latere-ai/auth:latest", "--build-arg VERSION=v1.2.3"} {
		if !strings.Contains(line, want) {
			t.Errorf("build lacks %q: %s", want, line)
		}
	}
	var pushed []string
	for _, c := range r.calls {
		if c.name == "podman" && c.args[0] == "push" {
			pushed = append(pushed, c.args[len(c.args)-1])
		}
	}
	if len(pushed) != 3 {
		t.Errorf("pushed %v", pushed)
	}
	if !strings.Contains(r.out.String(), testDigest) {
		t.Errorf("the digest is not printed:\n%s", r.out.String())
	}
	if left := r.leftovers(); len(left) != 0 {
		t.Errorf("left %v", left)
	}
}

func TestPushSkipsLatestForAPrerelease(t *testing.T) {
	r := newRig(t, "")
	r.tags = []string{"v1.3.0-rc.1"}
	r.have["docker"] = true
	if err := Push(r.d); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.calls {
		if strings.Contains(c.String(), ":latest") {
			t.Errorf("a prerelease moved latest: %s", c)
		}
	}
	if !strings.Contains(r.out.String(), testDigest) {
		t.Errorf("docker's reported digest is printed:\n%s", r.out.String())
	}
}

func TestPushAuthFailureSaysHowToLogIn(t *testing.T) {
	for _, said := range []string{"unauthorized: authentication required", "denied: permission_denied", "received unexpected HTTP status: 403 Forbidden", "401 Unauthorized"} {
		r := newRig(t, "")
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "podman" && c.args[0] == "push" {
				return []byte(said + "\n"), errors.New("exit 125"), true
			}
			return nil, nil, false
		}
		err := Push(r.d)
		if err == nil {
			t.Fatalf("%q: pushed", said)
		}
		for _, want := range []string{"registry refused the push", "write:packages", "podman login ghcr.io"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: error %q lacks %q", said, err, want)
			}
		}
	}
	// Another failure is not blamed on the login.
	r := newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "podman" && c.args[0] == "push" {
			return []byte("connection reset\n"), errors.New("exit 125"), true
		}
		return nil, nil, false
	}
	if err := Push(r.d); err == nil || strings.Contains(err.Error(), "login") {
		t.Errorf("a network failure: %v", err)
	}
}

const emptyShip = "ship: {}\n"

func TestBuildWithNoImageBuildsNothing(t *testing.T) {
	r := newRig(t, emptyShip)
	if err := Build(r.d); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Errorf("a repository with no image ran commands: %v", r.calls)
	}
	if !strings.Contains(r.out.String(), "this repository builds no image") {
		t.Errorf("output: %q", r.out.String())
	}
}

func TestPushWithNoImageRefuses(t *testing.T) {
	r := newRig(t, emptyShip)
	if err := Push(r.d); err == nil || !strings.Contains(err.Error(), "publishes no image") {
		t.Errorf("error: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("a refused push ran commands: %v", r.calls)
	}
}

func TestDeployAndCredentialWithNoDeployRefuse(t *testing.T) {
	for name, yaml := range map[string]string{
		"empty":      emptyShip,
		"image only": "ship:\n  image:\n    name: ghcr.io/latere-ai/auth\n",
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, yaml)
			if err := Deploy(r.d, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "deploys nothing") {
				t.Errorf("deploy: %v", err)
			}
			if err := DeployCredential(r.d, "admin"); err == nil || !strings.Contains(err.Error(), "deploys nothing") {
				t.Errorf("deploy-credential: %v", err)
			}
			if len(r.calls) != 0 {
				t.Errorf("a refused command ran commands: %v", r.calls)
			}
		})
	}
}

func TestBuildOfAnImageOnlyBlockStillBuilds(t *testing.T) {
	r := newRig(t, "ship:\n  image:\n    name: ghcr.io/latere-ai/auth\n")
	if err := Build(r.d); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.find("podman build"); !ok {
		t.Errorf("no build: %v", r.calls)
	}
}
