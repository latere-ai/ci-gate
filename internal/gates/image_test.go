// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"context"
	"errors"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// imageEngine answers the container engine's commands from a script: the output
// for a command's first argument, or an error for the ones listed in fail.
type imageEngine struct {
	ran  []string
	out  map[string]string
	fail map[string]bool
}

func (e *imageEngine) exec(_ []string, _ bool, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	e.ran = append(e.ran, line)
	key := args[0]
	if key == "run" && len(args) > 2 && args[1] == "--rm" && args[2] == "--entrypoint" {
		key = "shell"
	} else if key == "run" && len(args) > 1 && args[1] == "-d" {
		key = "start"
	} else if key == "run" {
		key = "version"
	}
	if e.fail[key] {
		return []byte(e.out[key]), errors.New("exit 1")
	}
	return []byte(e.out[key]), nil
}

func pons() config.ImageSpec {
	return config.ImageSpec{
		Name: "ponsd", VersionArgs: []string{"version"}, VersionExpect: "ponsd ", MaxMiB: 40,
		StartArgs: []string{"serve"}, Env: map[string]string{"B": "2", "A": "1"}, Port: 8080, Probe: "/livez",
	}
}

func answering(t *testing.T, err error) {
	t.Helper()
	old := ImageProbe
	ImageProbe = func(context.Context, string) error { return err }
	t.Cleanup(func() { ImageProbe = old })
}

// An image in shape builds, reports its version, is under its cap, holds no
// shell, starts read-only with no capabilities, answers, and is removed.
func TestImageInShapePasses(t *testing.T) {
	answering(t, nil)
	e := &imageEngine{
		out:  map[string]string{"version": "ponsd v1.2.3 (abc)\n", "image": "10485760\n", "port": "127.0.0.1:49153\n"},
		fail: map[string]bool{"shell": true},
	}
	var out strings.Builder
	if err := Image([]config.ImageSpec{pons()}, &out, found, e.exec); err != nil {
		t.Fatalf("Image = %v\n%s", err, out.String())
	}
	joined := strings.Join(e.ran, "\n")
	for _, want := range []string{
		"docker build -f Dockerfile -t localhost/lateregate-ponsd-",
		"--read-only --cap-drop ALL -p 127.0.0.1::8080 -e A=1 -e B=2 localhost/lateregate-ponsd-",
		"docker rm -f lateregate-ponsd-",
		"docker rmi -f localhost/lateregate-ponsd-",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in:\n%s", want, joined)
		}
	}
	if !strings.Contains(out.String(), "ponsd answers /livez") {
		t.Errorf("output:\n%s", out.String())
	}
}

// Each check that fails names what it found, and the candidate is removed.
func TestImageNamesEachFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  map[string]string
		fail map[string]bool
		want string
	}{
		{"no build", nil, map[string]bool{"build": true, "shell": true}, "does not build"},
		{"wrong version", map[string]string{"version": "other v1\n"}, map[string]bool{"shell": true}, `does not start with "ponsd "`},
		{"over the cap", map[string]string{"version": "ponsd v1\n", "image": "52428800\n"}, map[string]bool{"shell": true}, "50.0 MiB, over its cap of 40 MiB"},
		{"a shell", map[string]string{"version": "ponsd v1\n", "image": "1\n"}, nil, "holds a shell"},
		{"no start", map[string]string{"version": "ponsd v1\n", "image": "1\n"}, map[string]bool{"shell": true, "start": true}, "does not start read-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answering(t, nil)
			e := &imageEngine{out: tc.out, fail: tc.fail}
			var out strings.Builder
			err := Image([]config.ImageSpec{pons()}, &out, found, e.exec)
			if err == nil || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("want %q, got %v\n%s", tc.want, err, out.String())
			}
			if tc.name != "no build" && !strings.Contains(strings.Join(e.ran, "\n"), "rmi -f") {
				t.Errorf("the candidate was left behind:\n%s", strings.Join(e.ran, "\n"))
			}
		})
	}
}

// A probe that never answers fails with the container's logs, and the
// container is removed.
func TestImageThatDoesNotAnswerFails(t *testing.T) {
	answering(t, errors.New("connection refused"))
	im := pons()
	im.WaitSeconds = 1
	e := &imageEngine{
		out:  map[string]string{"version": "ponsd v1\n", "image": "1\n", "port": "127.0.0.1:49153\n", "logs": "listen: address in use"},
		fail: map[string]bool{"shell": true},
	}
	var out strings.Builder
	err := Image([]config.ImageSpec{im}, &out, found, e.exec)
	if err == nil || !strings.Contains(out.String(), "did not answer within 1s") || !strings.Contains(out.String(), "address in use") {
		t.Fatalf("Image = %v\n%s", err, out.String())
	}
	if !strings.Contains(strings.Join(e.ran, "\n"), "rm -f lateregate-ponsd-") {
		t.Error("the container was left running")
	}
}

// A declared shell and a declared skip are decisions with reasons; podman
// stands in where Docker is not installed; no engine at all is an error.
func TestImageDecisionsAndEngines(t *testing.T) {
	answering(t, nil)
	im := pons()
	im.AllowShell = "git runs hooks through sh"
	im.SkipStart = "needs Postgres first"
	e := &imageEngine{out: map[string]string{"version": "ponsd v1\n", "image": "1\n"}}
	podmanOnly := func(name string) (string, error) {
		if name == "podman" {
			return "/usr/bin/podman", nil
		}
		return "", errors.New("not found")
	}
	var out strings.Builder
	if err := Image([]config.ImageSpec{im}, &out, podmanOnly, e.exec); err != nil {
		t.Fatalf("Image = %v\n%s", err, out.String())
	}
	if !strings.HasPrefix(e.ran[0], "podman build") || strings.Contains(strings.Join(e.ran, "\n"), "--entrypoint") ||
		!strings.Contains(out.String(), "not started: needs Postgres first") {
		t.Fatalf("ran:\n%s\noutput:\n%s", strings.Join(e.ran, "\n"), out.String())
	}
	// podman starts the image with no writable tmpfs, as Docker does.
	e = &imageEngine{out: map[string]string{"version": "ponsd v1\n", "image": "1\n", "port": "127.0.0.1:1\n"}, fail: map[string]bool{"shell": true}}
	if err := Image([]config.ImageSpec{pons()}, &out, podmanOnly, e.exec); err != nil {
		t.Fatalf("Image under podman = %v\n%s", err, out.String())
	}
	if !strings.Contains(strings.Join(e.ran, "\n"), "--read-only --cap-drop ALL -p 127.0.0.1::8080 --read-only-tmpfs=false") {
		t.Errorf("podman ran:\n%s", strings.Join(e.ran, "\n"))
	}
	none := func(string) (string, error) { return "", errors.New("not found") }
	if err := Image([]config.ImageSpec{im}, &out, none, e.exec); err == nil || !strings.Contains(err.Error(), "neither docker nor podman") {
		t.Fatalf("no engine: %v", err)
	}
	if err := Image(nil, &out, found, e.exec); err == nil {
		t.Fatal("no image passed vacuously")
	}
}

// The host port is read from either engine's `port` output.
func TestHostPort(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:49153\n":                "49153",
		"0.0.0.0:32768\n[::]:32768\n":      "32768",
		"":                                 "",
		"Error: no public port 8080 found": "",
	} {
		if got := hostPort(in); got != want {
			t.Errorf("hostPort(%q) = %q, want %q", in, got, want)
		}
	}
}
