// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const lsRemote = `aaa	refs/tags/v1.9.0
bbb	refs/tags/v1.9.0^{}
ccc	refs/tags/v1.10.0
ddd	refs/tags/v1.10.0^{}
eee	refs/tags/v2.0.0-rc.1
fff	refs/tags/v1.10.1+build
ggg	refs/tags/latest
hhh	refs/tags/v1
`

func TestNewestTagIsBySemverNotLexicalOrder(t *testing.T) {
	r := newRig(t, "")
	r.remote = lsRemote
	got, err := r.d.NewestTag()
	if err != nil || got != "v1.10.0" {
		t.Fatalf("newest = %q, %v", got, err)
	}
	r.remote = "eee\trefs/tags/v2.0.0-rc.1\n"
	if _, err := r.d.NewestTag(); err == nil || !strings.Contains(err.Error(), "no stable") {
		t.Errorf("only a prerelease: %v", err)
	}
}

func TestDeployRunsEveryStepInOrderAsTheRolloutIdentity(t *testing.T) {
	r := newRig(t, goodShip+"    preflight: [./tools/check.sh]\n    after_roll:\n      - kubectl wait things\n")
	r.remote = lsRemote
	r.tree["tools/check.sh"] = "x"
	if err := Deploy(r.d, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bash ./tools/check.sh",
		"kubectl apply -f",
		"kubectl set image",
		"kubectl rollout status",
		"bash kubectl wait things",
		"./tools/smoke/release.sh",
	}
	if got := r.steps(); !slices.Equal(got, want) {
		t.Fatalf("steps:\n%v\nwant\n%v", got, want)
	}
	for _, c := range r.calls {
		if c.name != "kubectl" {
			continue
		}
		i := slices.Index(c.args, "--kubeconfig")
		if i != 0 || c.args[1] != r.kube {
			t.Errorf("kubectl without the temporary kubeconfig first: %s", c)
		}
		if c.mode != 0o600 {
			t.Errorf("the kubeconfig was %v when %s ran", c.mode, c)
		}
	}
	// Every step sees the temporary file, never the operator's default.
	for _, c := range r.calls {
		if c.where == "tree" && envOf(c.env, "KUBECONFIG") != r.kube {
			t.Errorf("%s ran with KUBECONFIG=%q", c, envOf(c.env, "KUBECONFIG"))
		}
	}
	si, _ := r.find("kubectl --kubeconfig " + r.kube + " -n latere set image")
	if got := si.args[len(si.args)-1]; got != "authd=ghcr.io/latere-ai/auth@"+testDigest {
		t.Errorf("set image to %q", got)
	}
	ro, _ := r.find("kubectl --kubeconfig " + r.kube + " -n latere rollout")
	if !ro.has("--timeout=300s") || !ro.has("deployment/auth") {
		t.Errorf("rollout: %s", ro)
	}
	for _, step := range []string{"./tools/check.sh", "kubectl wait things"} {
		var c call
		for _, x := range r.calls {
			if x.name == "bash" && strings.HasSuffix(strings.Join(x.args, " "), step) {
				c = x
			}
		}
		if c.name == "" {
			t.Fatalf("no bash call ending in %s", step)
		}
		for k, v := range map[string]string{"TAG": "v1.10.0", "COMMIT": testCommit, "IMAGE": "ghcr.io/latere-ai/auth@" + testDigest} {
			if got := envOf(c.env, k); got != v {
				t.Errorf("%s ran with %s=%q, want %q", step, k, got, v)
			}
		}
	}
	smoke, _ := r.find("./tools/smoke/release.sh")
	for k, v := range map[string]string{"BASE_URL": "https://auth.latere.ai", "TAG": "v1.10.0", "COMMIT": testCommit, "KUBECONFIG": r.kube} {
		if got := envOf(smoke.env, k); got != v {
			t.Errorf("smoke %s = %q, want %q", k, got, v)
		}
	}
	if got := envOf(smoke.env, "SERVICE_TOKEN"); got != "" {
		t.Errorf("the smoke ran with a token: %q", got)
	}
	if left := r.leftovers(); len(left) != 0 {
		t.Errorf("the deploy left %v", left)
	}
	if _, err := os.Stat(r.kube); err == nil {
		t.Error("the kubeconfig outlived the deploy")
	}
	for _, want := range []string{"v1.10.0", testDigest, "latere/auth", "live check passed"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, r.out.String())
		}
	}
	if strings.Contains(r.out.String(), secretText) {
		t.Error("the kubeconfig was printed")
	}
	if _, ok := r.find("gh release create v1.10.0"); !ok {
		t.Errorf("no GitHub release: %v", r.calls)
	}
}

func TestDeployAppliesAKustomizationWithDashK(t *testing.T) {
	r := newRig(t, "")
	r.tree["deploy/prod/kustomization.yaml"] = "resources: []\n"
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if got := r.steps()[0]; got != "kubectl apply -k" {
		t.Errorf("first step %q", got)
	}
	c, _ := r.find("kubectl --kubeconfig " + r.kube + " apply")
	if c.args[len(c.args)-1] != "deploy/prod/" {
		t.Errorf("apply: %s", c)
	}
}

// Whatever step fails stops the deploy there, names itself, and the
// credential is gone.
func TestDeployStopsAtTheFailingStep(t *testing.T) {
	steps := []struct{ name, fails, stopsBefore string }{
		{"preflight", "bash ./pre", "kubectl apply"},
		{"apply deploy/prod", "kubectl apply", "kubectl set"},
		{"set image", "kubectl set", "kubectl rollout"},
		{"rollout status", "kubectl rollout", "bash after"},
		{"after_roll", "bash after", "./tools"},
		{"live check", "./tools/smoke", "gh release"},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			r := newRig(t, goodShip+"    preflight: [./pre]\n    after_roll: [after]\n")
			r.answer = func(c call) ([]byte, error, bool) {
				text := c.name + " " + strings.Join(c.args, " ")
				if c.name == "bash" {
					text = "bash " + strings.TrimPrefix(c.args[1], "set -euo pipefail\n")
				}
				text = strings.Replace(text, "--kubeconfig "+r.kube+" ", "", 1)
				if strings.HasPrefix(text, st.fails) || strings.HasPrefix(strings.Replace(text, "-n latere ", "", 1), st.fails) {
					return nil, errors.New("exit 1"), true
				}
				return nil, nil, false
			}
			err := Deploy(r.d, "v1.2.3")
			if err == nil || !strings.Contains(err.Error(), "stopped at "+st.name) {
				t.Fatalf("error %v, want it to name %q", err, st.name)
			}
			for _, c := range r.calls {
				text := c.String()
				if c.name == "bash" {
					text = "bash " + strings.TrimPrefix(c.args[1], "set -euo pipefail\n")
				}
				text = strings.Replace(strings.Replace(text, "--kubeconfig "+r.kube+" ", "", 1), "-n latere ", "", 1)
				if strings.HasPrefix(text, st.stopsBefore) {
					t.Errorf("%q ran after %s failed", text, st.name)
				}
			}
			if left := r.leftovers(); len(left) != 0 {
				t.Errorf("a failed deploy left %v", left)
			}
		})
	}
}

func TestDeployRefusesAnImageNotInTheRegistry(t *testing.T) {
	r := newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "podman" && c.args[0] == "pull" {
			return []byte("Error: initializing source: manifest unknown\n"), errors.New("exit 125"), true
		}
		return nil, nil, false
	}
	err := Deploy(r.d, "v1.2.3")
	want := "the image for v1.2.3 is not in the registry; `lateregate push` at that tag builds and publishes it"
	if err == nil || err.Error() != want {
		t.Fatalf("error %v\nwant %s", err, want)
	}
	if _, ok := r.find("op "); ok {
		t.Error("the credential was read before the image was found")
	}
	if _, ok := r.find("kubectl"); ok {
		t.Error("the cluster was touched")
	}
}

func TestDeployResolvesTheDigestWithDocker(t *testing.T) {
	r := newRig(t, "")
	r.have["docker"] = true
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.find("docker buildx imagetools inspect ghcr.io/latere-ai/auth:v1.2.3"); !ok {
		t.Errorf("no imagetools: %v", r.calls)
	}
	r = newRig(t, "")
	r.have["docker"] = true
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "docker" {
			return []byte("ERROR: ghcr.io/latere-ai/auth:v1.2.3: not found\n"), errors.New("exit 1"), true
		}
		return nil, nil, false
	}
	if err := Deploy(r.d, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "not in the registry") {
		t.Errorf("docker not found: %v", err)
	}
}

func TestDeployRegistryLoginIsNamed(t *testing.T) {
	r := newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "podman" && c.args[0] == "pull" {
			return []byte("unauthorized: authentication required\n"), errors.New("exit 125"), true
		}
		return nil, nil, false
	}
	if err := Deploy(r.d, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "read:packages") {
		t.Errorf("error: %v", err)
	}
}

func TestDeployRefusals(t *testing.T) {
	t.Run("no live check in the tag", func(t *testing.T) {
		r := newRig(t, "")
		delete(r.tree, "tools/smoke/release.sh")
		err := Deploy(r.d, "v1.2.3")
		if err == nil || !strings.Contains(err.Error(), "tools/smoke/release.sh is not in v1.2.3") {
			t.Fatalf("error: %v", err)
		}
		if _, ok := r.find("kubectl"); ok {
			t.Error("deployed without a live check")
		}
		if left := r.leftovers(); len(left) != 0 {
			t.Errorf("left %v", left)
		}
	})
	t.Run("no deploy/prod", func(t *testing.T) {
		r := newRig(t, "")
		delete(r.tree, "deploy/prod/deployment.yaml")
		if err := Deploy(r.d, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "deploy/prod is not in v1.2.3") {
			t.Fatalf("error: %v", err)
		}
	})
	t.Run("op missing", func(t *testing.T) {
		r := newRig(t, "")
		delete(r.have, "op")
		err := Deploy(r.d, "v1.2.3")
		if err == nil || !strings.Contains(err.Error(), "op") || !strings.Contains(err.Error(), testRef) {
			t.Fatalf("error: %v", err)
		}
	})
	t.Run("op fails", func(t *testing.T) {
		r := newRig(t, "")
		r.answer = func(c call) ([]byte, error, bool) {
			if c.name == "op" {
				return []byte("[ERROR] you are not signed in\n"), errors.New("exit 1"), true
			}
			return nil, nil, false
		}
		err := Deploy(r.d, "v1.2.3")
		if err == nil || !strings.Contains(err.Error(), testRef) || !strings.Contains(err.Error(), "not signed in") {
			t.Fatalf("error: %v", err)
		}
		if _, ok := r.find("kubectl"); ok {
			t.Error("the cluster was touched")
		}
	})
	t.Run("not a release tag", func(t *testing.T) {
		r := newRig(t, "")
		if err := Deploy(r.d, "main"); err == nil || !strings.Contains(err.Error(), "not a release tag") {
			t.Fatalf("error: %v", err)
		}
	})
}

func TestDeployFetchesATagItDoesNotHave(t *testing.T) {
	r := newRig(t, "")
	n := 0
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "git" && c.args[0] == "rev-parse" && slices.Contains(c.args, "--verify") {
			n++
			if n == 1 {
				return nil, errors.New("unknown revision"), true
			}
		}
		return nil, nil, false
	}
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.find("git fetch origin refs/tags/v1.2.3:refs/tags/v1.2.3"); !ok {
		t.Errorf("the tag was not fetched: %v", r.calls)
	}
	// The deploy reads the tag's tree, not the working tree.
	if _, ok := r.find("git archive --format=tar refs/tags/v1.2.3"); !ok {
		t.Errorf("no archive of the tag: %v", r.calls)
	}
}

func TestDeployRemovesTheKubeconfigOnASignal(t *testing.T) {
	r := newRig(t, "")
	sigs := make(chan os.Signal, 1)
	r.d.Signals = sigs
	exited := make(chan int, 1)
	r.d.Exit = func(code int) { exited <- code }
	release := make(chan struct{})
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "kubectl" && c.has("rollout") {
			sigs <- syscall.SIGTERM
			code := <-exited
			if code != 130 {
				t.Errorf("exit code %d", code)
			}
			if _, err := os.Stat(r.kube); err == nil {
				t.Error("the kubeconfig survived the signal")
			}
			close(release)
			return nil, errors.New("terminated"), true
		}
		return nil, nil, false
	}
	if err := Deploy(r.d, "v1.2.3"); err == nil {
		t.Fatal("a terminated rollout succeeded")
	}
	<-release
	if left := r.leftovers(); len(left) != 0 {
		t.Errorf("left %v", left)
	}
}

func TestDeployListensForRealSignalsWhenNoneAreInjected(t *testing.T) {
	r := newRig(t, "")
	r.d.Signals = nil
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
}

func TestPublishReleaseIsIdempotentAndUsesTheChangelog(t *testing.T) {
	r := newRig(t, "")
	var notes string
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "gh" && c.args[1] == "create" {
			b, err := os.ReadFile(c.args[slices.Index(c.args, "--notes-file")+1])
			if err != nil {
				t.Error(err)
			}
			notes = string(b)
		}
		return nil, nil, false
	}
	if err := PublishRelease(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	c, _ := r.find("gh release create v1.2.3")
	if !c.has("--verify-tag") || c.has("--prerelease") {
		t.Errorf("create: %s", c)
	}
	if !strings.Contains(notes, "- the note") || strings.Contains(notes, "## v1.2.3") {
		t.Errorf("notes: %q", notes)
	}
	if _, ok := r.find("git show v1.2.3:CHANGELOG.md"); !ok {
		t.Error("the notes were not read at the tag")
	}

	// A release that exists is left alone.
	r = newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "gh" && c.args[1] == "view" {
			return []byte("v1.2.3\n"), nil, true
		}
		return nil, nil, false
	}
	if err := PublishRelease(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.find("gh release create"); ok {
		t.Error("created a release that exists")
	}

	// A prerelease is marked.
	r = newRig(t, "")
	if err := PublishRelease(r.d, "v1.3.0-rc.1"); err != nil {
		t.Fatal(err)
	}
	if c, _ := r.find("gh release create"); !c.has("--prerelease") {
		t.Errorf("create: %s", c)
	}
}

func TestPublishReleaseReportsFailures(t *testing.T) {
	r := newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "gh" && c.args[1] == "view" {
			return []byte("gh: To use GitHub CLI, run: gh auth login\n"), errors.New("exit 4"), true
		}
		return nil, nil, false
	}
	if err := PublishRelease(r.d, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("error: %v", err)
	}
	// After a successful deploy, a failed release is an error that says the
	// deploy stands.
	r = newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "gh" && c.args[1] == "create" {
			return []byte("HTTP 422\n"), errors.New("exit 1"), true
		}
		return nil, nil, false
	}
	err := Deploy(r.d, "v1.2.3")
	if err == nil || !strings.Contains(err.Error(), "is deployed and live") {
		t.Fatalf("error: %v", err)
	}
	if _, ok := r.find("kubectl --kubeconfig " + r.kube + " -n latere rollout"); !ok {
		t.Error("the deploy did not happen")
	}
	if err := PublishRelease(r.d, "main"); err == nil {
		t.Error("published a release for a branch name")
	}
}

func TestExtractRefusesAPathOutsideTheTree(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extract(buf.Bytes(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "outside the tree") {
		t.Errorf("error: %v", err)
	}
	if err := extract([]byte("not a tar"), t.TempDir()); err == nil {
		t.Error("garbage unpacked")
	}
}

func TestExtractKeepsDirectoriesLinksAndModes(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range []tar.Header{
		{Name: "a/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "a/run.sh", Typeflag: tar.TypeReg, Mode: 0o755, Size: 2},
		{Name: "a/link", Typeflag: tar.TypeSymlink, Linkname: "run.sh"},
	} {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte("#!")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := extract(buf.Bytes(), dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "a", "link"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("link to the script: %v %v", info, err)
	}
}

func TestLiveCheckRetriesUntilItPasses(t *testing.T) {
	r := newRig(t, "")
	smokes := 0
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name != "./tools/smoke/release.sh" {
			return nil, nil, false
		}
		smokes++
		if smokes < 3 {
			return []byte("old pod answered\n"), errors.New("exit 1"), true
		}
		return nil, nil, true
	}
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if smokes != 3 {
		t.Errorf("attempts = %d, want 3", smokes)
	}
	if len(r.slept) != 2 || r.slept[0] != LiveCheckInterval {
		t.Errorf("waits = %v", r.slept)
	}
}

func TestLiveCheckFailsAfterTheWindowNamingTheAttempts(t *testing.T) {
	r := newRig(t, "")
	smokes := 0
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name != "./tools/smoke/release.sh" {
			return nil, nil, false
		}
		smokes++
		return []byte("503 from the site\n"), errors.New("exit 1"), true
	}
	err := Deploy(r.d, "v1.2.3")
	want := fmt.Sprintf("failed %d times", smokes)
	if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "503 from the site") {
		t.Fatalf("error: %v, want it to say %q and carry the last output", err, want)
	}
	if smokes != int(LiveCheckWindow/LiveCheckInterval)+1 {
		t.Errorf("attempts = %d over a %s window every %s", smokes, LiveCheckWindow, LiveCheckInterval)
	}
	if total := time.Duration(len(r.slept)) * LiveCheckInterval; total > LiveCheckWindow {
		t.Errorf("waited %s, past the window", total)
	}
	if _, ok := r.find("gh release"); ok {
		t.Error("a failed live check published the release")
	}
}

func TestMissingLiveCheckIsNotRetried(t *testing.T) {
	r := newRig(t, "")
	delete(r.tree, "tools/smoke/release.sh")
	if err := Deploy(r.d, "v1.2.3"); err == nil {
		t.Fatal("deployed without a live check")
	}
	if len(r.slept) != 0 {
		t.Errorf("a missing script was waited on: %v", r.slept)
	}
	if _, ok := r.find("./tools/smoke/release.sh"); ok {
		t.Error("ran a script that is not there")
	}
}
