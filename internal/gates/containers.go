// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"latere.ai/x/ci-gate/internal/containers"
)

// findEngine is the engine the suite's helpers would reach. A variable so a
// test can hand the watch an engine of its own.
var findEngine = containers.Find

// Containers runs the suite and reports each container it left behind.
func Containers(goBin string, out io.Writer, run Exec) error {
	w := watchContainers(out)
	env := os.Environ()
	if w != nil {
		env = w.env(env)
	}
	_, runErr := run(env, true, goBin, "test", "./...")
	// The leak comes before the suite's own verdict, as in tempdir: a red
	// suite gets re-run, and a leak shown only on a green one is never seen.
	if w != nil {
		if f := w.leftovers(out); f != "" {
			return errors.New(f)
		}
	}
	if runErr != nil {
		return fmt.Errorf("the test suite failed: %w", runErr)
	}
	if w != nil {
		_, _ = fmt.Fprintln(out, "the suite leaves no container behind")
	}
	return nil
}

// containerWatch is one run's view of the engine: the run's label, and the
// engine to ask afterwards what still carries it.
type containerWatch struct {
	engine *containers.Engine
	run    string
}

// watchContainers sweeps what earlier runs left and names this run, or
// returns nil, saying why, when there is no engine to watch.
//
// The sweep removes containers whose owner has exited, from any run of any
// repository on this machine: a run that was killed never reached its own
// check, and the next one to start is the first chance anything has to
// remove what it left. Nothing the sweep finds fails this run.
func watchContainers(out io.Writer) *containerWatch {
	e, ok := findEngine()
	if !ok {
		_, _ = fmt.Fprintln(out, "no container engine socket, so the suite starts no container to watch")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	host, err := os.Hostname()
	if err != nil {
		_, _ = fmt.Fprintf(out, "the suite's containers are not watched: naming this host: %v\n", err)
		return nil
	}
	gone, err := containers.Sweep(ctx, e, host, containers.Alive)
	if err != nil {
		// An engine that does not answer now started nothing for the suite
		// either; one that answers after is still checked.
		_, _ = fmt.Fprintf(out, "the suite's containers are not watched: %v\n", err)
		return nil
	}
	for _, c := range gone {
		_, _ = fmt.Fprintf(out, "  removed %s %s, which an exited process left\n", containers.Short(c.ID), c.Image)
	}
	return &containerWatch{engine: e, run: runID()}
}

// env is env with the run named for the helpers to label with.
func (w *containerWatch) env(env []string) []string {
	return append(withoutKey(env, containers.RunEnv), containers.RunEnv+"="+w.run)
}

// leftovers removes and names each container still carrying the run's label,
// or returns "" when there is none. It runs after go test has exited, so every
// process of the run is gone and nothing will stop what is left.
func (w *containerWatch) leftovers(out io.Writer) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	left, err := containers.Leftovers(ctx, w.engine, w.run)
	for _, c := range left {
		_, _ = fmt.Fprintf(out, "  %s %s (%s)\n", containers.Short(c.ID), c.Image, c.State)
	}
	if err != nil {
		return fmt.Sprintf("removing the containers the suite left: %v", err)
	}
	if len(left) == 0 {
		return ""
	}
	noun := "containers"
	if len(left) == 1 {
		noun = "container"
	}
	return fmt.Sprintf("the suite left %d %s behind after its tests exited, removed now; "+
		"a test that starts a container stops it on every path, a failed start included",
		len(left), noun)
}

// runID names one run. Sixteen hex digits from the system's random source,
// which cannot fail on any platform Go supports.
func runID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
