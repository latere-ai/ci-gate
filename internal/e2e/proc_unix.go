// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build unix

package e2e

import (
	"os/exec"
	"syscall"
)

// own puts the process in a group of its own and, when its context ends,
// asks the whole group to stop: a suite is `go test`, `make` or a shell, and
// the test binary or cluster tool it started would otherwise outlive it.
func own(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = stopWait
}
