// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package registers

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// source is one parsed file with the import path of its package.
type source struct {
	rel     string
	pkgPath string
	file    *ast.File
}

// constDecl is a package-level constant's value expression and the imports of
// the file that declares it, which the expression's qualified names resolve
// through.
type constDecl struct {
	expr    ast.Expr
	imports map[string]string
}

// index is what reading the whole tree first makes available to one file:
// the name each package declares, and its string constants.
type index struct {
	// names maps an in-tree import path to the name its package clause gives.
	names map[string]string
	// consts maps an import path to the constants its files declare.
	consts map[string]map[string]constDecl
}

// newIndex reads the package clauses and package-level constants of srcs.
func newIndex(srcs []*source) *index {
	ix := &index{names: map[string]string{}, consts: map[string]map[string]constDecl{}}
	for _, s := range srcs {
		ix.names[s.pkgPath] = s.file.Name.Name
	}
	for _, s := range srcs {
		imports := ix.imports(s.file)
		for _, decl := range s.file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					if ix.consts[s.pkgPath] == nil {
						ix.consts[s.pkgPath] = map[string]constDecl{}
					}
					ix.consts[s.pkgPath][name.Name] = constDecl{expr: vs.Values[i], imports: imports}
				}
			}
		}
	}
	return ix
}

var majorElem = regexp.MustCompile(`^v[0-9]+$`)

// imports maps each local name in file to the import path it binds.
//
// The local name of an import without a rename is the name the imported
// package declares. For a package of this tree that is read from its package
// clause. For any other path the last element is the best the source alone
// says, except that a major-version element (example.com/lib/v2) or a
// gopkg.in suffix (gopkg.in/yaml.v3) is not a name: the element before it is.
// A dot or blank import binds no name.
func (ix *index) imports(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := ix.localName(ip)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "." || local == "_" {
			continue
		}
		out[local] = ip
	}
	return out
}

func (ix *index) localName(ip string) string {
	if name, ok := ix.names[ip]; ok {
		return name
	}
	base := path.Base(ip)
	if majorElem.MatchString(base) && path.Dir(ip) != "." {
		return path.Base(path.Dir(ip))
	}
	if strings.HasPrefix(ip, "gopkg.in/") {
		if i := strings.LastIndex(base, ".v"); i > 0 && majorElem.MatchString(base[i+1:]) {
			return base[:i]
		}
	}
	return base
}

// maxConstDepth bounds a chain of constants naming constants, so a cycle the
// compiler would reject cannot hang the scan.
const maxConstDepth = 16

// constant is the string value of e, when it is a string literal, a constant
// of pkg or of an imported tree package, or a sum of those.
func (ix *index) constant(e ast.Expr, pkg string, imports map[string]string, depth int) (string, bool) {
	if depth > maxConstDepth {
		return "", false
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return ix.constant(x.X, pkg, imports, depth+1)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, ok := ix.constant(x.X, pkg, imports, depth+1)
		if !ok {
			return "", false
		}
		r, ok := ix.constant(x.Y, pkg, imports, depth+1)
		return l + r, ok
	case *ast.Ident:
		d, ok := ix.consts[pkg][x.Name]
		if !ok {
			return "", false
		}
		return ix.constant(d.expr, pkg, d.imports, depth+1)
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		ip, ok := imports[id.Name]
		if !ok {
			return "", false
		}
		d, ok := ix.consts[ip][x.Sel.Name]
		if !ok {
			return "", false
		}
		return ix.constant(d.expr, ip, d.imports, depth+1)
	}
	return "", false
}

// text is one string a call hands to a surface: where it is written at the
// call, and what it says.
type text struct {
	pos token.Pos
	str string
}

// argTexts collects every string under e that the source fixes: each literal,
// and each constant the expression names, so a message held in a constant is
// read where it is passed.
func (ix *index) argTexts(e ast.Expr, pkg string, imports map[string]string) []text {
	var out []text
	var walk func(n ast.Node) bool
	walk = func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if s, err := strconv.Unquote(x.Value); err == nil {
					out = append(out, text{x.Pos(), s})
				}
			}
		case *ast.Ident:
			if s, ok := ix.constant(x, pkg, imports, 0); ok {
				out = append(out, text{x.Pos(), s})
			}
		case *ast.SelectorExpr:
			if s, ok := ix.constant(x, pkg, imports, 0); ok {
				out = append(out, text{x.Pos(), s})
			} else {
				ast.Inspect(x.X, walk)
			}
			return false
		}
		return true
	}
	ast.Inspect(e, walk)
	return out
}
