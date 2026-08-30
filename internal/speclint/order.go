// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package speclint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// numbered matches the NNN- a numbered spec file name starts with.
var numbered = regexp.MustCompile(`^([0-9]{3,})-`)

// CheckNumbering reports spec files whose name carries no number, and numbers
// used by more than one spec.
//
// The number is the identifier every reference resolves through, including
// the ones in other repositories and in commit messages. A file it cannot be
// read from cannot be cited, and a number that two specs share sends a citation
// to whichever the reader finds first. Reuse is the worse half: it happens when
// a spec is deleted and its number is handed to the next one, at which point
// every existing citation silently means something else.
func CheckNumbering(cfg config.Spec, specs []Spec) []string {
	if !cfg.Numbered {
		return nil
	}
	var out []string
	byNumber := map[int][]string{}
	for _, s := range specs {
		n, ok := specNumber(s.Name)
		if !ok {
			out = append(out, fmt.Sprintf("%s: the file name carries no number; "+
				"a numbered tree names its specs NNN-name.md", s.Name))
			continue
		}
		byNumber[n] = append(byNumber[n], s.Name)
	}
	for _, n := range slices.Sorted(maps.Keys(byNumber)) {
		names := byNumber[n]
		if len(names) > 1 {
			sort.Strings(names)
			out = append(out, fmt.Sprintf("number %03d is used by %s; a number is a "+
				"stable identifier and is never reused", n, strings.Join(names, ", ")))
		}
	}
	return out
}

func specNumber(name string) (int, bool) {
	m := numbered.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// CheckReadiness reports specs that started before their dependencies closed.
//
// `depends_on` states an ordering, and nothing else checks that anyone kept
// to it. A spec built ahead of what it depends on is built against a design
// that is still moving, and the tree records an ordering that never happened.
//
// The two vocabularies are the repository's, because the point at which work
// starts is a repository's own decision. Empty turns the rule off.
func CheckReadiness(cfg config.Spec, specs []Spec) []string {
	if len(cfg.Started) == 0 {
		return nil
	}
	byName := map[string]Spec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	var out []string
	for _, s := range specs {
		if !slices.Contains(cfg.Started, s.Status) {
			continue
		}
		for _, d := range s.DependsOn {
			if strings.Contains(d, "/") {
				continue // another repository's tree
			}
			dep, ok := byName[d]
			if !ok {
				continue // CheckDependencies reports the dangling edge
			}
			if slices.Contains(cfg.Settled, dep.Status) {
				continue
			}
			out = append(out, fmt.Sprintf("%s: status is %s while its dependency %s is %q; "+
				"work starts when what it depends on has closed",
				s.Name, s.Status, dep.Name, dep.Status))
		}
	}
	return out
}
