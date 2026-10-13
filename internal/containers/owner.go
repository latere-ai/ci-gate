// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// The labels a test helper puts on each container it starts, and the variable
// that carries the run label to it.
const (
	// OwnerLabel is pid@host of the process that started the container. A
	// pid means nothing on another host, so the host travels with it.
	OwnerLabel = "ai.latere.ci-gate.owner"
	// RunLabel is the lateregate run the starting process belongs to.
	RunLabel = "ai.latere.ci-gate.run"
	// RunEnv is the variable lateregate sets on the suite to name the run.
	RunEnv = "LATEREGATE_RUN"
)

// Labels are the labels for a container started by process pid on host,
// during run when run is not empty.
func Labels(pid int, host, run string) map[string]string {
	l := map[string]string{OwnerLabel: strconv.Itoa(pid) + "@" + host}
	if run != "" {
		l[RunLabel] = run
	}
	return l
}

// owner reads an OwnerLabel value.
func owner(v string) (int, string, bool) {
	p, host, ok := strings.Cut(v, "@")
	if !ok || host == "" {
		return 0, "", false
	}
	pid, err := strconv.Atoi(p)
	if err != nil || pid <= 0 {
		return 0, "", false
	}
	return pid, host, true
}

// Sweep removes each labelled container whose owner ran on host and is no
// longer alive, and returns the ones it removed.
//
// Liveness is the only test. A container is never judged by its age, because
// a suite on this machine may be minutes into using one; and a container
// whose owner ran elsewhere is left alone, because its pid names some other
// machine's process.
func Sweep(ctx context.Context, e *Engine, host string, alive func(pid int) bool) ([]Container, error) {
	cs, err := e.List(ctx, OwnerLabel)
	if err != nil {
		return nil, err
	}
	var gone []Container
	var errs []error
	for _, c := range cs {
		pid, h, ok := owner(c.Labels[OwnerLabel])
		if !ok || h != host || alive(pid) {
			continue
		}
		if err := e.Remove(ctx, c.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		gone = append(gone, c)
	}
	return gone, errors.Join(errs...)
}

// Leftovers removes every container labelled with run, whoever owns it, and
// returns the ones it removed. It is for after the run: once every process of
// the run has exited, each container still carrying its label is one nothing
// stopped.
func Leftovers(ctx context.Context, e *Engine, run string) ([]Container, error) {
	cs, err := e.List(ctx, RunLabel+"="+run)
	if err != nil {
		return nil, err
	}
	var gone []Container
	var errs []error
	for _, c := range cs {
		if err := e.Remove(ctx, c.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		gone = append(gone, c)
	}
	return gone, errors.Join(errs...)
}
