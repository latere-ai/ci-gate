// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package contract reports the ways a repository has drifted from the shape
// every repository shares.
//
// The gates live in the binary, so there is nothing per repository to check
// about them. What remains is the wiring that connects the binary to the
// places it is invoked from: a workflow caller, a hook, a gitignore, a pin.
// Each is a file that can drift, and this package reads all of them and
// names every difference in one run. Nothing here is waivable: a waiver
// says a repository is behind on a gate and names the day it catches up,
// and wiring is fixed in the commit that notices it.
package contract

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"latere.ai/x/ci-gate/internal/bar"
	"latere.ai/x/ci-gate/internal/changelog"
	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
)

// Workflow is the reusable workflow every repository calls.
const Workflow = "latere-ai/ci/.github/workflows/lateregate.yml@v1"

// oldWorkflow is the one it replaces, reported by name because the fix
// differs from the fix for no workflow at all.
const oldWorkflow = "latere-ai/ci/.github/workflows/go-verify.yml@"

// Caller is the workflow file init writes. It is the whole caller.
const Caller = `name: ci

# Every gate lives in lateregate, pinned in go.mod. The reusable workflow
# asks the binary which gates apply and runs one job per gate.

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  gate:
    uses: ` + Workflow + `
`

// ToolPath is the go.mod tool line every consumer carries.
const ToolPath = "latere.ai/x/ci-gate/cmd/lateregate"

// Ignored are the generated files .gitignore must list.
var Ignored = []string{golangci.Name, "coverage.out"}

// hookPath and prepushPath are where git looks once core.hooksPath is set.
const (
	hookPath    = ".githooks/pre-commit"
	prepushPath = ".githooks/pre-push"
)

// Run reports every drift, or prints what it checked.
func Run(root string, cfg *config.Config, out io.Writer, exec gates.Exec) error {
	var findings []string
	note := func(f string) { findings = append(findings, f) }

	if f := checkWorkflow(root); f != "" {
		note(f)
	}
	if f := checkHook(root); f != "" {
		note(f)
	}
	if f := checkPrepush(root); f != "" {
		note(f)
	}
	if f := checkGolangci(root, cfg, exec); f != "" {
		note(f)
	}
	if f := checkGitignore(root); f != "" {
		note(f)
	}
	if f := checkChangelog(root, exec); f != "" {
		note(f)
	}
	if f := checkIdentity(cfg); f != "" {
		note(f)
	}
	if len(cfg.Restated) > 0 {
		note(fmt.Sprintf("%s restates a default: %s\n\tdelete the key; a restated default is the line the next default change makes wrong",
			config.Name, strings.Join(cfg.Restated, ", ")))
	}
	handRolled, err := checkMakefile(root, exec)
	if err != nil {
		return err
	}
	if handRolled != "" {
		note(handRolled)
	}
	if f := checkPin(root); f != "" {
		note(f)
	}

	if len(findings) > 0 {
		return fmt.Errorf("%d drift(s) from the shared shape:\n- %s", len(findings), strings.Join(findings, "\n- "))
	}
	_, _ = fmt.Fprintf(out, "in shape: workflow calls %s, %s and %s delegate, %s untracked, %s ignored, %s tracked with an Unreleased heading, identity declares the role %s, no restated default, no hand-rolled gate target, go.mod pins the tool\n",
		Workflow, hookPath, prepushPath, golangci.Name, strings.Join(Ignored, " and "), changelog.Name, string(cfg.Identity.Role))
	return nil
}

// checkWorkflow finds the caller. Exactly one file calls the shared
// workflow, and it runs on push to main and on pull requests.
func checkWorkflow(root string) string {
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	var callers, old []string
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			return fmt.Sprintf("%s: %v", f, err)
		}
		rel, _ := filepath.Rel(root, f)
		switch {
		case strings.Contains(string(body), Workflow):
			callers = append(callers, rel)
			if f := checkTriggers(rel, body); f != "" {
				return f
			}
		case strings.Contains(string(body), oldWorkflow):
			old = append(old, rel)
		}
	}
	switch {
	case len(callers) == 1:
		return ""
	case len(callers) > 1:
		return fmt.Sprintf("%d workflows call %s (%s); one repository has one caller",
			len(callers), Workflow, strings.Join(callers, ", "))
	case len(old) > 0:
		return fmt.Sprintf("%s still calls go-verify.yml; switch its `uses:` to %s, which asks lateregate which gates apply",
			strings.Join(old, ", "), Workflow)
	default:
		return fmt.Sprintf("no workflow calls %s; run `lateregate init` to write .github/workflows/ci.yml", Workflow)
	}
}

// checkTriggers reads the caller's `on:` block. GitHub reads YAML where the
// bare key `on` may parse as the boolean true, so both spellings are
// accepted.
func checkTriggers(rel string, body []byte) string {
	var doc map[string]any
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return fmt.Sprintf("%s: %v", rel, err)
	}
	on, ok := doc["on"]
	if !ok {
		on, ok = doc["true"]
	}
	if !ok {
		return rel + ": has no `on:` block, so it never runs"
	}
	m, _ := on.(map[string]any)
	push, hasPush := m["push"]
	_, hasPR := m["pull_request"]
	if !hasPush || !hasPR {
		return rel + ": must trigger on push to main and on pull_request; a gate that runs on one and not the other is run at the wrong time"
	}
	if pm, ok := push.(map[string]any); ok {
		if b, ok := pm["branches"].([]any); ok && !containsAny(b, "main") {
			return rel + ": push trigger does not include main"
		}
	}
	return ""
}

func containsAny(xs []any, want string) bool {
	for _, x := range xs {
		if s, ok := x.(string); ok && s == want {
			return true
		}
	}
	return false
}

// checkHook wants an executable pre-commit that delegates to the binary.
func checkHook(root string) string {
	return checkDelegating(root, hookPath, gates.HookInvocation)
}

// checkPrepush wants an executable pre-push that delegates to the binary. A
// repository's own lines around the delegation are its own; the check is the
// one line, not the bytes.
func checkPrepush(root string) string {
	return checkDelegating(root, prepushPath, gates.PrepushInvocation)
}

func checkDelegating(root, rel, invocation string) string {
	p := filepath.Join(root, rel)
	info, err := os.Stat(p)
	if err != nil {
		return rel + " is missing; run `lateregate init` to write it"
	}
	if info.Mode()&0o111 == 0 {
		return rel + " is not executable, so git never runs it"
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return fmt.Sprintf("%s: %v", rel, err)
	}
	if !gates.Delegates(string(body), invocation) {
		return rel + " does not run `" + invocation + "`; it holds its own copy of the checks, which no gate keeps in step with the config"
	}
	return ""
}

// checkGolangci: the rendered config is not tracked, unless declared.
func checkGolangci(root string, cfg *config.Config, exec gates.Exec) string {
	_, err := exec(nil, false, "git", "ls-files", "--error-unmatch", golangci.Name)
	tracked := err == nil
	own := strings.TrimSpace(cfg.Golangci.Own) != ""
	switch {
	case tracked && !own:
		return golangci.Name + " is tracked by git; it is generated on every lint, so a committed copy drifts:\n\tgit rm --cached " + golangci.Name
	case own:
		if _, err := os.Stat(filepath.Join(root, golangci.Name)); err != nil {
			return "golangci.own says this repository keeps its own " + golangci.Name + ", and there is none"
		}
	}
	return ""
}

// checkGitignore: every generated file is listed, so `git add -A` cannot
// commit one.
func checkGitignore(root string) string {
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return ".gitignore is missing; it must list " + strings.Join(Ignored, " and ")
	}
	var missing []string
	for _, want := range Ignored {
		if !ignores(string(body), want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return ".gitignore does not list " + strings.Join(missing, ", ") + "; both are generated on every run"
	}
	return ""
}

// ignores reports whether a .gitignore lists a name, with or without a
// leading slash.
func ignores(body, name string) bool {
	for l := range strings.SplitSeq(body, "\n") {
		l = strings.TrimSpace(l)
		if l == name || l == "/"+name {
			return true
		}
	}
	return false
}

// targetNames maps the make targets a gate or command was ever called by
// onto it. A target with one of these names must delegate.
var targetNames = map[string]string{
	"fmt-check": "fmt-check", "test": "test", "test-race": "race", "race": "race",
	"cover": "cover", "lint": "lint", "lint-config": "lint", "lint-modernize": "modernize",
	"modernize": "modernize", "test-hermetic": "hermetic", "hermetic": "hermetic",
	"spec-lint": "spec-lint", "license": "license", "test-tempdir": "tempdir",
	"tempdir": "tempdir", "vuln": "vuln", "check": "check", "contract": "contract",
	"release": "release", "release-notes": "release-notes",
}

// checkChangelog: CHANGELOG.md is tracked and has the heading the next
// tag's notes go under. Every repository, whether or not it has tagged: a
// repository that starts tagging finds the rule already there.
func checkChangelog(root string, exec gates.Exec) string {
	body, err := os.ReadFile(filepath.Join(root, changelog.Name))
	if err != nil {
		return changelog.Name + " is missing; run `lateregate init` to write it. Every release tag needs a section in it"
	}
	if _, err := exec(nil, false, "git", "ls-files", "--error-unmatch", changelog.Name); err != nil {
		return changelog.Name + " is not tracked by git; the pre-push and the release workflow read it at the tag's commit:\n\tgit add " + changelog.Name
	}
	if _, err := changelog.Section(string(body), changelog.Unreleased); errors.Is(err, changelog.ErrNoSection) {
		return changelog.Name + " has no `## " + changelog.Unreleased + "` heading; the notes for the next tag go under it"
	}
	return ""
}

// checkIdentity: the file declares which layer of the family's identity
// shape this repository is. A repository with no block runs no rule of that
// shape, which is the same gap as a gate nobody runs.
func checkIdentity(cfg *config.Config) string {
	if cfg.Identity.Present {
		return ""
	}
	return config.Name + " has no identity block, so no rule of the family's identity shape runs here:\n" +
		"\tidentity:\n\t  role: none   # one of " + config.RoleList()
}

// rule matches a target line in make's database.
var rule = regexp.MustCompile(`^([A-Za-z0-9_.-]+):`)

// checkMakefile: a gate-named target must delegate to the binary. A
// Makefile may hold no gate targets at all; `make check` is a convenience.
//
// It reads make's rule database, where recipe lines follow the target
// tab-indented, rather than `make -n <target>`, which succeeds on a FILE of
// the target's name and prints nothing about a recipe.
func checkMakefile(root string, exec gates.Exec) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err != nil {
		// No Makefile is a repository with no gate targets, which is in
		// shape. Any other stat error surfaces when make runs below.
		//nolint:nilerr // absence is the pass, not an error
		return "", nil
	}
	// The probe owns temporary files left by make and its launcher. On macOS,
	// even reading the database leaves Apple's xcrun_db cache under TMPDIR.
	// Keep that work in a directory we remove, including on a failed probe.
	dir, err := os.MkdirTemp("", "lateregate-make")
	if err != nil {
		return "", fmt.Errorf("making the Makefile probe's temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := append(os.Environ(), "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
	// make -n exits non-zero when a default goal fails to build; the database
	// is printed either way and an empty one is caught below.
	db, _ := exec(env, false, "make", "-np")
	if strings.TrimSpace(string(db)) == "" {
		return "", fmt.Errorf("make printed no rule database for the Makefile at %s: this cannot tell a Makefile with no targets from make failing to run", root)
	}
	recipes := map[string][]string{}
	current := ""
	for l := range strings.SplitSeq(string(db), "\n") {
		if m := rule.FindStringSubmatch(l); m != nil {
			current = m[1]
			continue
		}
		if l == "" {
			current = ""
			continue
		}
		if current != "" && strings.HasPrefix(l, "\t") {
			recipes[current] = append(recipes[current], strings.TrimSpace(l))
		}
	}
	var bad []string
	for target, gate := range targetNames {
		lines, ok := recipes[target]
		if !ok {
			continue
		}
		delegates := false
		for _, l := range lines {
			if strings.Contains(l, "lateregate") {
				delegates = true
			}
		}
		if !delegates {
			bad = append(bad, fmt.Sprintf("%s (runs the %s gate itself)", target, gate))
		}
	}
	if len(bad) == 0 {
		return "", nil
	}
	sort.Strings(bad)
	return "Makefile hand-rolls a gate: " + strings.Join(bad, ", ") +
		"\n\ta target named for a gate runs `go tool lateregate <gate>`, or is deleted; `lateregate` runs every gate without one", nil
}

// toolLine matches the tool directive, bare or inside a tool ( ) block.
var toolLine = regexp.MustCompile(`(?m)^\s*(tool\s+)?` + regexp.QuoteMeta(ToolPath) + `\s*$`)

// checkPin: go.mod carries the tool line. This repository, whose module is
// the tool, carries it for its own package the same way.
func checkPin(root string) string {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "go.mod is missing; every gate here is a Go tool dependency"
	}
	if !toolLine.Match(body) {
		return "go.mod does not pin the tool:\n\tgo get -tool " + ToolPath
	}
	return ""
}

// Init writes what Run checks for, and never a file already in shape.
//
// It does not touch Makefile or .lateregate.yaml: those hold decisions, and
// a tool that writes decisions for a repository has made them for it.
func Init(root string, out io.Writer, exec gates.Exec) error {
	wrote := 0
	// The caller, only when no workflow calls either the new or the old
	// pipeline. An old caller carries inputs somebody chose, so it is edited
	// by hand and reported by Run until it is.
	if f := checkWorkflow(root); strings.HasPrefix(f, "no workflow calls") {
		p := filepath.Join(root, ".github", "workflows", "ci.yml")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(Caller), 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "wrote .github/workflows/ci.yml")
		wrote++
	}

	if f := checkHook(root); f != "" {
		p := filepath.Join(root, hookPath)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(gates.Staged), 0o755); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "wrote "+hookPath)
		wrote++
	}
	// The pre-push, only when there is none. One that exists and does not
	// delegate holds a check somebody wrote (pkg refuses a tag without a
	// changelog section there), so it is edited by hand to add the
	// delegation and reported by Run until it is.
	if _, err := os.Stat(filepath.Join(root, prepushPath)); err != nil {
		if err := os.WriteFile(filepath.Join(root, prepushPath), []byte(gates.Prepush), 0o755); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "wrote "+prepushPath)
		wrote++
	}
	if _, err := exec(nil, false, "git", "config", "core.hooksPath", ".githooks"); err != nil {
		return fmt.Errorf("git config core.hooksPath: %w", err)
	}

	if f := checkGitignore(root); f != "" {
		p := filepath.Join(root, ".gitignore")
		body, _ := os.ReadFile(p)
		var add []string
		for _, want := range Ignored {
			if !ignores(string(body), want) {
				add = append(add, want)
			}
		}
		text := string(body)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n# Generated by lateregate on every run; never committed.\n" + strings.Join(add, "\n") + "\n"
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "added %s to .gitignore\n", strings.Join(add, ", "))
		wrote++
	}

	// The changelog, only when there is none. One that exists holds notes,
	// and a missing heading is a one-line hand edit that Run reports.
	if _, err := os.Stat(filepath.Join(root, changelog.Name)); err != nil {
		if err := os.WriteFile(filepath.Join(root, changelog.Name), []byte(changelog.Seed), 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "wrote "+changelog.Name+"; commit it")
		wrote++
	}

	if wrote == 0 {
		_, _ = fmt.Fprintln(out, "nothing to write; the wiring is in shape")
	}
	_, _ = fmt.Fprintf(out, "next: `go get -tool %s`, then `lateregate contract`\n", ToolPath)
	return nil
}

// Required lists the gate names, for the callers that used to read the
// required target set from here.
func Required() []string { return bar.Names() }
