// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
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
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if line, ok := importsC(string(body)); ok {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
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

// importsC reports the line importing "C", either as a single import or
// inside an import block. A comment mentioning C is not an import.
func importsC(src string) (int, bool) {
	inBlock := false
	for i, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "//"):
			continue
		case t == "import (":
			inBlock = true
			continue
		case inBlock && t == ")":
			inBlock = false
			continue
		}
		if t == `import "C"` || (inBlock && (t == `"C"` || strings.HasPrefix(t, `_ "C"`))) {
			return i + 1, true
		}
	}
	return 0, false
}
