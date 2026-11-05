// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// Every command a ship command runs can fail, and each failure stops it with
// an error and leaves nothing under TMPDIR. The sweep fails the commands one at
// a time, in the order they run.
func TestEveryCommandFailureStopsTheCommand(t *testing.T) {
	plain := func(t *testing.T) *rig {
		r := newRig(t, goodShip+"    preflight: [pre]\n    after_roll: [post]\n")
		r.remote = lsRemote
		r.d.Cfg.Ship.Image.Prepare = []string{"make bin"}
		return r
	}
	cred := func(t *testing.T) *rig {
		r, _ := credRig(t)
		return r
	}
	cases := []struct {
		name  string
		setup func(*testing.T) *rig
		run   func(*rig) error
	}{
		{"build", plain, func(r *rig) error { return Build(r.d) }},
		{"push", plain, func(r *rig) error { return Push(r.d) }},
		{"deploy newest", plain, func(r *rig) error { return Deploy(r.d, "") }},
		{"deploy tag", plain, func(r *rig) error { return Deploy(r.d, "v1.2.3") }},
		{"credential", cred, func(r *rig) error { return DeployCredential(r.d, "admin") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := tc.setup(t)
			if err := tc.run(probe); err != nil {
				t.Fatalf("the unfailed run: %v", err)
			}
			for i := range probe.calls {
				r := tc.setup(t)
				inner := r.answer
				seen := 0
				var failed call
				r.answer = func(c call) ([]byte, error, bool) {
					seen++
					if seen-1 == i {
						failed = c
						return nil, fmt.Errorf("injected failure of %s", c), true
					}
					if inner != nil {
						return inner(c)
					}
					return nil, nil, false
				}
				err := tc.run(r)
				// A tag that is not local is fetched, so failing the first
				// look for it is not a failure.
				retried := failed.name == "git" && slices.Contains(failed.args, "--verify")
				if err == nil && !retried {
					t.Errorf("failing call %d (%s) was swallowed", i, failed)
				}
				if left := r.leftovers(); len(left) != 0 {
					t.Errorf("failing call %d (%s) left %v", i, failed, left)
				}
			}
		})
	}
}

func TestNewWiresTheRealMachine(t *testing.T) {
	d := New(t.TempDir(), &config.Config{}, io.Discard)
	if d.Exec == nil || d.In == nil || d.LookPath == nil || d.Environ == nil || d.Exit == nil {
		t.Fatalf("incomplete: %+v", d)
	}
	if _, err := d.ship(); err == nil {
		t.Error("no ship block must be refused")
	}
	if out, err := d.In(t.TempDir())(nil, false, "git", "--version"); err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Errorf("real exec: %q %v", out, err)
	}
}

func TestCommandsFailWhereTheyCannotMakeADirectory(t *testing.T) {
	r := newRig(t, "")
	rc, _ := credRig(t)
	t.Setenv("TMPDIR", "/nonexistent/for/lateregate")
	if err := Deploy(r.d, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "deploy directory") {
		t.Errorf("deploy: %v", err)
	}
	if err := Push(r.d); err == nil {
		t.Error("push needs a directory for the digest")
	}
	if err := DeployCredential(rc.d, "admin"); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("credential: %v", err)
	}
	if err := PublishRelease(r.d, "v1.2.3"); err == nil {
		t.Error("the notes need a file")
	}
}
