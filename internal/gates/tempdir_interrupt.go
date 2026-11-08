// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// exitProcess ends the process once an interrupted gate has cleaned up. A test
// replaces it.
var exitProcess = os.Exit

// onInterrupt runs cleanup once when the process is sent SIGINT or SIGTERM,
// then ends it with the status a shell reports for that signal (130 or 143).
// Without it the signal ends the process before any deferred release runs,
// and the sandbox and whatever the suite wrote in it stay on disk. The
// returned function disarms it, and must be called when the gate is done.
func onInterrupt(out io.Writer, cleanup func()) (disarm func()) {
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-ch:
			_, _ = fmt.Fprintf(out, "\ninterrupted (%v): releasing the sandbox\n", sig)
			cleanup()
			code := 130
			if sig == syscall.SIGTERM {
				code = 143
			}
			exitProcess(code)
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
		})
	}
}

// once makes f run at most once, however many callers reach it: the
// interrupt handler and the gate's own deferred release both release the
// sandbox, and the second must wait for the first rather than repeat it.
func once(f func()) func() {
	var o sync.Once
	return func() { o.Do(f) }
}
