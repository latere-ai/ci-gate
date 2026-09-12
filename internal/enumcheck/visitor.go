// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package enumcheck

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type visitor struct {
	*checker
	pkg       *packages.Package
	signature *types.Signature
	parser    bool
}

func (v *visitor) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.FuncDecl:
		child := *v
		obj := v.pkg.TypesInfo.Defs[n.Name]
		child.signature, _ = obj.Type().(*types.Signature)
		child.parser = v.parsers[obj]
		return &child
	case *ast.FuncLit:
		child := *v
		child.signature, _ = v.pkg.TypesInfo.TypeOf(n).(*types.Signature)
		child.parser = false
		return &child
	case *ast.ValueSpec:
		if len(n.Names) == len(n.Values) {
			for i, name := range n.Names {
				obj := v.pkg.TypesInfo.Defs[name]
				if d := v.domain(obj.Type()); d != nil && !d.members[obj] {
					v.checkValue(n.Values[i], d)
				}
			}
		}
	case *ast.AssignStmt:
		if len(n.Lhs) == len(n.Rhs) {
			for i, lhs := range n.Lhs {
				v.checkType(n.Rhs[i], v.pkg.TypesInfo.TypeOf(lhs))
			}
		}
	case *ast.ReturnStmt:
		if v.signature != nil && len(n.Results) == v.signature.Results().Len() {
			for i, result := range n.Results {
				v.checkType(result, v.signature.Results().At(i).Type())
			}
		}
	case *ast.CallExpr:
		v.call(n)
	case *ast.BinaryExpr:
		switch n.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			if d := v.expressionDomain(n.Y); d != nil {
				v.checkValue(v.uncast(n.X), d)
			}
			if d := v.expressionDomain(n.X); d != nil {
				v.checkValue(v.uncast(n.Y), d)
			}
		}
	case *ast.SendStmt:
		if channel, ok := v.pkg.TypesInfo.TypeOf(n.Chan).Underlying().(*types.Chan); ok {
			v.checkType(n.Value, channel.Elem())
		}
	case *ast.CompositeLit:
		v.composite(n)
	case *ast.IndexExpr:
		if m, ok := v.pkg.TypesInfo.TypeOf(n.X).Underlying().(*types.Map); ok {
			v.checkType(n.Index, m.Key())
		}
	case *ast.SwitchStmt:
		v.switchStmt(n)
	}
	return v
}

func (v *visitor) checkType(expr ast.Expr, typ types.Type) {
	if d := v.domain(typ); d != nil {
		v.checkValue(expr, d)
	}
}

func (v *visitor) checkValue(expr ast.Expr, d *domain) {
	expr = ast.Unparen(expr)
	// Conversion calls have their own diagnostic and reviewed parser exception.
	if call, ok := expr.(*ast.CallExpr); ok && v.pkg.TypesInfo.Types[call.Fun].IsType() {
		return
	}
	var obj types.Object
	switch e := expr.(type) {
	case *ast.Ident:
		obj = v.pkg.TypesInfo.ObjectOf(e)
	case *ast.SelectorExpr:
		obj = v.pkg.TypesInfo.ObjectOf(e.Sel)
	}
	if obj != nil && v.domain(obj.Type()) == d {
		return
	}
	// Contextual typing gives a raw literal the expected enum type. Constant
	// expressions must therefore be checked by their referenced declaration,
	// rather than trusting TypeOf alone.
	if tv := v.pkg.TypesInfo.Types[expr]; tv.Value == nil && v.domain(tv.Type) == d {
		return
	}
	v.report(v.pkg, expr, "use a named %s member or typed value", d.name)
}

func (v *visitor) call(n *ast.CallExpr) {
	if v.pkg.TypesInfo.Types[n.Fun].IsType() {
		if d := v.domain(v.pkg.TypesInfo.TypeOf(n)); d != nil && len(n.Args) == 1 && !v.parser {
			arg := n.Args[0]
			// Untyped literals may receive the conversion target's contextual type.
			// Only an expression with a declared enum type may bypass the boundary.
			if !v.declaredDomain(arg, d) {
				v.report(v.pkg, n, "conversion to %s requires a configured parser function", d.name)
			}
		}
		return
	}
	sig, ok := v.pkg.TypesInfo.TypeOf(n.Fun).Underlying().(*types.Signature)
	if !ok {
		return
	}
	if len(n.Args) == 1 {
		if _, tuple := v.pkg.TypesInfo.TypeOf(n.Args[0]).(*types.Tuple); tuple {
			return // Each tuple result already has its function's declared type.
		}
	}
	for i, arg := range n.Args {
		index := i
		if sig.Variadic() && index >= sig.Params().Len()-1 {
			index = sig.Params().Len() - 1
		}
		if index >= sig.Params().Len() {
			break // A single tuple-valued argument cannot contain a raw literal.
		}
		typ := sig.Params().At(index).Type()
		if sig.Variadic() && index == sig.Params().Len()-1 && !n.Ellipsis.IsValid() {
			if slice, ok := typ.(*types.Slice); ok {
				typ = slice.Elem()
			}
		}
		v.checkType(arg, typ)
	}
}

func (v *visitor) declaredDomain(expr ast.Expr, d *domain) bool {
	expr = ast.Unparen(expr)
	switch e := expr.(type) {
	case *ast.Ident:
		obj := v.pkg.TypesInfo.ObjectOf(e)
		return obj != nil && v.domain(obj.Type()) == d
	case *ast.SelectorExpr:
		obj := v.pkg.TypesInfo.ObjectOf(e.Sel)
		return obj != nil && v.domain(obj.Type()) == d
	}
	tv := v.pkg.TypesInfo.Types[expr]
	return tv.Value == nil && v.domain(tv.Type) == d
}

// Follow explicit conversions only in comparisons and switch tags/cases. Enum
// serialization is legal elsewhere, but casting to a primitive must not erase
// the domain immediately before a branch checks its value.
func (v *visitor) uncast(expr ast.Expr) ast.Expr {
	for {
		expr = ast.Unparen(expr)
		call, ok := expr.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !v.pkg.TypesInfo.Types[call.Fun].IsType() {
			return expr
		}
		expr = call.Args[0]
	}
}

func (v *visitor) expressionDomain(expr ast.Expr) *domain {
	if d := v.domain(v.pkg.TypesInfo.TypeOf(expr)); d != nil {
		return d
	}
	return v.domain(v.pkg.TypesInfo.TypeOf(v.uncast(expr)))
}

func (v *visitor) composite(n *ast.CompositeLit) {
	typ := v.pkg.TypesInfo.TypeOf(n).Underlying()
	for i, elt := range n.Elts {
		expr, key := elt, ast.Expr(nil)
		if pair, ok := elt.(*ast.KeyValueExpr); ok {
			key, expr = pair.Key, pair.Value
		}
		switch t := typ.(type) {
		case *types.Struct:
			index := i
			if ident, ok := key.(*ast.Ident); ok {
				for j := range t.NumFields() {
					if t.Field(j).Name() == ident.Name {
						index = j
						break
					}
				}
			}
			v.checkType(expr, t.Field(index).Type())
		case *types.Map:
			v.checkType(key, t.Key())
			v.checkType(expr, t.Elem())
		case *types.Slice:
			v.checkType(expr, t.Elem())
		case *types.Array:
			v.checkType(expr, t.Elem())
		}
	}
}

func (v *visitor) switchStmt(n *ast.SwitchStmt) {
	if n.Tag == nil {
		return
	}
	d := v.expressionDomain(n.Tag)
	if d == nil {
		return
	}
	seen := map[string]bool{}
	for _, item := range n.Body.List {
		clause, ok := item.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expr := range clause.List {
			expr = v.uncast(expr)
			v.checkValue(expr, d)
			if value := v.pkg.TypesInfo.Types[expr].Value; value != nil {
				seen[valueKey(value)] = true
			}
		}
	}
	var missing []string
	for value, name := range d.values {
		if !seen[value] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		v.report(v.pkg, n, "switch over %s is missing: %s", d.name, strings.Join(missing, ", "))
	}
}
