// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"latere.ai/x/ci-gate/internal/cover"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/scope"
)

// scoped is what a check scoped to a change does with one gate that the
// plan would run: skip it, with the reason, or run it, with a note on what
// it reads. Only the gates that take minutes narrow. Every other gate reads
// the whole tree in seconds, and a scan that read only the change would
// save nothing worth a second rule for what it reads.
func scoped(c Ctx, name string) (skip, note string, err error) {
	s := c.Scope
	if !s.Narrows() {
		return "", "", nil
	}
	none := "no Go package changed since " + s.Since()
	switch name {
	case Suite:
		if len(s.Changed) == 0 {
			return none, "", nil
		}
		note := "over the " + count(len(s.Reached), "package") + " the change reaches"
		// A waived cover leaves the run no floor to apply, and its own note
		// already says so.
		if run, _ := suiteRun(c); run.Floor != nil {
			note += "; the floor on the " + count(len(s.Changed), "package") + " it changes and on any it reaches that loses coverage"
		}
		return "", note, nil
	case "lint":
		// A change can move a finding into a package it did not touch: a
		// function that now returns an error leaves an unchecked call in
		// its caller, and a deprecation marks every use. So the linter reads
		// what the change reaches, as the suite does.
		if len(s.Changed) == 0 {
			return none, "", nil
		}
		return "", "over the " + count(len(s.Reached), "package") + " the change reaches", nil
	case "vuln":
		// govulncheck fails on a vulnerability the code calls, so a Go
		// change can fail it with no dependency moving: a new call to a
		// vulnerable function of a module already required. It reads the
		// whole module, since reachability is a property of the program.
		if len(s.Changed) == 0 {
			return none, "", nil
		}
		return "", "", nil
	case "lean":
		dirs, err := gates.LakeProjects(c.Exec)
		if err != nil {
			return "", "", err
		}
		if slices.ContainsFunc(dirs, s.Touches) {
			return "", "", nil
		}
		return "no Lake project changed since " + s.Since(), "", nil
	}
	return "", "", nil
}

// count is n things, in words a reader reads.
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// importPaths are the packages as go test and -coverpkg take them.
func importPaths(pkgs []scope.Package) []string {
	paths := make([]string, len(pkgs))
	for i, p := range pkgs {
		paths[i] = p.ImportPath
	}
	return paths
}

// patterns are the packages as directory patterns, which the linter takes.
func patterns(pkgs []scope.Package) []string {
	pats := make([]string, len(pkgs))
	for i, p := range pkgs {
		pats[i] = p.Pattern()
	}
	return pats
}

// header is the line a check opens with: what it reads, and why.
func header(s *scope.Scope) string {
	if s == nil {
		return ""
	}
	if !s.Narrows() {
		return "checking the whole tree: " + s.Whole
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "checking what changed since %s: %s", s.Since(), count(len(s.Files), "file"))
	if len(s.Changed) > 0 {
		_, _ = fmt.Fprintf(&b, ", %s, %s with what imports them", count(len(s.Changed), "Go package"), count(len(s.Reached), "package"))
	}
	b.WriteString("; -all checks the whole tree")
	return b.String()
}

// scopedFloor is the floor over a change: each package it changes, at the
// figure the run gives it, and each package its tests reach without its
// touching them, held to the coverage it had at the base.
//
// The run is every test that can reach a changed package, so a changed
// package's figure is the whole suite's. An untouched package is covered
// by those tests and by others the change cannot have moved, so it can
// lose coverage only through the run's tests. Running them again over the
// base shows each block they covered there: a package that kept every one
// kept its figure, which CI measured on the base. A package that lost one,
// or whose base cannot be replayed, is measured over every test that
// reaches it and held to the floor as a changed one is.
func scopedFloor(c Ctx, list cover.Lister) func(string, gates.Tester) error {
	return func(profile string, test gates.Tester) error {
		known, err := list()
		if err != nil {
			return err
		}
		// A package with no statements gives the floor nothing to measure
		// and an exempt one is outside it, so neither is worth a run.
		measurable := func(p string) bool {
			_, exempt := c.Cfg.Cover.ExemptFor(p)
			return !exempt && slices.Contains(known, p)
		}
		dir, err := os.MkdirTemp("", "lateregate-base-")
		if err != nil {
			return fmt.Errorf("making a directory for the base's overlay: %w", err)
		}
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				_, _ = fmt.Fprintf(c.Out, "the base's overlay is left at %s: %v\n", dir, err)
			}
		}()

		lost, err := untouchedLost(c, dir, profile, test)
		if err != nil {
			return err
		}
		lost = slices.DeleteFunc(lost, func(p string) bool { return !measurable(p) })
		profiles := append([]string{profile}, c.Profiles...)
		if len(lost) > 0 {
			rest, err := measureFully(c, dir, lost, test)
			if err != nil {
				return err
			}
			if rest != "" {
				profiles = append(profiles, rest)
			}
		}

		judged := slices.Concat(importPaths(c.Scope.Changed), lost)
		if !slices.ContainsFunc(judged, measurable) {
			_, _ = fmt.Fprintln(c.Out, "the floor measures nothing here: every package the change touches holds no statements or is exempt")
			return nil
		}
		return cover.RunOn(c.Cfg.Cover, profiles, c.Out, cover.Only(func() ([]string, error) { return known, nil }, judged), judged)
	}
}

// untouchedLost runs the run's tests again over the base and names the
// packages the change reaches without touching that lost a block those
// tests covered there, and any whose base cannot be replayed.
func untouchedLost(c Ctx, dir, now string, test gates.Tester) ([]string, error) {
	s := c.Scope
	b, err := s.Baseline(dir, c.GoBin, c.Exec)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be rebuilt to compare coverage against: %w; -all checks the whole tree", s.Since(), err)
	}
	if len(b.Untouched) == 0 {
		return nil, nil
	}
	names := func(pkgs []string) string {
		short := make([]string, len(pkgs))
		for i, p := range pkgs {
			short[i] = strings.TrimPrefix(p, c.Cfg.Cover.TrimPrefix)
		}
		return strings.Join(short, ", ")
	}
	lost := slices.Clone(b.Unreplayed)
	if len(b.Unreplayed) > 0 {
		_, _ = fmt.Fprintf(c.Out, "%s the change did not touch %s linked by tests that read a changed file at run time, "+
			"which the base cannot replay, so each is measured over every test that reaches it: %s\n",
			count(len(b.Unreplayed), "package"), isAre(len(b.Unreplayed)), names(b.Unreplayed))
	}
	if len(b.Tests) > 0 {
		// The base's run measures the untouched packages by name, never
		// ./...: go test compiles a package it instruments from the files
		// on disk and leaves the overlay out, so a changed package measured
		// there would run as the change left it. An untouched package holds
		// no changed file, so its disk is its base.
		profile := filepath.Join(dir, "base.out")
		args := slices.Concat([]string{"-overlay=" + b.Overlay, "-covermode=atomic",
			"-coverpkg=" + strings.Join(b.Untouched, ","), "-coverprofile=" + profile}, b.Tests)
		if out, err := test(false, args...); err != nil {
			_, _ = fmt.Fprintf(c.Out, "the tests at %s did not pass here (%v), so the %s the change reaches without touching "+
				"are each measured over every test that reaches them; the base's run ended:\n%s\n",
				s.Since(), err, count(len(b.Untouched), "package"), lastLines(out, 10))
			return b.Untouched, nil
		}
		took, err := cover.Lost(profile, now)
		if err != nil {
			return nil, err
		}
		took = slices.DeleteFunc(took, func(p string) bool {
			return !slices.Contains(b.Untouched, p) || slices.Contains(lost, p)
		})
		if len(took) > 0 {
			_, _ = fmt.Fprintf(c.Out, "%s the change did not touch lost coverage %s had at %s, so each is measured over every test that reaches it: %s\n",
				count(len(took), "package"), theyIt(len(took)), s.Since(), names(took))
		}
		lost = append(lost, took...)
	}
	slices.Sort(lost)
	if kept := len(b.Untouched) - len(lost); kept > 0 {
		_, _ = fmt.Fprintf(c.Out, "%s the change reaches without touching kept the coverage the same tests gave %s at %s\n",
			count(kept, "package"), themIt(kept), s.Since())
	}
	return lost, nil
}

// measureFully runs every test outside the run that reaches one of pkgs
// and returns the profile it writes: with the run's own, each package's
// figure is the one the whole suite would give it. Empty means the run
// already held every such test.
func measureFully(c Ctx, dir string, pkgs []string, test gates.Tester) (string, error) {
	rest := c.Scope.Contributors(pkgs)
	if len(rest) == 0 {
		return "", nil
	}
	profile := filepath.Join(dir, "rest.out")
	args := slices.Concat([]string{"-covermode=atomic", "-coverpkg=./...", "-coverprofile=" + profile}, rest)
	if _, err := test(true, args...); err != nil {
		return "", fmt.Errorf("the tests of the %s that also reach %s failed: %w", count(len(rest), "package"), themIt(len(pkgs)), err)
	}
	return profile, nil
}

// lastLines is the end of a run's output, enough to say why it failed.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func theyIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "they"
}

func themIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
