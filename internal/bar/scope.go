// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"fmt"
	"slices"
	"strings"

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
			note += "; the floor on the " + count(len(s.Changed), "package") + " it changes"
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
