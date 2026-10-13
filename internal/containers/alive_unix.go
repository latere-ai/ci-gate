// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build unix

package containers

import (
	"errors"
	"syscall"
)

// Alive reports whether process pid exists. Signal 0 checks without sending
// anything; EPERM means it exists and belongs to another user.
func Alive(pid int) bool {
	if pid <= 0 {
		return true
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
