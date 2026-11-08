// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Two starts of one service in one suite on one pid get different names, so a
// name another container holds is not one of ours.
func TestEachStartGetsItsOwnContainerName(t *testing.T) {
	name := regexp.MustCompile(`--name (lateregate-e2e-store-postgres-\d+-[0-9a-f]{8}) `)
	var seen []string
	for range 2 {
		e := scripted()
		h := testHost(e, io.Discard)
		if _, err := h.Start(t.Context(), config.E2ESuite{Services: []string{config.ServicePostgres}}); err != nil {
			t.Fatal(err)
		}
		m := name.FindStringSubmatch(e.ran[0])
		if m == nil {
			t.Fatalf("run command %q names no random suffix", e.ran[0])
		}
		seen = append(seen, m[1])
	}
	if seen[0] == seen[1] {
		t.Fatalf("both starts were named %s", seen[0])
	}
}

// Stop removes the container id a successful run printed, and nothing else.
func TestStopRemovesTheIDTheEnginePrinted(t *testing.T) {
	e := scripted()
	h := testHost(e, io.Discard)
	e.out["run"] = "WARNING: something\n" + containerID + "\n"
	if _, err := h.Start(t.Context(), config.E2ESuite{Services: []string{config.ServicePostgres}}); err != nil {
		t.Fatal(err)
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if last := e.ran[len(e.ran)-1]; last != "podman rm -f -v "+containerID {
		t.Fatalf("last command %q", last)
	}
}

// The refusal an engine prints on standard error reaches the error the
// person running the bar reads, a few lines of it.
func TestAStartThatFailsSaysWhyTheEngineRefused(t *testing.T) {
	e := scripted()
	h := testHost(e, io.Discard)
	h.RunCapturing = func(string, ...string) ([]byte, []byte, error) {
		return nil, []byte("\nError: name in use\nsecond\nthird\nfourth\nfifth\nsixth\n"), errors.New("exit status 125")
	}
	_, _, err := h.launch("postgres", "img", "5432", nil)
	if err == nil {
		t.Fatal("started")
	}
	want := "the container does not start: exit status 125: Error: name in use; second; third; fourth; fifth"
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

func TestOSCapturingKeepsStandardErrorApart(t *testing.T) {
	// /bin/sh by its path: the bar runs this test with a PATH stripped to the
	// Go toolchain.
	out, errOut, err := OSCapturing("/bin/sh", "-c", "echo id; echo refused >&2; exit 3")
	if err == nil || string(out) != "id\n" || string(errOut) != "refused\n" {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
	if got := firstLines(nil, 5); got != "" {
		t.Errorf("firstLines(nil) = %q", got)
	}
	if got := lastLine([]byte(" \n")); got != "" {
		t.Errorf("lastLine of blanks = %q", got)
	}
}

// A container that already holds the name this host would use is another's:
// the start fails, and Stop leaves that container where it is.
func TestStopLeavesAContainerItDidNotStart(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("needs podman")
	}
	const suite, pid = "names-test", 4242
	// A fixed source makes the random part of the name known to the test.
	name := "lateregate-e2e-" + suite + "-postgres-4242-01020304"
	// Someone else's container: no labels, not made by this Host.
	if out, err := exec.Command("podman", "run", "-d", "--name", name, "alpine:3.22", "sleep", "300").CombinedOutput(); err != nil {
		t.Skipf("cannot start the neighbor: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("podman", "rm", "-f", "-v", name).CombinedOutput(); err != nil {
			t.Errorf("removing the neighbor: %v\n%s", err, out)
		}
	})

	h := NewHost("podman", suite, io.Discard, gates.OSExec(t.TempDir(), io.Discard))
	h.PID = pid
	h.Rand = bytes.NewReader([]byte{1, 2, 3, 4})
	h.RunCapturing = OSCapturing
	_, _, err := h.launch("postgres", "alpine:3.22", "5432", nil, "sleep", "300")
	if err == nil {
		t.Fatal("the name is taken, so the start must fail")
	}
	if !strings.Contains(err.Error(), "exit status 125: ") {
		t.Errorf("err = %q, want the engine's reason after the exit status", err)
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("podman", "container", "inspect", name).Run(); err != nil {
		t.Fatalf("Stop removed %s, which this Host did not start", name)
	}
}
