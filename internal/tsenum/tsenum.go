// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package tsenum checks declared TypeScript enum domains using the consumer's compiler.
package tsenum

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"latere.ai/x/ci-gate/internal/gates"
)

// Project declares enum domains and conversion boundaries in one TypeScript project.
// Selectors are relative to the tsconfig directory and use file.ts#Symbol syntax.
type Project struct {
	Project string            `json:"project"`
	Types   []string          `json:"types"`
	Fields  map[string]string `json:"fields"`
	Parsers map[string]string `json:"parsers"`
}

//go:embed analyzer.mjs
var analyzer string

// Run checks projects against the TypeScript installation resolved from each tsconfig.
// The embedded script and JSON input are separate command arguments, never shell code.
func Run(projects []Project, root string, out io.Writer, run gates.Exec) error {
	if len(projects) == 0 {
		return fmt.Errorf("enum-typescript requires at least one configured project")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve TypeScript repository root: %w", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return fmt.Errorf("TypeScript repository root %q is not an accessible directory", root)
	}
	//nolint:errchkjson // The payload only contains strings, slices and string maps; it has no unsupported values or custom marshalers.
	input, _ := json.Marshal(struct {
		Root     string    `json:"root"`
		Projects []Project `json:"projects"`
	}{root, projects})
	if _, err := run(nil, true, "node", "--input-type=module", "--eval", analyzer, "--", "--enum-input", string(input)); err != nil {
		return fmt.Errorf("TypeScript enum domain check failed (requires Node and each project's installed TypeScript; run lateregate enum-typescript-prepare): %w", err)
	}
	_, _ = fmt.Fprintln(out, "TypeScript enum domains use named types and members")
	return nil
}
