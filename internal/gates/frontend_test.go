// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func found(string) (string, error) { return "/usr/bin/x", nil }

// feMachine records each command a package's directory runs, failing the
// ones named "<dir> <args>" in fail.
type feMachine struct {
	ran  []string
	fail []string
}

func (m *feMachine) in(dir string) Exec {
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		line := dir + " " + name + " " + strings.Join(args, " ")
		m.ran = append(m.ran, line)
		if slices.Contains(m.fail, line) {
			return nil, errors.New("exit 1")
		}
		return nil, nil
	}
}

// Each package installs from its lockfile, then runs the scripts it defines
// in order, in its own directory; a missing script is skipped.
func TestFrontendInstallsThenRunsTheScriptsEachPackageDefines(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "frontend/package.json", `{"scripts": {"build": "vite build", "typecheck": "tsc", "dev": "vite"}}`)
	writeFile(t, root, "web/app/package.json", `{"scripts": {"test": "vitest run"}}`)
	m := &feMachine{}
	var calls []call
	var out strings.Builder
	if err := Frontend([]string{"frontend", "web/app/"}, root, &out, found, fake(t, &calls), m.in); err != nil {
		t.Fatalf("Frontend = %v\n%s", err, out.String())
	}
	want := []string{
		"frontend bun install --frozen-lockfile",
		"frontend bun run typecheck",
		"frontend bun run build",
		"web/app bun install --frozen-lockfile",
		"web/app bun run test",
	}
	if !slices.Equal(m.ran, want) {
		t.Fatalf("ran %q", m.ran)
	}
	if len(calls) != 2 || !slices.Contains(calls[0].args, "frontend/bun.lock") {
		t.Fatalf("tracked check %v", calls)
	}
}

// A package stops at its first failing step and the next package is still
// checked; the error names each failed package.
func TestFrontendNamesEveryFailedPackage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/package.json", `{"scripts": {"typecheck": "tsc", "test": "vitest"}}`)
	writeFile(t, root, "b/package.json", `{"scripts": {"build": "vite build"}}`)
	writeFile(t, root, "c/package.json", `{"scripts": {"dev": "vite"}}`)
	m := &feMachine{fail: []string{"a bun run typecheck"}}
	var out strings.Builder
	err := Frontend([]string{"a", "b", "c"}, root, &out, found, fake(t, &[]call{}), m.in)
	if err == nil || !strings.Contains(err.Error(), "2 of 3 frontend package(s) failed: a, c") {
		t.Fatalf("Frontend = %v", err)
	}
	if slices.Contains(m.ran, "a bun run test") || !slices.Contains(m.ran, "b bun run build") {
		t.Fatalf("ran %q", m.ran)
	}
	if !strings.Contains(out.String(), "defines none of typecheck, test, build, e2e") {
		t.Fatalf("a package with nothing to check is not named:\n%s", out.String())
	}
}

// What the gate cannot trust fails it: an untracked lockfile, a frozen
// install that does not match, a package.json that does not parse, no bun
// or no node on PATH, and no package named at all.
func TestFrontendRefusesWhatItCannotCheck(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/package.json", `{"scripts": {"build": "vite build"}}`)
	writeFile(t, root, "bad/package.json", `{"scripts": `)
	var out strings.Builder
	if err := Frontend([]string{"a"}, root, &out, found, fake(t, &[]call{}, errors.New("untracked")), (&feMachine{}).in); err == nil ||
		!strings.Contains(out.String(), "must both be tracked") {
		t.Fatalf("an untracked lockfile: %v\n%s", err, out.String())
	}
	out.Reset()
	m := &feMachine{fail: []string{"a bun install --frozen-lockfile"}}
	if err := Frontend([]string{"a"}, root, &out, found, fake(t, &[]call{}), m.in); err == nil ||
		!strings.Contains(out.String(), "does not match package.json") || slices.Contains(m.ran, "a bun run build") {
		t.Fatalf("a stale lockfile: %v %q", err, m.ran)
	}
	out.Reset()
	if err := Frontend([]string{"bad"}, root, &out, found, fake(t, &[]call{}), (&feMachine{}).in); err == nil ||
		!strings.Contains(out.String(), "does not parse") {
		t.Fatalf("a broken package.json: %v\n%s", err, out.String())
	}
	for _, missing := range []string{"bun", "node"} {
		look := func(name string) (string, error) {
			if name == missing {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		}
		if err := Frontend([]string{"a"}, root, &out, look, fake(t, &[]call{}), (&feMachine{}).in); err == nil ||
			!strings.Contains(err.Error(), missing+" is not on PATH") {
			t.Fatalf("no %s: %v", missing, err)
		}
	}
	if err := Frontend(nil, root, &out, found, fake(t, &[]call{}), (&feMachine{}).in); err == nil {
		t.Fatal("no package passed vacuously")
	}
}

// e2e runs last, after typecheck, test and build, and only where defined. The
// gate installs no browser itself.
func TestFrontendRunsE2EAfterBuild(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "web/package.json", `{"scripts": {"e2e": "playwright test", "build": "vite build", "test": "vitest", "typecheck": "tsc"}}`)
	writeFile(t, root, "only/package.json", `{"scripts": {"e2e": "playwright test"}}`)
	m := &feMachine{}
	var out strings.Builder
	if err := Frontend([]string{"web", "only"}, root, &out, found, fake(t, &[]call{}), m.in); err != nil {
		t.Fatalf("Frontend = %v\n%s", err, out.String())
	}
	want := []string{
		"web bun install --frozen-lockfile",
		"web bun run typecheck",
		"web bun run test",
		"web bun run build",
		"web bun run e2e",
		"only bun install --frozen-lockfile",
		"only bun run e2e",
	}
	if !slices.Equal(m.ran, want) {
		t.Fatalf("ran %q", m.ran)
	}
	if !strings.Contains(out.String(), "build and e2e where defined") {
		t.Errorf("final message:\n%s", out.String())
	}
	for _, c := range m.ran {
		if strings.Contains(c, "playwright") {
			t.Errorf("the gate installs no browser: %q", c)
		}
	}

	// A failing e2e fails the package.
	m = &feMachine{fail: []string{"web bun run e2e"}}
	if err := Frontend([]string{"web"}, root, &out, found, fake(t, &[]call{}), m.in); err == nil {
		t.Fatal("a failing e2e must fail the gate")
	}
}
