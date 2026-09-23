// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// SuiteRun is what one run of the suite wraps around go test. Each field is
// a property one of the test, race, cover, tempdir and hermetic gates checks
// with a run of its own; a waiver on that gate turns its field off.
type SuiteRun struct {
	// Race runs the suite under the race detector with cgo on, and keeps the
	// C compiler's directory on a stripped PATH.
	Race bool
	// Timeout is race.timeout, the budget a repository sets for a run under
	// the detector. Empty leaves the toolchain's default.
	Timeout string
	// Hermetic strips PATH to the toolchain, the C compiler under Race, and
	// Allow.
	Hermetic bool
	Allow    []string
	// Sandbox runs the suite inside the repository's TMPDIR sandbox and
	// reports what survives it; TempDir holds the admitted survivors.
	Sandbox bool
	TempDir config.TempDir
	// Profile is the coverage profile the run writes, relative to the
	// repository root. Empty collects none.
	Profile string
	// Floor applies the coverage floor to Profile. Nil applies none.
	Floor func(profile string) error
}

// Suite runs go vet, then the suite once with every property in opt on, and
// reports each property that failed.
//
// The five properties are independent of each other and none of them is
// independent of the suite, so one run holds all of them: -race already
// forces atomic coverage, a watched TMPDIR watches whatever runs inside it,
// and a stripped PATH strips it for whatever runs inside it. Five separate
// runs compiled and ran the suite five times to learn the same five facts.
//
// Every flag is one the go command's test cache accepts, so a package whose
// inputs did not change replays its result instead of running again.
func Suite(opt SuiteRun, root, goBin string, out io.Writer, run Exec) error {
	if _, err := run(nil, true, goBin, "vet", "./..."); err != nil {
		return fmt.Errorf("go vet failed: %w", err)
	}

	env := os.Environ()
	args := []string{"test"}
	if opt.Race {
		env = append(withoutKey(env, "CGO_ENABLED"), "CGO_ENABLED=1")
		args = append(args, "-race")
	}
	if t := strings.TrimSpace(opt.Timeout); t != "" {
		args = append(args, "-timeout", t)
	}
	if opt.Profile != "" {
		args = append(args, "-covermode=atomic", "-coverpkg=./...", "-coverprofile="+opt.Profile)
	}
	args = append(args, "./...")

	if opt.Hermetic {
		path, note, err := suitePath(opt, goBin, run)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "PATH=%s%s\n", path, note)
		env = envWithPath(env, path)
	}

	var sandbox string
	var before time.Time
	if opt.Sandbox {
		dir, release, err := claimSandbox(root, out)
		if err != nil {
			return err
		}
		defer release()
		if before, err = touchedAt(dir); err != nil {
			return err
		}
		sandbox = dir
		env = envWithTemp(env, sandbox)
		_, _ = fmt.Fprintf(out, "TMPDIR=%s\n", sandbox)
	}

	output, runErr := run(env, true, goBin, args...)

	var failures []string
	if runErr != nil {
		failures = append(failures, runFailure(opt, string(output), runErr))
	}
	if opt.Sandbox {
		after, err := touchedAt(sandbox)
		if err != nil {
			return err
		}
		if f := survivors(opt.TempDir, sandbox, before, after, out); f != "" {
			failures = append(failures, f)
		}
	}
	// A floor over a run that failed measures the tests that happened to
	// finish, so it reports nothing the failure has not already said.
	if opt.Floor != nil && runErr == nil {
		if err := opt.Floor(filepath.Join(root, opt.Profile)); err != nil {
			failures = append(failures, "the suite's coverage is under the floor: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\nand "))
	}
	_, _ = fmt.Fprintf(out, "the suite passes%s\n", describe(opt))
	return nil
}

// suitePath is the stripped PATH, and a note naming the C compiler when the
// race detector keeps its directory there. The detector needs cgo, cgo needs
// the compiler, and a PATH without it fails every package before a test runs.
func suitePath(opt SuiteRun, goBin string, run Exec) (string, string, error) {
	dir, err := toolchainDir(goBin)
	if err != nil {
		return "", "", err
	}
	if !opt.Race {
		return PathFor(dir, opt.Allow), "", nil
	}
	cc, err := compiler(goBin, run)
	if err != nil {
		return "", "", err
	}
	return PathFor(dir, append([]string{filepath.Dir(cc)}, opt.Allow...)), " (kept for the race detector's C compiler " + cc + ")", nil
}

// compiler is the C compiler the go command would call, resolved against the
// PATH before it is stripped.
func compiler(goBin string, run Exec) (string, error) {
	out, err := run(nil, false, goBin, "env", "CC")
	if err != nil {
		return "", fmt.Errorf("asking the toolchain for its C compiler: %w", err)
	}
	fields := strings.Fields(string(out))
	name := "cc"
	if len(fields) > 0 {
		name = fields[0]
	}
	if filepath.IsAbs(name) {
		return name, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("the race detector needs cgo, and the C compiler %q is not on PATH: %w", name, err)
	}
	return p, nil
}

// raceReport is what the detector prints for each race it finds.
const raceReport = "WARNING: DATA RACE"

// offPath is what exec.LookPath reports for a tool the stripped PATH hides.
var offPath = regexp.MustCompile(`exec: "([^"]+)": executable file not found in \$PATH`)

// runFailure names the property a failed run broke, from what the run
// printed. The first line is the one a job summary shows.
func runFailure(opt SuiteRun, output string, err error) string {
	switch {
	case opt.Race && strings.Contains(output, raceReport):
		return fmt.Sprintf("the suite is not race-clean: %v", err)
	case opt.Hermetic && offPath.MatchString(output):
		return fmt.Sprintf("the suite reached for %s, which is not on the stripped PATH: %v",
			offPath.FindStringSubmatch(output)[1], err)
	default:
		return fmt.Sprintf("the test suite failed: %v", err)
	}
}

// survivors reports what the run left in the sandbox, printing each entry,
// or "" when nothing survived and the run did use the directory.
func survivors(cfg config.TempDir, sandbox string, before, after time.Time, out io.Writer) string {
	entries, err := os.ReadDir(sandbox)
	if err != nil {
		return fmt.Sprintf("reading the sandbox: %v", err)
	}
	if len(entries) == 0 && !after.After(before) {
		return fmt.Sprintf("nothing ever wrote to %s, so the run did not use it; "+
			"the gate cannot tell a clean suite from one that ignored TMPDIR", sandbox)
	}
	leaked, allowed := classify(entries, cfg, sandbox)
	for _, a := range allowed {
		_, _ = fmt.Fprintf(out, "  allowed %s: %s\n", a.name, a.why)
	}
	if len(leaked) == 0 {
		return ""
	}
	for _, l := range leaked {
		_, _ = fmt.Fprintf(out, "  %s (%s)\n", l.name, humanSize(l.size))
	}
	return fmt.Sprintf("the suite left %d entr%s under TMPDIR, %s in all; a directory left "+
		"there is never deleted", len(leaked), plural(len(leaked)), humanSize(totalSize(leaked)))
}

// describe names what the run checked beyond the suite itself.
func describe(opt SuiteRun) string {
	var on []string
	if opt.Race {
		on = append(on, "race-clean")
	}
	if opt.Floor != nil {
		on = append(on, "over the coverage floor")
	}
	if opt.Sandbox {
		on = append(on, "nothing left under TMPDIR")
	}
	if opt.Hermetic {
		on = append(on, "on the stripped PATH")
	}
	if len(on) == 0 {
		return ""
	}
	return ": " + strings.Join(on, ", ")
}
