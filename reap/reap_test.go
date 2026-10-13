// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package reap

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"latere.ai/x/ci-gate/internal/containers"
)

func TestLabelsNameThisProcessAndItsRun(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(containers.RunEnv, "")
	got := Labels()
	if got[containers.OwnerLabel] != strconv.Itoa(os.Getpid())+"@"+host {
		t.Fatalf("owner label = %q", got[containers.OwnerLabel])
	}
	if _, ok := got[containers.RunLabel]; ok {
		t.Fatal("a process outside a lateregate run carries a run label")
	}
	t.Setenv(containers.RunEnv, "r42")
	if got := Labels(); got[containers.RunLabel] != "r42" {
		t.Fatalf("run label = %q, want the run lateregate named", got[containers.RunLabel])
	}
}

func TestSweepWithNoEngineHasNothingToDo(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	n, err := Sweep(context.Background())
	if n != 0 || err != nil {
		t.Fatalf("Sweep = %d, %v; want nothing done and no error", n, err)
	}
}

// engine serves a container list over a unix socket and records removals.
type engine struct {
	mu       sync.Mutex
	list     []containers.Container
	networks []containers.Network
	removed  []string
}

func (e *engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.Method == http.MethodDelete {
		e.removed = append(e.removed, strings.TrimPrefix(r.URL.Path, "/containers"))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/networks" {
		_ = json.NewEncoder(w).Encode(e.networks)
		return
	}
	_ = json.NewEncoder(w).Encode(e.list)
}

func TestSweepRemovesWhatAnExitedProcessLeft(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	exited := exec.Command(os.Args[0], "-test.run=^$")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	e := &engine{
		list: []containers.Container{
			{ID: "left", Labels: containers.Labels(exited.Process.Pid, host, "")},
			{ID: "mine", Labels: Labels()},
		},
		networks: []containers.Network{
			{ID: "left-net", Labels: containers.Labels(exited.Process.Pid, host, "")},
			{ID: "my-net", Labels: Labels()},
		},
	}
	// A short relative path: a unix socket path is limited to about a
	// hundred bytes, which a test's temporary directory can exceed alone.
	t.Chdir(t.TempDir())
	l, err := net.Listen("unix", "e.sock")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: e}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("DOCKER_HOST", "unix://e.sock")

	n, err := Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/left", "/networks/left-net"}; n != 2 || !slices.Equal(e.removed, want) {
		t.Fatalf("Sweep removed %d: %v; want %v, the exited process's container and network", n, e.removed, want)
	}
}
