// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package license gates the licence notice on every source file.
//
// A licence in one file at the root binds anyone who clones the repository
// and reads it. Code rarely travels that way: it is pasted, vendored, lifted
// and scanned, and every one of those routes drops the root file. The notice
// has to be on the file, in the form a scanner can read.
//
// The repository declares what that notice says. This package only asserts
// that a declaration exists and that every file agrees with it, which is what
// makes one gate work for an MIT library and a copyleft service alike.
package license

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// CopyrightTag and IdentifierTag are the two SPDX fields the notice carries.
// They are the tags reuse, scancode and syft read, which is the whole reason
// the notice is not prose.
const (
	CopyrightTag  = "SPDX-FileCopyrightText:"
	IdentifierTag = "SPDX-License-Identifier:"
)

// year matches what a human writes for a copyright year: one, or a range.
// Checking a fixed value would turn every 1 January into a repository-wide
// diff, and a gate that fails for a reason nobody caused gets skipped.
var year = regexp.MustCompile(`^\d{4}(-\d{4})?$`)

// Run checks every source file under root and reports the ones whose notice
// is missing, stale or wrongly placed.
func Run(cfg config.License, root string, out io.Writer) error {
	if strings.TrimSpace(cfg.SPDX) == "" {
		return fmt.Errorf("license.spdx is not set in %s\n"+
			"a licence has no sensible default: an identifier guessed here would be "+
			"printed into every file in the repository. Declare the one this "+
			"repository is released under, e.g. license.spdx: MIT",
			config.Name)
	}
	if _, err := os.Stat(filepath.Join(root, "LICENSE")); err != nil {
		return fmt.Errorf("no LICENSE file at %s\n"+
			"the header names terms a reader has to be able to find; %s in every "+
			"file and nothing at the root points at nothing",
			root, cfg.SPDX)
	}

	var bad []string
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || slices.Contains(cfg.Skip, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !slices.Contains(cfg.Exts(), filepath.Ext(name)) {
			return nil
		}
		scanned++
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// WalkDir only ever yields paths under root, so trimming the prefix
		// is exact and leaves no failure to invent a fallback for.
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		if why := check(string(body), cfg); why != "" {
			bad = append(bad, rel+": "+why)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanning for licence notices: %w", err)
	}
	// A scan that read nothing proves nothing.
	if scanned == 0 {
		return fmt.Errorf("no %s file found under %s; the gate would pass vacuously",
			strings.Join(cfg.Exts(), "/"), root)
	}
	if len(bad) > 0 {
		for _, b := range bad {
			_, _ = fmt.Fprintln(out, "  "+b)
		}
		return fmt.Errorf("%d file(s) without the declared %s notice\n%s",
			len(bad), cfg.SPDX, Want(cfg))
	}
	_, _ = fmt.Fprintf(out, "%s declared on %d file(s)\n", cfg.SPDX, scanned)
	return nil
}

// Want renders the notice the repository declared, so a failure teaches the
// shape rather than only rejecting the one that was there.
func Want(cfg config.License) string {
	return fmt.Sprintf("the notice is the first two lines, then a blank one:\n\n"+
		"\t// %s <year> %s\n\t// %s %s\n\n",
		CopyrightTag, cfg.Holder, IdentifierTag, cfg.SPDX)
}

// check reports why a file's notice is wrong, or "" when it is right.
func check(src string, cfg config.License) string {
	lines := strings.Split(src, "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}

	first, ok := strings.CutPrefix(strings.TrimSpace(lines[0]), "// "+CopyrightTag)
	if !ok {
		return "no " + CopyrightTag + " on line 1"
	}
	fields := strings.Fields(first)
	if len(fields) == 0 {
		return CopyrightTag + " carries no year or holder"
	}
	if !year.MatchString(fields[0]) {
		return CopyrightTag + " starts with " + quote(fields[0]) + ", not a year"
	}
	if holder := strings.Join(fields[1:], " "); holder != cfg.Holder {
		return "holder is " + quote(holder) + ", declared " + quote(cfg.Holder)
	}

	id, ok := strings.CutPrefix(strings.TrimSpace(lines[1]), "// "+IdentifierTag)
	if !ok {
		return "no " + IdentifierTag + " on line 2"
	}
	if got := strings.TrimSpace(id); got != cfg.SPDX {
		return "identifier is " + quote(got) + ", declared " + quote(cfg.SPDX)
	}

	// In Go a comment block touching `package` is the package documentation,
	// so an unseparated notice becomes the first paragraph of the rendered
	// doc. The mistake is invisible in review and permanent once it is in
	// every file, which is why the separation is checked and not assumed.
	if strings.TrimSpace(lines[2]) != "" {
		return "line 3 is not blank, so the notice runs into the code below it"
	}
	return ""
}

func quote(s string) string { return fmt.Sprintf("%q", s) }
