// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package containers

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// A unix docker.host in ~/.testcontainers.properties names the engine
// testcontainers reaches, so it is a candidate like DOCKER_HOST, and
// DOCKER_HOST still wins when both are set.
func TestFindReadsDockerHostFromTheTestcontainersProperties(t *testing.T) {
	home := t.TempDir()
	props := "# comment\ndocker.host = unix:///run/props.sock\nother=1\n"
	if err := os.WriteFile(filepath.Join(home, propertiesFile), []byte(props), 0o600); err != nil {
		t.Fatal(err)
	}
	only := func(paths ...string) func(string) bool {
		return func(p string) bool { return slices.Contains(paths, p) }
	}
	if _, ok := find(envOf(map[string]string{"HOME": home}), only("/run/props.sock")); !ok {
		t.Fatal("the socket docker.host names was not found")
	}
	if _, ok := find(envOf(map[string]string{"HOME": home}), only("/var/run/docker.sock")); ok {
		t.Fatal("docker.host names one engine, so the default socket must not be tried")
	}
	env := map[string]string{"HOME": home, "DOCKER_HOST": "unix:///run/env.sock"}
	if _, ok := find(envOf(env), only("/run/props.sock")); ok {
		t.Fatal("DOCKER_HOST must outrank the properties file")
	}
	if _, ok := find(envOf(env), only("/run/env.sock")); !ok {
		t.Fatal("the socket DOCKER_HOST names was not found")
	}
}

// A missing properties file is not an error; one that exists and cannot be
// read is said.
func TestMissingNamesTheEngineItCannotReachOrWhereItLooked(t *testing.T) {
	home := t.TempDir()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"tcp DOCKER_HOST", map[string]string{"DOCKER_HOST": "tcp://h:2375"},
			[]string{"the engine at DOCKER_HOST=tcp://h:2375 is not one this gate reaches", "the suite's containers are not watched"}},
		{"ssh DOCKER_HOST", map[string]string{"DOCKER_HOST": "ssh://u@h"}, []string{"DOCKER_HOST=ssh://u@h"}},
		{"nothing set", map[string]string{"HOME": home, "XDG_RUNTIME_DIR": "/run/user/1"},
			[]string{"no container engine found (looked at /var/run/docker.sock, " + filepath.Join(home, ".docker", "run", "docker.sock") + ", /run/user/1/podman/podman.sock)", "the suite's containers are not watched"}},
	} {
		got := missing(envOf(tc.env))
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", tc.name, got, w)
			}
		}
	}

	if err := os.WriteFile(filepath.Join(home, propertiesFile), []byte("docker.host=tcp://p:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := missing(envOf(map[string]string{"HOME": home}))
	if !strings.Contains(got, "docker.host=tcp://p:1 in ~/"+propertiesFile) {
		t.Errorf("properties engine not named: %q", got)
	}

	unreadable := t.TempDir()
	if err := os.Mkdir(filepath.Join(unreadable, propertiesFile), 0o700); err != nil {
		t.Fatal(err)
	}
	got = missing(envOf(map[string]string{"HOME": unreadable}))
	if !strings.Contains(got, "reading "+filepath.Join(unreadable, propertiesFile)) {
		t.Errorf("an unreadable properties file was not said: %q", got)
	}
	if got := Missing(); got == "" {
		t.Error("Missing is empty")
	}
}
