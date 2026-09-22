// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package gates

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// lockable reports that the sandbox can be shared between runs: this platform
// has flock, whose lock the kernel drops when the holding process exits.
const lockable = true

// lockFile takes an exclusive lock on path, creating the file, and waits for a
// holder in another process to finish. The file stays: removing a lock file
// while another process waits on it hands the two of them different locks.
func lockFile(path string, out io.Writer) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	err = flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		_, _ = fmt.Fprintf(out, "waiting for another run of this repository's suite to release %s\n", path)
		err = flock(fd, syscall.LOCK_EX)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = flock(fd, syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// flock retries a call a signal interrupted, which a blocking wait on a busy
// machine sees.
func flock(fd, how int) error {
	for {
		err := syscall.Flock(fd, how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
