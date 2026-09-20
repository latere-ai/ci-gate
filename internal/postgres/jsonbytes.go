// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package postgres

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// The bug this file finds.
//
// The family's serving DSN carries default_query_exec_mode=exec, because a
// transaction pooler reassigns a backend between transactions and pgx must
// stop preparing statements server-side to survive that. Exec mode also
// decides how a parameter is encoded: with no prepared statement there is no
// parameter OID from the server, so pgx picks the wire type from the Go type
// and sends every parameter in the text format.
//
// A Go []byte is a bytea. In the text format a bytea is written as the hex
// literal \x7b2261..., and a hex literal is not json, so the server refuses a
// parameter that a json or jsonb column, or an explicit ::jsonb, has to read:
//
//	ERROR: invalid input syntax for type json (SQLSTATE 22P02)
//
// Under the binary format the same bytes arrive as the json text they are and
// the statement works, which is why the direct endpoint accepts exactly what
// the pool refuses, and why no test on a direct connection can see it. auth
// v0.37.0 deployed on 2026-09-19 and crash-looped at start-up on two such
// parameters; a hand audit of agents found nine more.
//
// The repair is to bind a string, or a *string where a nil must stay SQL
// NULL rather than become an empty document.
//
// What is not the bug: a []byte bound to a bytea column. That is the correct
// type for a bytea and the family has several, encrypted blobs among them. A
// check that flagged those would be waived within a week, and a waived check
// protects nothing, so the rule below asks what the value is and what the
// statement does with it rather than what its Go type is.

// jsonMarshallers are the functions whose result is a json encoding. The name
// alone is not enough: a Marshal in a package that encodes something else
// returns something else, so each is named by its package.
var jsonMarshallers = map[string][]string{
	"encoding/json":                                 {"Marshal", "MarshalIndent"},
	"encoding/json/v2":                              {"Marshal"},
	"github.com/goccy/go-json":                      {"Marshal", "MarshalIndent"},
	"github.com/json-iterator/go":                   {"Marshal", "MarshalIndent"},
	"github.com/bytedance/sonic":                    {"Marshal", "MarshalIndent"},
	"google.golang.org/protobuf/encoding/protojson": {"Marshal"},
}

// jsonPackages are the packages whose named byte-slice types hold a json
// document, json.RawMessage above all. A value of one of them is json
// wherever it came from.
//
// jsontext is in the list because json.RawMessage is an alias for
// jsontext.Value from Go 1.26 on, so the name a repository writes and the
// type it resolves to live in different packages and both have to be
// recognised.
var jsonPackages = []string{
	"encoding/json",
	"encoding/json/v2",
	"encoding/json/jsontext",
	"github.com/goccy/go-json",
	"github.com/json-iterator/go",
}

// queryPrefixes are the names a call has to start with to be a statement.
// The name alone decides nothing; it narrows the set the signature test
// below is applied to, and it is a prefix so the family's wrappers are in it:
// Exec, ExecContext, ExecOne, Query, QueryRow, QueryRowContext, QueryOne.
var queryPrefixes = []string{"Exec", "Query"}

// queueName is pgx's batch member, which takes the same statement and the
// same parameters under a name no prefix covers.
const queueName = "Queue"

// paramCast finds an explicit cast of a numbered parameter, `$6::jsonb`, in
// either spelling Postgres accepts.
var paramCast = regexp.MustCompile(`(?is)\$(\d+)\s*::\s*([a-z_][a-z0-9_]*)|cast\s*\(\s*\$(\d+)\s+as\s+([a-z_][a-z0-9_]*)\s*\)`)

// jsonBind is one argument that carries bytes into a parameter the statement
// reads as json.
type jsonBind struct {
	rel   string
	line  int
	param string
	why   string
}

// analysis is what the type-aware pass read, so the check can say what it
// looked at as well as what it found.
type analysis struct {
	binds    []jsonBind
	packages int
}

// analyze type-checks the module at root and returns every byte slice that
// reaches a statement's json parameter.
//
// Nothing here guesses at a package it could not read: a module that does not
// type-check is reported as the error it is, because a package this pass
// skipped is a package the bug can sit in unseen.
func analyze(root string) (analysis, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return analysis{}, fmt.Errorf("resolving the repository root: %w", err)
	}
	env := append(os.Environ(), "GOWORK=off", "GOFLAGS="+os.Getenv("GOFLAGS")+" -mod=readonly")
	pkgs, err := packages.Load(&packages.Config{
		Dir:  abs,
		Env:  env,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
	}, "./...")
	if err != nil {
		return analysis{}, fmt.Errorf("type-checking the tree for the json parameter check: %w", err)
	}
	var loadErrors []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrors = append(loadErrors, e.Error())
		}
	})
	if len(loadErrors) > 0 {
		sort.Strings(loadErrors)
		return analysis{}, fmt.Errorf("the json parameter check reads types, and this tree does not type-check:\n%s\n"+
			"build it first; a package that did not type-check is a package this check did not read",
			strings.Join(loadErrors, "\n"))
	}
	out := analysis{packages: len(pkgs)}
	for _, p := range pkgs {
		a := &analyzer{root: abs, pkg: p, info: p.TypesInfo, summary: map[*types.Func][]bool{}}
		out.binds = append(out.binds, a.run()...)
	}
	sort.SliceStable(out.binds, func(i, j int) bool {
		if out.binds[i].rel != out.binds[j].rel {
			return out.binds[i].rel < out.binds[j].rel
		}
		return out.binds[i].line < out.binds[j].line
	})
	return out, nil
}

// analyzer reads one package.
type analyzer struct {
	root string
	pkg  *packages.Package
	info *types.Info
	// summary records, per function of this package and per result index,
	// whether that result carries a json encoding. A helper that wraps
	// json.Marshal is the shape agents used nine times, and without the
	// summary every one of them is invisible at the call.
	summary map[*types.Func][]bool
}

// run computes the summaries to a fixed point and then reads the statements.
func (a *analyzer) run() []jsonBind {
	var funcs []*ast.FuncDecl
	for _, f := range a.pkg.Syntax {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				funcs = append(funcs, fn)
			}
		}
	}
	// A helper may call a helper, so the summaries settle rather than being
	// read once. The depth of this in the family is one; the bound is three
	// because a fixed point that needs more than that is a shape nobody
	// writes, and an unbounded loop over a cycle is not a thing to ship.
	for pass := 0; pass < 3; pass++ {
		changed := false
		for _, fn := range funcs {
			if a.summarize(fn) {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	var binds []jsonBind
	for _, fn := range funcs {
		binds = append(binds, a.statements(fn, a.taint(fn))...)
	}
	return binds
}

// summarize records which of a function's results carry a json encoding, and
// reports whether that changed anything.
func (a *analyzer) summarize(fn *ast.FuncDecl) bool {
	obj, ok := a.info.ObjectOf(fn.Name).(*types.Func)
	if !ok {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok || sig.Results().Len() == 0 {
		return false
	}
	n := sig.Results().Len()
	got := make([]bool, n)
	tainted := a.taint(fn)
	// A named result is a local the body assigns to, so the taint set
	// already holds it; auth's columns() is written that way.
	if names := sig.Results(); names != nil {
		for i := range n {
			if v := names.At(i); v != nil && v.Name() != "" && tainted[v] {
				got[i] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		ret, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		switch {
		case len(ret.Results) == n:
			for i, e := range ret.Results {
				if a.jsonBytes(e, tainted) {
					got[i] = true
				}
			}
		case len(ret.Results) == 1 && n > 1:
			// `return json.Marshal(v)` forwards a whole tuple.
			if call, ok := ast.Unparen(ret.Results[0]).(*ast.CallExpr); ok {
				for i := range n {
					if a.callResult(call, i, tainted) {
						got[i] = true
					}
				}
			}
		}
		return true
	})
	old := a.summary[obj]
	if len(old) == n {
		same := true
		for i := range got {
			got[i] = got[i] || old[i]
			if got[i] != old[i] {
				same = false
			}
		}
		if same {
			return false
		}
	}
	a.summary[obj] = got
	return true
}

// taint is the set of locals in one function that hold a json encoding as
// bytes.
//
// It grows and never shrinks: a variable assigned a json encoding on one
// branch carries it to the statement whatever the other branch wrote, which
// is agents' `var payload any` exactly. Several passes, because a local can
// be written after the line that reads it.
func (a *analyzer) taint(fn *ast.FuncDecl) map[types.Object]bool {
	set := map[types.Object]bool{}
	mark := func(e ast.Expr, changed *bool) {
		id, ok := ast.Unparen(e).(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		if obj := a.info.ObjectOf(id); obj != nil && !set[obj] {
			set[obj] = true
			*changed = true
		}
	}
	for pass := 0; pass < 3; pass++ {
		changed := false
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			var lhs, rhs []ast.Expr
			switch s := node.(type) {
			case *ast.AssignStmt:
				lhs, rhs = s.Lhs, s.Rhs
			case *ast.ValueSpec:
				for _, name := range s.Names {
					lhs = append(lhs, name)
				}
				rhs = s.Values
			default:
				return true
			}
			if len(rhs) == 1 && len(lhs) > 1 {
				call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
				if !ok {
					return true
				}
				for i, target := range lhs {
					if a.callResult(call, i, set) {
						mark(target, &changed)
					}
				}
				return true
			}
			if len(lhs) != len(rhs) {
				return true
			}
			for i, target := range lhs {
				if a.jsonBytes(rhs[i], set) {
					mark(target, &changed)
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return set
}

// jsonBytes reports whether an expression is a json encoding held as bytes.
//
// Bytes are part of the question, not an afterthought: string(receipt) is the
// repair, so a conversion that leaves the byte slice leaves the bug behind
// with it.
func (a *analyzer) jsonBytes(e ast.Expr, tainted map[types.Object]bool) bool {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		if obj := a.info.ObjectOf(x); obj != nil && tainted[obj] {
			return true
		}
	case *ast.SelectorExpr:
		if obj := a.info.ObjectOf(x.Sel); obj != nil && tainted[obj] {
			return true
		}
	case *ast.CallExpr:
		if tv, ok := a.info.Types[x.Fun]; ok && tv.IsType() {
			return isByteSlice(a.info.TypeOf(x)) && len(x.Args) == 1 && a.jsonBytes(x.Args[0], tainted)
		}
		return a.callResult(x, 0, tainted)
	}
	return isJSONType(a.info.TypeOf(e))
}

// callResult reports whether result i of a call is a json encoding held as
// bytes: a marshaller, a MarshalJSON method, a conversion, or a function of
// this package the summaries have already decided.
func (a *analyzer) callResult(call *ast.CallExpr, i int, tainted map[types.Object]bool) bool {
	if tv, ok := a.info.Types[call.Fun]; ok && tv.IsType() {
		return i == 0 && isByteSlice(a.info.TypeOf(call)) && len(call.Args) == 1 && a.jsonBytes(call.Args[0], tainted)
	}
	fn, _ := a.calleeObject(call).(*types.Func)
	if fn == nil {
		return false
	}
	if i == 0 && isMarshaller(fn) {
		return true
	}
	if got, ok := a.summary[fn]; ok && i < len(got) {
		return got[i]
	}
	return false
}

// calleeObject is the function or method a call names.
func (a *analyzer) calleeObject(call *ast.CallExpr) types.Object {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		return a.info.ObjectOf(fun)
	case *ast.SelectorExpr:
		if sel, ok := a.info.Selections[fun]; ok {
			return sel.Obj()
		}
		return a.info.ObjectOf(fun.Sel)
	}
	return nil
}

// statements reads every statement in one function and reports the binds.
func (a *analyzer) statements(fn *ast.FuncDecl, tainted map[types.Object]bool) []jsonBind {
	var binds []jsonBind
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		first, ok := a.queryArgs(call)
		if !ok {
			return true
		}
		// `args...` hands the statement a slice this pass cannot see into,
		// so there is nothing to number and nothing to decide.
		if call.Ellipsis.IsValid() {
			return true
		}
		casts := parameterCasts(a.statementText(call, first))
		for i := first; i < len(call.Args); i++ {
			arg := call.Args[i]
			number := i - first + 1
			cast, hasCast := casts[number]
			// A parameter the statement casts to something that is not json
			// is not this bug, whatever the value is: `$3::bytea` says the
			// column takes bytes and bytes are what it should get.
			if hasCast && !isJSONCast(cast) {
				continue
			}
			why := ""
			switch {
			case a.jsonBytes(arg, tainted):
				why = "the value is a json encoding"
			case hasCast && isByteSlice(a.info.TypeOf(arg)):
				why = "the statement casts that parameter to " + cast
			default:
				continue
			}
			pos := a.pkg.Fset.Position(arg.Pos())
			binds = append(binds, jsonBind{
				rel:   a.rel(pos.Filename),
				line:  pos.Line,
				param: "$" + strconv.Itoa(number),
				why:   why,
			})
		}
		return true
	})
	return binds
}

// queryArgs reports the index the parameters start at, for a call that is a
// statement.
//
// The name narrows and the signature decides: a statement takes its SQL as a
// string and its parameters as the variadic empty interface right after it,
// which is the shape of every pgx entry point and of the family's wrappers
// over them, and is not the shape of anything else called Query or Exec.
// Nothing here matches an import path, so a repository's own Querier
// interface and its test double are read as what they are.
func (a *analyzer) queryArgs(call *ast.CallExpr) (int, bool) {
	name := ""
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	default:
		return 0, false
	}
	named := name == queueName
	for _, p := range queryPrefixes {
		named = named || strings.HasPrefix(name, p)
	}
	if !named {
		return 0, false
	}
	sig, ok := a.info.TypeOf(call.Fun).(*types.Signature)
	if !ok || !sig.Variadic() {
		return 0, false
	}
	params := sig.Params()
	last := params.Len() - 1
	if last < 1 {
		return 0, false
	}
	slice, ok := params.At(last).Type().(*types.Slice)
	if !ok {
		return 0, false
	}
	iface, ok := slice.Elem().Underlying().(*types.Interface)
	if !ok || iface.NumMethods() != 0 {
		return 0, false
	}
	if basic, ok := params.At(last - 1).Type().Underlying().(*types.Basic); !ok || basic.Kind() != types.String {
		return 0, false
	}
	return last, true
}

// statementText is the SQL of a call, when the statement is constant. A
// statement assembled at run time has no text to read, and the check then
// decides on the value alone.
func (a *analyzer) statementText(call *ast.CallExpr, first int) string {
	if first < 1 || first-1 >= len(call.Args) {
		return ""
	}
	tv, ok := a.info.Types[call.Args[first-1]]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return ""
	}
	return constant.StringVal(tv.Value)
}

// rel is a file's path inside the repository.
func (a *analyzer) rel(name string) string {
	if r, err := filepath.Rel(a.root, name); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(name)
}

// parameterCasts maps a parameter number to the type the statement casts it
// to. The first cast of a parameter wins, and a parameter cast nowhere is
// absent from the map rather than present as an empty string.
func parameterCasts(sql string) map[int]string {
	out := map[int]string{}
	for _, m := range paramCast.FindAllStringSubmatch(sql, -1) {
		digits, name := m[1], m[2]
		if digits == "" {
			digits, name = m[3], m[4]
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			continue
		}
		if _, seen := out[n]; !seen {
			out[n] = strings.ToLower(name)
		}
	}
	return out
}

// isJSONCast reports whether a cast names a json type.
func isJSONCast(name string) bool { return name == "json" || name == "jsonb" }

// isByteSlice reports whether a type is a slice of bytes, under whatever name
// it carries. json.RawMessage is one.
func isByteSlice(t types.Type) bool {
	if t == nil {
		return false
	}
	slice, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	basic, ok := slice.Elem().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Byte
}

// isJSONType reports whether a type is a named byte slice from a json
// package: json.RawMessage, whose every value is a json document.
//
// Both the name written and the type it resolves to are asked, because
// json.RawMessage is an alias and the two sit in different packages.
func isJSONType(t types.Type) bool {
	if t == nil || !isByteSlice(t) {
		return false
	}
	if alias, ok := t.(*types.Alias); ok && fromJSONPackage(alias.Obj()) {
		return true
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && fromJSONPackage(named.Obj())
}

// fromJSONPackage reports whether a type name was declared in a json package.
func fromJSONPackage(obj *types.TypeName) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return slices.Contains(jsonPackages, obj.Pkg().Path())
}

// isMarshaller reports whether a function returns a json encoding: one of the
// named package functions, or any MarshalJSON, which is the one method name
// the encoder itself defines and calls.
func isMarshaller(fn *types.Func) bool {
	if fn.Name() == "MarshalJSON" {
		return true
	}
	if fn.Pkg() == nil {
		return false
	}
	for _, name := range jsonMarshallers[fn.Pkg().Path()] {
		if fn.Name() == name {
			return true
		}
	}
	return false
}

// hasQueryCall reports whether a parsed file calls something shaped like a
// statement. It reads names alone, which is what decides whether the
// type-aware pass runs at all: a repository with no such call has nothing for
// it to read, and type-checking a tree to learn that is a cost with no answer
// on the other side of it.
func hasQueryCall(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch fun := ast.Unparen(call.Fun).(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		default:
			return true
		}
		if len(call.Args) < 2 {
			return true
		}
		if name == queueName {
			found = true
			return false
		}
		for _, p := range queryPrefixes {
			if strings.HasPrefix(name, p) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// bindSentence is what a finding says. It names the parameter, why the
// parameter is json, the mechanism, and both repairs, because a reader who
// has just been told a byte slice is wrong here has to be told immediately
// that it is right one column over.
const bindSentence = "this binds a byte slice into parameter %s and %s; " +
	"the pooled endpoint runs in exec mode, where a parameter travels in the text format, " +
	"a byte slice is sent as bytea and reaches the server as a hex literal, and json refuses it " +
	"with SQLSTATE 22P02, while the direct endpoint accepts the same call; " +
	"bind a string, or a pointer to string where a nil has to stay SQL NULL, " +
	"and keep the byte slice for a bytea column"

// ruleJSONBytes: no statement carries bytes into a parameter read as json.
//
// It runs under every role, the absent one included. The failure is latent
// rather than conditional: a repository on the direct endpoint holds it
// silently and starts crash-looping on the day it is pooled, which is the day
// nobody is looking for an encoding bug.
func ruleJSONBytes(t *tree) result {
	if len(t.files) == 0 {
		return result{skip: "no non-test Go file to read"}
	}
	if !t.queries {
		return result{note: fmt.Sprintf("no statement across %d Go file(s), so nothing binds a parameter", len(t.files))}
	}
	if len(t.analysis.binds) == 0 {
		return result{note: fmt.Sprintf("no byte slice reaches a json parameter in %d package(s)", t.analysis.packages)}
	}
	found := make([]Finding, 0, len(t.analysis.binds))
	for _, b := range t.analysis.binds {
		found = append(found, at(b.rel, b.line, bindSentence, b.param, b.why))
	}
	return result{findings: found}
}
