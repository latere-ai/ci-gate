// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Lister returns every package in the module that can produce coverage data.
//
// It exists because a package with no test file is simply absent from a
// profile, and an absent package clears a per-package floor by not being
// measured. The floor then says nothing about the packages that need it most.
type Lister func() ([]string, error)

// GoLister returns a Lister backed by `go list ./...`.
//
// A package is listed only when it declares a function with a body. The
// coverage tool instruments statements, so a package that holds none produces
// no data however it is tested, and reporting it as unmeasured would be a
// finding no test can clear.
func GoLister(goBin, dir string) Lister {
	return func() ([]string, error) {
		cmd := exec.CommandContext(context.Background(), goBin,
			"list", "-json=ImportPath,Dir,GoFiles", "./...")
		cmd.Dir = dir
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("listing the module packages: %w: %s",
				err, strings.TrimSpace(stderr.String()))
		}

		var pkgs []string
		dec := json.NewDecoder(strings.NewReader(string(out)))
		for dec.More() {
			var pkg struct {
				ImportPath string
				Dir        string
				GoFiles    []string
			}
			if err := dec.Decode(&pkg); err != nil {
				return nil, fmt.Errorf("parsing the package list: %w", err)
			}
			if len(pkg.GoFiles) == 0 {
				continue
			}
			executable, err := hasStatements(pkg.Dir, pkg.GoFiles)
			if err != nil {
				return nil, err
			}
			if executable {
				pkgs = append(pkgs, pkg.ImportPath)
			}
		}
		sort.Strings(pkgs)
		return pkgs, nil
	}
}

// hasStatements reports whether a package declares a function with a body.
func hasStatements(dir string, files []string) (bool, error) {
	fset := token.NewFileSet()
	for _, name := range files {
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return false, fmt.Errorf("read %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && len(fn.Body.List) > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}
