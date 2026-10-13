// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package reap is for a test helper that starts containers: it labels each
// one with the process that owns it, and removes the ones whose owner died
// before it could stop them.
//
// A helper that starts a container once per test binary stops it after
// m.Run returns, which never happens when the binary panics on -timeout, is
// killed, or is interrupted. Ryuk, the reaper testcontainers runs, covers
// that where it can start; it cannot on Podman, which refuses the network it
// asks for. These two calls cover it on every engine:
//
//	_, _ = reap.Sweep(ctx)
//	ctr, err := postgres.Run(ctx, image, testcontainers.WithLabels(reap.Labels()))
//	if err != nil && ctr != nil {
//		_ = ctr.Terminate(ctx) // a failed start still made a container
//	}
//
// The labels also carry the lateregate run, so the suite gate can name and
// remove whatever the run left behind.
package reap

import (
	"context"
	"os"
	"syscall"

	"latere.ai/x/ci-gate/internal/containers"
)

// Labels returns the labels to put on each container the calling process
// starts: its owner, and the lateregate run it belongs to when there is one.
func Labels() map[string]string {
	host, _ := os.Hostname()
	// syscall.Getenv and not os.Getenv: the go command keys a package's
	// cached test result on each variable the test reads through package os,
	// and the run label changes on every run while deciding nothing about the
	// outcome. Read through os, it would keep every package that starts a
	// container from ever replaying from the cache.
	run, _ := syscall.Getenv(containers.RunEnv)
	return containers.Labels(os.Getpid(), host, run)
}

// Sweep removes each labelled container on this machine's engine whose owner
// has exited, and returns how many it removed. A container whose owner is
// alive is never touched, however old it is, so a sweep is safe while other
// suites run. With no engine socket there is nothing to sweep.
func Sweep(ctx context.Context) (int, error) {
	e, ok := containers.Find()
	if !ok {
		return 0, nil
	}
	host, err := os.Hostname()
	if err != nil {
		return 0, err
	}
	gone, err := containers.Sweep(ctx, e, host, containers.Alive)
	return len(gone), err
}
