// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"errors"
	"os"
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

// Labels are the labels for a container or network made by process pid on
// host, during run when run is not empty.
func Labels(pid int, host, run string) map[string]string {
	l := map[string]string{OwnerLabel: strconv.Itoa(pid) + "@" + host}
	if run != "" {
		l[RunLabel] = run
	}
	return l
}

// UnknownHost is the host name an owner label carries when the machine will
// not say its own.
const UnknownHost = "unknown-host"

// Hostname is this machine's name, as the owner label carries it. A machine
// that will not say gets a name no other process shares, so the label stays
// well formed and a sweep leaves the container alone rather than never being
// able to read the label.
func Hostname() string {
	return hostName(os.Hostname())
}

func hostName(h string, err error) string {
	if err != nil || h == "" {
		return UnknownHost
	}
	return h
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

// Removed is what a sweep or a run's check took off the engine.
type Removed struct {
	Containers []Container
	Networks   []Network
}

// Len is how many containers and networks were removed.
func (r Removed) Len() int { return len(r.Containers) + len(r.Networks) }

// Sweep removes each labeled container, then each labeled network, whose
// owner ran on host and is no longer alive, and returns what it removed.
//
// Liveness is the only test. A container is never judged by its age, because
// a suite on this machine may be minutes into using one; and a container
// whose owner ran elsewhere is left alone, because its pid names some other
// machine's process. Containers go first, so a network is free by its turn.
func Sweep(ctx context.Context, e *Engine, host string, alive func(pid int) bool) (Removed, error) {
	return SweepOutside(ctx, e, host, alive, "")
}

// SweepOutside is Sweep for a caller inside the lateregate run named run,
// when run is not empty: whatever carries that run's label is left alone,
// because the run's own check names it and removes it after the run, and a
// sweep that removed it first would leave the check nothing to report.
func SweepOutside(ctx context.Context, e *Engine, host string, alive func(pid int) bool, run string) (Removed, error) {
	orphaned := func(labels map[string]string) bool {
		if run != "" && labels[RunLabel] == run {
			return false
		}
		pid, h, ok := owner(labels[OwnerLabel])
		return ok && h == host && !alive(pid)
	}
	return remove(ctx, e, OwnerLabel, orphaned)
}

// Leftovers removes every container and network labeled with run, whoever
// owns it, and returns what it removed. It is for after the run: once every
// process of the run has exited, each one still carrying its label is one
// nothing stopped. The label is checked on each entry as well as in the
// request, because an engine or proxy may list more than the filter asks for.
func Leftovers(ctx context.Context, e *Engine, run string) (Removed, error) {
	return remove(ctx, e, RunLabel+"="+run, func(labels map[string]string) bool { return labels[RunLabel] == run })
}

// remove takes off the engine each container, then each network, that
// carries label and that pick chooses by its labels.
func remove(ctx context.Context, e *Engine, label string, pick func(map[string]string) bool) (Removed, error) {
	var gone Removed
	cs, err := e.List(ctx, label)
	if err != nil {
		return gone, err
	}
	var errs []error
	for _, c := range cs {
		if !pick(c.Labels) {
			continue
		}
		if err := e.Remove(ctx, c.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		gone.Containers = append(gone.Containers, c)
	}
	ns, err := e.Networks(ctx, label)
	if err != nil {
		return gone, errors.Join(append(errs, err)...)
	}
	for _, n := range ns {
		if !pick(n.Labels) {
			continue
		}
		if err := e.RemoveNetwork(ctx, n.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		gone.Networks = append(gone.Networks, n)
	}
	return gone, errors.Join(errs...)
}
