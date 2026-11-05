// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scope

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/gates"
)

// inert reports whether no changed file can move the verdict of a gate that
// reads Go, and when one can, which file and why. A file is inert when all of these hold:
//
//   - it is not Go source. A whole-tree file never gets here.
//   - it lies under no package directory, and under no nested module. A
//     package reads the files in its directory and below it: testdata,
//     fixtures, and what it embeds. The root package reads only the files
//     in the root itself, since every file below it is reached by a path a
//     literal or an embed pattern names, and both are checked next.
//   - no //go:embed pattern matches it.
//   - no string literal in a tracked Go file names it: its path, a
//     directory on its path, or its base name, as a path element. This is
//     what a hand-written path filter encoded: a test that reads
//     "../../specs/003-api.md" holds the literal "specs".
//
// A test that walks the tree without naming anything is the case this
// misses, and the release bar, which checks the whole tree, catches it.
//
// Any failure to read the repository returns false: the scope never
// narrows on a guess.
func inert(root string, files []string, run gates.Exec) (bool, string) {
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			return false, f + " is Go source"
		}
	}
	if len(files) == 0 {
		return true, ""
	}
	out, err := run(nil, false, "git", "ls-files", "-z")
	if err != nil {
		return false, "git cannot list the tracked files: " + err.Error()
	}
	var goFiles, modules []string
	pkgDirs := map[string]bool{}
	for f := range strings.SplitSeq(string(out), "\x00") {
		switch {
		case strings.HasPrefix(f, "vendor/"):
		case strings.HasSuffix(f, ".go"):
			goFiles = append(goFiles, f)
			pkgDirs[path.Dir(f)] = true
		case path.Base(f) == "go.mod" && path.Dir(f) != ".":
			modules = append(modules, path.Dir(f))
		}
	}
	for _, f := range files {
		if underPackage(f, pkgDirs, modules) {
			return false, f + " lies in a package directory or a nested module"
		}
	}

	n := newNeedles(files)
	for _, g := range goFiles {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(g)))
		if err != nil {
			// A tracked file that cannot be read is not one to rule out.
			if os.IsNotExist(err) {
				continue
			}
			return false, "cannot read " + g + ": " + err.Error()
		}
		if why := n.scan(path.Dir(g), src); why != "" {
			return false, why + " in " + g
		}
	}
	return true, ""
}

// underPackage reports whether f sits in a package directory or below it,
// or in a nested module.
func underPackage(f string, pkgDirs map[string]bool, modules []string) bool {
	for d := path.Dir(f); d != "."; d = path.Dir(d) {
		if pkgDirs[d] {
			return true
		}
	}
	for _, m := range modules {
		if within(f, m) {
			return true
		}
	}
	return false
}

// needles are the names a Go file can use to read one of the changed files.
type needles struct {
	files []string
	// elems are the path elements that name a file: its base name and every
	// directory on its path. paths are its own path and its parent
	// directories' paths.
	elems map[string]bool
	paths map[string]bool
}

func newNeedles(files []string) *needles {
	n := &needles{files: files, elems: map[string]bool{}, paths: map[string]bool{}}
	for _, f := range files {
		n.paths[f] = true
		n.elems[path.Base(f)] = true
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			n.paths[d] = true
			n.elems[path.Base(d)] = true
		}
	}
	return n
}

// names reports whether the string a literal holds names a changed file.
func (n *needles) names(lit string) bool {
	for e := range strings.SplitSeq(lit, "/") {
		if e != "" && e != "." && n.elems[e] {
			return true
		}
	}
	return n.paths[path.Clean(lit)]
}

// embeds reports whether an embed pattern in a package directory dir
// matches a changed file: the file itself, or a directory above it that
// the pattern names.
func (n *needles) embeds(dir, pattern string) bool {
	pattern = strings.TrimPrefix(pattern, "all:")
	for _, f := range n.files {
		if !within(f, dir) {
			continue
		}
		rel := f
		if dir != "." {
			rel = strings.TrimPrefix(f, dir+"/")
		}
		parts := strings.Split(rel, "/")
		for i := 1; i <= len(parts); i++ {
			// A pattern go build would reject cannot be told from one that
			// matches, so it counts as a match.
			if ok, err := path.Match(pattern, strings.Join(parts[:i], "/")); ok || err != nil {
				return true
			}
		}
	}
	return false
}

// scan reads one Go file's tokens and returns what in it names a changed
// file, a string literal or an embed directive, or "" when nothing does. A file that does not scan
// still counts for every token before the error: it fails the build, which
// is a failure the bar reports on its own.
func (n *needles) scan(dir string, src []byte) string {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	for {
		_, tok, lit := s.Scan()
		switch tok {
		case token.EOF:
			return ""
		case token.STRING:
			if v, err := strconv.Unquote(lit); err == nil {
				lit = v
			} else {
				lit = strings.Trim(lit, "\"`")
			}
			if n.names(lit) {
				return fmt.Sprintf("the literal %q names a changed file", lit)
			}
		case token.COMMENT:
			rest, ok := strings.CutPrefix(lit, "//go:embed ")
			if !ok {
				continue
			}
			for _, p := range embedPatterns(rest) {
				if n.embeds(dir, p) {
					return "the embed pattern " + strconv.Quote(p) + " matches a changed file"
				}
			}
		}
	}
}

// embedPatterns splits a //go:embed line into its patterns, which are
// space separated and may be quoted.
func embedPatterns(s string) []string {
	var out []string
	s = strings.TrimSpace(s)
	for s != "" {
		var p string
		if q, err := strconv.QuotedPrefix(s); err == nil {
			p, s = q, s[len(q):]
			if v, err := strconv.Unquote(p); err == nil {
				p = v
			}
		} else {
			p, s, _ = strings.Cut(s, " ")
		}
		out = append(out, p)
		s = strings.TrimSpace(s)
	}
	return out
}
