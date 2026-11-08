// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// Leftovers checks the run label on each entry itself, so an engine or proxy
// that ignores the label filter and lists everything gets only the run's own
// removed.
func TestLeftoversRemovesOnlyWhatCarriesTheRunLabel(t *testing.T) {
	var mu sync.Mutex
	var removed []string
	all := []Container{
		owned("mine", "1@box", "r1"),
		owned("other-run", "2@box", "r2"),
		{ID: "unlabeled", Labels: map[string]string{}},
	}
	nets := []Network{
		{ID: "my-net", Labels: owned("", "1@box", "r1").Labels},
		{ID: "other-net", Labels: owned("", "2@box", "r2").Labels},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodDelete:
			removed = append(removed, strings.TrimPrefix(r.URL.Path, "/"))
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/networks":
			_ = json.MarshalWrite(w, nets)
		default: // ignores the filters query
			_ = json.MarshalWrite(w, all)
		}
	}))
	t.Cleanup(srv.Close)
	e := &Engine{base: srv.URL, client: srv.Client()}
	if _, err := Leftovers(context.Background(), e, "r1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"containers/mine", "networks/my-net"}; !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want only %v, what carries run=r1", removed, want)
	}
}

// A machine that will not name itself still labels its containers with a
// well-formed owner, one no sweep will mistake for another process's.
func TestHostnameFallsBackToAnUnknownHostTheLabelCanCarry(t *testing.T) {
	if got := hostName("box", nil); got != "box" {
		t.Errorf("hostName = %q", got)
	}
	for _, got := range []string{hostName("", nil), hostName("box", errors.New("no name"))} {
		if got != UnknownHost {
			t.Errorf("hostName = %q, want %q", got, UnknownHost)
		}
		if _, _, ok := owner(Labels(7, got, "")[OwnerLabel]); !ok {
			t.Errorf("the owner label with host %q cannot be read back", got)
		}
	}
	if Hostname() == "" {
		t.Error("Hostname is empty")
	}
}
