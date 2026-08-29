// Copyright 2026 Latere AI.
// Licensed under the Apache License, Version 2.0.

// Package cover gates coverage per package rather than as a repository
// average.
//
// An average lets a well-tested package carry an untested one and reports a
// number nobody can act on. That is not hypothetical: when this check was
// first written one repository sat at 90.4% and passed, while two packages
// inside it were at 85.7% and 87.8% — both below the floor, both invisible
// behind the average.
package cover

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// Run reads a coverage profile and reports whether every non-exempt package
// clears the configured floor.
func Run(cfg config.Cover, profile string, out io.Writer) error {
	byPkg, err := parse(profile)
	if err != nil {
		return err
	}
	// A profile with no records means the tests did not run. Reporting that
	// as a pass is the worst outcome available: green over nothing.
	if len(byPkg) == 0 {
		return fmt.Errorf("%s covers no packages; did the tests run?", profile)
	}

	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	var failed []string
	measured := 0
	for _, p := range pkgs {
		c := byPkg[p]
		if c.total == 0 {
			continue // no statements: nothing to cover
		}
		pct := 100 * float64(c.covered) / float64(c.total)
		if why, ok := cfg.ExemptFor(p); ok {
			fmt.Fprintf(out, "     %-44s %6.1f%%  exempt: %s\n", short(cfg, p), pct, why)
			continue
		}
		measured++
		mark := "ok  "
		if pct < cfg.Threshold {
			mark = "FAIL"
			failed = append(failed, fmt.Sprintf("%s %.1f%%", short(cfg, p), pct))
		}
		fmt.Fprintf(out, "%s %-44s %6.1f%%  (%d/%d statements)\n",
			mark, short(cfg, p), pct, c.covered, c.total)
	}

	if len(failed) > 0 {
		return fmt.Errorf("below %.0f%%: %s", cfg.Threshold, strings.Join(failed, ", "))
	}
	// A gate that passes because it measured nothing keeps reporting green as
	// the tree fills up, until somebody notices the number never moved. Every
	// package being exempt is the shape that produces it.
	if measured == 0 {
		return fmt.Errorf("no non-exempt package was measured, so the gate would " +
			"pass vacuously; either the tests did not run or every package is " +
			"exempt, and both are failures rather than a green build")
	}
	fmt.Fprintf(out, "\nevery package clears %.0f%% (%d measured)\n", cfg.Threshold, measured)
	return nil
}

type counts struct{ covered, total int }

// parse totals statement counts per package.
//
// With -coverpkg=./... the same block appears once per test binary that
// executed it. Each block is therefore counted once and marked covered if any
// run covered it; summing the appearances inflates both totals and is a bug,
// not a variant.
func parse(profile string) (map[string]*counts, error) {
	f, err := os.Open(profile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type blockKey struct{ file, span string }
	type blockVal struct {
		pkg     string
		stmts   int
		covered bool
	}
	blocks := map[blockKey]blockVal{}

	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if text == "" || strings.HasPrefix(text, "mode:") {
			continue
		}
		// path/to/file.go:startLine.col,endLine.col numStmts count
		colon := strings.LastIndex(text, ":")
		if colon < 0 {
			return nil, fmt.Errorf("%s:%d: no file/span separator in %q", profile, line, text)
		}
		file, rest := text[:colon], text[colon+1:]
		fields := strings.Fields(rest)
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: want 'span stmts count', got %q", profile, line, rest)
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: malformed statement count in %q", profile, line, text)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: malformed hit count in %q", profile, line, text)
		}
		k := blockKey{file, fields[0]}
		b := blocks[k]
		b.pkg, b.stmts = path.Dir(file), stmts
		b.covered = b.covered || count > 0
		blocks[k] = b
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", profile, err)
	}

	byPkg := map[string]*counts{}
	for _, b := range blocks {
		c := byPkg[b.pkg]
		if c == nil {
			c = &counts{}
			byPkg[b.pkg] = c
		}
		c.total += b.stmts
		if b.covered {
			c.covered += b.stmts
		}
	}
	return byPkg, nil
}

// short drops the module prefix so the report fits a terminal.
func short(cfg config.Cover, pkg string) string {
	if cfg.TrimPrefix == "" {
		return pkg
	}
	return strings.TrimPrefix(pkg, cfg.TrimPrefix)
}
