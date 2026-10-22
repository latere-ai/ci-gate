// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package platformsdk

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// unknown is how a part the scan cannot know is written in a route: a
// variable, a call's result, a parameter.
const unknown = "{}"

// piece is one run of a string value: text the source fixes, or a part
// only the running program knows.
type piece struct {
	text  string
	known bool
}

// value is a string expression as far as the source fixes it, in order.
type value []piece

// constant is the value's text when the source fixes all of it.
func (v value) constant() (string, bool) {
	if len(v) == 1 && v[0].known {
		return v[0].text, true
	}
	return "", false
}

// fixed reports whether any of the value is text the source fixes.
func (v value) fixed() bool {
	for _, p := range v {
		if p.known {
			return true
		}
	}
	return false
}

// render writes the value with every unknown part as {}.
func (v value) render() string {
	var b strings.Builder
	for _, p := range v {
		if p.known {
			b.WriteString(p.text)
		} else {
			b.WriteString(unknown)
		}
	}
	return b.String()
}

// opaque is a value the source does not fix at all.
func opaque() value { return value{{}} }

// concat is a + b, with adjacent runs of the same kind merged.
func concat(a, b value) value {
	out := append(value{}, a...)
	for _, p := range b {
		if n := len(out); n > 0 && out[n-1].known == p.known {
			out[n-1].text += p.text
			continue
		}
		out = append(out, p)
	}
	return out
}

// constDecl is one package-level constant: its value's expression, and the
// file whose imports that expression is read with.
type constDecl struct {
	expr ast.Expr
	file *srcFile
}

// folded is a constant's value once read; busy marks one being read, so a
// cycle, which the compiler refuses anyway, ends instead of recursing.
type folded struct {
	text     string
	ok, busy bool
}

// index holds every package-level constant in the tree, so a route built
// from a prefix constant that another package declares is read where it is
// built.
type index struct {
	decls map[string]constDecl
	memo  map[string]folded
}

// newIndex reads the constants of every file. A constant with no value in
// a const block repeats the expression of the one before it, as the
// language says.
func newIndex(files []*srcFile) *index {
	ix := &index{decls: map[string]constDecl{}, memo: map[string]folded{}}
	for _, f := range files {
		for _, decl := range f.ast.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			var last []ast.Expr
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				values := vs.Values
				if len(values) == 0 {
					values = last
				}
				last = values
				for i, name := range vs.Names {
					if i < len(values) {
						ix.decls[f.pkg+"."+name.Name] = constDecl{expr: values[i], file: f}
					}
				}
			}
		}
	}
	return ix
}

// constant is the text of the string constant name in package pkg, when the
// source fixes it.
func (ix *index) constant(pkg, name string) (string, bool) {
	key := pkg + "." + name
	if m, ok := ix.memo[key]; ok {
		return m.text, m.ok && !m.busy
	}
	d, ok := ix.decls[key]
	if !ok {
		return "", false
	}
	ix.memo[key] = folded{busy: true}
	fo := &folder{ix: ix, file: d.file}
	text, ok := fo.fold(d.expr).constant()
	ix.memo[key] = folded{text: text, ok: ok}
	return text, ok
}

// formats are the fmt functions whose format string is folded with its
// arguments, by the index of the format among the call's arguments. A
// route read into an error message is then read as the message, which
// names no call, rather than as a constant on its own.
var formats = map[string]int{"Sprintf": 0, "Errorf": 0, "Printf": 0, "Fprintf": 1}

// folder reads one expression's value. used collects the nodes whose text
// the value holds, so the scan reads each part of a string once, as part of
// the outermost expression that holds it.
type folder struct {
	ix     *index
	file   *srcFile
	locals map[string]bool
	used   []ast.Node
}

// fold reads e as a string value: a literal, a concatenation, a constant of
// this package or one the file imports, a conversion to string, or a fmt
// format with its arguments. Anything else is a part the source does not
// fix.
func (fo *folder) fold(e ast.Expr) value {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return opaque()
		}
		s, err := strconv.Unquote(x.Value)
		if err != nil {
			return opaque()
		}
		fo.used = append(fo.used, x)
		return value{{text: s, known: true}}
	case *ast.ParenExpr:
		v := fo.fold(x.X)
		if v.fixed() {
			fo.used = append(fo.used, x)
		}
		return v
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return opaque()
		}
		v := concat(fo.fold(x.X), fo.fold(x.Y))
		if v.fixed() {
			fo.used = append(fo.used, x)
		}
		return v
	case *ast.Ident:
		if fo.locals[x.Name] {
			return opaque()
		}
		s, ok := fo.ix.constant(fo.file.pkg, x.Name)
		if !ok {
			return opaque()
		}
		fo.used = append(fo.used, x)
		return value{{text: s, known: true}}
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok || fo.locals[id.Name] {
			return opaque()
		}
		pkg, ok := fo.file.imports[id.Name]
		if !ok {
			return opaque()
		}
		s, ok := fo.ix.constant(pkg, x.Sel.Name)
		if !ok {
			return opaque()
		}
		fo.used = append(fo.used, x, id)
		return value{{text: s, known: true}}
	case *ast.CallExpr:
		return fo.call(x)
	}
	return opaque()
}

// call reads a conversion to string and a fmt format; any other call's
// result is a part the source does not fix.
func (fo *folder) call(x *ast.CallExpr) value {
	if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "string" && !fo.locals["string"] && len(x.Args) == 1 {
		v := fo.fold(x.Args[0])
		if v.fixed() {
			fo.used = append(fo.used, x)
		}
		return v
	}
	sel, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		return opaque()
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || fo.file.fmt == "" || id.Name != fo.file.fmt || fo.locals[id.Name] {
		return opaque()
	}
	at, ok := formats[sel.Sel.Name]
	if !ok || at >= len(x.Args) {
		return opaque()
	}
	format, ok := fo.fold(x.Args[at]).constant()
	if !ok {
		return opaque()
	}
	fo.used = append(fo.used, x)
	return fo.format(format, x.Args[at+1:])
}

// format substitutes each %s and %v of a constant format with its
// argument's value. Any other verb, and a verb whose argument is chosen by
// index or that takes a width from the arguments, is a part the source
// does not fix.
func (fo *folder) format(f string, args []ast.Expr) value {
	var out value
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = concat(out, value{{text: text.String(), known: true}})
			text.Reset()
		}
	}
	next := 0
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			text.WriteByte(f[i])
			continue
		}
		if i+1 < len(f) && f[i+1] == '%' {
			text.WriteByte('%')
			i++
			continue
		}
		j := i + 1
		for j < len(f) && strings.IndexByte("+-# 0123456789.*[]", f[j]) >= 0 {
			j++
		}
		if j >= len(f) {
			text.WriteString(f[i:])
			break
		}
		flush()
		spec := f[i : j+1]
		arg := opaque()
		if next < len(args) && (f[j] == 's' || f[j] == 'v') && !strings.ContainsAny(spec, "*[") {
			arg = fo.fold(args[next])
		}
		out = concat(out, arg)
		next++
		i = j
	}
	flush()
	if len(out) == 0 {
		return value{{known: true}}
	}
	return out
}
