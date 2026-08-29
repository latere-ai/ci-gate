// Copyright 2026 Latere AI.
// Licensed under the Apache License, Version 2.0.

// Package gates holds the checks that drive the Go toolchain.
//
// Each takes its subprocess through an Exec, so the decision a gate makes is
// testable without a toolchain. What is left uninjected is the process
// wiring itself, which is a handful of statements and is exercised end to end
// by this repository's own gates.
package gates

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// Exec runs a command and returns its standard output. env, when non-nil,
// replaces the environment rather than adding to it. stream selects whether
// the child's output also reaches the terminal, which the test run needs and
// the diff-capturing gates do not.
type Exec func(env []string, stream bool, name string, args ...string) ([]byte, error)

// OSExec runs commands for real.
func OSExec(out io.Writer) Exec {
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		if env != nil {
			cmd.Env = env
		}
		var buf strings.Builder
		if stream {
			cmd.Stdout = io.MultiWriter(out, &buf)
			cmd.Stderr = out
		} else {
			cmd.Stdout = &buf
			// A gate that reads a diff decides on the diff alone. `go fix`
			// also exits non-zero when a package does not type-check, which
			// is a build error rather than a finding.
			cmd.Stderr = io.Discard
		}
		err := cmd.Run()
		return []byte(buf.String()), err
	}
}

// Hermetic runs the test suite with only the Go toolchain and the explicitly
// allowed directories on PATH.
//
// Three CI failures in one day came from tests that depended on what happened
// to be installed on the machine running them: systemctl present but
// unprivileged on a runner and absent on macOS, and a harness binary on a
// developer's PATH and not on a runner's. Each passed locally and failed in
// CI, which is the worst order to find out. Stripping PATH reproduces a
// runner's environment closely enough to catch that class before a push.
func Hermetic(cfg config.Hermetic, goBin string, out io.Writer, run Exec) error {
	dir, err := toolchainDir(goBin)
	if err != nil {
		return err
	}
	path := PathFor(dir, cfg.Allow)
	fmt.Fprintf(out, "PATH=%s\n", path)
	if _, err := run(envWithPath(os.Environ(), path), true, goBin, "test", "./..."); err != nil {
		return fmt.Errorf("hermetic test run failed: %w", err)
	}
	return nil
}

// PathFor builds the stripped PATH: the toolchain's own directory first, then
// whatever the repository declared it needs.
//
// A repository that names a directory here is making an ambient dependency
// visible, which is the point; the empty default is the strictest setting.
func PathFor(toolchainDir string, allow []string) string {
	return strings.Join(append([]string{toolchainDir}, allow...), string(os.PathListSeparator))
}

func envWithPath(env []string, path string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+path)
}

func toolchainDir(goBin string) (string, error) {
	if strings.ContainsRune(goBin, os.PathSeparator) {
		return filepath.Dir(goBin), nil
	}
	p, err := exec.LookPath(goBin)
	if err != nil {
		return "", fmt.Errorf("cannot locate the %s toolchain: %w", goBin, err)
	}
	return filepath.Dir(p), nil
}

// FmtCheck fails if any Go source is not gofmt-formatted.
func FmtCheck(out io.Writer, run Exec) error {
	listed, err := run(nil, false, "gofmt", "-l", ".")
	if err != nil {
		return fmt.Errorf("gofmt: %w", err)
	}
	files := nonEmptyLines(string(listed))
	if len(files) == 0 {
		fmt.Fprintln(out, "every Go source is gofmt-formatted")
		return nil
	}
	for _, f := range files {
		fmt.Fprintln(out, "  "+f)
	}
	return fmt.Errorf("%d file(s) are not gofmt-formatted; run gofmt -w .", len(files))
}

// Modernize fails on code a standard library call already covers.
//
// A repository may turn a fixer off, which is a decision. The gate verifies
// each named fixer still exists first: `go fix` rejects an unknown -name=false
// and the check would then pass silently, which is the one outcome worse than
// failing.
func Modernize(cfg config.Modernize, goBin string, out io.Writer, run Exec) error {
	args := []string{"fix", "-diff"}
	if len(cfg.Disable) > 0 {
		help, err := run(nil, false, goBin, "tool", "fix", "help")
		if err != nil {
			return fmt.Errorf("cannot list the available fixers: %w", err)
		}
		for _, f := range cfg.Disable {
			if !hasFixer(string(help), f) {
				return fmt.Errorf("go fix no longer carries the %q fixer, so -%s=false "+
					"would be rejected and this check would pass silently; drop it from "+
					"modernize.disable", f, f)
			}
			args = append(args, "-"+f+"=false")
		}
	}
	args = append(args, "./...")

	patch, err := run(nil, false, goBin, args...)
	if len(patch) > 0 {
		out.Write(patch)
		return fmt.Errorf("the diff above is already in the standard library; apply it with go fix")
	}
	if err != nil {
		// An empty patch with a non-zero exit is a build error, and the build
		// is another gate's job.
		fmt.Fprintln(out, "no modernization found (go fix reported no patch)")
		return nil
	}
	fmt.Fprintln(out, "no modernization found")
	return nil
}

// hasFixer reports whether `go tool fix help` lists a fixer by that name. The
// listing indents each name, so a substring match alone would accept a fixer
// mentioned in prose.
func hasFixer(help, name string) bool {
	for line := range strings.SplitSeq(help, "\n") {
		if !strings.HasPrefix(line, " ") {
			continue
		}
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

func nonEmptyLines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
