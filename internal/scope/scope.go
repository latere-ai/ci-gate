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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

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
}

// Narrows reports whether the scope holds: a nil scope, or one that fell
// back to the whole tree, narrows nothing.
func (s *Scope) Narrows() bool { return s != nil && s.Whole == "" }

// Since names the base for a reader: the branch and the commit.
func (s *Scope) Since() string {
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

// wholeTreeFiles are the files whose change can move any gate's verdict on
// any package: the module's dependencies and the bar's own configuration.
var wholeTreeFiles = []string{
	"go.mod", "go.sum", "go.work", "go.work.sum",
	config.Name, ".golangci.yml", ".golangci.yaml",
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

	files, err := changedFiles(run, s.Base)
	if err != nil {
		s.Whole = err.Error()
		return s
	}
	s.Files = files
	for _, f := range files {
		if slices.Contains(wholeTreeFiles, f) {
			s.Whole = f + " changed since " + s.Since()
			return s
		}
	}

	pkgs, err := list(root, goBin, run)
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
// changed.
func changedFiles(run gates.Exec, base string) ([]string, error) {
	diff, err := run(nil, false, "git", "diff", "--name-only", "--no-renames", "-z", base)
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

// listed is one package as go list reports it.
type listed struct {
	ImportPath   string
	Deps         []string
	TestImports  []string
	XTestImports []string
	dir          string
}

// list asks the toolchain for every package of the module, with what each
// one's build and tests import.
func list(root, goBin string, run gates.Exec) ([]listed, error) {
	module, err := run(nil, false, goBin, "list", "-m")
	if err != nil {
		return nil, fmt.Errorf("go list cannot name the module at %s: %w", root, err)
	}
	prefix := strings.TrimSpace(string(module))
	// Outside a module go list -m names command-line-arguments, and in a
	// workspace it names every module, one per line; neither is one module
	// whose ./... the gates read.
	if prefix == "" || prefix == "command-line-arguments" || strings.Contains(prefix, "\n") {
		return nil, fmt.Errorf("%s is not one Go module, so its packages cannot be scoped", root)
	}
	out, err := run(nil, false, goBin, "list", "-e", "-json=ImportPath,Deps,TestImports,XTestImports", "./...")
	if err != nil {
		return nil, fmt.Errorf("go list cannot list the module's packages: %w", err)
	}
	var pkgs []listed
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("reading go list's packages: %w", err)
		}
		// A package's directory is its import path below the module's: go
		// list's own Dir is absolute, and a checkout reached through a
		// symlink reports it on the other side of the link.
		switch {
		case p.ImportPath == prefix:
			p.dir = "."
		case strings.HasPrefix(p.ImportPath, prefix+"/"):
			p.dir = strings.TrimPrefix(p.ImportPath, prefix+"/")
		default:
			continue
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		return nil, errors.New("go list found no package in the module, so nothing can be scoped")
	}
	return pkgs, nil
}

// owners names the package each changed file belongs to: the package whose
// directory is the file's nearest. A file in a package's directory or below
// it is an input of that package, whether it is Go source, a file the
// package embeds, or testdata its tests read. A file under no package, such
// as a frontend or the changelog, is an input of none.
//
// A Go file that is gone, from a directory that no longer holds a package,
// is a package removed or moved, and whatever imported it is not in the
// graph any more, so the reason to check the whole tree is returned.
func owners(root string, files []string, pkgs []listed) ([]Package, string) {
	var changed []Package
	for _, f := range files {
		if nested(root, path.Dir(f)) {
			continue
		}
		owner := -1
		for i, p := range pkgs {
			if within(f, p.dir) && (owner < 0 || depth(p.dir) > depth(pkgs[owner].dir)) {
				owner = i
			}
		}
		if strings.HasSuffix(f, ".go") && removed(root, f) && (owner < 0 || pkgs[owner].dir != path.Dir(f)) && !ignored(path.Dir(f)) {
			return nil, fmt.Sprintf("the package in %s was removed or moved, so what imported it is not in the graph", path.Dir(f))
		}
		if owner < 0 {
			continue
		}
		p := Package{ImportPath: pkgs[owner].ImportPath, Dir: pkgs[owner].dir}
		if !slices.Contains(changed, p) {
			changed = append(changed, p)
		}
	}
	slices.SortFunc(changed, func(a, b Package) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return changed, ""
}

// importers is changed and every package whose build or tests reach one of
// them: through its own imports, or through what its tests import.
func importers(changed []Package, pkgs []listed) []Package {
	hit := map[string]bool{}
	for _, c := range changed {
		hit[c.ImportPath] = true
	}
	deps := map[string][]string{}
	for _, p := range pkgs {
		deps[p.ImportPath] = p.Deps
	}
	reaches := func(imp string) bool {
		if hit[imp] {
			return true
		}
		return slices.ContainsFunc(deps[imp], func(d string) bool { return hit[d] })
	}
	var reached []Package
	for _, p := range pkgs {
		if reaches(p.ImportPath) ||
			slices.ContainsFunc(p.TestImports, reaches) ||
			slices.ContainsFunc(p.XTestImports, reaches) {
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

// removed reports whether the file is gone from the working tree.
func removed(root, f string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(f)))
	return errors.Is(err, os.ErrNotExist)
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

// short is a commit as a reader sees it.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
