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
	"path/filepath"
	"slices"
	"strings"
)

// OtelClient reports outbound HTTP clients that carry no tracing transport.
//
// Two shapes lose a trace. An &http.Client{...} literal with no Transport
// field, and any use of http.DefaultClient, both call out with the stdlib
// transport: no client span is recorded and no traceparent header is sent, so
// the service on the other end opens a fresh trace instead of joining the
// caller's. The hop disappears, and it disappears silently, which is why this
// needs a gate rather than review.
//
// The scan parses each file rather than matching text. A Transport field
// several lines below the opening brace still counts, a comment explaining
// this rule does not trip it, and Timeout: cfg.Transport.Timeout does not
// pass for a Transport field the way a substring test would.
//
// Test files are excluded. A test that dials an httptest server has no trace
// to continue, and requiring instrumentation there buys nothing.
func OtelClient(root string, out io.Writer, skip []string) error {
	var found []string
	scanned := 0
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// .claude holds agent worktrees, which are whole scratch copies of
			// the repository. Walking them reports the same file twice and
			// resurrects violations already fixed on the branch.
			if name == ".git" || name == ".claude" || name == "testdata" || name == "node_modules" || slices.Contains(skip, name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		scanned++

		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			// A file the parser cannot read is a build failure the compiler
			// will report with a better message. Do not fail the gate on it.
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, v := range violations(file) {
			pos := fset.Position(v.pos)
			found = append(found, fmt.Sprintf("%s:%d: %s", rel, pos.Line, v.reason))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanning for uninstrumented HTTP clients: %w", err)
	}
	// A scan that read nothing proves nothing.
	if scanned == 0 {
		return fmt.Errorf("no non-test Go files found under %s; the gate would pass vacuously", root)
	}
	if len(found) > 0 {
		for _, f := range found {
			_, _ = fmt.Fprintln(out, "  "+f)
		}
		return fmt.Errorf("%d uninstrumented outbound HTTP client(s)", len(found))
	}
	_, _ = fmt.Fprintf(out, "every outbound HTTP client is instrumented, across %d Go file(s)\n", scanned)
	return nil
}

// violation is one uninstrumented client and why it is one.
type violation struct {
	pos    token.Pos
	reason string
}

// violations walks a parsed file for both losing shapes.
func violations(file *ast.File) []violation {
	// The import may be renamed. Resolve the local name for net/http first,
	// so a file importing it as nethttp is still checked, and a different
	// package that happens to expose a Client type is not.
	local, ok := httpImportName(file)
	if !ok {
		return nil
	}

	var out []violation
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if !isSelector(node.Type, local, "Client") {
				return true
			}
			if !hasKey(node, "Transport") {
				out = append(out, violation{
					pos:    node.Pos(),
					reason: "http.Client literal sets no Transport; use otel.Transport(...) or otel.HTTPClient()",
				})
			}
		case *ast.SelectorExpr:
			if isSelector(node, local, "DefaultClient") {
				out = append(out, violation{
					pos:    node.Pos(),
					reason: "http.DefaultClient is not instrumented; use otel.HTTPClient()",
				})
			}
		}
		return true
	})
	return out
}

// httpImportName returns the local name net/http is bound to, and whether the
// file imports it at all.
func httpImportName(file *ast.File) (string, bool) {
	for _, imp := range file.Imports {
		if imp.Path == nil || imp.Path.Value != `"net/http"` {
			continue
		}
		if imp.Name != nil {
			// A dot import puts Client in scope unqualified. Rare enough, and
			// unresolvable without type information, that the honest answer is
			// to skip the file rather than guess.
			if imp.Name.Name == "." || imp.Name.Name == "_" {
				return "", false
			}
			return imp.Name.Name, true
		}
		return "http", true
	}
	return "", false
}

// isSelector reports whether e is the expression pkg.name.
func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// hasKey reports whether the composite literal sets the named field. Only the
// key side counts, so a value mentioning Transport does not satisfy it.
func hasKey(lit *ast.CompositeLit, name string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == name {
			return true
		}
	}
	return false
}
