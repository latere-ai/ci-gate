// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package reap

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"testing"

	"latere.ai/x/ci-gate/internal/containers"
)

// Inside a lateregate run, Sweep leaves what carries the run's label to the
// run's own check, which reports it; a container of another run, or of none,
// whose owner exited is still removed.
func TestSweepLeavesWhatThisRunLabeledToTheRunsCheck(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	dead := exitedPid(t)
	e := &engine{
		list: []containers.Container{
			{ID: "this-run", Labels: containers.Labels(dead, host, "r1")},
			{ID: "other-run", Labels: containers.Labels(dead, host, "r2")},
			{ID: "no-run", Labels: containers.Labels(dead, host, "")},
		},
		networks: []containers.Network{
			{ID: "this-run-net", Labels: containers.Labels(dead, host, "r1")},
			{ID: "other-run-net", Labels: containers.Labels(dead, host, "r2")},
		},
	}
	t.Chdir(t.TempDir())
	l, err := net.Listen("unix", "e.sock")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: e}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("DOCKER_HOST", "unix://e.sock")
	t.Setenv(containers.RunEnv, "r1")

	n, err := Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/other-run", "/no-run", "/networks/other-run-net"}
	if n != 3 || !slices.Equal(e.removed, want) {
		t.Fatalf("Sweep removed %d: %v; want %v, and nothing labeled run r1", n, e.removed, want)
	}
}

// exitedPid is the pid of a process that has exited.
func exitedPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// Labels names the owner with the host name the sweep compares against, so a
// container it labels is one a later sweep can read.
func TestLabelsCarryTheHostNameTheSweepReads(t *testing.T) {
	want := strconv.Itoa(os.Getpid()) + "@" + containers.Hostname()
	if got := Labels()[containers.OwnerLabel]; got != want {
		t.Fatalf("owner label = %q, want %q", got, want)
	}
}
