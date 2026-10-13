// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeEngine serves the two requests this package makes over a container
// list of its own, and records what was removed.
type fakeEngine struct {
	mu      sync.Mutex
	list    []Container
	removed []string
	// status, when set, is what a removal of that id answers.
	status map[string]int
	// listStatus, when set, is what every listing answers.
	listStatus int
	// filters is the label filter of the last listing.
	filters []string
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/containers/json":
		if r.URL.Query().Get("all") != "1" {
			http.Error(w, "the listing must include stopped containers", http.StatusTeapot)
			return
		}
		var q map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &q); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.filters = q["label"]
		if f.listStatus != 0 {
			w.WriteHeader(f.listStatus)
			_, _ = w.Write([]byte(`{"message":"engine is resting"}`))
			return
		}
		var out []Container
		for _, c := range f.list {
			if matches(c, f.filters) {
				out = append(out, c)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/containers/"):
		if r.URL.Query().Get("force") != "1" || r.URL.Query().Get("v") != "1" {
			http.Error(w, "removal must force and take volumes", http.StatusTeapot)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/containers/")
		if s, ok := f.status[id]; ok {
			w.WriteHeader(s)
			return
		}
		f.removed = append(f.removed, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// matches applies a label filter the way an engine does: key alone, or
// key=value.
func matches(c Container, filters []string) bool {
	for _, f := range filters {
		k, v, hasValue := strings.Cut(f, "=")
		got, ok := c.Labels[k]
		if !ok || hasValue && got != v {
			return false
		}
	}
	return true
}

func serve(t *testing.T, f *fakeEngine) *Engine {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Engine{base: srv.URL, client: srv.Client()}
}

func owned(id, owner, run string) Container {
	l := map[string]string{OwnerLabel: owner}
	if run != "" {
		l[RunLabel] = run
	}
	return Container{ID: id, Image: "postgres:17-alpine", State: "running", Labels: l}
}

func TestLabelsNameTheOwnerAndTheRun(t *testing.T) {
	got := Labels(42, "box", "r1")
	if got[OwnerLabel] != "42@box" || got[RunLabel] != "r1" || len(got) != 2 {
		t.Fatalf("Labels = %v", got)
	}
	if _, ok := Labels(42, "box", "")[RunLabel]; ok {
		t.Fatal("a process outside a run must not carry a run label")
	}
}

func TestOwnerReadsOnlyAWellFormedLabel(t *testing.T) {
	if pid, host, ok := owner("42@box"); !ok || pid != 42 || host != "box" {
		t.Fatalf("owner(42@box) = %d %q %v", pid, host, ok)
	}
	for _, bad := range []string{"", "42", "42@", "x@box", "0@box", "-3@box"} {
		if _, _, ok := owner(bad); ok {
			t.Errorf("owner(%q) accepted a label it cannot judge", bad)
		}
	}
}

func TestSweepRemovesOnlyWhatAnExitedOwnerOnThisHostLeft(t *testing.T) {
	f := &fakeEngine{list: []Container{
		owned("dead-here", "100@box", ""),
		owned("alive-here", "200@box", ""),
		owned("dead-elsewhere", "100@other", ""),
		owned("unreadable", "nobody", ""),
		{ID: "unlabelled", Labels: map[string]string{"org.testcontainers": "true"}},
	}}
	alive := func(pid int) bool { return pid == 200 }
	gone, err := Sweep(context.Background(), serve(t, f), "box", alive)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0].ID != "dead-here" || !slices.Equal(f.removed, []string{"dead-here"}) {
		t.Fatalf("swept %v, removed %v; want only dead-here", gone, f.removed)
	}
	if !slices.Equal(f.filters, []string{OwnerLabel}) {
		t.Fatalf("listed with %v, want the owner label", f.filters)
	}
}

func TestSweepKeepsGoingPastARemovalTheEngineRefuses(t *testing.T) {
	f := &fakeEngine{
		list:   []Container{owned("stuck", "1@box", ""), owned("fine", "2@box", "")},
		status: map[string]int{"stuck": http.StatusInternalServerError},
	}
	gone, err := Sweep(context.Background(), serve(t, f), "box", func(int) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "removing container stuck") {
		t.Fatalf("err = %v, want the refused removal named", err)
	}
	if len(gone) != 1 || gone[0].ID != "fine" {
		t.Fatalf("swept %v, want fine removed despite stuck", gone)
	}
}

func TestSweepReportsAnEngineThatDoesNotList(t *testing.T) {
	f := &fakeEngine{listStatus: http.StatusServiceUnavailable}
	_, err := Sweep(context.Background(), serve(t, f), "box", Alive)
	if err == nil || !strings.Contains(err.Error(), "engine is resting") {
		t.Fatalf("err = %v, want the engine's own message", err)
	}
}

func TestLeftoversRemovesEverythingTheRunLabelled(t *testing.T) {
	f := &fakeEngine{
		list: []Container{
			owned("mine-1", "1@box", "run-a"),
			owned("mine-2", "2@box", "run-a"),
			owned("theirs", "3@box", "run-b"),
			owned("gone-already", "4@box", "run-a"),
		},
		status: map[string]int{"gone-already": http.StatusNotFound},
	}
	gone, err := Leftovers(context.Background(), serve(t, f), "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 3 || !slices.Equal(f.removed, []string{"mine-1", "mine-2"}) {
		t.Fatalf("left %v, removed %v; want run-a's three, theirs untouched", gone, f.removed)
	}
	if !slices.Equal(f.filters, []string{RunLabel + "=run-a"}) {
		t.Fatalf("listed with %v, want the run label", f.filters)
	}
}

func TestLeftoversReportsWhatItCouldNotRemove(t *testing.T) {
	f := &fakeEngine{
		list:   []Container{owned("stuck", "1@box", "r")},
		status: map[string]int{"stuck": http.StatusForbidden},
	}
	if _, err := Leftovers(context.Background(), serve(t, f), "r"); err == nil {
		t.Fatal("a refused removal passed")
	}
	f.listStatus = http.StatusInternalServerError
	if _, err := Leftovers(context.Background(), serve(t, f), "r"); err == nil {
		t.Fatal("a refused listing passed")
	}
}

func TestRemoveTakesARaceForOneContainerAsDone(t *testing.T) {
	f := &fakeEngine{status: map[string]int{"racing": http.StatusConflict, "absent": http.StatusNotFound}}
	e := serve(t, f)
	for _, id := range []string{"racing", "absent"} {
		if err := e.Remove(context.Background(), id); err != nil {
			t.Errorf("Remove(%s) = %v", id, err)
		}
	}
}

func TestListRejectsWhatIsNotAList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{"))
	}))
	t.Cleanup(srv.Close)
	e := &Engine{base: srv.URL, client: srv.Client()}
	if _, err := e.List(context.Background(), OwnerLabel); err == nil {
		t.Fatal("a truncated listing decoded")
	}
}

func TestRefusedSaysTheStatusWithoutAMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	e := &Engine{base: srv.URL, client: srv.Client()}
	_, err := e.List(context.Background(), OwnerLabel)
	if err == nil || !strings.HasSuffix(err.Error(), "the engine answered 502") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnreachableEngineIsAnError(t *testing.T) {
	e := Socket("no-such.sock")
	if _, err := e.List(context.Background(), OwnerLabel); err == nil {
		t.Fatal("listing through a missing socket succeeded")
	}
	if err := e.Remove(context.Background(), "x"); err == nil {
		t.Fatal("removing through a missing socket succeeded")
	}
	bad := &Engine{base: "http://engine\x7f", client: http.DefaultClient}
	if _, err := bad.List(context.Background(), OwnerLabel); err == nil {
		t.Fatal("a malformed request was sent")
	}
}

// listenUnix serves h on a socket named by a short relative path: a unix
// socket path is limited to about a hundred bytes, and a test's temporary
// directory alone can exceed that.
func listenUnix(t *testing.T, h http.Handler) string {
	t.Helper()
	t.Chdir(t.TempDir())
	l, err := net.Listen("unix", "e.sock")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "e.sock"
}

func TestSocketSpeaksTheAPIOverTheSocket(t *testing.T) {
	f := &fakeEngine{list: []Container{owned("c1", "1@box", "")}}
	e := Socket(listenUnix(t, f))
	cs, err := e.List(context.Background(), OwnerLabel)
	if err != nil || len(cs) != 1 || cs[0].ID != "c1" {
		t.Fatalf("List = %v, %v", cs, err)
	}
}

func TestFindFollowsDockerHostAndThenTheDefaults(t *testing.T) {
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	sockets := func(paths ...string) func(string) bool {
		return func(p string) bool { return slices.Contains(paths, p) }
	}
	cases := []struct {
		name string
		env  map[string]string
		live []string
		want bool
	}{
		{"docker host socket", map[string]string{"DOCKER_HOST": "unix:///run/x.sock"}, []string{"/run/x.sock"}, true},
		{"docker host only", map[string]string{"DOCKER_HOST": "unix:///run/x.sock"}, []string{"/var/run/docker.sock"}, false},
		{"tcp is not reached", map[string]string{"DOCKER_HOST": "tcp://h:2375"}, []string{"/var/run/docker.sock"}, false},
		{"system socket", nil, []string{"/var/run/docker.sock"}, true},
		{"desktop socket", map[string]string{"HOME": "/h"}, []string{"/h/.docker/run/docker.sock"}, true},
		{"rootless podman", map[string]string{"XDG_RUNTIME_DIR": "/run/user/1"}, []string{"/run/user/1/podman/podman.sock"}, true},
		{"nothing", map[string]string{"HOME": "/h"}, nil, false},
	}
	for _, c := range cases {
		if _, ok := find(env(c.env), sockets(c.live...)); ok != c.want {
			t.Errorf("%s: found = %v, want %v", c.name, ok, c.want)
		}
	}
}

func TestIsSocketFollowsALink(t *testing.T) {
	f := &fakeEngine{}
	sock := listenUnix(t, f)
	if err := os.Symlink(sock, "link.sock"); err != nil {
		t.Fatal(err)
	}
	if !isSocket(sock) || !isSocket("link.sock") {
		t.Fatal("a socket, or a link to one, was not recognized")
	}
	if err := os.WriteFile("plain", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if isSocket("plain") || isSocket("missing") {
		t.Fatal("a plain file or a missing path counted as a socket")
	}
	t.Setenv("DOCKER_HOST", "unix://"+sock)
	if _, ok := Find(); !ok {
		t.Fatal("Find missed the socket DOCKER_HOST names")
	}
}

func TestAliveTellsARunningProcessFromAnExitedOne(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("this process is not alive")
	}
	if !Alive(0) || !Alive(-1) {
		t.Fatal("a pid that names no single process must count as alive, so nothing is removed for it")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(cmd.Process.Pid) {
		t.Fatal("an exited child counts as alive")
	}
}

func TestShortCutsAnIDToTwelve(t *testing.T) {
	if Short("0123456789abcdef") != "0123456789ab" || Short("abc") != "abc" {
		t.Fatal("Short")
	}
}
