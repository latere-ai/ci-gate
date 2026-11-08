// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Ctrl-C reaches the gate and its suite together. The gate gives the sandbox
// back, with whatever the suite had written in it, and exits non-zero.
func TestInterruptedTempDirGateReleasesItsSandbox(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "lateregate")
	build := exec.Command("go", "build", "-o", bin, ".")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	repo := filepath.Join(tmp, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	// The suite under test is a shell script. It names /bin/sh and sets its own
	// PATH, because the bar runs this test with a PATH stripped to the Go
	// toolchain, where neither the shell nor mkdir and sleep are found.
	cmd := exec.Command(bin, "tempdir", "-C", repo, "--", "/bin/sh", "-c",
		`PATH=/usr/bin:/bin; mkdir "$TMPDIR/leak"; head -c 1000000 /dev/zero > "$TMPDIR/leak/f"; sleep 30`)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The suite's file appears inside the gate's sandbox, whatever it is named.
	var leaked []string
	for range 200 {
		leaked, _ = filepath.Glob(filepath.Join(tmp, "lateregate-tempdir-*", "leak", "f"))
		if len(leaked) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(leaked) == 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatal("the suite never wrote into the sandbox")
	}
	sandbox := filepath.Dir(filepath.Dir(leaked[0]))
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) // what a terminal sends
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Errorf("an interrupted gate must exit non-zero, got %v", err)
	}
	if _, err := os.Stat(sandbox); err == nil {
		t.Fatalf("%s outlived the interrupted gate", sandbox)
	}
}
