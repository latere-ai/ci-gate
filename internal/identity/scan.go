// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

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
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// deployDir holds the manifests the deployment rules read.
const deployDir = "deploy"

// archiveDir is the path segment that makes a document a record of what was
// once true rather than a description of what is.
const archiveDir = ".archive"

// sourceFile is a file the scans read as text, so a finding can name a line.
type sourceFile struct {
	rel   string
	text  string
	lines []string
}

// goFile is a parsed non-test Go file.
type goFile struct {
	sourceFile
	file    *ast.File
	imports []string
}

// imported reports whether the file imports a path, or anything under it.
func (g goFile) imported(prefix string) bool {
	for _, ip := range g.imports {
		if ip == prefix || strings.HasPrefix(ip, prefix+"/") {
			return true
		}
	}
	return false
}

// manifest is one deployment file and the documents it holds. err records a
// file the decoder stopped on, which the deployment rules report rather than
// pass over: a manifest nothing could read is a manifest nothing checked.
type manifest struct {
	sourceFile
	docs []any
	err  error
}

// tree is every scan target of one repository, read once and shared by the
// rules, so a rule is a decision over the tree rather than a walk of it.
type tree struct {
	cfg       config.Identity
	root      string
	exec      gates.Exec
	fset      *token.FileSet
	goFiles   []goFile
	docs      []sourceFile
	manifests []manifest
	// binaries are the commands this repository builds, which is how a
	// container is recognised as running it.
	binaries []string
}

// scan reads the tree once.
func scan(cfg config.Identity, root string, exec gates.Exec) (*tree, error) {
	t := &tree{cfg: cfg, root: root, exec: exec, fset: token.NewFileSet()}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() {
			if p == root {
				return nil
			}
			if name == ".git" || name == ".claude" || name == "node_modules" || name == "testdata" || t.skipped(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if t.skipped(rel) {
			return nil
		}
		switch {
		case strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go"):
			return t.readGo(p, rel)
		case strings.HasSuffix(name, ".md") && !archived(rel):
			return t.readText(p, rel)
		case isManifest(rel, name):
			return t.readManifest(p, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the tree for the identity rules: %w", err)
	}
	t.binaries = commands(root)
	return t, nil
}

// skipped reports whether the block named this path, as a directory or a
// file. A path skipped here is one the rules assert nothing about.
func (t *tree) skipped(rel string) bool {
	for _, s := range t.cfg.Skip {
		s = strings.Trim(filepath.ToSlash(strings.TrimSpace(s)), "/")
		if s != "" && (rel == s || strings.HasPrefix(rel, s+"/")) {
			return true
		}
	}
	return false
}

// archived reports whether a path is inside an archive, where a document
// records what was once true.
func archived(rel string) bool {
	return slices.Contains(strings.Split(rel, "/"), archiveDir)
}

// isManifest reports whether a file is a deployment document.
func isManifest(rel, name string) bool {
	if !strings.HasPrefix(rel, deployDir+"/") {
		return false
	}
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

func read(p, rel string) (sourceFile, error) {
	body, err := os.ReadFile(p)
	if err != nil {
		return sourceFile{}, err
	}
	text := string(body)
	return sourceFile{rel: rel, text: text, lines: strings.Split(text, "\n")}, nil
}

func (t *tree) readText(p, rel string) error {
	s, err := read(p, rel)
	if err != nil {
		return err
	}
	t.docs = append(t.docs, s)
	return nil
}

// readGo parses one file. A file the parser cannot read is a build failure,
// which another gate reports; there is nothing here to decide about it.
func (t *tree) readGo(p, rel string) error {
	s, err := read(p, rel)
	if err != nil {
		return err
	}
	file, parseErr := parser.ParseFile(t.fset, p, s.text, parser.SkipObjectResolution)
	if parseErr != nil {
		//nolint:nilerr // a file that does not parse is a build failure, which another gate reports
		return nil
	}
	g := goFile{sourceFile: s, file: file}
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		ip, unquoteErr := strconv.Unquote(imp.Path.Value)
		if unquoteErr != nil {
			continue
		}
		g.imports = append(g.imports, ip)
	}
	t.goFiles = append(t.goFiles, g)
	return nil
}

func (t *tree) readManifest(p, rel string) error {
	s, err := read(p, rel)
	if err != nil {
		return err
	}
	m := manifest{sourceFile: s}
	dec := yaml.NewDecoder(strings.NewReader(s.text))
	for {
		var doc any
		decErr := dec.Decode(&doc)
		if errors.Is(decErr, io.EOF) {
			break
		}
		if decErr != nil {
			m.err = decErr
			break
		}
		m.docs = append(m.docs, doc)
	}
	t.manifests = append(t.manifests, m)
	return nil
}

// commands lists the binaries a repository builds, by the directories under
// cmd/. A container whose image names one of them runs this repository.
func commands(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// line reports the 1-based line of a position in a parsed file.
func (t *tree) line(pos token.Pos) int { return t.fset.Position(pos).Line }

// passthrough reports whether a file is one the block admits as a place a
// claim may be named, because forwarding a claim is what that file does.
func (t *tree) passthrough(rel string) bool {
	for _, s := range t.cfg.ClaimsPassthrough {
		s = strings.Trim(filepath.ToSlash(strings.TrimSpace(s)), "/")
		if s != "" && (rel == s || strings.HasPrefix(rel, s+"/")) {
			return true
		}
	}
	return false
}

// claiming lists the Go files a claim rule reads: every non-test file the
// block does not admit as a passthrough.
func (t *tree) claiming() []goFile {
	var out []goFile
	for _, g := range t.goFiles {
		if !t.passthrough(g.rel) {
			out = append(out, g)
		}
	}
	return out
}

// inDir reports whether any segment of a path is a directory name.
func inDir(rel, dir string) bool {
	return slices.Contains(strings.Split(path.Dir(rel), "/"), dir)
}

// stringLiterals collects every string literal under a node, so a literal
// built by concatenation or handed to a formatter is still read.
func stringLiterals(n ast.Node) []*ast.BasicLit {
	var out []*ast.BasicLit
	ast.Inspect(n, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			out = append(out, lit)
		}
		return true
	})
	return out
}

// literalText unquotes a literal, or reports that it could not be read.
func literalText(lit *ast.BasicLit) (string, bool) {
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
