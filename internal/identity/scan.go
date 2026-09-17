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
	// frontendFiles are the browser sources the roles rule reads. They are their
	// own bucket rather than part of everyFile: a rule that reads them says
	// so, and the rules that read Go, documents and manifests keep the scan
	// target they were written against.
	frontendFiles []sourceFile
	// binaries are the commands this repository builds, which is how a
	// container is recognised as running it.
	binaries []string
	// overlayHosts are the addresses the declared overlays carry, read from
	// the overlays themselves so the exemption is a declaration rather than
	// a list of names beside the tree.
	overlayHosts map[string]bool
	// module is the module path of the tree, which is how a repository says
	// it is the shared package rather than one of its callers.
	module string
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
		case isFrontendSource(rel, name):
			return t.readFrontendSource(p, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the tree for the identity rules: %w", err)
	}
	t.module = modulePath(root)
	t.binaries = t.commands()
	t.dropIgnoredFrontend()
	if err := t.readOverlays(); err != nil {
		return nil, err
	}
	if err := t.checkEnvelopeExempt(); err != nil {
		return nil, err
	}
	return t, nil
}

// checkEnvelopeExempt refuses a declared exemption the tree does not hold.
// An exemption that matches nothing hides a typo, and a typo that lowers the
// bar is the failure this binary is against.
func (t *tree) checkEnvelopeExempt() error {
	for _, rel := range clean(t.cfg.EnvelopeExempt) {
		if _, err := os.Stat(filepath.Join(t.root, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("%s: identity.envelope_exempt names %s, which this tree does not hold\n"+
				"name the file whose type carries the envelope's field names for a reason "+
				"of its own, or delete the entry", config.Name, rel)
		}
	}
	return nil
}

// readOverlays collects the addresses each declared overlay carries.
//
// The overlays are read here and not by the walk, because a repository that
// declares one usually skips it as well, and a value the rules exempt must
// come from the file that sets it rather than from a name written beside it.
// A declared path the tree does not hold stops the run: an exemption that
// matches nothing hides a typo, and a typo that lowers the bar is the
// failure this binary is against.
func (t *tree) readOverlays() error {
	t.overlayHosts = map[string]bool{}
	for _, rel := range clean(t.cfg.Overlays) {
		p := filepath.Join(t.root, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s: identity.overlays names %s, which this tree does not hold\n"+
				"name the directory the company's own overlay is in, or delete the entry",
				config.Name, rel)
		}
		err := filepath.WalkDir(p, func(fp string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			body, readErr := os.ReadFile(fp)
			if readErr != nil {
				return readErr
			}
			for line := range strings.SplitSeq(string(body), "\n") {
				for _, h := range hosts(setting(line)) {
					t.overlayHosts[h] = true
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("reading the overlay %s: %w", rel, err)
		}
	}
	return nil
}

// setting is the part of a line before a comment, so an address an overlay
// mentions in prose is not one it sets.
func setting(line string) string {
	for i := range len(line) {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

// overlay reports whether a path is inside a declared overlay, which is
// where one company's own values belong.
func (t *tree) overlay(rel string) bool { return under(rel, t.cfg.Overlays) }

// carries reports whether a declared overlay sets this address.
func (t *tree) carries(host string) bool { return t.overlayHosts[host] }

// clean normalises the paths of one block list, dropping the empty ones.
func clean(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = strings.Trim(filepath.ToSlash(strings.TrimSpace(p)), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// under reports whether a path is one of a block list's entries, or inside
// one, which is how every list of paths in the block names a tree.
func under(rel string, paths []string) bool {
	for _, p := range clean(paths) {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// skipped reports whether the block named this path, as a directory or a
// file. A path skipped here is one the rules assert nothing about.
func (t *tree) skipped(rel string) bool { return under(rel, t.cfg.Skip) }

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

// frontendExts are the browser sources a decision can be written in. A page
// that branches on a retired flag decides access as surely as a handler does,
// and the Go half of this scan cannot see it.
var frontendExts = []string{".ts", ".tsx", ".vue", ".svelte", ".js"}

// frontendSkipDirs hold what no person in this repository wrote: a package
// manager's tree, a bundler's output, a fixture. They are skipped here and not
// in the walk, because the walk's skip list feeds every rule, and a rule that
// reads less than it did is a rule that catches less.
var frontendSkipDirs = []string{"node_modules", "dist", "build", "testdata"}

// generatedMarkers are the headers a tool leaves on a file it owns.
var generatedMarkers = []string{"@generated", "Code generated by", "DO NOT EDIT"}

// isFrontendSource reports whether a file is a frontend source read as a decision.
//
// A test is not one. A test asserts, and the assertion a repository writes
// after retiring the flag is that the flag confers nothing, which names it:
// read as a decision, the regression test would be the finding. The telling is
// the path and never the assertion inside it, the same way the Go half of this
// scan skips _test.go without reading what the test claims. The frontend
// spelling of that suffix is a `.test` or `.spec` segment before the
// extension, or a `__tests__` directory.
func isFrontendSource(rel, name string) bool {
	if !slices.Contains(frontendExts, path.Ext(name)) {
		return false
	}
	if archived(rel) {
		return false
	}
	for seg := range strings.SplitSeq(path.Dir(rel), "/") {
		if seg == "__tests__" || slices.Contains(frontendSkipDirs, seg) {
			return false
		}
	}
	base := strings.TrimSuffix(name, path.Ext(name))
	for _, suffix := range []string{".test", ".spec", ".min", ".bundle"} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	return true
}

// generated reports whether a tool wrote the file, read from the header the
// tool leaves on it. A generated bundle is the build's output; the decision
// belongs to the source it was built from, and that source is read.
func generated(text string) bool {
	head := text
	if lines := strings.SplitN(text, "\n", 6); len(lines) > 5 {
		head = strings.Join(lines[:5], "\n")
	}
	for _, m := range generatedMarkers {
		if strings.Contains(head, m) {
			return true
		}
	}
	return false
}

// dropIgnoredFrontend removes the frontend sources the repository ignores.
//
// A frontend tree holds build residue beside its sources. One repository's
// .gitignore names `frontend/src/**/*.js`, where the Vue toolchain leaves a
// `.vue.js` sidecar next to every component; those files are on a laptop and
// not in a checkout, so reading them makes the gate red locally and green on
// the runner, which is the one thing a gate must never be. What git ignores is
// not the repository, and the directory names this scan carries are that same
// statement written a second time.
//
// git deciding nothing, because there is no repository, no git, or nothing
// ignored, leaves every file read. A scan that reads too much reports a
// finding a person can see and argue with; a scan that reads too little
// reports nothing at all, and that is the failure this binary is against.
func (t *tree) dropIgnoredFrontend() {
	ignored := map[string]bool{}
	// In batches, because the paths go on a command line.
	const batch = 1000
	for i := 0; i < len(t.frontendFiles); i += batch {
		args := []string{"check-ignore"}
		for _, f := range t.frontendFiles[i:min(i+batch, len(t.frontendFiles))] {
			args = append(args, f.rel)
		}
		// A non-zero status is how git says nothing in the batch is ignored.
		out, _ := t.exec(nil, false, "git", args...)
		for line := range strings.SplitSeq(string(out), "\n") {
			if rel := strings.TrimSpace(line); rel != "" {
				ignored[filepath.ToSlash(rel)] = true
			}
		}
	}
	if len(ignored) == 0 {
		return
	}
	kept := t.frontendFiles[:0]
	for _, f := range t.frontendFiles {
		if !ignored[f.rel] {
			kept = append(kept, f)
		}
	}
	t.frontendFiles = kept
}

func (t *tree) readFrontendSource(p, rel string) error {
	s, err := read(p, rel)
	if err != nil {
		return err
	}
	if generated(s.text) {
		return nil
	}
	t.frontendFiles = append(t.frontendFiles, s)
	return nil
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
	if record(rel, s.text, t.cfg.Settled) {
		return nil
	}
	t.docs = append(t.docs, s)
	return nil
}

// record reports whether a document records what was once true rather than
// describing the system that exists: a changelog, a release note, or a spec
// whose status the tree calls settled. A record may name the mechanism it
// retired; the rules that read documents do not read records.
func record(rel, text string, settled []string) bool {
	base := strings.ToLower(rel[strings.LastIndex(rel, "/")+1:])
	if strings.HasPrefix(base, "changelog") || strings.Contains(base, "release-notes") {
		return true
	}
	if !strings.HasPrefix(rel, "specs/") {
		return false
	}
	status := frontmatterStatus(text)
	return status == "archived" || slices.Contains(settled, status)
}

// frontmatterStatus reads status: from a leading YAML block, or "".
func frontmatterStatus(text string) string {
	if !strings.HasPrefix(text, "---\n") {
		return ""
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return ""
	}
	for line := range strings.SplitSeq(text[4:4+end], "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "status:"); ok {
			return strings.Trim(strings.TrimSpace(v), "\"'")
		}
	}
	return ""
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

// commands lists the binaries a repository builds. A container whose image
// names one of them runs this repository.
//
// Three things name a binary. A directory under cmd/ is one, and is the
// shape most repositories have. A repository whose only main package is the
// module root is the second: it builds one binary, go build calls it the
// last segment of the module path, and there is no cmd/ to read it from. The
// third is the block's own image, for a workload whose image was built under
// neither name; it is a declaration rather than a guess, because an image
// name a rule inferred would be a rule that stops checking as soon as it
// infers wrong.
func (t *tree) commands() []string {
	var out []string
	if entries, err := os.ReadDir(filepath.Join(t.root, "cmd")); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, e.Name())
			}
		}
	}
	if name := t.rootCommand(); name != "" {
		out = append(out, name)
	}
	if image := strings.TrimSpace(t.cfg.Image); image != "" {
		out = append(out, image)
	}
	return out
}

// rootCommand is what the module root builds when the root is itself a
// command, and "" when it is not. The package clause of a file at the root
// says whether it is one; the module path says what the binary is called.
func (t *tree) rootCommand() string {
	root := false
	for _, g := range t.goFiles {
		if !strings.Contains(g.rel, "/") && g.file.Name != nil && g.file.Name.Name == "main" {
			root = true
			break
		}
	}
	if !root {
		return ""
	}
	if t.module == "" {
		return ""
	}
	return path.Base(t.module)
}

// modulePath is the module line of go.mod, or "" where there is none.
func modulePath(root string) string {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// line reports the 1-based line of a position in a parsed file.
func (t *tree) line(pos token.Pos) int { return t.fset.Position(pos).Line }

// passthrough reports whether a file is one the block admits as a place a
// claim may be named, because forwarding a claim is what that file does.
func (t *tree) passthrough(rel string) bool { return under(rel, t.cfg.ClaimsPassthrough) }

// claiming lists the Go files a claim rule reads: every non-test file the
// block does not admit as a passthrough.
// frontend reports whether a file is one the block names as the
// repository's browser frontend, which forwards the person's own token to
// the issuer's API and so may name the issuer's paths.
func (t *tree) frontend(rel string) bool { return under(rel, t.cfg.BFF) }

// shared reports whether this tree is the package the envelope is declared
// in, where declaring it is the point.
func (t *tree) shared() bool {
	return t.module == sharedModule || strings.HasPrefix(t.module, sharedModule+"/")
}

// declaring lists the Go files the envelope rule reads: every non-test file
// the block does not exempt.
func (t *tree) declaring() []goFile {
	var out []goFile
	for _, g := range t.goFiles {
		if !under(g.rel, t.cfg.EnvelopeExempt) {
			out = append(out, g)
		}
	}
	return out
}

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

// inspect walks a file's syntax without descending into an import spec.
//
// An import path is a string in the grammar and a dependency in the file:
// nothing the file says, and nothing a reader of it could rewrite without
// dropping the package. A rule that reads it as a literal reports the name
// of what a file imports as a value the file carries, which is how three
// conformance files came to be skipped for importing a test double. The
// rules that ask what a file imports read the import list itself.
func inspect(n ast.Node, fn func(ast.Node) bool) {
	ast.Inspect(n, func(n ast.Node) bool {
		if _, ok := n.(*ast.ImportSpec); ok {
			return false
		}
		return fn(n)
	})
}

// stringLiterals collects every string literal under a node, so a literal
// built by concatenation or handed to a formatter is still read. An import
// path is not one of them.
func stringLiterals(n ast.Node) []*ast.BasicLit {
	var out []*ast.BasicLit
	inspect(n, func(n ast.Node) bool {
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
