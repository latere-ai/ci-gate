// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/gates"
)

// links are the packages of the module the test binary of pkg links, as
// the graph Resolve listed shows them.
func (s *Scope) links(pkg string) []string {
	byPath := map[string]listed{}
	for _, p := range s.pkgs {
		byPath[p.ImportPath] = p
	}
	p := byPath[pkg]
	var out []string
	for _, imp := range slices.Concat([]string{pkg}, p.TestImports, p.XTestImports) {
		if d, ok := byPath[imp]; ok {
			out = append(out, imp)
			out = append(out, d.Deps...)
		}
	}
	return out
}

// unchanged keeps the module's packages in set that the change did not
// touch, sorted.
func (s *Scope) unchanged(set map[string]bool) []string {
	var out []string
	for p := range set {
		if _, ok := dirOf(s.module, p); !ok || gates.InNodeModules(p) {
			continue
		}
		if slices.ContainsFunc(s.Changed, func(c Package) bool { return c.ImportPath == p }) {
			continue
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// Contributors are the packages whose test binaries link one of pkgs and
// whose tests are outside Reached: with the Reached run, every test that
// can cover them, which is what gives each the figure the whole suite
// would.
func (s *Scope) Contributors(pkgs []string) []string {
	var named []Package
	for _, p := range s.pkgs {
		if slices.Contains(pkgs, p.ImportPath) {
			named = append(named, Package{ImportPath: p.ImportPath, Dir: p.dir})
		}
	}
	var out []string
	for _, p := range importers(named, s.pkgs) {
		if !slices.Contains(s.Reached, p) {
			out = append(out, p.ImportPath)
		}
	}
	return out
}

// Baseline is the base, rebuilt in place for the go command. An overlay
// gives it each changed file a package reads, as the file was at the base
// or as absent where the change added it, so go test under the overlay is
// the base's run of the same tests, against the checkout's warm build
// cache.
//
// The overlay reaches what the go command reads: source, and the files a
// package embeds. A test that opens a file at run time reads it from disk,
// as the change left it, so a package whose tests read a changed file that
// way cannot replay its base.
type Baseline struct {
	// Overlay is the file go test's -overlay takes.
	Overlay string
	// Tests are the Reached packages whose tests replay the base: each
	// had a test at the base, and no file it reads at run time changed.
	Tests []string
	// Untouched are the packages the change did not touch that a Reached
	// test binary links now or linked at the base. A test binary outside
	// Reached links no changed package and holds no changed file, so it is
	// the base's, and what it covers it still covers: an untouched
	// package's coverage can move only through the Reached tests. A
	// package the base's tests reached only through an import the change
	// removed is one of these.
	Untouched []string
	// Unreplayed are the Untouched packages a test binary links whose
	// base cannot be replayed: what it covered there is not known.
	Unreplayed []string
}

// buildSource are the extensions of the files the go command compiles
// from a package's own directory. Any other file a package holds is
// either embedded, which the overlay reaches, or read at run time, which
// it does not.
var buildSource = []string{
	".go", ".c", ".h", ".s", ".S", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx",
	".m", ".f", ".F", ".for", ".f90", ".syso", ".swig", ".swigcxx",
}

// Baseline writes the overlay into dir, which the caller removes, and asks
// the go command what the Reached tests were at the base.
func (s *Scope) Baseline(dir, goBin string, run gates.Exec) (*Baseline, error) {
	if !s.Narrows() || len(s.Reached) == 0 {
		return nil, errors.New("the scope holds no change to rebuild a base for")
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return nil, fmt.Errorf("resolving the module's directory: %w", err)
	}
	read := s.read()
	present, err := s.atBase(run, slices.Sorted(maps.Keys(read)))
	if err != nil {
		return nil, err
	}
	overlay, err := s.overlay(run, dir, root, read, present)
	if err != nil {
		return nil, err
	}
	tests, err := s.baseTests(goBin, run, overlay)
	if err != nil {
		return nil, err
	}

	// A file is replayed when the go command reads it: source in the
	// directory of the package nearest it, or a file a package embedded at
	// the base. Anything else the nearest package holds, testdata above
	// all, is read by its tests from disk.
	unreplayed := map[string]bool{}
	for f, owner := range read {
		if tests.embedded[f] {
			continue
		}
		if p := s.pkgs[slices.IndexFunc(s.pkgs, func(p listed) bool { return p.ImportPath == owner })]; path.Dir(f) == p.dir && slices.Contains(buildSource, path.Ext(f)) {
			continue
		}
		unreplayed[owner] = true
	}

	b := &Baseline{Overlay: overlay}
	linked, lost := map[string]bool{}, map[string]bool{}
	for _, r := range s.Reached {
		for _, p := range slices.Concat(s.links(r.ImportPath), tests.links[r.ImportPath]) {
			linked[p] = true
			if unreplayed[r.ImportPath] {
				lost[p] = true
			}
		}
		if _, ok := tests.links[r.ImportPath]; ok && !unreplayed[r.ImportPath] {
			b.Tests = append(b.Tests, r.ImportPath)
		}
	}
	b.Untouched, b.Unreplayed = s.unchanged(linked), s.unchanged(lost)
	return b, nil
}

// read are the changed files a package of the module reads, each with the
// import path of the package nearest it. A file a package embeds sits below
// that package's directory, so it has a nearest package too.
func (s *Scope) read() map[string]string {
	read := map[string]string{}
	for _, f := range s.Files {
		if i := nearest(f, s.pkgs); i >= 0 && !nested(s.root, path.Dir(f)) {
			read[f] = s.pkgs[i].ImportPath
		}
	}
	return read
}

// atBase reports which of files the base holds.
func (s *Scope) atBase(run gates.Exec, files []string) (map[string]bool, error) {
	out, err := run(nil, false, "git", slices.Concat([]string{"ls-tree", "-r", "-z", "--name-only", s.Base, "--"}, files)...)
	if err != nil {
		return nil, fmt.Errorf("git cannot list the files %s holds: %w", short(s.Base), err)
	}
	present := map[string]bool{}
	for f := range strings.SplitSeq(string(out), "\x00") {
		if f != "" {
			present[f] = true
		}
	}
	return present, nil
}

// overlay writes each file as the base held it under dir, and the overlay
// that maps the checkout's path to it, or to nothing for a file the base
// did not hold.
func (s *Scope) overlay(run gates.Exec, dir, root string, read map[string]string, present map[string]bool) (string, error) {
	replace := map[string]string{}
	for i, f := range slices.Sorted(maps.Keys(read)) {
		at := filepath.Join(root, filepath.FromSlash(f))
		if !present[f] {
			replace[at] = ""
			continue
		}
		blob, err := run(nil, false, "git", "show", s.Base+":./"+f)
		if err != nil {
			return "", fmt.Errorf("git cannot show %s as %s held it: %w", f, short(s.Base), err)
		}
		backing := filepath.Join(dir, strconv.Itoa(i), path.Base(f))
		if err := os.MkdirAll(filepath.Dir(backing), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(backing, blob, 0o644); err != nil {
			return "", err
		}
		replace[at] = backing
	}
	data, err := json.Marshal(struct{ Replace map[string]string }{replace})
	if err != nil {
		return "", err
	}
	overlay := filepath.Join(dir, "overlay.json")
	return overlay, os.WriteFile(overlay, data, 0o644)
}

// baseTests is what go list says of the Reached packages at the base.
type baseTests struct {
	// links maps each Reached package that had a test at the base to the
	// packages its test binary linked.
	links map[string][]string
	// embedded are the files a Reached package embedded at the base.
	embedded map[string]bool
}

// baseTests asks go list, under the overlay, for each Reached package and
// its test binary as the base had them.
func (s *Scope) baseTests(goBin string, run gates.Exec, overlay string) (baseTests, error) {
	var pkgs []string
	for _, r := range s.Reached {
		pkgs = append(pkgs, r.ImportPath)
	}
	out, err := run(nil, false, goBin, slices.Concat([]string{"list", "-overlay=" + overlay, "-e", "-test",
		"-json=ImportPath,Deps,Error,EmbedFiles,TestEmbedFiles,XTestEmbedFiles"}, pkgs)...)
	if err != nil {
		return baseTests{}, fmt.Errorf("go list cannot list the base's tests: %w", err)
	}
	b := baseTests{links: map[string][]string{}, embedded: map[string]bool{}}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p struct {
			ImportPath                                  string
			Deps                                        []string
			Error                                       *struct{ Err string }
			EmbedFiles, TestEmbedFiles, XTestEmbedFiles []string
		}
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return baseTests{}, fmt.Errorf("reading go list's base packages: %w", err)
		}
		// A test binary is listed as pkg.test, and the packages it builds
		// for itself as "pkg [pkg.test]": the same package, so the same
		// coverage blocks.
		if pkg, ok := strings.CutSuffix(p.ImportPath, ".test"); ok && !strings.Contains(pkg, " ") {
			for _, d := range p.Deps {
				d, _, _ = strings.Cut(d, " ")
				b.links[pkg] = append(b.links[pkg], d)
			}
			continue
		}
		dir, ok := dirOf(s.module, p.ImportPath)
		if !ok || p.Error != nil {
			continue
		}
		for _, e := range slices.Concat(p.EmbedFiles, p.TestEmbedFiles, p.XTestEmbedFiles) {
			b.embedded[path.Join(dir, e)] = true
		}
	}
	return b, nil
}
