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
		if len(s.Changed) == 0 {
			return none, "", nil
		}
		return "", "over the " + count(len(s.Changed), "package") + " the change touches", nil
	case "vuln":
		// A dependency change checks the whole tree, so a scope that holds
		// has none; the advisories that move with no commit are CI's, which
		// scans on every push.
		return "no dependency changed since " + s.Since(), "", nil
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
