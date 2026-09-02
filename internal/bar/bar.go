// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package bar is the gate set, and the run of all of it.
//
// The organisation has one bar, so the binary that holds it is the one thing
// a repository runs: `lateregate` with no arguments runs every gate that
// applies and reports all of them. Which gates apply is decided by asking
// the tree, not by configuring a list, and the only way a gate that applies
// does not run is a waiver with a reason and a date.
package bar

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/cover"
	"latere.ai/x/ci-gate/internal/depcheck"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
	"latere.ai/x/ci-gate/internal/license"
	"latere.ai/x/ci-gate/internal/speclint"
)

// Ctx is what a gate runs with.
type Ctx struct {
	Cfg   *config.Config
	Root  string
	GoBin string
	Out   io.Writer
	Exec  gates.Exec
	// Profiles are coverage profiles given explicitly, for a repository whose
	// coverage is split across tiers it runs itself. Empty means collect.
	Profiles []string
	// Args is whatever followed --, which tempdir reads as the command to
	// watch.
	Args []string
	Now  time.Time
}

// Gate is one check in the set.
type Gate struct {
	Name string
	Doc  string
	// Applies reports whether the tree has a subject for this gate, and if
	// not, why not. nil means it always does. This is not a waiver: a gate
	// with no subject has nothing to be behind on.
	Applies func(Ctx) (bool, string, error)
	Run     func(Ctx) error
}

// Gates is the set, in the order check runs them: the static scans first
// because they are seconds, the suite runs last because they are minutes and
// a formatting failure should not wait for them.
var Gates = []Gate{
	{Name: "fmt-check", Doc: "no tracked Go source is unformatted",
		Run: func(c Ctx) error { return gates.FmtCheck(c.Out, c.Exec) }},
	{Name: "modernize", Doc: "no code a standard library call already covers",
		Run: func(c Ctx) error { return gates.Modernize(c.Cfg.Modernize, c.GoBin, c.Out, c.Exec) }},
	{Name: "cgo-free", Doc: "no Go file imports \"C\"",
		Run: func(c Ctx) error { return gates.CgoFree(c.Root, c.Out, c.Cfg.CgoFree.Skip) }},
	{Name: "otel-client", Doc: "no outbound HTTP client without a tracing transport",
		Run: func(c Ctx) error { return gates.OtelClient(c.Root, c.Out, c.Cfg.OtelClient.Skip) }},
	{Name: "license", Doc: "every source file carries the declared SPDX notice",
		Run: func(c Ctx) error { return license.Run(c.Cfg.License, c.Root, c.Out) }},
	{Name: "spec-lint", Doc: "the spec tree agrees with itself and its index",
		Applies: tracksSpecs,
		Run:     func(c Ctx) error { return speclint.Run(c.Cfg.Spec, c.Root, c.Out) }},
	{Name: "depcheck", Doc: "no build reaches a dependency nobody admitted",
		Applies: func(c Ctx) (bool, string, error) {
			if len(c.Cfg.Depcheck.Packages) == 0 {
				return false, "depcheck.packages names no package", nil
			}
			return true, "", nil
		},
		Run: func(c Ctx) error { return depcheck.Run(c.Cfg.Depcheck, c.Out, depcheck.GoLister(c.GoBin, c.Root)) }},
	{Name: "lint", Doc: "golangci-lint " + golangci.Version + " against the shared config",
		Run: func(c Ctx) error { return golangci.Lint(c.Root, c.Cfg, c.GoBin, c.Out, c.Exec) }},
	{Name: "vuln", Doc: "govulncheck " + gates.VulnVersion + " finds no reachable vulnerability",
		Run: func(c Ctx) error { return gates.Vuln(c.GoBin, c.Out, c.Exec) }},
	{Name: "test", Doc: "go vet and the suite",
		Run: func(c Ctx) error { return gates.Test(c.GoBin, c.Out, c.Exec) }},
	{Name: "race", Doc: "the suite under the race detector",
		Run: func(c Ctx) error { return gates.Race(c.Cfg.Race, c.GoBin, c.Out, c.Exec) }},
	{Name: "hermetic", Doc: "the suite with only the toolchain on PATH",
		Run: func(c Ctx) error { return gates.Hermetic(c.Cfg.Hermetic, c.GoBin, c.Out, c.Exec) }},
	{Name: "tempdir", Doc: "the suite leaves nothing under TMPDIR",
		Run: func(c Ctx) error { return gates.TempDir(c.Cfg.TempDir, c.Args, c.Out, c.Exec) }},
	{Name: "cover", Doc: "every package clears the floor",
		Run: runCover},
}

// runCover collects a profile unless the caller supplied one.
func runCover(c Ctx) error {
	profiles := c.Profiles
	if len(profiles) == 0 {
		p, err := cover.Collect(c.GoBin, c.Root, c.Out, c.Exec)
		if err != nil {
			return err
		}
		profiles = []string{p}
	}
	return cover.Run(c.Cfg.Cover, profiles, c.Out, cover.GoLister(c.GoBin, c.Root))
}

// tracksSpecs asks git, not the config. Deriving this from spec.dir would
// let a repository escape the gate by deleting a line, and a repository with
// a real spec tree and no spec section is precisely the gap to report.
func tracksSpecs(c Ctx) (bool, string, error) {
	out, err := c.Exec(nil, false, "git", "ls-files", "--", config.DefaultSpecDir)
	if err != nil {
		// Applicability that cannot be determined must not resolve to "does
		// not apply": that is the vacuous pass this package exists to refuse.
		return false, "", fmt.Errorf("cannot list %s/ with git: %w\n"+
			"whether a repository has specs to lint is asked of git, so this "+
			"needs to run inside a git repository", config.DefaultSpecDir, err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return false, "tracks no " + config.DefaultSpecDir + "/ files", nil
	}
	return true, "", nil
}

// Find returns the gate by name.
func Find(name string) (Gate, bool) {
	for _, g := range Gates {
		if g.Name == name {
			return g, true
		}
	}
	return Gate{}, false
}

// Names lists the gate names in run order.
func Names() []string {
	n := make([]string, len(Gates))
	for i, g := range Gates {
		n[i] = g.Name
	}
	return n
}

// ValidateWaivers rejects a waiver keyed on a name that is not a gate. A
// waiver for a gate nobody runs hides a typo in the name of a gate somebody
// does.
func ValidateWaivers(cfg *config.Config) error {
	var bad []string
	for name := range cfg.Waive {
		if _, ok := Find(name); !ok {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s: waive names %s, which is not a gate\ngates: %s",
		config.Name, strings.Join(bad, ", "), strings.Join(Names(), ", "))
}

// Status is what the plan decided for one gate.
type Status string

const (
	// Run means the gate applies and no live waiver covers it.
	Run Status = "run"
	// Skip means the tree has no subject for the gate.
	Skip Status = "skip"
	// Waived means a dated waiver covers it, for now.
	Waived Status = "waived"
)

// Entry is one gate's place in the plan.
type Entry struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	// Reason is why a gate is skipped or waived, or the note that its waiver
	// has expired and it therefore runs.
	Reason string `json:"reason,omitempty"`
	Until  string `json:"until,omitempty"`
}

// Plan decides, for every gate, whether it runs.
//
// An expired waiver does not skip the gate. It runs and fails on its own
// terms, so the reason for the work is in the gate's own output rather than
// in a date; the plan carries a note saying so.
func Plan(c Ctx) ([]Entry, error) {
	if err := ValidateWaivers(c.Cfg); err != nil {
		return nil, err
	}
	var plan []Entry
	for _, g := range Gates {
		e := Entry{Name: g.Name, Status: Run}
		if g.Applies != nil {
			ok, why, err := g.Applies(c)
			if err != nil {
				return nil, err
			}
			if !ok {
				e.Status, e.Reason = Skip, why
				plan = append(plan, e)
				continue
			}
		}
		if w, ok := c.Cfg.Waive[g.Name]; ok {
			// Validated at load, so it parses. It names a day and is
			// inclusive: the waiver dies when the day after it begins.
			until, _ := w.UntilDate()
			e.Until = w.Until
			if c.Now.Before(until.AddDate(0, 0, 1)) {
				e.Status, e.Reason = Waived, w.Reason
			} else {
				e.Reason = "waiver expired " + w.Until + ": " + w.Reason
			}
		}
		plan = append(plan, e)
	}
	return plan, nil
}

// List prints the plan, as text or as JSON for the pipeline's matrix.
func List(c Ctx, asJSON bool) error {
	plan, err := Plan(c)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(c.Out).Encode(plan)
	}
	for _, e := range plan {
		_, _ = fmt.Fprintln(c.Out, line(e, "", ""))
	}
	return nil
}

// Check runs every gate the plan says to, and reports all of them.
//
// It does not stop at the first failure: a repository behind on four gates
// learns that in one run. Each gate's own output streams as it runs; the
// summary is last because it is the part a reader scrolls to.
func Check(c Ctx) error {
	plan, err := Plan(c)
	if err != nil {
		return err
	}
	verdict := map[string]string{}
	var failed []string
	for _, e := range plan {
		if e.Status != Run {
			continue
		}
		g, _ := Find(e.Name)
		_, _ = fmt.Fprintf(c.Out, "== %s\n", g.Name)
		if err := g.Run(c); err != nil {
			verdict[e.Name] = firstLine(err.Error())
			failed = append(failed, e.Name)
			_, _ = fmt.Fprintf(c.Out, "FAIL %s: %s\n", g.Name, err)
			continue
		}
		verdict[e.Name] = ""
	}

	_, _ = fmt.Fprintln(c.Out)
	ran := 0
	for _, e := range plan {
		switch e.Status {
		case Run:
			ran++
			if v := verdict[e.Name]; v != "" {
				if e.Reason != "" {
					v += " (" + e.Reason + ")"
				}
				_, _ = fmt.Fprintln(c.Out, line(e, "FAIL", v))
			} else {
				_, _ = fmt.Fprintln(c.Out, line(e, "PASS", ""))
			}
		default:
			_, _ = fmt.Fprintln(c.Out, line(e, "", ""))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d gates failed: %s", len(failed), ran, strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(c.Out, "%d gates passed\n", ran)
	return nil
}

// line renders one summary row. mark replaces the status word and note the
// reason; both default from the entry.
func line(e Entry, mark, note string) string {
	word := strings.ToUpper(string(e.Status))
	if mark != "" {
		word = mark
	}
	if note == "" {
		note = e.Reason
		if e.Status == Waived {
			word = "WAIV"
			note = "until " + e.Until + ": " + e.Reason
		}
	}
	if note == "" {
		return fmt.Sprintf("%-4s %s", word, e.Name)
	}
	return fmt.Sprintf("%-4s %-12s %s", word, e.Name, note)
}

// firstLine keeps a multi-line error's first line for the summary; the
// whole of it was already printed where the gate ran.
func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// Known reports whether a name is a gate, for callers that map old target
// names onto the set.
func Known(name string) bool {
	return slices.Contains(Names(), name)
}
