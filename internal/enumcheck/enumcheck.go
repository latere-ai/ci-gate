// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package enumcheck enforces named members and exhaustive switches for explicitly
// configured Go enum domains. It does not infer enums from primitive fields.
package enumcheck

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Policy identifies enum types, fields that must retain those types, and parser
// functions permitted to convert external primitives. Parser values explain why
// the conversion boundary is needed; validation inside it remains the caller's job.
type Policy struct {
	Types   []string
	Fields  map[string]string
	Parsers map[string]string
}

type domain struct {
	typ     types.Type
	name    string
	values  map[string]string
	members map[types.Object]bool
}

type checker struct {
	root     string
	out      io.Writer
	domains  map[types.Type]*domain
	parsers  map[types.Object]bool
	failures int
}

// Run checks the module's production packages under the current build tags.
// It resolves module-relative or complete import paths and never updates module
// files or resolves dependencies through a surrounding Go workspace.
func Run(policy Policy, root string, out io.Writer) error {
	if len(policy.Types) == 0 {
		return fmt.Errorf("enum-go: configure at least one enum type")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("enum-go: resolve repository root: %w", err)
	}
	env := append(os.Environ(), "GOWORK=off", "GOFLAGS="+os.Getenv("GOFLAGS")+" -mod=readonly")
	pkgs, err := packages.Load(&packages.Config{
		Dir: root, Env: env, Mode: packages.LoadSyntax | packages.NeedModule,
	}, "./...")
	if err != nil {
		return fmt.Errorf("enum-go: load packages: %w", err)
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("enum-go: no production packages found")
	}
	var loadErrors []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrors = append(loadErrors, e.Error())
		}
	})
	if len(loadErrors) != 0 {
		sort.Strings(loadErrors)
		return fmt.Errorf("enum-go: load packages: %s", strings.Join(loadErrors, "\n"))
	}
	c := &checker{root: root, out: out, domains: map[types.Type]*domain{}, parsers: map[types.Object]bool{}}
	if err := c.configure(policy, pkgs); err != nil {
		return err
	}
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			ast.Walk(&visitor{checker: c, pkg: p}, f)
		}
	}
	if c.failures != 0 {
		return fmt.Errorf("enum-go: %d violation(s)", c.failures)
	}
	_, _ = fmt.Fprintf(out, "enum-go: checked %d domain(s) in %d package(s)\n", len(c.domains), len(pkgs))
	return nil
}

func (c *checker) configure(policy Policy, pkgs []*packages.Package) error {
	imports := map[string]*types.Package{}
	var collect func(*types.Package)
	collect = func(p *types.Package) {
		if imports[p.Path()] != nil {
			return
		}
		imports[p.Path()] = p
		for _, dep := range p.Imports() {
			collect(dep)
		}
	}
	module := ""
	for _, p := range pkgs {
		collect(p.Types)
		if p.Module != nil && p.Module.Main {
			module = p.Module.Path
		}
	}
	resolve := func(selector string) types.Object {
		i := strings.LastIndex(selector, ".")
		if i < 0 {
			return nil
		}
		path, name := strings.TrimPrefix(selector[:i], "./"), selector[i+1:]
		p := imports[path]
		if p == nil {
			p = imports[strings.TrimSuffix(module+"/"+path, "/")]
		}
		if p == nil {
			return nil
		}
		return p.Scope().Lookup(name)
	}
	configured := map[string]*domain{}
	for _, selector := range policy.Types {
		obj, ok := resolve(selector).(*types.TypeName)
		if !ok {
			return fmt.Errorf("enum-go: unknown type %q", selector)
		}
		typ := types.Unalias(obj.Type())
		named, ok := typ.(*types.Named)
		if !ok {
			return fmt.Errorf("enum-go: %s must be a defined string or integer type", selector)
		}
		basic, ok := named.Underlying().(*types.Basic)
		if !ok || basic.Info()&(types.IsString|types.IsInteger) == 0 {
			return fmt.Errorf("enum-go: %s must be a defined string or integer type", selector)
		}
		d := &domain{typ: typ, name: selector, values: map[string]string{}, members: map[types.Object]bool{}}
		scope := named.Obj().Pkg().Scope()
		for _, name := range scope.Names() {
			member, ok := scope.Lookup(name).(*types.Const)
			if ok && types.Identical(types.Unalias(member.Type()), typ) {
				d.members[member] = true
				key := valueKey(member.Val())
				if _, exists := d.values[key]; !exists {
					d.values[key] = name
				}
			}
		}
		if len(d.values) == 0 {
			return fmt.Errorf("enum-go: %s has no named enum constants", selector)
		}
		c.domains[typ], configured[selector] = d, d
	}
	for _, selector := range sortedKeys(policy.Fields) {
		i := strings.LastIndex(selector, ".")
		if i < 0 {
			return fmt.Errorf("enum-go: unknown field %q", selector)
		}
		obj, ok := resolve(selector[:i]).(*types.TypeName)
		if !ok {
			return fmt.Errorf("enum-go: unknown field %q", selector)
		}
		field, _, _ := types.LookupFieldOrMethod(obj.Type(), true, obj.Pkg(), selector[i+1:])
		v, ok := field.(*types.Var)
		if !ok || !v.IsField() {
			return fmt.Errorf("enum-go: unknown field %q", selector)
		}
		d := configured[policy.Fields[selector]]
		if d == nil {
			return fmt.Errorf("enum-go: field %s references unconfigured enum %q", selector, policy.Fields[selector])
		}
		actual := types.Unalias(v.Type())
		if pointer, ok := actual.(*types.Pointer); ok {
			actual = types.Unalias(pointer.Elem())
		}
		if !types.Identical(actual, d.typ) {
			return fmt.Errorf("enum-go: field %s must use %s (or *%s), got %s", selector, d.name, d.name, v.Type())
		}
	}
	for _, selector := range sortedKeys(policy.Parsers) {
		obj, ok := resolve(selector).(*types.Func)
		if !ok {
			return fmt.Errorf("enum-go: unknown parser function %q", selector)
		}
		if strings.TrimSpace(policy.Parsers[selector]) == "" {
			return fmt.Errorf("enum-go: parser %s requires a reason", selector)
		}
		c.parsers[obj] = true
	}
	return nil
}

func valueKey(value constant.Value) string { return value.Kind().String() + ":" + value.ExactString() }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *checker) domain(typ types.Type) *domain {
	if typ == nil {
		return nil
	}
	return c.domains[types.Unalias(typ)]
}

func (c *checker) report(pkg *packages.Package, node ast.Node, format string, args ...any) {
	pos := pkg.Fset.Position(node.Pos())
	path, err := filepath.Rel(c.root, pos.Filename)
	if err != nil {
		path = pos.Filename
	}
	_, _ = fmt.Fprintf(c.out, "%s:%d:%d: %s\n", path, pos.Line, pos.Column, fmt.Sprintf(format, args...))
	c.failures++
}
