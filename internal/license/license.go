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
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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

// fingerprints maps each identifier a repository may declare to phrases that
// are in the canonical text of that licence and in no other the fleet uses.
// The two "only"/"or-later" forms of a GNU licence share one text, so they
// share one fingerprint. LicenseRef-Proprietary is the SPDX spelling for
// terms not on the list, and its fingerprint is the two phrases a
// proprietary notice cannot do without. An identifier missing here fails the gate rather
// than passing unchecked: the check that made the declaration and the root
// file agree is the whole point, and a hole in the table would have let the
// mismatch this exists to catch through.
var fingerprints = map[string][]string{
	"MIT":               {"MIT License", "Permission is hereby granted, free of charge"},
	"Apache-2.0":        {"Apache License", "Version 2.0"},
	"AGPL-3.0-only":     {"GNU AFFERO GENERAL PUBLIC LICENSE", "Version 3"},
	"AGPL-3.0-or-later": {"GNU AFFERO GENERAL PUBLIC LICENSE", "Version 3"},
	// The SPDX form for terms that are not on the licence list: a
	// proprietary repository declares LicenseRef-Proprietary, its headers
	// stay machine-readable, and its root file must say what such a file
	// says, that every right is reserved and no licence is granted.
	"LicenseRef-Proprietary": {"All rights reserved", "No license is granted"},
}

// licenseText reports why the root LICENSE text is not the licence spdx
// names, or "" when it is. A header stating one licence over a root file
// carrying another is exactly the mismatch the notice was meant to prevent,
// and nothing else in the gate reads the root file.
func licenseText(spdx, text string) string {
	phrases, ok := fingerprints[spdx]
	if !ok {
		return "the gate has no fingerprint for " + quote(spdx) +
			"; add its distinctive phrases to the fingerprint table before declaring it"
	}
	for _, p := range phrases {
		if !strings.Contains(text, p) {
			return "the declaration is " + quote(spdx) + " but LICENSE does not contain " +
				quote(p) + reads(text)
		}
	}
	return ""
}

// reads names the licence the text does look like, when one fingerprint
// matches, so the failure says "reads as MIT" and not only "is not Apache".
func reads(text string) string {
	for id, phrases := range fingerprints {
		if strings.HasSuffix(id, "-only") {
			continue
		}
		hit := true
		for _, p := range phrases {
			hit = hit && strings.Contains(text, p)
		}
		if hit {
			return "; the text reads as " + id
		}
	}
	return ""
}

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
	text, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return fmt.Errorf("no LICENSE file at %s\n"+
			"the header names terms a reader has to be able to find; %s in every "+
			"file and nothing at the root points at nothing",
			root, cfg.SPDX)
	}
	if why := licenseText(cfg.SPDX, string(text)); why != "" {
		return fmt.Errorf("LICENSE disagrees with license.spdx in %s: %s\n"+
			"the notice on every file and the text at the root name the same terms; "+
			"change one to match the other",
			config.Name, why)
	}

	var bad []string
	scanned := 0
	skip := untracked(root)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || slices.Contains(cfg.Skip, name) || skipped(root, path, d, skip) {
				return filepath.SkipDir
			}
			return nil
		}
		prefix, ok := cfg.CommentFor(name)
		if !ok || skipped(root, path, d, skip) {
			return nil
		}
		scanned++
		// WalkDir only ever yields paths under root, so trimming the prefix
		// is exact and leaves no failure to invent a fallback for.
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		finding, err := checkFile(cfg, path, rel, prefix)
		if err != nil {
			return err
		}
		if finding != "" {
			bad = append(bad, finding)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanning for licence notices: %w", err)
	}
	// A scan that read nothing proves nothing.
	if scanned == 0 {
		return fmt.Errorf("no %s file found under %s; the gate would pass vacuously",
			strings.Join(cfg.Checked(), "/"), root)
	}
	if len(bad) > 0 {
		for _, b := range bad {
			_, _ = fmt.Fprintln(out, "  "+b)
		}
		return fmt.Errorf("%d file(s) without the declared %s notice\n%s",
			len(bad), cfg.SPDX, Want(cfg, "//"))
	}
	_, _ = fmt.Fprintf(out, "%s declared on %d file(s)\n", cfg.SPDX, scanned)
	return nil
}

// Files checks just the named files, relative to root, and returns one
// "path: why" line per wrong notice. It is what the pre-commit hook runs
// over the staged files: the same check as Run, so the hook and the gate
// cannot disagree on what a violation is. A file whose type has no comment
// marker is not checked. Nothing here is vacuous: an empty list is an
// empty result, and it is the caller's business whether that means pass.
func Files(cfg config.License, root string, rels []string) ([]string, error) {
	if strings.TrimSpace(cfg.SPDX) == "" {
		return nil, fmt.Errorf("license.spdx is not set in %s", config.Name)
	}
	var bad []string
	for _, rel := range rels {
		prefix, ok := cfg.CommentFor(filepath.Base(rel))
		if !ok {
			continue
		}
		// The hook sees a path, not a walk, so it applies the same skip
		// list Run applies to directories: a file under a skipped directory
		// is not the repository's to notice, whichever route found it.
		if underSkipped(cfg.Skip, rel) {
			continue
		}
		finding, err := checkFile(cfg, filepath.Join(root, rel), rel, prefix)
		if err != nil {
			return nil, err
		}
		if finding != "" {
			bad = append(bad, finding)
		}
	}
	return bad, nil
}

// checkFile reads one file and renders its finding as "rel: why", or ""
// when the notice is right.
func checkFile(cfg config.License, path, rel, prefix string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if why := check(string(body), prefix, cfg); why != "" {
		return rel + ": " + why, nil
	}
	return "", nil
}

// Want renders the notice the repository declared, so a failure teaches the
// shape rather than only rejecting the one that was there.
func Want(cfg config.License, prefix string) string {
	return fmt.Sprintf("the notice is the first two lines, then a blank one, "+
		"below a shebang if the file has one:\n\n"+
		"\t%s %s <year> %s\n\t%s %s %s\n\n",
		prefix, CopyrightTag, cfg.Holder, prefix, IdentifierTag, cfg.SPDX)
}

// check reports why a file's notice is wrong, or "" when it is right.
//
// prefix is the file type's line-comment marker. The notice sits at the very
// top except below a shebang, which the kernel only honours on line 1: a
// script whose first line is a comment is not executable, so the notice moves
// down rather than the file breaking.
func check(src, prefix string, cfg config.License) string {
	lines := strings.Split(src, "\n")
	at := 0
	if strings.HasPrefix(src, "#!") {
		at = 1
	}
	for len(lines) < at+3 {
		lines = append(lines, "")
	}
	lines = lines[at:]

	first, ok := strings.CutPrefix(strings.TrimSpace(lines[0]), prefix+" "+CopyrightTag)
	if !ok {
		return "no " + CopyrightTag + " on line " + itoa(at+1)
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

	id, ok := strings.CutPrefix(strings.TrimSpace(lines[1]), prefix+" "+IdentifierTag)
	if !ok {
		return "no " + IdentifierTag + " on line " + itoa(at+2)
	}
	if got := strings.TrimSpace(id); got != cfg.SPDX {
		return "identifier is " + quote(got) + ", declared " + quote(cfg.SPDX)
	}

	// In Go a comment block touching `package` is the package documentation,
	// so an unseparated notice becomes the first paragraph of the rendered
	// doc. The mistake is invisible in review and permanent once it is in
	// every file, which is why the separation is checked and not assumed.
	if strings.TrimSpace(lines[2]) != "" {
		return "line " + itoa(at+3) + " is not blank, so the notice runs into the code below it"
	}
	return ""
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

func itoa(n int) string { return strconv.Itoa(n) }

// Write puts the declared notice on every checked file that has none, and
// reports the ones whose notice is present but wrong rather than rewriting
// them: replacing a holder or an identifier somebody wrote is a legal edit,
// not a mechanical one.
//
// It is deterministic given the config, which is what makes it safe as a
// tool: nothing is guessed, the identifier and holder are the ones the
// repository declared, and the year is the one the notice is written in.
func Write(cfg config.License, root string, out io.Writer, now time.Time) error {
	if strings.TrimSpace(cfg.SPDX) == "" || strings.TrimSpace(cfg.Holder) == "" {
		return fmt.Errorf("license.spdx and license.holder must both be set in %s before a notice can be written", config.Name)
	}
	var wrong []string
	written := 0
	skip := untracked(root)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || slices.Contains(cfg.Skip, name) || skipped(root, path, d, skip) {
				return filepath.SkipDir
			}
			return nil
		}
		prefix, ok := cfg.CommentFor(name)
		if !ok || skipped(root, path, d, skip) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		why := check(string(body), prefix, cfg)
		switch {
		case why == "":
			return nil
		case !strings.HasPrefix(why, "no "+CopyrightTag):
			wrong = append(wrong, rel+": "+why)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(withNotice(string(body), prefix, cfg, now)), info.Mode().Perm()); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "  "+rel)
		written++
		return nil
	})
	if err != nil {
		return fmt.Errorf("writing licence notices: %w", err)
	}
	_, _ = fmt.Fprintf(out, "%s written on %d file(s)\n", cfg.SPDX, written)
	if len(wrong) > 0 {
		for _, w := range wrong {
			_, _ = fmt.Fprintln(out, "  "+w)
		}
		return fmt.Errorf("%d file(s) carry a notice that disagrees with the declaration; those are edited by hand", len(wrong))
	}
	return nil
}

// withNotice prepends the notice, below a shebang if the file has one.
func withNotice(src, prefix string, cfg config.License, now time.Time) string {
	notice := fmt.Sprintf("%s %s %d %s\n%s %s %s\n\n",
		prefix, CopyrightTag, now.Year(), cfg.Holder, prefix, IdentifierTag, cfg.SPDX)
	if strings.HasPrefix(src, "#!") {
		first, rest, _ := strings.Cut(src, "\n")
		return first + "\n" + notice + rest
	}
	return notice + src
}

// untracked lists the files git does not track under root, keyed by the
// path WalkDir will hand back. A file that is not in the repository is not
// the repository's to notice: a work-in-progress test from another change
// sitting in the tree fails nothing here, and never reaches a runner. When
// root is not a git checkout there is nothing to exclude.
func untracked(root string) map[string]bool {
	cmd := exec.CommandContext(context.Background(), "git", "ls-files", "-z", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	skip := map[string]bool{}
	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			skip[filepath.Join(root, p)] = true
		}
	}
	return skip
}

// skipped reports whether a walk entry is outside the repository's own
// files: an untracked file, anything under an untracked directory (git
// lists the directory once, not its contents), or a nested checkout such as
// an agent worktree parked under .claude, which is a whole other tree with
// its own history.
func skipped(root, path string, d os.DirEntry, untracked map[string]bool) bool {
	if untracked[path] {
		return true
	}
	if d.IsDir() && path != root {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return true
		}
	}
	return false
}

// underSkipped reports whether rel, a slash path relative to the root, has
// a directory in skip anywhere on its path, the way Run's walk prunes it.
func underSkipped(skip []string, rel string) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(filepath.Dir(rel)), "/") {
		if slices.Contains(skip, part) {
			return true
		}
	}
	return false
}
