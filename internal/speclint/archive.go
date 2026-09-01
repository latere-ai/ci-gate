// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package speclint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// LoadArchive reads the specs a tree has retired, returning them alongside a
// problem for every file it could not parse.
//
// A parse failure is a problem rather than an error, which is the one way
// this differs from Load. The archive is where a tree's oldest files are, and
// one written before the frontmatter convention existed would otherwise abort
// the run and hide every other finding in the report.
func LoadArchive(cfg config.Spec, dir string) ([]Spec, []string, error) {
	if !cfg.Archive.Enabled() {
		return nil, nil, nil
	}
	adir := filepath.Join(dir, cfg.Archive.Dir)
	info, err := os.Stat(adir)
	// A tree that has retired nothing yet has no directory, and git cannot
	// track an empty one. The forward half of the rule still holds -- a
	// terminal spec at the root is reported and told where it belongs -- so
	// the absence is the empty archive rather than a misconfiguration.
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", adir)
	}

	paths, err := filepath.Glob(filepath.Join(adir, "*.md"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	index := filepath.Base(cfg.Index)

	var specs []Spec
	var problems []string
	for _, p := range paths {
		name := filepath.Base(p)
		if cfg.IsExcluded(name) || (cfg.Index != "" && name == index) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		s, err := Parse(name, string(data))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s/%v", cfg.Archive.Dir, err))
			continue
		}
		s.Archived = true
		specs = append(specs, s)
	}
	return specs, problems, nil
}

// CheckArchive reports specs on the wrong side of the archive boundary.
//
// The rule runs in both directions, the way CheckStatusRequires does, because
// that is what makes the pair exhaustive. A terminal spec at the root is the
// obvious half: it finished and nobody moved it, so a reader opening the
// directory cannot tell the live design from the record of what shipped. An
// archived spec at a working status is the half that catches the opposite
// mistake, a spec retired while its work was still open.
func CheckArchive(cfg config.Spec, active, archived []Spec) []string {
	if !cfg.Archive.Enabled() {
		return nil
	}
	var out []string
	for _, s := range active {
		if cfg.Archive.IsTerminal(s.Status) {
			out = append(out, fmt.Sprintf("%s: status %q is terminal; the spec belongs in %s/",
				s.Name, s.Status, cfg.Archive.Dir))
		}
	}
	for _, s := range archived {
		if !cfg.Archive.IsTerminal(s.Status) {
			out = append(out, fmt.Sprintf("%s/%s: status %q is not terminal (%s); "+
				"a spec whose work is still open belongs beside the others",
				cfg.Archive.Dir, s.Name, s.Status, strings.Join(cfg.Archive.Statuses, "|")))
		}
	}
	return out
}

// archiveHref reports whether a link or a depends_on edge points into this
// tree's archive, and the file's base name.
//
// Matching the last directory alone is not enough. Another repository has an
// archive too, and `../../terraform/specs/.archive/x.md` ends in the same
// segment while naming a file this tree cannot see. So the path must be the
// archive relative to the spec directory or to the repository root, and a
// `..` anywhere means it left the tree.
func archiveHref(cfg config.Spec, href string) (string, bool) {
	if !cfg.Archive.Enabled() {
		return "", false
	}
	clean := filepath.Clean(href)
	if strings.HasPrefix(clean, "..") {
		return "", false
	}
	dir, name := filepath.Split(clean)
	dir = filepath.Clean(dir)
	switch dir {
	case filepath.Clean(cfg.Archive.Dir),
		filepath.Clean(filepath.Join(cfg.Dir, cfg.Archive.Dir)):
		return name, true
	}
	return "", false
}
