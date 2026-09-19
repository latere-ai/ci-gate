// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package postgres gates a repository's relationship to the family's shared
// database.
//
// One managed Postgres serves the family with about 22 usable connection
// slots, and every service that connects directly claims its share of them.
// The family's fix is a transaction-mode pool per service: the serving path
// reads the pooled DSN and falls back to the direct one, and the migrator,
// which holds a session-scoped lock, keeps the direct one. That is two lines
// in each of eleven repositories over weeks, which is the shape that drifts,
// so the rule runs on every push.
//
// A repository declares a role in .lateregate.yaml: none, direct or pooled.
// The role selects the checks, and every check is a scan of the non-test Go
// files: what they import, and which environment names they read. Nothing
// here is type-checked and nothing runs a service. An absent role is decided
// from the imports, so an undeclared consumer is the finding and a tool with
// no client passes without writing a block.
package postgres

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// clients are the import paths that make a file a Postgres client. A path
// matches whole or as a prefix followed by a slash, so every major version
// and every subpackage of the pgx tree counts.
//
// database/sql is not in the list: it is generic, and the driver it is
// opened with is what makes it Postgres. That driver's import is the finding.
var clients = []client{
	{path: "github.com/jackc/pgx", name: "pgx"},
	{path: "github.com/lib/pq", name: "pq"},
	{path: "github.com/golang-migrate/migrate/v4/database/postgres", name: "the golang-migrate postgres driver"},
	{path: "github.com/golang-migrate/migrate/v4/database/pgx", name: "the golang-migrate pgx driver"},
	{path: "latere.ai/x/pkg/pgxmigrate", name: "the shared migration runner"},
}

// client is one import path that connects to Postgres, with the name a
// finding calls it by. The name is not the path: a finding is written for
// whoever runs the tool, and an import path in it is the developer's register.
type client struct {
	path string
	name string
}

func (c client) matches(importPath string) bool {
	return importPath == c.path || strings.HasPrefix(importPath, c.path+"/")
}

// Finding is one place the tree leaves the declared role.
//
// The location is the file and the line; the sentence says what to do and is
// written in the register of somebody who runs the tool.
type Finding struct {
	Rel      string
	Line     int
	Sentence string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.Rel, f.Line, f.Sentence) }

// result is what one check reports: nothing to read and why, what it read,
// or what it found.
type result struct {
	skip     string
	note     string
	findings []Finding
}

// check is one row of a role's table.
type check struct {
	name string
	run  func(*tree) result
}

// checks maps a role to what it runs. The absent role has its own entry
// under PostgresUnset, because deciding from the imports is a check too.
//
// direct runs one check that passes by declaration. The family's build order
// makes it require a dated waiver once the first cutovers land, and that
// release changes ruleDirect and nothing else here.
var checks = map[config.PostgresRole][]check{
	config.PostgresUnset:  {{name: "declared", run: ruleDeclared}},
	config.PostgresNone:   {{name: "client-free", run: ruleClientFree}},
	config.PostgresDirect: {{name: "direct", run: ruleDirect}},
	config.PostgresPooled: {
		{name: "client", run: ruleClient},
		{name: "pool-url", run: rulePoolURL},
		{name: "direct-url", run: ruleDirectURL},
	},
}

// Run applies the checks of the declared role to the tree.
func Run(cfg config.Postgres, root string, out io.Writer) error {
	role := cfg.Role
	if !cfg.Present {
		role = config.PostgresUnset
		_, _ = fmt.Fprintf(out, "postgres: no role declared; deciding from the imports\n")
	} else {
		_, _ = fmt.Fprintf(out, "postgres: role %s\n", string(role))
	}
	t, err := scan(cfg, root)
	if err != nil {
		return err
	}
	var failed, skipped []string
	rows := checks[role]
	for _, c := range rows {
		res := c.run(t)
		switch {
		case len(res.findings) > 0:
			failed = append(failed, c.name)
			_, _ = fmt.Fprintf(out, "%-4s %-12s %d finding(s)\n", "FAIL", c.name, len(res.findings))
			for _, f := range sorted(res.findings) {
				_, _ = fmt.Fprintln(out, "  "+f.String())
			}
		case res.skip != "":
			skipped = append(skipped, c.name)
			_, _ = fmt.Fprintf(out, "%-4s %-12s %s\n", "SKIP", c.name, res.skip)
		default:
			_, _ = fmt.Fprintf(out, "%-4s %-12s %s\n", "PASS", c.name, res.note)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d postgres check(s) failed for role %s: %s",
			len(failed), len(rows), roleWord(cfg), strings.Join(failed, ", "))
	}
	// A role every check of which skipped has held nothing, and a gate that
	// measured nothing fails rather than passing over an empty tree.
	if len(skipped) == len(rows) {
		return fmt.Errorf("no postgres check of role %s read anything: %s\n"+
			"the role declares what this repository does with the database, and a tree with "+
			"no Go file to read shows none of it; declare none until there is one, or delete the block",
			roleWord(cfg), strings.Join(skipped, ", "))
	}
	_, _ = fmt.Fprintf(out, "role %s holds %d check(s)\n", roleWord(cfg), len(rows))
	return nil
}

// roleWord is the role for a sentence: the declared one, or "absent".
func roleWord(cfg config.Postgres) string {
	if !cfg.Present {
		return "absent"
	}
	return string(cfg.Role)
}

// ruleDeclared decides an undeclared repository from its imports. A tree
// with no client passes and says how it was decided; a tree with one is the
// undeclared consumer this gate exists to catch.
func ruleDeclared(t *tree) result {
	if len(t.files) == 0 {
		return result{note: "no non-test Go file, so nothing here connects to a database"}
	}
	var found []Finding
	for _, imp := range t.clientImports() {
		found = append(found, at(imp.rel, imp.line,
			"this imports %s and no postgres role is declared; declare one under postgres.role, "+
				"one of %s: direct while this repository connects on the direct endpoint, pooled once "+
				"its serving path reads the pooled name and falls back to the direct one, none only "+
				"when the import goes",
			imp.name, config.PostgresRoleList()))
	}
	return result{findings: found,
		note: fmt.Sprintf("no Postgres client across %d Go file(s); declaring postgres.role none records that as a decision", len(t.files))}
}

// ruleClientFree holds a repository that says it has no client to that.
func ruleClientFree(t *tree) result {
	if len(t.files) == 0 {
		return result{note: "no non-test Go file, so nothing here connects to a database"}
	}
	var found []Finding
	for _, imp := range t.clientImports() {
		found = append(found, at(imp.rel, imp.line,
			"this imports %s, and postgres.role says none; a repository with a client declares "+
				"direct while it connects on the direct endpoint, or pooled once its serving path "+
				"reads the pooled name and falls back to the direct one", imp.name))
	}
	return result{findings: found, note: fmt.Sprintf("no Postgres client across %d Go file(s)", len(t.files))}
}

// ruleDirect passes by declaration. This is the seam the family's build order
// step 4 opens: a direct repository will need a dated waiver naming why it
// stays on the direct endpoint, and that check is written here when the first
// ten cutovers have landed.
func ruleDirect(t *tree) result {
	return result{note: fmt.Sprintf("passes by declaration in this release, %d Go file(s) unread; "+
		"a later release requires a dated reason for staying on the direct endpoint", len(t.files))}
}

// ruleClient: a pooled repository imports a client, because one that does
// not has declared a relationship it does not have.
func ruleClient(t *tree) result {
	if len(t.files) == 0 {
		return result{skip: "no non-test Go file to read"}
	}
	imports := t.clientImports()
	if len(imports) == 0 {
		return result{findings: []Finding{at("go.mod", 1,
			"nothing here imports a Postgres client, and postgres.role says pooled; a repository "+
				"with no client declares none, and one whose client is on the way declares it in the "+
				"release that adds the import")}}
	}
	return result{note: fmt.Sprintf("%s imported in %d file(s)", imports[0].name, len(imports))}
}

// rulePoolURL: the serving path reads the pooled name.
func rulePoolURL(t *tree) result {
	return readsName(t, t.cfg.PoolURL(),
		"nothing here reads %s; the serving path reads it first and falls back to %s, so "+
			"the pool and not the direct endpoint carries requests")
}

// ruleDirectURL: the serving path falls back to the direct name, and the
// migrator receives it.
func ruleDirectURL(t *tree) result {
	return readsName(t, t.cfg.DirectURL(),
		"nothing here reads %s; the serving path falls back to it and the migrator receives it, "+
			"because a migration holds a session and %s is the pool, which does not")
}

// readsName reports whether any file reads the environment name.
func readsName(t *tree, name, sentence string) result {
	if len(t.files) == 0 {
		return result{skip: "no non-test Go file to read"}
	}
	var where []string
	for _, f := range t.files {
		if n := f.reads[name]; n > 0 {
			where = append(where, f.rel)
		}
	}
	if len(where) == 0 {
		other := t.cfg.DirectURL()
		if name == other {
			other = t.cfg.PoolURL()
		}
		return result{findings: []Finding{at("go.mod", 1, sentence, name, other)}}
	}
	return result{note: fmt.Sprintf("%s read in %s", name, strings.Join(where, ", "))}
}

// at builds a finding at a location.
func at(rel string, line int, format string, args ...any) Finding {
	return Finding{Rel: rel, Line: line, Sentence: fmt.Sprintf(format, args...)}
}

// sorted orders findings by location, so two runs over one tree print the
// same report.
func sorted(f []Finding) []Finding {
	out := slices.Clone(f)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rel != out[j].Rel {
			return out[i].Rel < out[j].Rel
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// goFile is one parsed non-test Go file and what the checks ask of it.
type goFile struct {
	rel     string
	imports []importLine
	// reads counts, per environment name, the reads the file makes of it.
	reads map[string]int
}

// importLine is one import and where it is.
type importLine struct {
	path string
	line int
}

// clientImport is one client import, named for a finding.
type clientImport struct {
	rel  string
	line int
	name string
}

// tree is every scan target of one repository, read once and shared by the
// checks.
type tree struct {
	cfg   config.Postgres
	files []goFile
}

// clientImports lists the files that import a client, each at its first
// such import. One finding per file rather than per import, because the fix
// for every one of them is the same line in the tool's file, and a report
// that says it forty times says it less well than once.
func (t *tree) clientImports() []clientImport {
	var out []clientImport
	for _, f := range t.files {
	imports:
		for _, imp := range f.imports {
			for _, c := range clients {
				if c.matches(imp.path) {
					out = append(out, clientImport{rel: f.rel, line: imp.line, name: c.name})
					break imports
				}
			}
		}
	}
	return out
}

// skipDirs are what no person in the repository wrote, or what another tool
// owns: the same set the identity scan does not enter.
var skipDirs = []string{".git", ".claude", "node_modules", "testdata"}

// scan reads the tree once. Files are parsed with the syntax alone, and the
// string constants of a package are resolved across its files afterwards,
// because a name bound in one file is read in another.
func scan(cfg config.Postgres, root string) (*tree, error) {
	t := &tree{cfg: cfg}
	fset := token.NewFileSet()
	parsed := map[string][]*ast.File{}
	var order []string
	byRel := map[string]*ast.File{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && slices.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		body, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		// A file that does not parse is a build failure, which another gate
		// reports; there is nothing here to decide about it, so it is not
		// read and the walk goes on.
		if file, parseErr := parser.ParseFile(fset, p, body, parser.SkipObjectResolution); parseErr == nil {
			dir := path.Dir(rel)
			parsed[dir] = append(parsed[dir], file)
			byRel[rel] = file
			order = append(order, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the tree for the postgres checks: %w", err)
	}
	// The string constants of each package, so getenv(dsnName) resolves.
	constants := map[string]map[string]string{}
	for dir, files := range parsed {
		constants[dir] = stringConstants(files)
	}
	for _, rel := range order {
		file := byRel[rel]
		dir := path.Dir(rel)
		g := goFile{rel: rel, reads: map[string]int{}}
		for _, imp := range file.Imports {
			if imp.Path == nil {
				continue
			}
			ip, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				continue
			}
			g.imports = append(g.imports, importLine{path: ip, line: fset.Position(imp.Pos()).Line})
		}
		for _, name := range envReads(file, constants[dir]) {
			g.reads[name]++
		}
		t.files = append(t.files, g)
	}
	return t, nil
}

// stringConstants collects every package-level const or var bound to one
// string literal, by name, across the files of one package.
func stringConstants(files []*ast.File) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if s, err := strconv.Unquote(lit.Value); err == nil {
						out[name.Name] = s
					}
				}
			}
		}
	}
	return out
}

// envReads lists the environment names a file reads, in the two shapes the
// family uses: a call to something named for the environment with the name
// as an argument, and a struct tag under the env key. A name in a comment,
// a log line or an error message is neither.
func envReads(file *ast.File, constants map[string]string) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.CallExpr:
			if !envCallee(n.Fun) {
				return true
			}
			for _, arg := range n.Args {
				if name, ok := stringArgument(arg, constants); ok {
					out = append(out, name)
				}
			}
		case *ast.Field:
			if n.Tag == nil {
				return true
			}
			tag, err := strconv.Unquote(n.Tag.Value)
			if err != nil {
				return true
			}
			if v, ok := reflect.StructTag(tag).Lookup("env"); ok {
				name, _, _ := strings.Cut(v, ",")
				if name = strings.TrimSpace(name); name != "" {
					out = append(out, name)
				}
			}
		}
		return true
	})
	return out
}

// envCallee reports whether a call is to something named for the
// environment: os.Getenv, os.LookupEnv, a local getenv, an envOr helper. The
// last identifier of the callee is what carries the name.
func envCallee(fun ast.Expr) bool {
	var name string
	switch f := fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	default:
		return false
	}
	return strings.Contains(strings.ToLower(name), "env")
}

// stringArgument is the text of an argument that is a string literal, or an
// identifier the package binds to one.
func stringArgument(arg ast.Expr, constants map[string]string) (string, bool) {
	switch a := arg.(type) {
	case *ast.BasicLit:
		if a.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(a.Value)
		return s, err == nil
	case *ast.Ident:
		s, ok := constants[a.Name]
		return s, ok
	}
	return "", false
}
