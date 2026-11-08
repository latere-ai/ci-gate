// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CgoFree reports Go files that import "C".
//
// It reads the source rather than trusting the build, because a file can
// import "C" behind a build tag this platform does not select and still be a
// violation: the promise is that no supported build reaches cgo, and a build
// on one machine only answers for that machine.
//
// A repository makes this promise when it ships static binaries or claims to
// cross-compile. A claim in a user-facing document with no gate behind it goes
// stale silently.
func CgoFree(root string, out io.Writer, skip []string) error {
	var found []string
	scanned := 0
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// Skip the repository's own plumbing and anything the caller
			// named, but never silently skip a source directory.
			if name == ".git" || name == "testdata" || slices.Contains(skip, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		scanned++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		line, ok, err := importsC(fset, path)
		if err != nil {
			return fmt.Errorf("%s: %w; the gate cannot tell whether it imports \"C\", so fix the syntax first", rel, err)
		}
		if ok {
			found = append(found, fmt.Sprintf("%s:%d", rel, line))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanning for cgo: %w", err)
	}
	// A scan that read nothing proves nothing.
	if scanned == 0 {
		return fmt.Errorf("no Go files found under %s; the gate would pass vacuously", root)
	}
	if len(found) > 0 {
		for _, f := range found {
			_, _ = fmt.Fprintln(out, "  "+f)
		}
		return fmt.Errorf(`%d file(s) import "C"`, len(found))
	}
	_, _ = fmt.Fprintf(out, "no cgo in %d Go file(s)\n", scanned)
	return nil
}

// importsC parses the import declarations of the file at path and reports the
// line of the one importing "C", however it is written: alone, in a block,
// blank, renamed, or followed by a comment. A comment mentioning C is not an
// import. A file that does not parse is an error, not a pass.
func importsC(fset *token.FileSet, path string) (int, bool, error) {
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return 0, false, err
	}
	for _, imp := range file.Imports {
		if imp.Path != nil && imp.Path.Value == `"C"` {
			return fset.Position(imp.Pos()).Line, true, nil
		}
	}
	return 0, false, nil
}
