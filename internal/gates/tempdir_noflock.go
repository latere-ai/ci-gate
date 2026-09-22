// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package gates

import (
	"errors"
	"io"
)

// lockable reports that the sandbox cannot be shared between runs here: with
// no flock, a lock a killed run held would outlive it, so every run makes a
// directory of its own and gives up the test cache that a shared one allows.
const lockable = false

func lockFile(string, io.Writer) (func(), error) {
	return nil, errors.New("no file lock on this platform")
}
