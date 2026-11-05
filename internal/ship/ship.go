// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package ship builds, publishes and deploys a release from a workstation,
// for a repository whose .lateregate.yaml carries a ship block.
//
// Every command goes through a gates.Exec, so what a step decides is
// testable without a container engine, a cluster or a password manager.
package ship

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"latere.ai/x/ci-gate/internal/changelog"
	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Deps is everything a command reaches outside itself with.
type Deps struct {
	Root string
	Cfg  *config.Config
	Out  io.Writer
	// Exec runs commands in Root; In runs them in another directory.
	Exec gates.Exec
	In   func(dir string) gates.Exec
	// LookPath finds a program on PATH.
	LookPath func(string) (string, error)
	// Environ is the environment commands inherit.
	Environ func() []string
	// Signals, when set, replaces the interrupt and terminate signals
	// deploy listens for, and Exit replaces os.Exit; both exist for tests.
	Signals chan os.Signal
	Exit    func(int)
}

// New wires Deps to the real machine.
func New(root string, cfg *config.Config, out io.Writer) Deps {
	return Deps{
		Root:     root,
		Cfg:      cfg,
		Out:      out,
		Exec:     gates.OSExec(root, out),
		In:       func(dir string) gates.Exec { return gates.OSExec(dir, out) },
		LookPath: exec.LookPath,
		Environ:  os.Environ,
		Exit:     os.Exit,
	}
}

func (d Deps) ship() (*config.Ship, error) {
	if d.Cfg == nil || d.Cfg.Ship == nil {
		return nil, fmt.Errorf("%s has no ship block: this repository does not build and ship from a workstation", config.Name)
	}
	return d.Cfg.Ship, nil
}

func (d Deps) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(d.Out, format, a...)
}

// engine is the first container engine on PATH.
func (d Deps) engine() (string, error) {
	for _, e := range gates.Engines {
		if _, err := d.LookPath(e); err == nil {
			return e, nil
		}
	}
	return "", fmt.Errorf("neither %s is on PATH; the image is built and published with one", strings.Join(gates.Engines, " nor "))
}

// git runs git in the repository and returns its trimmed output.
func (d Deps) git(args ...string) (string, error) {
	out, err := d.Exec(nil, false, "git", args...)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}

// combined runs a command with its standard error folded into the output
// the seam returns, which is the only way to read what a failing engine or
// cluster client said. The output is returned with the error.
func combined(run gates.Exec, env []string, name string, args ...string) ([]byte, error) {
	return run(env, false, "sh", append([]string{"-c", `exec "$0" "$@" 2>&1`, name}, args...)...)
}

// withEnv returns base with each KEY=value set, replacing earlier values.
func withEnv(base []string, set ...string) []string {
	keys := map[string]bool{}
	for _, kv := range set {
		k, _, _ := strings.Cut(kv, "=")
		keys[k] = true
	}
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !keys[k] {
			out = append(out, kv)
		}
	}
	return append(out, set...)
}

// without returns base minus the named variables.
func without(base []string, names ...string) []string {
	return slices.DeleteFunc(slices.Clone(base), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(names, k)
	})
}

// script runs each command with bash, failing on the first error or unset
// variable or failed pipe, as the shared pipeline's commands run.
func script(run gates.Exec, env []string, label string, cmds []string) error {
	for _, c := range cmds {
		if _, err := run(env, true, "bash", "-c", "set -euo pipefail\n"+c); err != nil {
			return fmt.Errorf("%s `%s`: %w", label, firstLine(c), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// stable reports whether tag is a release version a workstation ships:
// vMAJOR.MINOR.PATCH with an optional prerelease and no build metadata, which
// an image tag cannot hold.
func stable(tag string) bool {
	return changelog.IsReleaseTag(tag) && !strings.Contains(tag, "+") && semver.IsValid(tag)
}

func prerelease(tag string) bool { return semver.Prerelease(tag) != "" }

var authFailure = regexp.MustCompile(`(?i)\b401\b|\b403\b|denied|unauthorized|unauthenticated`)

// registryRefused says the registry turned the engine away and how to log in.
func registryRefused(engine, registry, verb string) error {
	scope := "read:packages"
	if verb == "push" {
		scope = "write:packages"
	}
	return fmt.Errorf("the registry refused the %s: %s needs a login to %s with a token that has %s, e.g. `%s login %s`",
		verb, engine, registry, scope, engine, registry)
}

func registryOf(image string) string {
	host, _, _ := strings.Cut(image, "/")
	return host
}

var errNoTag = errors.New("no release tag")

func changelogNotes(d Deps, tag string) (string, error) {
	return changelog.Notes(d.Root, tag, tag, d.Exec)
}
