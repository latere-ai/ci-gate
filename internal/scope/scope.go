// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package scope measures what a checkout changed since its default branch,
// so the bar run before a push checks that change instead of the whole tree.
//
// The default branch is already held to the whole bar: CI runs every gate
// on every push to it. What a push can break is what it changes and what
// depends on that, so a run that checks the change, and the packages that
// import it, reaches the same verdict on those packages in a fraction of the
// time. Anything the change could reach in ways the import graph does not
// show, a dependency, the bar's own configuration, a package removed
// outright, sends the run back to the whole tree.
package scope

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Package is one package of the module the change reaches.
type Package struct {
	// ImportPath is what go test and -coverpkg take.
	ImportPath string
	// Dir is the package's directory relative to the root, as a slash
	// path: "." for the root, else a path without a leading "./".
	Dir string
}

// Pattern is the package as a directory pattern, which golangci-lint takes.
func (p Package) Pattern() string {
	if p.Dir == "." {
		return "."
	}
	return "./" + p.Dir
}

// Scope is the change one checkout carries over its default branch.
type Scope struct {
	// Ref is the default branch the change is measured from, such as
	// origin/main.
	Ref string
	// Base is the merge base of HEAD and Ref.
	Base string
	// Whole is why the whole tree is checked anyway. Empty means the scope
	// holds.
	Whole string
	// Files are the paths changed since Base, committed or not, and the
	// untracked ones, as slash paths relative to the root.
	Files []string
	// Changed are the packages holding a changed file.
	Changed []Package
	// Reached are Changed and every package whose build or tests import one
	// of them, directly or through another package of the module.
	Reached []Package

	// CI is set when the scope was measured for a CI run: the packages are
	// never narrowed, and each gate either reads the whole tree or is
	// skipped.
	CI bool
	// GoInert is set, on a CI scope, when no changed file can move the
	// verdict of a gate that reads Go.
	GoInert bool
	// GoWhy is the changed file that makes a CI scope not inert, and why.
	GoWhy string

	// The module the change was measured in, as Resolve listed it.
	root   string
	module string
	pkgs   []listed
}

// Narrows reports whether the scope holds: a nil scope, or one that fell
// back to the whole tree, narrows nothing.
func (s *Scope) Narrows() bool { return s != nil && s.Whole == "" }

// Packages reports whether gates narrow to the packages the change reaches:
// the scope holds and was not measured for CI, where a gate reads the whole
// tree or does not run.
func (s *Scope) Packages() bool { return s.Narrows() && !s.CI }

// Since names the base for a reader: the branch and the commit, or the
// commit alone when it was named rather than found.
func (s *Scope) Since() string {
	if s.Ref == "" {
		return short(s.Base)
	}
	return fmt.Sprintf("%s (%s)", s.Ref, short(s.Base))
}

// Touches reports whether a changed file sits in dir, a slash path relative
// to the root, or below it.
func (s *Scope) Touches(dir string) bool {
	for _, f := range s.Files {
		if within(f, dir) {
			return true
		}
	}
	return false
}

// whole returns a scope that checks the whole tree, for the reason given.
func whole(reason string) *Scope { return &Scope{Whole: reason} }

// WholeTree is the scope of a run that has no base to measure from: it
// checks the whole tree, for the reason given.
func WholeTree(reason string) *Scope { return whole(reason) }

// wholeTreeFiles are the files whose change can move any gate's verdict on
// any package: the module's dependencies and the bar's own configuration.
var wholeTreeFiles = []string{
	"go.mod", "go.sum", "go.work", "go.work.sum",
	config.Name, ".golangci.yml", ".golangci.yaml",
}

// wholeTree reports whether a change to f can move any package's verdict:
// one of wholeTreeFiles, or a vendored dependency.
func wholeTree(f string) bool {
	return slices.Contains(wholeTreeFiles, f) || strings.HasPrefix(f, "vendor/")
}

// Resolve measures the change the checkout at root carries.
//
// It never narrows on a guess: when the base cannot be found, the module
// cannot be listed, or the change reaches past the import graph, the scope
// it returns checks the whole tree and says why.
func Resolve(root, goBin string, run gates.Exec) *Scope {
	ref, err := defaultBranch(run)
	if err != nil {
		return whole(err.Error())
	}
	out, err := run(nil, false, "git", "merge-base", "HEAD", ref)
	if err != nil {
		return whole(fmt.Sprintf("HEAD shares no commit with %s, so there is no base to measure a change from", ref))
	}
	s := &Scope{Ref: ref, Base: strings.TrimSpace(string(out))}
	return measure(s, root, goBin, run)
}

// ResolveSince measures the change from base, a commit the caller already
// resolved, instead of the merge base with the default branch.
//
// On a laptop it narrows to packages as Resolve does. With ci set it never
// does: the scope instead says whether every changed file is inert to Go,
// which is all a gate that reads the whole tree needs to know to be skipped.
func ResolveSince(root, goBin string, run gates.Exec, base string, ci bool) *Scope {
	return measure(&Scope{Base: base, CI: ci}, root, goBin, run)
}

// measure fills in what changed since s.Base.
func measure(s *Scope, root, goBin string, run gates.Exec) *Scope {
	files, err := changedFiles(run, s.Base)
	if err != nil {
		s.Whole = err.Error()
		return s
	}
	s.Files = files
	for _, f := range files {
		if wholeTree(f) {
			s.Whole = f + " changed since " + s.Since()
			return s
		}
	}
	if s.CI {
		s.GoInert, s.GoWhy = inert(root, files, run)
		return s
	}

	if reason := nestedReached(root, files); reason != "" {
		s.Whole = reason
		return s
	}
	module, pkgs, err := list(root, goBin, run)
	if err != nil {
		s.Whole = err.Error()
		return s
	}
	changed, reason := owners(root, files, pkgs)
	if reason != "" {
		s.Whole = reason
		return s
	}
	s.Changed = changed
	s.Reached = importers(changed, pkgs)
	s.root, s.module, s.pkgs = root, module, pkgs
	return s
}

// defaultBranch is the remote's default branch, origin/HEAD, or origin/main
// when the clone never recorded origin/HEAD.
func defaultBranch(run gates.Exec) (string, error) {
	if out, err := run(nil, false, "git", "rev-parse", "--abbrev-ref", "origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" && ref != "origin/HEAD" {
			return ref, nil
		}
	}
	if _, err := run(nil, false, "git", "rev-parse", "--verify", "--quiet", "origin/main^{commit}"); err != nil {
		return "", errors.New("there is no origin/main to measure a change from; fetch it, or run the whole bar with -all")
	}
	return "origin/main", nil
}

// changedFiles lists every path that differs from base in the working tree,
// staged or not, and every untracked path git does not ignore. A rename is
// listed as the path it left and the path it took, since both directories
// changed. Paths are relative to the root, which is the module's directory
// and need not be the repository's top level: --relative keeps the diff to
// it, as ls-files already does.
func changedFiles(run gates.Exec, base string) ([]string, error) {
	diff, err := run(nil, false, "git", "diff", "--name-only", "--no-renames", "--relative", "-z", base)
	if err != nil {
		return nil, fmt.Errorf("git cannot list what changed since %s: %w", short(base), err)
	}
	untracked, err := run(nil, false, "git", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("git cannot list the untracked files: %w", err)
	}
	var files []string
	for _, list := range [][]byte{diff, untracked} {
		for f := range strings.SplitSeq(string(list), "\x00") {
			if f != "" && !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
	}
	slices.Sort(files)
	return files, nil
}

// foreignJSON reads JSON this package does not produce. Duplicate keys,
// invalid UTF-8 and any letter case are accepted, as the tools whose output
// is read here may produce them.
var foreignJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)

// listed is one package as go list reports it.
type listed struct {
	ImportPath   string
	Deps         []string
	TestImports  []string
	XTestImports []string
	// The files the package and its tests embed, relative to its
	// directory. go:embed reads only below the package's directory, but
	// that can be another package's directory too.
	EmbedFiles      []string
	TestEmbedFiles  []string
	XTestEmbedFiles []string
	// IgnoredGoFiles are the Go files the build leaves out on this machine,
	// for another GOOS, GOARCH or a build tag, relative to the directory.
	IgnoredGoFiles []string
	dir            string

	// What the ignored files import, split by whether they are test files.
	// Another platform or tag builds them, so a package they import is
	// reached through this one.
	ignoredImports     []string
	ignoredTestImports []string
}

// embeds reports whether the package or its tests embed f, a slash path
// relative to the root.
func (p listed) embeds(f string) bool {
	for _, files := range [][]string{p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles} {
		for _, e := range files {
			if path.Join(p.dir, e) == f {
				return true
			}
		}
	}
	return false
}

// list asks the toolchain for the module's path and every package of the
// module, with what each one's build and tests import.
func list(root, goBin string, run gates.Exec) (string, []listed, error) {
	module, err := run(nil, false, goBin, "list", "-m")
	if err != nil {
		return "", nil, fmt.Errorf("go list cannot name the module at %s: %w", root, err)
	}
	prefix := strings.TrimSpace(string(module))
	// Outside a module go list -m names command-line-arguments, and in a
	// workspace it names every module, one per line; neither is one module
	// whose ./... the gates read.
	if prefix == "" || prefix == "command-line-arguments" || strings.Contains(prefix, "\n") {
		return "", nil, fmt.Errorf("%s is not one Go module, so its packages cannot be scoped", root)
	}
	out, err := run(nil, false, goBin, "list", "-e", "-json=ImportPath,Deps,TestImports,XTestImports,EmbedFiles,TestEmbedFiles,XTestEmbedFiles,IgnoredGoFiles", "./...")
	if err != nil {
		return "", nil, fmt.Errorf("go list cannot list the module's packages: %w", err)
	}
	var pkgs []listed
	dec := jsontext.NewDecoder(bytes.NewReader(out), foreignJSON)
	for {
		var p listed
		if err := json.UnmarshalDecode(dec, &p, foreignJSON); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", nil, fmt.Errorf("reading go list's packages: %w", err)
		}
		var ok bool
		if p.dir, ok = dirOf(prefix, p.ImportPath); !ok {
			continue
		}
		if err := p.readIgnoredImports(root); err != nil {
			return "", nil, err
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		return "", nil, errors.New("go list found no package in the module, so nothing can be scoped")
	}
	return prefix, pkgs, nil
}

// readIgnoredImports fills in what the package's ignored Go files import.
// Only their import lines are read; a file whose imports do not parse is an
// error, since what it imports cannot be known.
func (p *listed) readIgnoredImports(root string) error {
	fset := token.NewFileSet()
	for _, name := range p.IgnoredGoFiles {
		file := filepath.Join(root, filepath.FromSlash(p.dir), name)
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("%s is built only on another platform or under a build tag, and its imports cannot be read, "+
				"so what it imports is outside the graph: %w", path.Join(p.dir, name), err)
		}
		for _, spec := range f.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s imports %s, which is not a path: %w", path.Join(p.dir, name), spec.Path.Value, err)
			}
			if strings.HasSuffix(name, "_test.go") {
				p.ignoredTestImports = append(p.ignoredTestImports, imp)
			} else {
				p.ignoredImports = append(p.ignoredImports, imp)
			}
		}
	}
	return nil
}

// dirOf is a package's directory, its import path below the module's, and
// whether the package is the module's at all. go list's own Dir is
// absolute, and a checkout reached through a symlink reports it on the
// other side of the link.
func dirOf(module, importPath string) (string, bool) {
	switch {
	case importPath == module:
		return ".", true
	case strings.HasPrefix(importPath, module+"/"):
		return strings.TrimPrefix(importPath, module+"/"), true
	}
	return "", false
}

// owners names the packages each changed file belongs to: the package whose
// directory is the file's nearest, and every package that embeds it. A file
// in a package's directory or below it is an input of that package, whether
// it is Go source, a file the package embeds, or testdata its tests read; a
// package above it that embeds it, through a directory that is a package of
// its own, reads it too. A file under no package, such as a frontend or the
// changelog, is an input of none.
//
// A Go file in a directory go list does not report as a package is a
// package removed or moved, or one whose every file a build constraint
// excludes on this machine. Whatever imported it is outside the graph the
// scope reads, so the reason to check the whole tree is returned.
func owners(root string, files []string, pkgs []listed) ([]Package, string) {
	var changed []Package
	add := func(p listed) {
		pkg := Package{ImportPath: p.ImportPath, Dir: p.dir}
		if !slices.Contains(changed, pkg) {
			changed = append(changed, pkg)
		}
	}
	for _, f := range files {
		dir := path.Dir(f)
		if nested(root, dir) {
			continue
		}
		if strings.HasSuffix(f, ".go") && !ignored(dir) && !slices.ContainsFunc(pkgs, func(p listed) bool { return p.dir == dir }) {
			return nil, fmt.Sprintf("%s holds Go files but no package go list reports: a package removed or moved, "+
				"or one build constraints exclude here, so what imports it is outside the graph", dir)
		}
		if owner := nearest(f, pkgs); owner >= 0 {
			add(pkgs[owner])
		}
		for _, p := range pkgs {
			if p.embeds(f) {
				add(p)
			}
		}
	}
	slices.SortFunc(changed, func(a, b Package) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return changed, ""
}

// nearest is the index of the package whose directory is f's nearest, or -1
// when f sits under none.
func nearest(f string, pkgs []listed) int {
	owner := -1
	for i, p := range pkgs {
		if within(f, p.dir) && (owner < 0 || depth(p.dir) > depth(pkgs[owner].dir)) {
			owner = i
		}
	}
	return owner
}

// importers is changed and every package whose build or tests reach one of
// them: through its own imports, or through what its tests import. The
// files the build leaves out on this machine count too, since another
// platform or tag builds them.
func importers(changed []Package, pkgs []listed) []Package {
	hit := map[string]bool{}
	for _, c := range changed {
		hit[c.ImportPath] = true
	}
	// A package reaches the change when its build does, and a package that
	// imports it reaches the change in turn. Deps already holds the
	// transitive imports of the files built here, so only the ignored
	// files' imports need the walk to a fixed point.
	for grew := true; grew; {
		grew = false
		for _, p := range pkgs {
			if hit[p.ImportPath] {
				continue
			}
			if slices.ContainsFunc(p.Deps, func(d string) bool { return hit[d] }) ||
				slices.ContainsFunc(p.ignoredImports, func(d string) bool { return hit[d] }) {
				hit[p.ImportPath] = true
				grew = true
			}
		}
	}
	var reached []Package
	for _, p := range pkgs {
		if hit[p.ImportPath] ||
			slices.ContainsFunc(p.TestImports, func(d string) bool { return hit[d] }) ||
			slices.ContainsFunc(p.XTestImports, func(d string) bool { return hit[d] }) ||
			slices.ContainsFunc(p.ignoredTestImports, func(d string) bool { return hit[d] }) {
			reached = append(reached, Package{ImportPath: p.ImportPath, Dir: p.dir})
		}
	}
	slices.SortFunc(reached, func(a, b Package) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return reached
}

// depth is how many directories below the root dir is: 0 for the root.
func depth(dir string) int {
	if dir == "." {
		return 0
	}
	return strings.Count(dir, "/") + 1
}

// within reports whether the slash path f is dir or sits below it.
func within(f, dir string) bool {
	return dir == "." || f == dir || strings.HasPrefix(f, dir+"/")
}

// ignored reports whether the go command leaves dir out of ./...: a
// directory named testdata, or one whose name starts with a dot or an
// underscore, at any depth.
func ignored(dir string) bool {
	for part := range strings.SplitSeq(dir, "/") {
		if part == "testdata" || strings.HasPrefix(part, "_") || (strings.HasPrefix(part, ".") && part != ".") {
			return true
		}
	}
	return false
}

// nested reports whether dir, a slash path relative to root, sits under a
// go.mod other than the root's: a module of its own, which ./... does not
// reach.
func nested(root, dir string) bool {
	for dir != "." && dir != "" && dir != "/" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "go.mod")); err == nil {
			return true
		}
		dir = path.Dir(dir)
	}
	return false
}

// nestedModule is the directory of the module that holds dir, a slash path
// relative to root: the nearest ancestor with a go.mod of its own, or ""
// when dir belongs to the root module.
func nestedModule(root, dir string) string {
	for dir != "." && dir != "" && dir != "/" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "go.mod")); err == nil {
			return dir
		}
		dir = path.Dir(dir)
	}
	return ""
}

// nestedReached returns why the whole tree is checked when a changed file
// sits in a nested module the root module reaches through a replace
// directive in go.mod or a use line in go.work, both pointing at a local
// directory. Root packages build against that directory, but their import
// graph ends at the module boundary. A nested module the root does not
// reach is not part of this tree's build and stays out.
func nestedReached(root string, files []string) string {
	var modules []string
	for _, f := range files {
		if m := nestedModule(root, path.Dir(f)); m != "" && !slices.Contains(modules, m) {
			modules = append(modules, m)
		}
	}
	if len(modules) == 0 {
		return ""
	}
	targets, err := localTargets(root)
	if err != nil {
		return fmt.Sprintf("%s changed in a nested module, and whether the root module reaches it cannot be told: %v", modules[0], err)
	}
	for _, m := range modules {
		if how, ok := targets[m]; ok {
			return fmt.Sprintf("%s is a nested module the root module reaches through %s, so what imports it is outside the graph",
				m, how)
		}
	}
	return ""
}

// localTargets maps each local directory the root module's go.mod replaces
// to, and each one go.work uses, to the directive that names it.
func localTargets(root string) (map[string]string, error) {
	targets := map[string]string{}
	rel := func(p string) (string, bool) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(p))
		}
		r, err := filepath.Rel(root, p)
		if err != nil || r == "." || strings.HasPrefix(r, "..") {
			return "", false
		}
		return filepath.ToSlash(r), true
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		mod, err := modfile.Parse("go.mod", data, nil)
		if err != nil {
			return nil, err
		}
		for _, r := range mod.Replace {
			if r.New.Version == "" {
				if d, ok := rel(r.New.Path); ok {
					targets[d] = "a replace directive in go.mod (=> " + r.New.Path + ")"
				}
			}
		}
	}
	data, err = os.ReadFile(filepath.Join(root, "go.work"))
	if errors.Is(err, os.ErrNotExist) {
		return targets, nil
	}
	if err != nil {
		return nil, err
	}
	work, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		return nil, err
	}
	for _, u := range work.Use {
		if d, ok := rel(u.Path); ok {
			targets[d] = "a use line in go.work (" + u.Path + ")"
		}
	}
	return targets, nil
}

// short is a commit as a reader sees it.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
