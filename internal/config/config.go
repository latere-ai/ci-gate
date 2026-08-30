// Copyright 2026 Latere AI.
// Licensed under the MIT License.

// Package config reads .lateregate.yaml, the one file a consumer repo uses
// to configure every gate.
//
// One file rather than one per check, because a repository has a single
// quality bar and splitting it across files makes the bar hard to read. The
// file lives in the consumer repo rather than in this one: a gate that needs
// a second checkout to know its own threshold is a gate that only runs in CI,
// and the whole point is that it runs on a laptop first.
//
// Every section is optional. A repo that only wants the required targets
// needs no file at all, and Load returns defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Name is the file every consumer repo carries at its root.
const Name = ".lateregate.yaml"

// DefaultThreshold is the per-package coverage floor when a repo does not
// set one. Ninety, because that is the bar both repos this tool was
// extracted from already held.
const DefaultThreshold = 90.0

// Config is the whole of .lateregate.yaml.
type Config struct {
	Cover     Cover     `yaml:"cover"`
	Spec      Spec      `yaml:"spec"`
	Hermetic  Hermetic  `yaml:"hermetic"`
	Modernize Modernize `yaml:"modernize"`
	Depcheck  Depcheck  `yaml:"depcheck"`
}

// Cover configures the per-package coverage gate.
type Cover struct {
	// Threshold is the floor every non-exempt package clears.
	Threshold float64 `yaml:"threshold"`
	// Exempt maps a package suffix to the reason it does not have to clear
	// the floor. The value is the reason, so an entry cannot exist without
	// one: a package exempted without a reason is a package nobody decided
	// to exempt. Load rejects an empty value.
	Exempt map[string]string `yaml:"exempt"`
	// TrimPrefix is dropped from package paths in the report so the output
	// fits a terminal. Defaults to the module path.
	TrimPrefix string `yaml:"trim_prefix"`
}

// Spec configures the spec-tree linter.
type Spec struct {
	// Dir holds the spec files. Empty disables spec-lint entirely.
	Dir string `yaml:"dir"`
	// Status is the closed vocabulary a spec's status must come from. Empty
	// means any value passes.
	Status []string `yaml:"status"`
	// Require lists frontmatter keys every spec must carry with a non-empty
	// value.
	Require []string `yaml:"require"`
	// Index is a Markdown file whose table rows must agree with the specs
	// they link to. Empty disables the index checks.
	Index string `yaml:"index"`
	// Wikilinks turns on [[name]] resolution against Dir.
	Wikilinks bool `yaml:"wikilinks"`
	// Exclude lists file names in Dir that are not specs.
	Exclude []string `yaml:"exclude"`
	// Vocabulary closes further frontmatter keys the way Status closes
	// `status`: a key here must hold one of the listed values.
	Vocabulary map[string][]string `yaml:"vocabulary"`
	// StatusRequires ties a frontmatter key to a status. The key must be
	// present when the spec has that status and absent otherwise, which makes
	// the status carry information rather than decorate the file.
	StatusRequires map[string]StatusRule `yaml:"status_requires"`
	// RequireSection lists headings every spec must carry.
	RequireSection []string `yaml:"require_section"`
	// RequireSectionByStatus lists headings a spec must carry once it reaches
	// a given status. A spec that claims to be built and does not say what
	// happened leaves the claim unchecked.
	RequireSectionByStatus map[string][]string `yaml:"require_section_by_status"`
	// SectionExempt names files the section rules do not apply to.
	SectionExempt []string `yaml:"section_exempt"`
	// ScopedIDs matches identifiers that belong to the spec they appear in.
	// A mismatched id means a record was copied between specs, which silently
	// gives two different decisions one name.
	ScopedIDs string `yaml:"scoped_ids"`
	// Tables turns on the table well-formedness check.
	Tables bool `yaml:"tables"`
	// Register gates a table of identifiers that one spec defines and the
	// rest of the tree cites.
	Register Register `yaml:"register"`
}

// Register describes an id table one spec owns.
//
// A tree that numbers something -- conformance rows, requirements, risks --
// and cites those numbers from elsewhere has two failures nothing else sees:
// a gap, which means a row was deleted and the numbers after it now mean
// something different, and a citation of a row that does not exist.
type Register struct {
	// File is the spec that defines the rows. Empty disables the check.
	File string `yaml:"file"`
	// Define matches a defining row; its first group is the id.
	Define string `yaml:"define"`
	// Cite matches a citation elsewhere in the tree. The first non-empty
	// group is the id, so one pattern can carry several citation shapes.
	Cite string `yaml:"cite"`
	// Sequential requires the ids to run from 1 with no gaps, given Prefix.
	Sequential bool `yaml:"sequential"`
	// Prefix is the letter the numbers carry, e.g. "C" for C1, C2.
	Prefix string `yaml:"prefix"`
}

// StatusRule is one status-to-frontmatter-key rule.
type StatusRule struct {
	// Field is the frontmatter key the status requires.
	Field string `yaml:"field"`
	// Match, when set, is a pattern the field's value must satisfy. It exists
	// because a field can be filled in with something that is not a record
	// anyone can act on.
	Match string `yaml:"match"`
	// Hint is added to the failure so a reader learns what a good value looks
	// like rather than only that theirs was rejected.
	Hint string `yaml:"hint"`
}

// Hermetic configures the PATH-stripped test run.
type Hermetic struct {
	// Allow lists directories kept on PATH besides the Go toolchain's own.
	// Empty is the strictest setting and the default; a repo whose tests
	// legitimately need a system tool names the directory here, which makes
	// the dependency visible instead of ambient.
	Allow []string `yaml:"allow"`
}

// Depcheck configures the dependency-footprint gate.
type Depcheck struct {
	// Platforms are the GOOS/GOARCH pairs the build list is taken on. What a
	// repository claims to build for is what has to be checked: a dependency
	// reached only on darwin is invisible to a linux-only gate. Empty means
	// the host's own platform.
	Platforms []string `yaml:"platforms"`
	// Packages maps an import path to what its build may reach.
	Packages map[string]Gated `yaml:"packages"`
}

// Gated is one package's allowlist and the decision that owns it.
type Gated struct {
	// Decision names the record that argues for this list, so a failure sends
	// a reader to the argument rather than to the config.
	Decision string `yaml:"decision"`
	// Allow maps an import-path prefix to why the build may reach it. A prefix
	// rather than an exact package: a dependency's own internal subpackages
	// are its business, and pinning each would make an upstream refactor a
	// failure here without any new dependency.
	Allow map[string]string `yaml:"allow"`
}

// Modernize configures the `go fix` gate.
type Modernize struct {
	// Disable names fixers to turn off. A repo that disables a fixer is
	// making a decision, and the gate verifies the fixer still exists before
	// trusting the flag: `go fix` rejects an unknown -name=false, and a
	// rejected flag would make this check pass silently.
	Disable []string `yaml:"disable"`
}

// Load reads Name from dir. A missing file is not an error: it yields the
// defaults, so a repo adopts the required targets without writing config.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, Name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults(&Config{}), nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.UnmarshalWithOptions(data, &c, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(path); err != nil {
		return nil, err
	}
	return defaults(&c), nil
}

func defaults(c *Config) *Config {
	if c.Cover.Threshold == 0 {
		c.Cover.Threshold = DefaultThreshold
	}
	return c
}

// validate rejects a config that would make a gate weaker than it looks.
func (c *Config) validate(path string) error {
	var bad []string
	for pkg, why := range c.Cover.Exempt {
		if strings.TrimSpace(why) == "" {
			bad = append(bad, pkg)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("%s: exempt without a reason: %s\n"+
			"an exemption is a decision: write why the package does not have "+
			"to clear the floor, or delete the entry",
			path, strings.Join(bad, ", "))
	}
	var noReason []string
	for pkg, g := range c.Depcheck.Packages {
		for prefix, why := range g.Allow {
			if strings.TrimSpace(why) == "" {
				noReason = append(noReason, pkg+" -> "+prefix)
			}
		}
	}
	if len(noReason) > 0 {
		sort.Strings(noReason)
		return fmt.Errorf("%s: depcheck allowance without a reason: %s\n"+
			"admitting a dependency is a decision: write why the build may reach "+
			"it, or delete the entry",
			path, strings.Join(noReason, ", "))
	}
	if t := c.Cover.Threshold; t < 0 || t > 100 {
		return fmt.Errorf("%s: cover.threshold %.1f is not a percentage", path, t)
	}
	return nil
}

// ExemptFor reports whether a package is exempt, and why. Matching is by
// suffix so a config does not have to repeat the module path.
func (c Cover) ExemptFor(pkg string) (string, bool) {
	for suffix, why := range c.Exempt {
		if strings.HasSuffix(pkg, suffix) {
			return why, true
		}
	}
	return "", false
}

// AllowsStatus reports whether s is in the configured vocabulary. An empty
// vocabulary allows everything.
func (s Spec) AllowsStatus(v string) bool {
	if len(s.Status) == 0 {
		return true
	}
	return slices.Contains(s.Status, v)
}

// IsExcluded reports whether a file name in Dir is not a spec.
func (s Spec) IsExcluded(name string) bool {
	return slices.Contains(s.Exclude, name)
}
