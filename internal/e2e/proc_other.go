// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build !unix

package e2e

import "os/exec"

// own gives the process time to exit after its context ends.
func own(cmd *exec.Cmd) {
	cmd.WaitDelay = stopWait
}
