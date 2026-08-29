// Copyright 2026 Latere AI.
// Licensed under the Apache License, Version 2.0.

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
}

// Hermetic configures the PATH-stripped test run.
type Hermetic struct {
	// Allow lists directories kept on PATH besides the Go toolchain's own.
	// Empty is the strictest setting and the default; a repo whose tests
	// legitimately need a system tool names the directory here, which makes
	// the dependency visible instead of ambient.
	Allow []string `yaml:"allow"`
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
			"An exemption is a decision. Write why the package does not have "+
			"to clear the floor, or delete the entry.",
			path, strings.Join(bad, ", "))
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
