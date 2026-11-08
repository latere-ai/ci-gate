// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
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
	fset := token.NewFileSet()
	files, err := TrackedFiles(root, "the Go files to scan for cgo", append([]string{"testdata", "node_modules"}, skip...), "*.go")
	if err != nil {
		return fmt.Errorf("scanning for cgo: %w; run it inside a git checkout", err)
	}
	scanned := len(files)
	for _, rel := range files {
		line, ok, err := importsC(fset, filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("%s: %w; the gate cannot tell whether it imports \"C\", so fix the syntax first", rel, err)
		}
		if ok {
			found = append(found, fmt.Sprintf("%s:%d", filepath.FromSlash(rel), line))
		}
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
