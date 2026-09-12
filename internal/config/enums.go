// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
)

// Enums names closed domains rather than guessing them from field names.
type Enums struct {
	Go         EnumPolicy        `yaml:"go"`
	TypeScript []TypeScriptEnums `yaml:"typescript"`
}

// EnumPolicy names Go types, their required struct fields and reviewed input
// parsers. A parser exception permits primitive conversion, not incomplete
// switches. Each exception must explain why that function is an input boundary.
type EnumPolicy struct {
	Types   []string          `yaml:"types"`
	Fields  map[string]string `yaml:"fields"`
	Parsers map[string]string `yaml:"parsers"`
}

// TypeScriptEnums scopes selectors to the directory holding Project's tsconfig.
// Types use file.ts#Enum; fields use file.ts#Interface.field. The project path
// itself is relative to the repository root.
type TypeScriptEnums struct {
	Project string            `yaml:"project"`
	Types   []string          `yaml:"types"`
	Fields  map[string]string `yaml:"fields"`
	Parsers map[string]string `yaml:"parsers"`
}

func (e Enums) validate() error {
	if err := validateEnumPolicy("enums.go", e.Go, goEnumSelector); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range e.TypeScript {
		if !localEnumPath(p.Project) || filepath.Ext(p.Project) != ".json" {
			return fmt.Errorf("enums.typescript project %q must be a repository-relative tsconfig JSON path", p.Project)
		}
		key := filepath.Clean(p.Project)
		if seen[key] {
			return fmt.Errorf("enums.typescript repeats project %q", p.Project)
		}
		seen[key] = true
		if len(p.Types) == 0 {
			return fmt.Errorf("enums.typescript project %q names no enum types", p.Project)
		}
		policy := EnumPolicy{Types: p.Types, Fields: p.Fields, Parsers: p.Parsers}
		if err := validateEnumPolicy("enums.typescript "+p.Project, policy, tsEnumSelector); err != nil {
			return err
		}
	}
	return nil
}

func validateEnumPolicy(name string, p EnumPolicy, valid func(string, bool) bool) error {
	seen := map[string]bool{}
	for _, t := range p.Types {
		if !valid(t, false) {
			return fmt.Errorf("%s.types: invalid enum selector %q", name, t)
		}
		if seen[t] {
			return fmt.Errorf("%s.types repeats %q", name, t)
		}
		seen[t] = true
	}
	for _, field := range sortedEnumKeys(p.Fields) {
		if !valid(field, true) {
			return fmt.Errorf("%s.fields: invalid field selector %q", name, field)
		}
		if !slices.Contains(p.Types, p.Fields[field]) {
			return fmt.Errorf("%s.fields: %q refers to undeclared enum %q; add it to types", name, field, p.Fields[field])
		}
	}
	for _, parser := range sortedEnumKeys(p.Parsers) {
		if !valid(parser, false) {
			return fmt.Errorf("%s.parsers: invalid function selector %q", name, parser)
		}
		if strings.TrimSpace(p.Parsers[parser]) == "" {
			return fmt.Errorf("%s.parsers: %q needs a reason for the conversion boundary", name, parser)
		}
		if len(p.Types) == 0 {
			return fmt.Errorf("%s.parsers names a boundary but types names no enum domain", name)
		}
	}
	return nil
}

func sortedEnumKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func goEnumSelector(s string, field bool) bool {
	if strings.TrimSpace(s) != s || strings.ContainsAny(s, "\\\t\n ") {
		return false
	}
	parts := 1
	if field {
		parts++
	}
	for range parts {
		i := strings.LastIndexByte(s, '.')
		if i < 0 || !token.IsIdentifier(s[i+1:]) || s[i+1:] == "_" {
			return false
		}
		s = s[:i]
	}
	// An empty package is the explicit module-root spelling `.Type`.
	return s == "" || localEnumPath(s)
}

func tsEnumSelector(s string, field bool) bool {
	file, symbol, ok := strings.Cut(s, "#")
	extension := filepath.Ext(file)
	if !ok || !localEnumPath(file) || !slices.Contains([]string{".ts", ".tsx", ".vue"}, extension) {
		return false
	}
	parts := strings.Split(symbol, ".")
	want := 1
	if field {
		want = 2
	}
	if len(parts) != want {
		return false
	}
	for _, p := range parts {
		// JavaScript identifiers also permit '$'. Configured symbols otherwise
		// follow Go's Unicode identifier grammar, which accepts common TS names.
		if !token.IsIdentifier(strings.ReplaceAll(p, "$", "_")) {
			return false
		}
	}
	return true
}

func localEnumPath(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\\\t\n") && filepath.IsLocal(s)
}
