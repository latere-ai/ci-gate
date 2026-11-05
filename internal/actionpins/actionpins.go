// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package actionpins holds every third-party GitHub Action a repository uses
// to the organization's one commit.
//
// The list is action.yml in this package, embedded in the binary, so the gate
// reads the same commits on a laptop and on a runner and needs no network. It
// is shaped as a composite action that is never run, so Dependabot's
// github-actions updater keeps it current.
//
// The gate reads every `uses:` in the tracked workflows and actions: a job's
// reusable workflow, a job's step, and an action's step. A reference is keyed
// by its owner and repository, so a sub-path action shares its repository's
// row. Local references, docker:// images and the organization's own
// repositories are left to other rules.
package actionpins

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// ListFile is where the list lives, for the messages that name it.
const ListFile = "internal/actionpins/action.yml"

// FixCommand is the command that realigns a tree to the list.
const FixCommand = "go tool lateregate action-pins -w"

// Owner is the organization's own namespace, whose actions are pinned by
// other rules.
const Owner = "latere-ai"

//go:embed action.yml
var listYAML []byte

// Pin is the commit and version the organization holds an action to.
type Pin struct {
	Commit  string
	Version string
}

var (
	commitRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionRE = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	shortRE   = regexp.MustCompile(`^[0-9a-f]{7,39}$`)
	tagRE     = regexp.MustCompile(`^v?\d+(\.\d+)*$`)
)

// Key is the owner and repository of a reference, the list's key, in lower
// case: GitHub resolves Azure/setup-kubectl and azure/setup-kubectl to the
// same repository, so the list holds both spellings to one row.
func Key(ref string) string {
	parts := strings.SplitN(ref, "/", 3)
	if len(parts) < 2 {
		return strings.ToLower(ref)
	}
	return strings.ToLower(parts[0] + "/" + parts[1])
}

// List parses the embedded list.
func List() (map[string]Pin, error) { return parseList(listYAML) }

func parseList(body []byte) (map[string]Pin, error) {
	var doc struct {
		Runs struct {
			Steps []struct {
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", ListFile, err)
	}
	if len(doc.Runs.Steps) == 0 {
		return nil, fmt.Errorf("%s lists no action", ListFile)
	}
	src := strings.Split(string(body), "\n")
	versions := map[string]string{}
	for _, l := range src {
		if m := useLineRE.FindStringSubmatch(l); m != nil {
			versions[m[3]] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m[5]), "#"))
		}
	}
	pins := map[string]Pin{}
	for _, s := range doc.Runs.Steps {
		ref, commit, ok := strings.Cut(s.Uses, "@")
		if !ok || !commitRE.MatchString(commit) {
			return nil, fmt.Errorf("%s: %q is not an action pinned to a 40-character commit", ListFile, s.Uses)
		}
		v := versions[s.Uses]
		if !versionRE.MatchString(v) {
			return nil, fmt.Errorf("%s: %s has the version comment %q, want vX.Y.Z", ListFile, ref, v)
		}
		k := Key(ref)
		if _, dup := pins[k]; dup {
			return nil, fmt.Errorf("%s lists %s twice", ListFile, k)
		}
		pins[k] = Pin{Commit: commit, Version: v}
	}
	return pins, nil
}

// useLineRE reads a line that holds a uses: key: the text before the value,
// the opening quote, the reference, the closing quote and what follows.
var useLineRE = regexp.MustCompile(`^(\s*(?:-\s+)*uses:\s*)(["']?)([^"'\s#]+)(["']?)(\s*#.*)?\s*$`)

// Use is one `uses:` in a file.
type Use struct {
	File string
	Line int
	Ref  string
}

// Uses finds the `uses:` values of a workflow or an action: jobs.<id>.uses,
// jobs.<id>.steps[].uses and runs.steps[].uses.
func Uses(file string, body []byte) ([]Use, error) {
	f, err := parser.ParseBytes(body, 0)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", file, err)
	}
	var out []Use
	add := func(n ast.Node) {
		if s, ok := n.(*ast.StringNode); ok {
			out = append(out, Use{File: file, Line: s.GetToken().Position.Line, Ref: s.Value})
		}
	}
	steps := func(n ast.Node) {
		seq, ok := n.(*ast.SequenceNode)
		if !ok {
			return
		}
		for _, st := range seq.Values {
			for _, kv := range entries(st) {
				if keyOf(kv) == "uses" {
					add(kv.Value)
				}
			}
		}
	}
	for _, d := range f.Docs {
		for _, kv := range entries(d.Body) {
			switch keyOf(kv) {
			case "jobs":
				for _, job := range entries(kv.Value) {
					for _, jf := range entries(job.Value) {
						switch keyOf(jf) {
						case "uses":
							add(jf.Value)
						case "steps":
							steps(jf.Value)
						}
					}
				}
			case "runs":
				for _, rf := range entries(kv.Value) {
					if keyOf(rf) == "steps" {
						steps(rf.Value)
					}
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Use) int { return a.Line - b.Line })
	return out, nil
}

// entries is the key/value pairs of a mapping node; any other node has none.
func entries(n ast.Node) []*ast.MappingValueNode {
	switch m := n.(type) {
	case *ast.MappingNode:
		return m.Values
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{m}
	}
	return nil
}

func keyOf(kv *ast.MappingValueNode) string {
	if s, ok := kv.Key.(*ast.StringNode); ok {
		return s.Value
	}
	return ""
}

// Finding is one reference the list does not hold.
type Finding struct {
	File, Message string
	Line          int
	// Listed is true when the list has the action, so write mode can fix it.
	Listed bool
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Message) }

// thirdParty reports whether a reference is one this gate holds to the list.
func thirdParty(ref string) bool {
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
		return false
	}
	owner, _, _ := strings.Cut(ref, "/")
	return !strings.EqualFold(owner, Owner)
}

// Check compares the uses of one file, with its lines, to the list.
func Check(list map[string]Pin, file string, body []byte) ([]Finding, error) {
	uses, err := Uses(file, body)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(body), "\n")
	var out []Finding
	for _, u := range uses {
		if !thirdParty(u.Ref) {
			continue
		}
		path, ref, _ := strings.Cut(u.Ref, "@")
		key := Key(path)
		pin, ok := list[key]
		if !ok {
			out = append(out, Finding{File: file, Line: u.Line, Message: fmt.Sprintf(
				"%s is not in the organization's list; add it to %s in latere-ai/ci-gate first", key, ListFile)})
			continue
		}
		fnd := func(msg string) Finding {
			return Finding{File: file, Line: u.Line, Listed: true, Message: msg}
		}
		if ref != pin.Commit {
			out = append(out, fnd(fmt.Sprintf("%s is pinned to %s, the list has commit %s (%s)",
				key, describe(ref), pin.Commit, pin.Version)))
			continue
		}
		comment := ""
		if u.Line >= 1 && u.Line <= len(lines) {
			if m := useLineRE.FindStringSubmatch(lines[u.Line-1]); m != nil {
				comment = strings.TrimSpace(m[5])
			}
		}
		if comment != "# "+pin.Version {
			got := "no comment"
			if comment != "" {
				got = fmt.Sprintf("%q", comment)
			}
			out = append(out, fnd(fmt.Sprintf("%s has %s after the commit, the list has %q", key, got, "# "+pin.Version)))
		}
	}
	return out, nil
}

// describe says what kind of reference a ref is.
func describe(ref string) string {
	switch {
	case ref == "":
		return "no ref"
	case commitRE.MatchString(ref):
		return "commit " + ref
	case shortRE.MatchString(ref):
		return "the short commit " + ref
	case tagRE.MatchString(ref):
		return "the tag " + ref
	}
	return "the branch or tag " + ref
}

// Run checks every file against the embedded list and reports every finding
// in one run.
func Run(root string, files []string, out io.Writer) error {
	list, err := List()
	if err != nil {
		return err
	}
	findings, n, err := scan(list, root, files)
	if err != nil {
		return err
	}
	return report(findings, n, out)
}

func scan(list map[string]Pin, root string, files []string) ([]Finding, int, error) {
	var all []Finding
	read := 0
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if errors.Is(err, os.ErrNotExist) {
			// Tracked and deleted in the working tree: nothing to read.
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", f, err)
		}
		read++
		fs, err := Check(list, f, body)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, fs...)
	}
	return all, read, nil
}

func report(findings []Finding, files int, out io.Writer) error {
	if len(findings) == 0 {
		_, _ = fmt.Fprintf(out, "every third-party action in %d file(s) is the organization's commit\n", files)
		return nil
	}
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.String() + "\n")
	}
	unlisted := slices.ContainsFunc(findings, func(f Finding) bool { return !f.Listed })
	fmt.Fprintf(&b, "%d third-party action reference(s) are not the organization's commit; run %s to realign them", len(findings), FixCommand)
	if unlisted {
		fmt.Fprintf(&b, " (an action missing from the list needs it added to %s in latere-ai/ci-gate first)", ListFile)
	}
	return errors.New(b.String())
}

// Write moves every listed action to the list's commit and version comment,
// line by line so indentation, a leading "- " and quoting survive, and prints
// each file it changed. An action the list does not hold is left as it is and
// still fails the run.
func Write(root string, files []string, out io.Writer) error {
	list, err := List()
	if err != nil {
		return err
	}
	var rest []Finding
	read := 0
	for _, f := range files {
		p := filepath.Join(root, f)
		body, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		read++
		uses, err := Uses(f, body)
		if err != nil {
			return err
		}
		lines := strings.Split(string(body), "\n")
		changed := false
		for _, u := range uses {
			if !thirdParty(u.Ref) || u.Line < 1 || u.Line > len(lines) {
				continue
			}
			path, _, _ := strings.Cut(u.Ref, "@")
			pin, ok := list[Key(path)]
			if !ok {
				continue
			}
			m := useLineRE.FindStringSubmatch(lines[u.Line-1])
			if m == nil {
				continue
			}
			ref := path + "@" + pin.Commit
			next := m[1] + m[2] + ref + m[4] + " # " + pin.Version
			if next != lines[u.Line-1] {
				lines[u.Line-1] = next
				changed = true
			}
		}
		if changed {
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "wrote %s\n", f)
		}
		fs, err := Check(list, f, []byte(strings.Join(lines, "\n")))
		if err != nil {
			return err
		}
		rest = append(rest, fs...)
	}
	return report(rest, read, out)
}
