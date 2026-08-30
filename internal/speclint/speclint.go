// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package speclint checks that a spec tree describes itself consistently.
//
// It exists because the index drifts: in one repository every row in
// specs/README.md read `draft`, including five specs that were built,
// deployed and serving. The table had been hand-edited a dozen times that
// day. A status column that disagrees with the code is worse than no column,
// because a reader trusts it.
//
// The checks here are the ones every spec tree needs whatever its
// vocabulary: frontmatter parses, required keys are present, status comes
// from a closed set, dependency edges resolve, the graph is acyclic, the
// index agrees with the specs, and cross-references point at something. A
// repository whose lifecycle carries more meaning than this — decision
// records, layers, outcome rules — keeps those checks itself, because they
// are conventions rather than hygiene.
package speclint

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"latere.ai/x/ci-gate/internal/config"
)

// Spec is one parsed spec file.
type Spec struct {
	Name        string // base name, e.g. 019-gb10-serving-target.md
	Title       string
	Status      string
	DependsOn   []string
	frontmatter map[string]any
	body        string
}

// Run lints the tree described by cfg, writing a report to out. The returned
// error names how many problems were found; the problems themselves are in
// the report, because a list of thirty is more useful than the first one.
func Run(cfg config.Spec, root string, out io.Writer) error {
	if cfg.Dir == "" {
		_, _ = fmt.Fprintln(out, "spec-lint: no spec.dir configured, nothing to check")
		return nil
	}
	dir := filepath.Join(root, cfg.Dir)
	specs, err := Load(cfg, dir)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("%s holds no specs; a lint that checks nothing reports green forever", dir)
	}

	var problems []string
	problems = append(problems, CheckFrontmatter(cfg, specs)...)
	problems = append(problems, CheckVocabulary(cfg, specs)...)
	problems = append(problems, CheckStatusRequires(cfg, specs)...)
	problems = append(problems, CheckSections(cfg, specs)...)
	problems = append(problems, CheckScopedIDs(cfg, specs)...)
	problems = append(problems, CheckRegister(cfg, specs)...)
	problems = append(problems, CheckStatusLinked(cfg, specs)...)
	problems = append(problems, CheckMarker(cfg, specs)...)
	problems = append(problems, CheckDependencies(specs)...)
	problems = append(problems, CheckAcyclic(specs)...)
	problems = append(problems, CheckNumbering(cfg, specs)...)
	problems = append(problems, CheckReadiness(cfg, specs)...)
	if cfg.Tables {
		problems = append(problems, CheckTables(specs)...)
	}
	if cfg.Wikilinks {
		problems = append(problems, CheckWikilinks(specs)...)
	}
	if cfg.Index != "" {
		p, err := CheckIndex(cfg, root, specs)
		if err != nil {
			return err
		}
		problems = append(problems, p...)
	}

	sort.Strings(problems)
	for _, p := range problems {
		_, _ = fmt.Fprintln(out, "  "+p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s) in %s", len(problems), dir)
	}
	_, _ = fmt.Fprintf(out, "%d specs consistent\n", len(specs))
	return nil
}

// Load reads and parses every spec in dir, skipping the index and anything
// the config excludes.
func Load(cfg config.Spec, dir string) ([]Spec, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	index := filepath.Base(cfg.Index)

	var specs []Spec
	for _, p := range paths {
		name := filepath.Base(p)
		if cfg.IsExcluded(name) || (cfg.Index != "" && name == index) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		s, err := Parse(name, string(data))
		if err != nil {
			return nil, err
		}
		specs = append(specs, s)
	}
	return specs, nil
}

// Parse splits a spec into its frontmatter and body.
//
// Only the frontmatter block is scanned for fields, so prose that mentions
// "status:" cannot be mistaken for the field.
func Parse(name, src string) (Spec, error) {
	s := Spec{Name: name, frontmatter: map[string]any{}}
	// Windows checkouts turn LF into CRLF, and a parser that matches on "\n"
	// then leaves a stray "\r" on every value.
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return s, fmt.Errorf("%s: no frontmatter", name)
	}
	end := strings.Index(src[4:], "\n---")
	if end < 0 {
		return s, fmt.Errorf("%s: unterminated frontmatter", name)
	}
	head, body := src[4:4+end], src[4+end:]
	if err := yaml.Unmarshal([]byte(head), &s.frontmatter); err != nil {
		return s, fmt.Errorf("%s: frontmatter is not valid YAML: %w", name, err)
	}
	s.body = body
	s.Title, _ = s.frontmatter["title"].(string)
	s.Status, _ = s.frontmatter["status"].(string)
	if deps, ok := s.frontmatter["depends_on"].([]any); ok {
		for _, d := range deps {
			if v, ok := d.(string); ok && strings.TrimSpace(v) != "" {
				s.DependsOn = append(s.DependsOn, strings.TrimSpace(v))
			}
		}
	}
	return s, nil
}

// CheckFrontmatter reports specs missing a required key or using a status
// outside the configured vocabulary.
func CheckFrontmatter(cfg config.Spec, specs []Spec) []string {
	var out []string
	for _, s := range specs {
		for _, key := range cfg.Require {
			v, ok := s.frontmatter[key]
			if !ok {
				out = append(out, fmt.Sprintf("%s: frontmatter has no %s", s.Name, key))
				continue
			}
			if v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				out = append(out, fmt.Sprintf("%s: %s is empty", s.Name, key))
			}
		}
		if s.Status != "" && !cfg.AllowsStatus(s.Status) {
			out = append(out, fmt.Sprintf("%s: status %q is not one of %s",
				s.Name, s.Status, strings.Join(cfg.Status, "|")))
		}
	}
	return out
}

// CheckDependencies reports depends_on edges that do not resolve to a spec in
// the tree. An edge to a path outside the tree is left alone: a spec may
// legitimately depend on one in another repository, and this check cannot see
// that repository.
func CheckDependencies(specs []Spec) []string {
	known := map[string]bool{}
	for _, s := range specs {
		known[s.Name] = true
	}
	var out []string
	for _, s := range specs {
		for _, d := range s.DependsOn {
			if strings.Contains(d, "/") {
				continue // another repository's tree
			}
			if !known[d] {
				out = append(out, fmt.Sprintf("%s: depends_on %s, which does not exist", s.Name, d))
			}
		}
	}
	return out
}

// CheckAcyclic reports dependency cycles. A cycle makes the reading order in
// the index a lie and makes "what must land first" unanswerable.
func CheckAcyclic(specs []Spec) []string {
	deps := map[string][]string{}
	known := map[string]bool{}
	for _, s := range specs {
		known[s.Name] = true
	}
	for _, s := range specs {
		for _, d := range s.DependsOn {
			if known[d] {
				deps[s.Name] = append(deps[s.Name], d)
			}
		}
	}

	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	state := map[string]int{}
	var stack []string
	var out []string

	var visit func(string)
	visit = func(n string) {
		state[n] = onStack
		stack = append(stack, n)
		for _, d := range deps[n] {
			switch state[d] {
			case unvisited:
				visit(d)
			case onStack:
				if i := slices.Index(stack, d); i >= 0 {
					out = append(out, "dependency cycle: "+strings.Join(append(slices.Clone(stack[i:]), d), " -> "))
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
	}
	for _, s := range specs {
		if state[s.Name] == unvisited {
			visit(s.Name)
		}
	}
	return out
}

var wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// CheckWikilinks reports [[name]] references that resolve to nothing. These
// get checked by hand otherwise, which is exactly the work that stops
// happening.
func CheckWikilinks(specs []Spec) []string {
	known := map[string]bool{}
	for _, s := range specs {
		known[strings.TrimSuffix(s.Name, ".md")] = true
	}
	var out []string
	for _, s := range specs {
		seen := map[string]bool{}
		for _, m := range wikilinkRe.FindAllStringSubmatch(s.body, -1) {
			target := strings.TrimSuffix(m[1], ".md")
			if !known[target] && !seen[target] {
				seen[target] = true
				out = append(out, fmt.Sprintf("%s: [[%s]] resolves to nothing", s.Name, target))
			}
		}
	}
	return out
}

// linkRe matches a Markdown link to a local .md file.
var linkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+\.md)\)`)

// CheckIndex reports rows of the index that disagree with the specs they link
// to, and specs the index never lists.
//
// Finding the index rows is the whole difficulty, and two shapes make a naive
// match wrong. A spec cites other specs from prose tables, so "any table row
// with a link" reports drift that is not there. And a spec tree may document
// its own vocabulary in a legend table -- `| status | means |`, with rows that
// cite a spec or two as examples -- so "any table with a Status column" reads
// the legend's rows as claims about those specs.
//
// The rule that separates them is column order. An index row names the spec
// and then gives its status, so the link comes before the Status column. A
// legend row is about the status itself and mentions a spec afterwards. That
// also allows a tree to split its index across several tables, which tgo does.
func CheckIndex(cfg config.Spec, root string, specs []Spec) ([]string, error) {
	path := filepath.Join(root, cfg.Index)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	byName := map[string]Spec{}
	for _, s := range specs {
		byName[s.Name] = s
	}

	var out []string
	listed := map[string]bool{}
	rows := 0
	// statusCol is the Status column of the table being read, or -1 when the
	// current table has none.
	statusCol := -1
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		if !isTableRow(line) {
			statusCol = -1
			continue
		}
		if col, ok := indexHeader(line, i, lines); ok {
			statusCol = col
			continue
		}
		if statusCol < 0 || isSeparator(line) {
			continue
		}
		cells := rowCells(line)
		linkCol, target, href := linkCell(cells)
		if linkCol < 0 || linkCol >= statusCol {
			continue // prose citation, or a legend row about the status itself
		}
		// A row pointing into a subdirectory is pointing outside the linted
		// set -- an archive, or another tree. Load never reads those, so the
		// index cannot be asked to agree with them, the same way a depends_on
		// edge into another repository is left alone.
		if strings.Contains(href, "/") {
			continue
		}
		rows++
		spec, ok := byName[target]
		if !ok {
			out = append(out, fmt.Sprintf("%s: row links to %s, which is not a spec in the tree", cfg.Index, target))
			continue
		}
		listed[target] = true
		if len(cfg.Status) == 0 {
			continue
		}
		if statusCol >= len(cells) {
			out = append(out, fmt.Sprintf("%s: the row for %s has no status cell", cfg.Index, target))
			continue
		}
		if got := strings.Trim(cells[statusCol], "*_`"); got != spec.Status {
			out = append(out, fmt.Sprintf("%s: row says %s is %q; the spec says %q",
				cfg.Index, target, got, spec.Status))
		}
	}
	if rows == 0 {
		return nil, fmt.Errorf("%s lists no specs; has the table shape changed?", path)
	}
	// A spec absent from the index is invisible.
	for _, s := range specs {
		if !listed[s.Name] {
			out = append(out, fmt.Sprintf("%s: %s is not listed", cfg.Index, s.Name))
		}
	}
	return out, nil
}

// linkCell returns the index of the first cell holding a link to a .md file,
// and the file's base name.
func linkCell(cells []string) (int, string, string) {
	for i, c := range cells {
		if m := linkRe.FindStringSubmatch(c); m != nil {
			return i, filepath.Base(m[1]), m[1]
		}
	}
	return -1, "", ""
}

func isTableRow(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// isSeparator matches the |---|---| line under a table header.
var separatorRe = regexp.MustCompile(`^\|[\s:|-]+\|?$`)

func isSeparator(line string) bool {
	return separatorRe.MatchString(strings.TrimSpace(line))
}

// indexHeader reports the Status column of a header row, if this row is a
// header (the next line is a separator) and it has one.
func indexHeader(line string, i int, lines []string) (int, bool) {
	if i+1 >= len(lines) || !isSeparator(lines[i+1]) {
		return 0, false
	}
	for col, cell := range rowCells(line) {
		if strings.EqualFold(cell, "status") {
			return col, true
		}
	}
	return 0, false
}

// rowCells splits a table row into its trimmed cells.
func rowCells(line string) []string {
	parts := strings.Split(strings.TrimSpace(line), "|")
	if len(parts) < 2 {
		return nil
	}
	parts = parts[1:]
	if strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}
