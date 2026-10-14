// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	// testcontainersModule is the module whose packages start containers.
	testcontainersModule = "github.com/testcontainers/testcontainers-go"
	// reapPackage labels what they start, for the sweep and the run's check.
	reapPackage = "latere.ai/x/ci-gate/reap"
)

// ContainerLabels reports each package that imports testcontainers-go and
// does not import reap.
//
// The containers gate sees only what carries the reap labels. A container
// started without them is neither swept nor counted against the run, so a
// helper that never adopted the contract leaks as it did before, and the
// suite still says it left nothing behind. The labels are passed at the call
// that starts the container, through option slices and helpers a syntax scan
// cannot follow, so the gate asks one level up: a package that reaches for
// testcontainers imports the package that labels.
//
// The package is the directory, test files included. A test that starts a
// container with no helper does it from a _test.go file, and an external test
// package shares its directory with the package it tests.
func ContainerLabels(root string, out io.Writer) error {
	pkgs, scanned, err := testcontainersPackages(root)
	if err != nil {
		return err
	}
	// A scan that read nothing proves nothing.
	if scanned == 0 {
		return fmt.Errorf("no Go files found under %s; the gate would pass vacuously", root)
	}
	if len(pkgs) == 0 {
		_, _ = fmt.Fprintf(out, "no package imports testcontainers-go, across %d Go file(s), so none owes the reap labels\n", scanned)
		return nil
	}
	var missing, labelled []string
	for _, p := range pkgs {
		if p.reap {
			labelled = append(labelled, p.dir)
			continue
		}
		missing = append(missing, fmt.Sprintf("%s:%d: imports %s, and no file of the package imports %s",
			p.file, p.line, p.path, reapPackage))
	}
	if len(missing) > 0 {
		for _, m := range missing {
			_, _ = fmt.Fprintln(out, "  "+m)
		}
		verb := "imports"
		if len(missing) > 1 {
			verb = "import"
		}
		return fmt.Errorf("%s %s testcontainers-go without reap, so no sweep and no suite sees the containers it starts; "+
			"label each container and network with reap.Labels() and sweep with reap.Sweep(ctx)",
			ofNoun(len(missing), "package"), verb)
	}
	_, _ = fmt.Fprintf(out, "every package that imports testcontainers-go imports reap: %s\n", strings.Join(labelled, ", "))
	return nil
}

// ImportsTestcontainers reports whether any package under root imports
// testcontainers-go, which is what gives ContainerLabels a subject.
func ImportsTestcontainers(root string) (bool, error) {
	pkgs, _, err := testcontainersPackages(root)
	return len(pkgs) > 0, err
}

// testcontainersPackage is one directory holding a testcontainers import:
// the first file and line that imports it, and whether any file of the
// directory imports reap.
type testcontainersPackage struct {
	dir  string
	file string
	line int
	path string
	reap bool
}

// testcontainersPackages walks root the way the go command reads ./..., and
// returns the directories that import testcontainers-go, in walk order, with
// the number of Go files it read.
func testcontainersPackages(root string) ([]testcontainersPackage, int, error) {
	var pkgs []testcontainersPackage
	reaps := map[string]bool{}
	scanned := 0
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && notPackageDir(p, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		scanned++
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		dir := path.Dir(rel)
		for _, imp := range importsOf(fset, p) {
			ip, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				continue
			}
			switch {
			case ip == reapPackage:
				reaps[dir] = true
			case ip == testcontainersModule || strings.HasPrefix(ip, testcontainersModule+"/"):
				// The walk reads a directory's files in name order, so the
				// first one seen is the one to point at.
				if !slices.ContainsFunc(pkgs, func(t testcontainersPackage) bool { return t.dir == dir }) {
					pkgs = append(pkgs, testcontainersPackage{dir: dir, file: rel, line: fset.Position(imp.Pos()).Line, path: ip})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("scanning for testcontainers imports: %w", err)
	}
	for i := range pkgs {
		pkgs[i].reap = reaps[pkgs[i].dir]
	}
	return pkgs, scanned, nil
}

// importsOf parses the imports of the Go file at p. A file the parser cannot
// read yields none: that is a build failure, which the compiler reports
// better than this gate could.
func importsOf(fset *token.FileSet, p string) []*ast.ImportSpec {
	file, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	return file.Imports
}

// notPackageDir reports a directory whose files are no package of this
// module's ./...: the ones the go command ignores, a vendored copy of a
// dependency, and a nested checkout of another repository, such as an agent's
// worktree, which is another tree with its own gate.
func notPackageDir(p, name string) bool {
	switch {
	case strings.HasPrefix(name, "."), strings.HasPrefix(name, "_"),
		name == "testdata", name == "vendor", name == "node_modules":
		return true
	}
	_, err := os.Lstat(filepath.Join(p, ".git"))
	return err == nil
}
