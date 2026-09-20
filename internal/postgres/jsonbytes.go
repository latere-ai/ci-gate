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
// decides how a parameter is encoded: the driver sends no parameter OID and
// the server describes nothing, so pgx picks the wire encoding from the Go
// type alone and writes every parameter in the text format.
//
// Two failures follow from that, and they are different failures.
//
// The first is a value pgx encodes as something the column refuses. A Go
// []byte is a bytea, and a bytea in the text format is the hex literal
// \x7b2261..., which a json or jsonb column will not read:
//
//	ERROR: invalid input syntax for type json (SQLSTATE 22P02)
//
// The second is a value pgx cannot encode at all. Its plan lookup takes the
// Go type and an undescribed parameter, and a Go struct or map is in neither
// its registered set nor any of its wrappers, so the call fails in the driver
// before the statement is sent:
//
//	unable to encode ...: cannot find encode plan
//
// That second one is not about the column. The plan lookup never sees the
// column, so a struct bound to a json column, a text column or a cast fails
// the same way. It is also the harder one to see by reading: nothing in the
// statement says json, and nothing in the value says json either.
//
// An endpoint that describes the statement first accepts both, which is why
// the direct endpoint and a cache_describe DSN hide them and the pooled
// endpoint in exec mode does not. auth v0.37.0 deployed on 2026-09-19 and
// crash-looped at start-up on two parameters of the first kind.
//
// Measured against Postgres 17 in exec mode, binding each Go type into a json
// and a jsonb parameter:
//
//	[]byte                       22P02
//	a named byte slice, not json 22P02
//	json.RawMessage              accepted
//	*json.RawMessage             accepted
//	string, *string              accepted
//	a named string type          accepted
//	a driver.Valuer, a Stringer  accepted
//	a pgtype text value          accepted
//	untyped nil                  accepted
//	a number                     accepted
//	a bool                       22P02
//	[]string                     22P02
//	time.Time                    22P02
//	a struct, *struct, a map     cannot find encode plan
//	[]struct, []namedString      cannot find encode plan
//
// The repair for a json parameter is to bind a string, or a *string where a
// nil has to stay SQL NULL rather than become an empty document.
//
// What is not the bug: a []byte bound to a bytea column. That is the correct
// type for a bytea and the family has several, encrypted blobs among them. A
// check that flagged those would be waived within a week, and a waived check
// protects nothing, so the carrier rule below fires only where something says
// the parameter is json.

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
// wherever it came from, and pgx holds a codec for it, so it is both the
// evidence that a parameter is json and an accepted way to carry one.
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

// registeredPackages are the packages whose named types pgx's default type
// map holds, so a value of one of them has a wire encoding without the server
// describing anything: the clock and duration types, the two address
// families, and pgx's own column types.
//
// Reading the package rather than each type name is deliberate. pgx registers
// about forty of its own, the list moves between releases, and a name this
// file failed to copy across would be reported as unencodable although it
// encodes fine.
var registeredPackages = []string{
	"time",
	"net",
	"net/netip",
	"github.com/jackc/pgx/v5/pgtype",
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

// jsonBind is one argument a statement cannot carry into its parameter.
type jsonBind struct {
	rel  string
	line int
	// param is the parameter, `$5`, and what describes the value in the
	// register of somebody reading the report rather than the type checker.
	param string
	what  string
	// why says what makes the parameter json, and is empty on the finding
	// that does not depend on the parameter being json.
	why string
}

// analysis is what the type-aware pass read, so the check can say what it
// looked at as well as what it found.
type analysis struct {
	binds    []jsonBind
	packages int
}

// analyze type-checks the module at root and returns every parameter a
// statement cannot carry its argument into.
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
	// One summary map across the module, because a helper that launders a
	// value through the empty interface is as often one package over as it is
	// next to the statement: llm-gateway's is in its own pgx utility package
	// and is bound six times from six others.
	summary := map[string][]bound{}
	readers := make([]*analyzer, 0, len(pkgs))
	for _, p := range pkgs {
		a := &analyzer{root: abs, pkg: p, info: p.TypesInfo, summary: summary}
		a.collect()
		readers = append(readers, a)
	}
	// A helper may call a helper, so the summaries settle rather than being
	// read once. The depth of this in the family is one; the bound is three
	// because a fixed point that needs more than that is a shape nobody
	// writes, and an unbounded loop over a cycle is not a thing to ship.
	for range 3 {
		changed := false
		for _, a := range readers {
			if a.summarize() {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	out := analysis{packages: len(pkgs)}
	for _, a := range readers {
		out.binds = append(out.binds, a.statements()...)
	}
	sort.SliceStable(out.binds, func(i, j int) bool {
		if out.binds[i].rel != out.binds[j].rel {
			return out.binds[i].rel < out.binds[j].rel
		}
		return out.binds[i].line < out.binds[j].line
	})
	return out, nil
}

// bound is what an expression can hand to a parameter.
//
// It is a set rather than one type because a variable of interface type is
// the shape both blind spots hide behind: agents declared `var payload any`
// and assigned an encoding on one branch, and llm-gateway returned `any` from
// a helper with a raw message on one path and a nil on the other. Every
// concrete value that can reach the statement has to be judged, and a value
// whose type is out of reach has to be judged as nothing at all, because
// reporting every empty interface would report most of the family.
type bound struct {
	// json records that the value is a json document, whatever Go type
	// carries it. It is the evidence that a parameter is json where the
	// statement does not say so.
	json bool
	// kinds are the concrete types the value can have.
	kinds []types.Type
	// opaque records that some value reaching here has a type this pass
	// could not name.
	opaque bool
}

// merge unions two bounds. Taint grows and never shrinks: a value that
// reaches the statement on any branch reaches it.
func (b bound) merge(o bound) bound {
	b.json = b.json || o.json
	b.opaque = b.opaque || o.opaque
	for _, t := range o.kinds {
		if !slices.ContainsFunc(b.kinds, func(seen types.Type) bool { return types.Identical(seen, t) }) {
			b.kinds = append(b.kinds, t)
		}
	}
	return b
}

// known reports whether the bound names at least one concrete type.
func (b bound) known() bool { return len(b.kinds) > 0 }

// analyzer reads one package against a summary shared with the rest.
type analyzer struct {
	root  string
	pkg   *packages.Package
	info  *types.Info
	funcs []*ast.FuncDecl
	// summary records, per function of this module and per result index,
	// what that result can carry. A helper that wraps the encoder is the
	// shape agents used nine times, and one that hands the value back as an
	// empty interface is the shape llm-gateway used six; without the summary
	// every one of them is invisible at the call.
	summary map[string][]bound
}

// collect lists the functions of this package with a body.
func (a *analyzer) collect() {
	for _, f := range a.pkg.Syntax {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				a.funcs = append(a.funcs, fn)
			}
		}
	}
}

// summarize records what each function's results can carry, and reports
// whether that changed anything.
func (a *analyzer) summarize() bool {
	changed := false
	for _, fn := range a.funcs {
		if a.summarizeFunc(fn) {
			changed = true
		}
	}
	return changed
}

// summarizeFunc records one function's results.
//
// A result of concrete type carries that type and nothing else, so only the
// json flag has to be traced through it. A result declared as an interface
// carries whatever the return statements put in it, which is the whole point
// of the summary.
func (a *analyzer) summarizeFunc(fn *ast.FuncDecl) bool {
	obj, ok := a.info.ObjectOf(fn.Name).(*types.Func)
	if !ok {
		return false
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok || sig.Results().Len() == 0 {
		return false
	}
	n := sig.Results().Len()
	got := make([]bound, n)
	facts := a.facts(fn)
	// A named result is a local the body assigns to, so the facts already
	// hold it; auth's columns() is written that way.
	for i := range n {
		if v := sig.Results().At(i); v != nil && v.Name() != "" {
			got[i] = got[i].merge(facts[v])
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
				got[i] = got[i].merge(a.valueOf(e, facts))
			}
		case len(ret.Results) == 1 && n > 1:
			// `return json.Marshal(v)` forwards a whole tuple.
			if call, ok := ast.Unparen(ret.Results[0]).(*ast.CallExpr); ok {
				for i := range n {
					got[i] = got[i].merge(a.callResult(call, i, facts))
				}
			}
		}
		return true
	})
	// A concrete result type is what it says it is; the return expressions
	// decide only whether a json document travels in it.
	for i := range n {
		declared := sig.Results().At(i).Type()
		if !unresolved(declared) {
			got[i].kinds = []types.Type{declared}
			got[i].opaque = false
		}
	}
	key := summaryKey(obj)
	old := a.summary[key]
	if len(old) == n {
		same := true
		for i := range got {
			got[i] = got[i].merge(old[i])
			if got[i].json != old[i].json || got[i].opaque != old[i].opaque || len(got[i].kinds) != len(old[i].kinds) {
				same = false
			}
		}
		if same {
			return false
		}
	}
	a.summary[key] = got
	return true
}

// summaryKey names a function across the module, by package path and, for a
// method, by receiver. A pointer would do inside one package; the summaries
// cross packages, so the name is what they are keyed on.
func summaryKey(fn *types.Func) string { return fn.FullName() }

// facts is what each local of one function can hold.
//
// Several passes, because a local can be written after the line that reads
// it, and the merge only ever adds.
func (a *analyzer) facts(fn *ast.FuncDecl) map[types.Object]bound {
	set := map[types.Object]bound{}
	mark := func(e ast.Expr, b bound, changed *bool) {
		id, ok := ast.Unparen(e).(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		obj := a.info.ObjectOf(id)
		if obj == nil {
			return
		}
		before := set[obj]
		after := before.merge(b)
		if after.json != before.json || after.opaque != before.opaque || len(after.kinds) != len(before.kinds) {
			set[obj] = after
			*changed = true
		}
	}
	for range 3 {
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
					mark(target, a.callResult(call, i, set), &changed)
				}
				return true
			}
			if len(lhs) != len(rhs) {
				return true
			}
			for i, target := range lhs {
				mark(target, a.valueOf(rhs[i], set), &changed)
			}
			return true
		})
		if !changed {
			break
		}
	}
	return set
}

// valueOf is what one expression can hand to a parameter.
func (a *analyzer) valueOf(e ast.Expr, facts map[types.Object]bound) bound {
	var b bound
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		if obj := a.info.ObjectOf(x); obj != nil {
			b = facts[obj]
		}
	case *ast.SelectorExpr:
		if obj := a.info.ObjectOf(x.Sel); obj != nil {
			b = facts[obj]
		}
	case *ast.CallExpr:
		if tv, ok := a.info.Types[x.Fun]; ok && tv.IsType() {
			// A conversion keeps the document and changes the carrier:
			// []byte(receipt) is still json and still bytes, string(receipt)
			// is still json and is the repair.
			if len(x.Args) == 1 {
				b = a.valueOf(x.Args[0], facts)
				b.kinds = nil
				b.opaque = false
			}
			break
		}
		b = a.callResult(x, 0, facts)
	}
	t := a.info.TypeOf(e)
	switch {
	case t == nil:
		b.opaque = true
	case unresolved(t):
		// The declared type says nothing, so what the facts and the
		// summaries found is all there is; nothing found means nothing to
		// judge.
		if !b.known() {
			b.opaque = true
		}
	default:
		// The encoder's own raw message type is a json document wherever it
		// came from, which is the evidence that carries a parameter no
		// statement casts.
		b = b.merge(bound{json: isJSONType(t), kinds: []types.Type{t}})
	}
	return b
}

// callResult is what result i of a call can hand to a parameter: a
// marshaller's encoding, a conversion, or a function of this module the
// summaries have already decided.
func (a *analyzer) callResult(call *ast.CallExpr, i int, facts map[types.Object]bound) bound {
	if tv, ok := a.info.Types[call.Fun]; ok && tv.IsType() {
		if i != 0 || len(call.Args) != 1 {
			return bound{}
		}
		inner := a.valueOf(call.Args[0], facts)
		to := a.info.TypeOf(call)
		return bound{json: inner.json || isJSONType(to), kinds: []types.Type{to}}
	}
	fn, _ := a.calleeObject(call).(*types.Func)
	if fn == nil {
		return bound{}
	}
	if i == 0 && isMarshaller(fn) {
		return bound{json: true}
	}
	if got, ok := a.summary[summaryKey(fn)]; ok && i < len(got) {
		return got[i]
	}
	return bound{}
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

// statements reads every statement in this package and reports the binds.
func (a *analyzer) statements() []jsonBind {
	var binds []jsonBind
	for _, fn := range a.funcs {
		facts := a.facts(fn)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			first, ok := a.queryArgs(call)
			if !ok {
				return true
			}
			// `args...` hands the statement a slice this pass cannot see
			// into, so there is nothing to number and nothing to decide.
			if call.Ellipsis.IsValid() {
				return true
			}
			binds = append(binds, a.parameters(call, first, facts)...)
			return true
		})
	}
	return binds
}

// parameters judges every argument of one statement.
func (a *analyzer) parameters(call *ast.CallExpr, first int, facts map[types.Object]bound) []jsonBind {
	var binds []jsonBind
	casts := parameterCasts(a.statementText(call, first))
	for i := first; i < len(call.Args); i++ {
		arg := call.Args[i]
		number := "$" + strconv.Itoa(i-first+1)
		cast, hasCast := casts[i-first+1]
		value := a.valueOf(arg, facts)
		pos := a.pkg.Fset.Position(arg.Pos())
		bind := jsonBind{rel: a.rel(pos.Filename), line: pos.Line, param: number}
		// The driver holds no encoding for this value at all, which the
		// column has no say in: the plan lookup reads the Go type and an
		// undescribed parameter, and never the column.
		if what, ok := unencodable(value); ok {
			bind.what = what
			binds = append(binds, bind)
			continue
		}
		// A parameter the statement casts to something that is not json is
		// not the other bug, whatever the value is: `$3::bytea` says the
		// column takes bytes and bytes are what it should get.
		if hasCast && !isJSONCast(cast) {
			continue
		}
		var why string
		switch {
		case value.json:
			why = "the value is a json encoding"
		case hasCast:
			why = "the statement casts that parameter to " + cast
		default:
			continue
		}
		what, ok := wrongCarrier(value)
		if !ok {
			continue
		}
		bind.what, bind.why = what, why
		binds = append(binds, bind)
	}
	return binds
}

// wrongCarrier reports whether a value bound to a json parameter is one the
// driver does not deliver as a json document, and says what it is.
//
// The accepted set is named rather than the refused one, so a type nobody
// measured is reported rather than let through. It is what a json parameter
// took in the measurement at the head of this file: a string under any name,
// a pointer to one, the encoder's own raw message type, anything the driver
// asks for a text or a driver value, a number, and an untyped nil. A byte
// slice is not in it, and neither is a bool, a list or a clock reading.
func wrongCarrier(v bound) (string, bool) {
	if v.opaque || !v.known() {
		return "", false
	}
	for _, t := range v.kinds {
		if !carriesJSON(t) {
			return describe(t), true
		}
	}
	return "", false
}

// carriesJSON reports whether a json parameter takes a value of this type.
//
// The order is the driver's own. It reads a text method before anything
// else, then its registered set, and only then the wrappers that find a
// stringer or a database value behind a type it does not know. A clock
// reading is the case that order decides: it has a string method, and it is
// never asked for it, because the driver holds a column type for it and
// writes a timestamp that json refuses.
func carriesJSON(t types.Type) bool {
	t = deref(t)
	if t == nil || unresolved(t) {
		return true
	}
	if basic, ok := t.(*types.Basic); ok && basic.Kind() == types.UntypedNil {
		return true
	}
	if isJSONType(t) || hasTextValue(t) {
		return true
	}
	if registered(t) {
		basic, ok := t.(*types.Basic)
		return ok && basic.Info()&(types.IsString|types.IsInteger|types.IsFloat) != 0
	}
	if speaksText(t) {
		return true
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Info()&(types.IsString|types.IsInteger|types.IsFloat) != 0
}

// unencodable reports whether the driver holds no encoding for this value at
// an undescribed parameter, and says what it is.
//
// This is the failure the column has no part in. Values of the driver's own
// registered set encode, so do the types that hand it a text or a driver
// value, and so does anything whose underlying type is a builtin. A Go struct
// and a Go map are in none of those, and neither is a list of them.
func unencodable(v bound) (string, bool) {
	if !v.known() {
		return "", false
	}
	for _, t := range v.kinds {
		if noEncoding(t) {
			return describe(t), true
		}
	}
	return "", false
}

// noEncoding reports whether the driver's plan lookup comes back empty.
func noEncoding(t types.Type) bool {
	t = deref(t)
	if t == nil || unresolved(t) {
		return false
	}
	if registered(t) || speaksText(t) {
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Struct, *types.Map, *types.Chan, *types.Signature:
		return true
	case *types.Slice:
		return !registered(u.Elem()) && !isByteType(u.Elem())
	case *types.Array:
		return !registered(u.Elem()) && !isByteType(u.Elem())
	}
	return false
}

// registered reports whether the driver's default map holds this exact type:
// a builtin, a byte slice, or a named type from one of the packages it
// registers.
func registered(t types.Type) bool {
	if t == nil {
		return false
	}
	if isJSONType(t) || fromPackages(t, registeredPackages) {
		return true
	}
	switch u := t.(type) {
	case *types.Basic:
		return u.Kind() != types.UnsafePointer
	case *types.Slice:
		// The driver registers a list of T beside T for everything in its
		// default map, and a list of lists for nothing but the byte slice.
		if inner, ok := u.Elem().Underlying().(*types.Slice); ok {
			return isByteType(inner.Elem()) && registered(u.Elem())
		}
		return registered(u.Elem())
	}
	return false
}

// hasTextValue reports whether a type hands the driver a text of its own,
// which is the first thing the driver asks any value for.
func hasTextValue(t types.Type) bool { return hasMethod(t, "TextValue", twoWithError) }

// speaksText reports whether a type hands the driver a text or a driver
// value of its own: the column type's text method, the standard stringer, or
// the standard library's database value method. Each is read by shape rather
// than by name, because none of the three packages that declare them is in
// the analysed package's import graph.
func speaksText(t types.Type) bool {
	return hasMethod(t, "String", func(sig *types.Signature) bool {
		if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
			return false
		}
		basic, ok := sig.Results().At(0).Type().Underlying().(*types.Basic)
		return ok && basic.Info()&types.IsString != 0
	}) || hasMethod(t, "Value", databaseValue) || hasMethod(t, "TextValue", twoWithError)
}

// twoWithError is the shape of a method that returns a value and an error.
func twoWithError(sig *types.Signature) bool {
	return sig.Params().Len() == 0 && sig.Results().Len() == 2 &&
		types.Identical(sig.Results().At(1).Type(), types.Universe.Lookup("error").Type())
}

// databaseValue is the shape of the standard library's database value method,
// whose first result is the empty interface and nothing narrower. A method of
// the same name returning a concrete type is not that method, and the driver
// does not read it, so a value carrying one is a shape nobody measured and is
// reported rather than let through.
func databaseValue(sig *types.Signature) bool {
	if !twoWithError(sig) {
		return false
	}
	iface, ok := sig.Results().At(0).Type().Underlying().(*types.Interface)
	return ok && iface.NumMethods() == 0
}

// hasMethod reports whether a type or a pointer to it carries a method of
// this name and shape.
func hasMethod(t types.Type, name string, shape func(*types.Signature) bool) bool {
	for _, on := range []types.Type{t, types.NewPointer(t)} {
		for sel := range types.NewMethodSet(on).Methods() {
			fn, ok := sel.Obj().(*types.Func)
			if !ok || fn.Name() != name {
				continue
			}
			if sig, ok := fn.Type().(*types.Signature); ok && shape(sig) {
				return true
			}
		}
	}
	return false
}

// describe says what a value is, in the words of somebody reading the report.
// It never prints a type name: a finding is written for whoever runs the
// tool, and a qualified identifier in it is the developer's register.
func describe(t types.Type) string {
	t = deref(t)
	if t == nil {
		return "a value"
	}
	if isByteSlice(t) {
		return "a byte slice"
	}
	// A type the driver holds a column type for is that column type, whatever
	// Go shape carries it: a clock reading is a timestamp and not a struct.
	if fromPackages(t, registeredPackages) {
		return "a value the driver sends as its own column type"
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return "a Go struct"
	case *types.Map:
		return "a Go map"
	case *types.Slice:
		if isByteType(u.Elem()) {
			return "a byte slice"
		}
		return "a Go slice"
	case *types.Array:
		if isByteType(u.Elem()) {
			return "a byte array"
		}
		return "a Go array"
	case *types.Chan:
		return "a Go channel"
	case *types.Signature:
		return "a Go function"
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return "a boolean"
		case u.Info()&types.IsString != 0:
			return "a string"
		case u.Info()&types.IsNumeric != 0:
			return "a number"
		}
	}
	return "a value the driver sends as something else"
}

// deref strips pointers and aliases down to the type a value really is.
func deref(t types.Type) types.Type {
	for range 8 {
		if t == nil {
			return nil
		}
		if p, ok := types.Unalias(t).Underlying().(*types.Pointer); ok {
			t = p.Elem()
			continue
		}
		return t
	}
	return t
}

// unresolved reports whether a type says nothing about the value in it: an
// interface, or a type parameter constrained by one.
func unresolved(t types.Type) bool {
	if t == nil {
		return true
	}
	if _, ok := types.Unalias(t).(*types.TypeParam); ok {
		return true
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
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
	var name string
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

// isByteType reports whether a type is the byte.
func isByteType(t types.Type) bool {
	if t == nil {
		return false
	}
	basic, ok := t.Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Byte
}

// isByteSlice reports whether a type is a slice of bytes, under whatever name
// it carries. json.RawMessage is one.
func isByteSlice(t types.Type) bool {
	if t == nil {
		return false
	}
	slice, ok := t.Underlying().(*types.Slice)
	return ok && isByteType(slice.Elem())
}

// isJSONType reports whether a type is a named byte slice from a json
// package: json.RawMessage, whose every value is a json document and for
// which the driver holds a codec of its own, so it is both the evidence that
// a parameter is json and an accepted way to carry one.
func isJSONType(t types.Type) bool {
	return isByteSlice(t) && fromPackages(t, jsonPackages)
}

// fromPackages reports whether a named type, or the alias naming it, was
// declared in one of these packages.
//
// Both the name written and the type it resolves to are asked, because
// json.RawMessage is an alias from Go 1.26 on and the two sit in different
// packages.
func fromPackages(t types.Type, paths []string) bool {
	if t == nil {
		return false
	}
	if alias, ok := t.(*types.Alias); ok && declaredIn(alias.Obj(), paths) {
		return true
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && declaredIn(named.Obj(), paths)
}

// declaredIn reports whether a type name was declared in one of these
// packages.
func declaredIn(obj *types.TypeName, paths []string) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return slices.Contains(paths, obj.Pkg().Path())
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
		var name string
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

// bindSentence is what a finding about a json parameter says. It names the
// parameter, why the parameter is json, the mechanism, and both repairs,
// because a reader who has just been told a byte slice is wrong here has to
// be told immediately that it is right one column over.
const bindSentence = "this binds %s into parameter %s and %s; " +
	"the pooled endpoint runs in exec mode, where a parameter travels in the text format, " +
	"a byte slice is sent as bytea and reaches the server as a hex literal, and json refuses it " +
	"with SQLSTATE 22P02, while the direct endpoint accepts the same call; " +
	"bind a string, or a pointer to string where a nil has to stay SQL NULL, " +
	"and keep the byte slice for a bytea column"

// planSentence is what a finding about a value the driver cannot encode says.
// It does not mention the column, because the driver's plan lookup does not
// read the column: this call fails before the statement is sent, whatever the
// column turns out to be.
const planSentence = "this binds %s into parameter %s; " +
	"the pooled endpoint runs in exec mode, where the server describes no parameter and the driver " +
	"picks the wire encoding from the Go type alone, and it holds no encoding for that type, so the " +
	"call fails in the driver with `cannot find encode plan` before the statement is sent, while an " +
	"endpoint that describes the statement first accepts it; " +
	"encode the value and bind the encoding as a string"

// ruleJSONBytes: no statement carries a value its parameter cannot take.
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
		return result{note: fmt.Sprintf("every statement parameter takes the value bound to it, across %d package(s)", t.analysis.packages)}
	}
	found := make([]Finding, 0, len(t.analysis.binds))
	for _, b := range t.analysis.binds {
		if b.why == "" {
			found = append(found, at(b.rel, b.line, planSentence, b.what, b.param))
			continue
		}
		found = append(found, at(b.rel, b.line, bindSentence, b.what, b.param, b.why))
	}
	return result{findings: found}
}
