// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package registers gates the register of the strings a user reads.
//
// Every sentence a product emits is written for one of three readers: the
// user, the contributor, or the developer debugging a running system. The
// rule is docs/writing/registers.md in latere.ai/x/pkg. The leak that
// survives review most often is a developer sentence handed to the function
// that writes the user's error: a package path, a package-qualified
// identifier, a Kubernetes object, or a file path, in a string literal
// passed to WriteError. Each is a mechanical tell, so it is a gate.
//
// The scan parses each file and looks only at calls to the functions the
// repository named as user surfaces, then at the string literals in their
// arguments, including a literal nested in a fmt.Sprintf argument. It reads
// no type information: a surface is matched by import path and name, so a
// method or a function passed as a value is not seen, and the repository
// names the package-level functions its user output goes through.
package registers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// tell is one shape a developer sentence takes on a user surface.
type tell struct {
	name string
	re   *regexp.Regexp
	// skipGroup, when positive, names a capture group whose presence makes
	// the match not a finding. The import-path pattern uses it to let a URL
	// through: a page is what a user is told to open.
	skipGroup int
}

var tells = []tell{
	{name: "Go import path",
		re: regexp.MustCompile(`(https?://)?\b[a-z0-9][a-z0-9-]*(?:\.[a-z0-9-]+)+/[A-Za-z0-9_./-]*[A-Za-z0-9_]`), skipGroup: 1},
	{name: "package-qualified identifier",
		re: regexp.MustCompile(`\b[a-z][a-z0-9]*\.[A-Z][A-Za-z0-9]*\b`)},
	// A kind followed by a generated name: one with a separator or a digit
	// in it, so "Service unavailable" is a sentence and "Service
	// api-7d9c" is an object.
	{name: "Kubernetes object",
		re: regexp.MustCompile(`\b(?:Pod|Deployment|StatefulSet|DaemonSet|ReplicaSet|Job|CronJob|Service|Ingress|Namespace|ConfigMap|Secret|PersistentVolumeClaim|PersistentVolume|ServiceAccount|Node|HTTPRoute|Gateway|Certificate|NetworkPolicy)s?\s+"?[a-z0-9]+(?:[-./][a-z0-9]+)+"?`)},
	{name: "Kubernetes object",
		re: regexp.MustCompile(`\b(?:pods|deployments|statefulsets|daemonsets|replicasets|jobs|cronjobs|services|ingresses|namespaces|configmaps|secrets|persistentvolumeclaims|serviceaccounts|nodes)\s+"[a-z0-9][a-z0-9.-]*"`)},
	// An absolute path under a system or workspace root, a home path, or a
	// Go source position. A bare file name such as latere.yaml is not a
	// tell: it is the file the user edits.
	{name: "file path",
		re: regexp.MustCompile(`(?:^|[\s"'(=:,])(?:/(?:etc|var|tmp|usr|home|root|run|opt|srv|mnt|proc|sys|dev|work|app|data|workspace)/[A-Za-z0-9_./-]+|~/[A-Za-z0-9_./-]+|[A-Za-z0-9_./-]*[A-Za-z0-9_]\.go(?::\d+)?\b)`)},
}

// Finding is one string in the wrong register.
type Finding struct {
	Rel     string
	Line    int
	Surface string
	Tell    string
	Text    string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s %q in a string passed to %s", f.Rel, f.Line, f.Tell, f.Text, f.Surface)
}

// Run scans root for developer sentences on the named user surfaces.
func Run(cfg config.Registers, root string, out io.Writer) error {
	surfaces := cfg.Surfaces()
	if len(surfaces) == 0 {
		_, _ = fmt.Fprintln(out, "registers: no user surface named, nothing to check")
		return nil
	}
	module, err := modulePath(root)
	if err != nil {
		return err
	}
	calls := map[string]int{}
	var found []Finding
	scanned := 0
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (name == ".git" || name == ".claude" || name == "testdata" || name == "node_modules" || slices.Contains(cfg.Skip, name)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			rel = p
		}
		file, ok := parse(fset, p)
		if !ok {
			return nil
		}
		scanned++
		pkgPath := path.Join(module, filepath.ToSlash(filepath.Dir(rel)))
		found = append(found, fileFindings(fset, file, filepath.ToSlash(rel), pkgPath, module, surfaces, calls)...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanning for user-surface strings: %w", err)
	}
	if scanned == 0 {
		return fmt.Errorf("no non-test Go files found under %s; the gate would pass vacuously", root)
	}
	// A surface nothing calls is a typo in the config, and a typo that
	// disables a check is the failure this binary is against.
	var unused []string
	for _, s := range surfaces {
		if calls[s.Pkg+"."+s.Func] == 0 {
			unused = append(unused, s.Pkg+"."+s.Func)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return fmt.Errorf("registers.user_surfaces names %s, which no Go file calls; "+
			"a surface nothing reaches is a check that never runs, so fix the name or delete the entry",
			strings.Join(unused, ", "))
	}
	for _, f := range found {
		_, _ = fmt.Fprintln(out, "  "+f.String())
	}
	if len(found) > 0 {
		return fmt.Errorf("%d developer sentence(s) on a user surface; move the detail to the details field or the log line, and keep the message in the user's terms", len(found))
	}
	total := 0
	for _, n := range calls {
		total += n
	}
	_, _ = fmt.Fprintf(out, "every string on a user surface is in the user's register, across %d call(s) in %d Go file(s)\n", total, scanned)
	return nil
}

// parse reads one file. A file the parser cannot read is a build failure,
// and the build is another gate's job, so the answer is a boolean rather
// than an error: there is nothing for this gate to report.
func parse(fset *token.FileSet, path string) (*ast.File, bool) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	return file, err == nil
}

// fileFindings walks one parsed file for calls to a surface and checks the
// string literals in their arguments. calls counts every matched call, so
// the caller can tell a clean surface from one the tree never reaches.
func fileFindings(fset *token.FileSet, file *ast.File, rel, pkgPath, module string, surfaces []config.Surface, calls map[string]int) []Finding {
	imports := map[string]string{} // local name -> import path
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(ip)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		imports[local] = ip
	}
	var out []Finding
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		s, ok := calledSurface(call, imports, pkgPath, module, surfaces)
		if !ok {
			return true
		}
		key := s.Pkg + "." + s.Func
		calls[key]++
		for _, arg := range call.Args {
			for _, lit := range stringLiterals(arg) {
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				for _, t := range tells {
					if m := t.match(text); m != "" {
						out = append(out, Finding{
							Rel: rel, Line: fset.Position(lit.Pos()).Line,
							Surface: key, Tell: t.name, Text: m,
						})
					}
				}
			}
		}
		return true
	})
	return out
}

// calledSurface reports which named surface a call reaches, if any. A
// qualified call resolves its qualifier through the file's imports; an
// unqualified call matches only from inside the surface's own package.
func calledSurface(call *ast.CallExpr, imports map[string]string, pkgPath, module string, surfaces []config.Surface) (config.Surface, bool) {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		ident, ok := fn.X.(*ast.Ident)
		if !ok {
			return config.Surface{}, false
		}
		ip, ok := imports[ident.Name]
		if !ok {
			return config.Surface{}, false
		}
		for _, s := range surfaces {
			if s.Func == fn.Sel.Name && samePackage(s.Pkg, ip, module) {
				return s, true
			}
		}
	case *ast.Ident:
		for _, s := range surfaces {
			if s.Func == fn.Name && samePackage(s.Pkg, pkgPath, module) {
				return s, true
			}
		}
	}
	return config.Surface{}, false
}

// samePackage reports whether a surface's package, written relative to the
// module or in full, is the import path.
func samePackage(surface, importPath, module string) bool {
	return surface == importPath || path.Join(module, surface) == importPath
}

// stringLiterals collects every string literal under e, so a literal built
// by concatenation or passed through fmt.Sprintf is still read.
func stringLiterals(e ast.Expr) []*ast.BasicLit {
	var out []*ast.BasicLit
	ast.Inspect(e, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			out = append(out, lit)
		}
		return true
	})
	return out
}

// match returns the offending text, or "" when the tell is absent.
func (t tell) match(text string) string {
	for _, m := range t.re.FindAllStringSubmatch(text, -1) {
		if t.skipGroup > 0 && m[t.skipGroup] != "" {
			continue
		}
		return strings.TrimLeft(m[0], " \t\"'(=:,")
	}
	return ""
}

// modulePath reads the module line of go.mod at root. The gate needs it to
// turn a relative surface such as internal/api into the import path a file
// binds it to.
func modulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod to resolve the user surfaces: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("go.mod at %s has no module line", root)
}

// Tells reports the developer-register tells a sentence carries, each named
// with the text that matched, and nothing when the sentence is in the user's
// register.
//
// It is exported so another gate's own output can be held to this gate's
// rule: a gate that prints a package path at a person is the leak this
// package exists to find, whoever printed it.
func Tells(text string) []string {
	var out []string
	for _, t := range tells {
		if m := t.match(text); m != "" {
			out = append(out, t.name+" "+strconv.Quote(m))
		}
	}
	return out
}
