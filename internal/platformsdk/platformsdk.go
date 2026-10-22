// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package platformsdk gates a client to reaching the Latere platform through
// its Go SDK, latere.ai/x/sdk-go.
//
// The SDK holds the platform's routes, its error envelope, its retries and
// its pagination once. A client that writes a route itself holds a second
// copy, which drifts from the first when the API changes, and the drift
// shows as a call that fails against the platform rather than a build that
// fails. So in a repository that turns the gate on, a platform route outside
// the SDK is a finding.
//
// Two shapes are read, from the source and with no type information:
//
//   - a string that names a platform route: a literal, a concatenation, a
//     constant, or a fmt format with its arguments, folded across the
//     module's own constants, so a route built from a prefix constant that
//     another package declares is found where it is built;
//   - a request built with net/http, or a url.URL, addressed to a platform
//     route or to the platform origin's host.
//
// A string names a route when its value starts with /v1/ and a segment the
// platform origin serves, after a scheme and the platform host, or after a
// part the scan cannot know, such as a base URL read from configuration. A
// route mentioned inside a sentence names no call and is not a finding. A
// route that reaches a request only through a variable is found where the
// string is written, not where it is sent: the scan follows constants, not
// data flow.
package platformsdk

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// Finding is one place a client reaches the platform outside the SDK.
type Finding struct {
	Rel  string
	Line int
	// What says what was found: a route in a string, or the request that
	// carries one.
	What string
	// Route is the path found, normalized, which the allow list is matched
	// against.
	Route string
	// Instead is what to use: the SDK call that makes the route, or the
	// instruction to add one.
	Instead string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s; %s", f.Rel, f.Line, f.What, f.Instead)
}

// Run scans the tree at root for platform calls outside the SDK. It does not
// read cfg.Enabled: whether the gate applies is the plan's question, and a
// gate run by name runs.
func Run(cfg config.PlatformViaSDK, root string, out io.Writer) error {
	fset := token.NewFileSet()
	files, err := load(fset, root, cfg.Skip)
	if err != nil {
		return fmt.Errorf("scanning for platform routes: %w", err)
	}
	// A scan that read nothing proves nothing: a tree that is all tests, or
	// the SDK itself, has no client code to hold to the rule.
	if len(files) == 0 {
		return fmt.Errorf("no non-test Go file outside %s found under %s; the gate would pass vacuously", SDK, root)
	}
	ix := newIndex(files)
	var found []Finding
	for _, f := range files {
		found = append(found, scan(fset, ix, f)...)
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Rel != found[j].Rel {
			return found[i].Rel < found[j].Rel
		}
		return found[i].Line < found[j].Line
	})

	needed := map[string]bool{}
	var kept []Finding
	for _, f := range found {
		keys := allows(f.Route, cfg.Allow)
		for _, k := range keys {
			needed[k] = true
		}
		if len(keys) == 0 {
			kept = append(kept, f)
		}
	}
	var unneeded []string
	for k := range cfg.Allow {
		if !needed[k] {
			unneeded = append(unneeded, k)
		}
	}
	sort.Strings(unneeded)

	for _, f := range kept {
		_, _ = fmt.Fprintln(out, "  "+f.String())
	}
	var errs []error
	if len(kept) > 0 {
		errs = append(errs, fmt.Errorf("%d platform route(s) outside %s; make each call through the SDK, and add a call it does not make yet to the SDK", len(kept), SDK))
	}
	if len(unneeded) > 0 {
		errs = append(errs, fmt.Errorf("platform_via_sdk.allow names %s, which no finding needs; "+
			"an exception nothing uses is where the next real call hides, so delete the entry",
			strings.Join(unneeded, ", ")))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	allowed := len(found) - len(kept)
	_, _ = fmt.Fprintf(out, "every platform call goes through %s, across %d Go file(s)", SDK, len(files))
	if allowed > 0 {
		_, _ = fmt.Fprintf(out, "; %d route(s) allowed by platform_via_sdk.allow", allowed)
	}
	_, _ = fmt.Fprintln(out)
	return nil
}

// srcFile is one parsed file and what reading its names takes.
type srcFile struct {
	rel string
	// pkg is the file's import path.
	pkg string
	ast *ast.File
	// imports maps a local name to the import path it binds.
	imports map[string]string
	// fmt, http and url are the local names of fmt, net/http and net/url,
	// or empty where the file does not import them by a usable name.
	fmt, http, url string
}

// pruned are the directories no scan enters. .claude holds agent worktrees,
// whole copies of the repository that would report every finding twice;
// vendor holds other modules' code, the SDK's among them.
var pruned = []string{".git", ".claude", "testdata", "node_modules", "vendor"}

// load parses every non-test Go file under root, outside the SDK module and
// the pruned and skipped directories. A file the parser cannot read is left
// out: that is a build failure, which the compiler reports better.
func load(fset *token.FileSet, root string, skip []string) ([]*srcFile, error) {
	modules := map[string]string{}
	var files []*srcFile
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (slices.Contains(pruned, name) || slices.Contains(skip, name)) {
				return filepath.SkipDir
			}
			mod, ok, err := modulePath(p)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
			if mod == SDK || strings.HasPrefix(mod, SDK+"/") {
				return filepath.SkipDir
			}
			modules[p] = mod
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil //nolint:nilerr // an unparsable file is the compiler's to report
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, &srcFile{rel: filepath.ToSlash(rel), pkg: importPath(modules, root, filepath.Dir(p)), ast: file})
		return nil
	})
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, f := range files {
		names[f.pkg] = f.ast.Name.Name
	}
	for _, f := range files {
		f.imports = imports(f.ast, names)
		for local, ip := range f.imports {
			switch ip {
			case "fmt":
				f.fmt = local
			case "net/http":
				f.http = local
			case "net/url":
				f.url = local
			}
		}
	}
	return files, nil
}

// modulePath reads the module line of dir's go.mod, and reports whether dir
// has one.
func modulePath(dir string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), true, nil
		}
	}
	return "", false, nil
}

// importPath is the import path of the package in dir: its nearest module's
// path and the directory below it. A tree with no go.mod has the path
// relative to root, which still tells its packages apart.
func importPath(modules map[string]string, root, dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if mod, ok := modules[d]; ok {
			rel, err := filepath.Rel(d, dir)
			if err != nil || rel == "." {
				return mod
			}
			return path.Join(mod, filepath.ToSlash(rel))
		}
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}

// imports maps each local name in file to the import path it binds. A
// package of the tree is bound by its own name, which names knows; another
// by its last path element. A dot or blank import binds no name.
func imports(file *ast.File, names map[string]string) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local, ok := names[ip]
		if !ok {
			local = path.Base(ip)
		}
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

// requests are the net/http functions that build or send a request, by the
// index of the URL among their arguments.
var requests = map[string]int{"NewRequest": 1, "NewRequestWithContext": 2, "Get": 0, "Head": 0, "Post": 0, "PostForm": 0}

// scan reads one file's findings. Each string is read once, as part of the
// outermost expression that holds it, so a concatenation is one finding and
// not one per operand; a part of it the source does not fix is read on its
// own, since it may hold a route of its own.
func scan(fset *token.FileSet, ix *index, f *srcFile) []Finding {
	var found []Finding
	skip := map[ast.Node]bool{}
	used := map[ast.Node]bool{}
	report := func(n ast.Node, what, route string) {
		found = append(found, Finding{Rel: f.rel, Line: fset.Position(n.Pos()).Line, What: what, Route: route, Instead: hint(route)})
	}
	for _, decl := range f.ast.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		locals := localNames(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			names(n, skip)
			switch x := n.(type) {
			case *ast.CallExpr:
				request(ix, f, locals, x, used, report)
			case *ast.CompositeLit:
				urlLiteral(ix, f, locals, x, used, report)
			}
			e, ok := n.(ast.Expr)
			if !ok || skip[n] || used[n] {
				return true
			}
			fo := &folder{ix: ix, file: f, locals: locals}
			v := fo.fold(e)
			for _, u := range fo.used {
				used[u] = true
			}
			if t := address(v); t.api {
				report(e, "platform route "+t.route+" in a string", t.route)
			}
			return true
		})
	}
	return found
}

// request reports a net/http request addressed to a platform route or to
// the platform host.
func request(ix *index, f *srcFile, locals map[string]bool, call *ast.CallExpr, used map[ast.Node]bool, report func(ast.Node, string, string)) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || f.http == "" || !isPkg(sel.X, f.http, locals) {
		return
	}
	at, ok := requests[sel.Sel.Name]
	if !ok || at >= len(call.Args) {
		return
	}
	fo := &folder{ix: ix, file: f, locals: locals}
	t := address(fo.fold(call.Args[at]))
	if !t.api && !t.host {
		return
	}
	for _, u := range fo.used {
		used[u] = true
	}
	what := "http." + sel.Sel.Name
	if t.api {
		report(call, what+" to platform route "+t.route, t.route)
		return
	}
	report(call, what+" to the platform origin, "+Host, t.route)
}

// urlLiteral reports a url.URL whose Path is a platform route, or whose Host
// is the platform host.
func urlLiteral(ix *index, f *srcFile, locals map[string]bool, lit *ast.CompositeLit, used map[ast.Node]bool, report func(ast.Node, string, string)) {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || f.url == "" || sel.Sel.Name != "URL" || !isPkg(sel.X, f.url, locals) {
		return
	}
	var host, route *folder
	var hostTarget, routeTarget target
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		fo := &folder{ix: ix, file: f, locals: locals}
		switch key.Name {
		case "Host":
			host = fo
			// A Host is an authority alone; read with a scheme in front, it is
			// the URL address knows how to read.
			hostTarget = address(concat(value{{text: "https://", known: true}}, fo.fold(kv.Value)))
		case "Path":
			route = fo
			routeTarget = address(fo.fold(kv.Value))
		}
	}
	consume := func(fo *folder) {
		if fo != nil {
			for _, u := range fo.used {
				used[u] = true
			}
		}
	}
	switch {
	case routeTarget.api:
		consume(route)
		if hostTarget.host {
			consume(host)
		}
		report(lit, "url.URL for platform route "+routeTarget.route, routeTarget.route)
	case hostTarget.host:
		consume(host)
		report(lit, "url.URL addressed to the platform origin, "+Host, routeTarget.route)
	}
}

// isPkg reports whether e names the package bound to local, and not a
// variable of the same name.
func isPkg(e ast.Expr, local string, locals map[string]bool) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == local && !locals[local]
}

// names marks the identifiers under n that name something rather than
// read a value: a declaration's name, a field, a label, a struct literal's
// key and a selector's member, along with a field's tag. A package
// constant of the same name is not what they hold.
func names(n ast.Node, skip map[ast.Node]bool) {
	mark := func(ids ...*ast.Ident) {
		for _, id := range ids {
			if id != nil {
				skip[id] = true
			}
		}
	}
	switch x := n.(type) {
	case *ast.ValueSpec:
		mark(x.Names...)
	case *ast.TypeSpec:
		mark(x.Name)
	case *ast.FuncDecl:
		mark(x.Name)
	case *ast.Field:
		mark(x.Names...)
		if x.Tag != nil {
			skip[x.Tag] = true
		}
	case *ast.LabeledStmt:
		mark(x.Label)
	case *ast.BranchStmt:
		mark(x.Label)
	case *ast.AssignStmt:
		if x.Tok == token.DEFINE {
			for _, l := range x.Lhs {
				if id, ok := l.(*ast.Ident); ok {
					mark(id)
				}
			}
		}
	case *ast.KeyValueExpr:
		if id, ok := x.Key.(*ast.Ident); ok {
			mark(id)
		}
	case *ast.SelectorExpr:
		mark(x.Sel)
	}
}

// localNames is every name declared inside the functions under decl: their
// receivers, parameters and results, and what their bodies declare. A
// local name shadows a package constant of the same name, so it is read as
// a value the source does not fix, wherever in the function it is used. A
// constant declared inside a function is reported where its value is
// written, and not again where it is used.
func localNames(decl ast.Decl) map[string]bool {
	out := map[string]bool{}
	add := func(ids ...*ast.Ident) {
		for _, id := range ids {
			if id != nil && id.Name != "_" {
				out[id.Name] = true
			}
		}
	}
	fields := func(lists ...*ast.FieldList) {
		for _, l := range lists {
			if l == nil {
				continue
			}
			for _, field := range l.List {
				add(field.Names...)
			}
		}
	}
	body := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncType:
				fields(x.TypeParams, x.Params, x.Results)
			case *ast.AssignStmt:
				if x.Tok == token.DEFINE {
					for _, l := range x.Lhs {
						if id, ok := l.(*ast.Ident); ok {
							add(id)
						}
					}
				}
			case *ast.ValueSpec:
				add(x.Names...)
			case *ast.TypeSpec:
				add(x.Name)
			case *ast.RangeStmt:
				if x.Tok == token.DEFINE {
					for _, e := range []ast.Expr{x.Key, x.Value} {
						if id, ok := e.(*ast.Ident); ok {
							add(id)
						}
					}
				}
			}
			return true
		})
	}
	ast.Inspect(decl, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			fields(x.Recv)
			body(x.Type)
			if x.Body != nil {
				body(x.Body)
			}
			return false
		case *ast.FuncLit:
			body(x)
			return false
		}
		return true
	})
	return out
}
