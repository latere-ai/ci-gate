// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Proc is one process a suite runs.
type Proc struct {
	Dir    string
	Env    []string
	Name   string
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
}

// Start runs a process to its end, or stops it when ctx ends. It is a type
// so a test runs a suite without starting one.
type Start func(ctx context.Context, p Proc) error

// stopWait is how long a process has to exit after it is asked to before it
// is killed.
const stopWait = 10 * time.Second

// OSStart runs p as a process of this machine. The environment is p's alone:
// a suite runs with the machine's full environment, not a gate's hermetic
// one, and the caller says which.
func OSStart(ctx context.Context, p Proc) error {
	cmd := exec.CommandContext(ctx, p.Name, p.Args...)
	cmd.Dir, cmd.Env = p.Dir, p.Env
	cmd.Stdout, cmd.Stderr = p.Stdout, p.Stderr
	own(cmd)
	return cmd.Run()
}

// lockedWriter serializes writes from the two pipes of a process and from
// the runner itself.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
