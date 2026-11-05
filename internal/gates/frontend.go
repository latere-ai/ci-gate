// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// frontendScripts are the package scripts the frontend gate runs, in order,
// where the package defines them. e2e is last: it drives the built page, and
// a project whose tests need a browser has one installed before the gate
// runs, by the workflow from the plan or already on a laptop.
var frontendScripts = []string{"typecheck", "test", "build", "e2e"}

// Frontend checks each named Bun package: a frozen install from its tracked
// lockfile, then whichever of typecheck, test, build and e2e its package.json
// defines, in its own directory. A package fails at its first failing step,
// and every package is checked.
//
// The gate installs no browser. A project that names one in frontend.browser
// has it installed from the plan before this runs.
//
// Bun and real Node must both be on PATH. The scripts run tools (tsc, vite)
// built and tested for Node, and without it bun stands in a node shim under
// which a type checker can check no files and report nothing.
func Frontend(projects []string, root string, out io.Writer, lookPath func(string) (string, error), run Exec, in func(dir string) Exec) error {
	if len(projects) == 0 {
		return errors.New("frontend.projects names no package to check")
	}
	for _, tool := range []string{"bun", "node"} {
		if _, err := lookPath(tool); err != nil {
			return fmt.Errorf("%s is not on PATH; the frontend gate installs each package with bun and runs its scripts under node", tool)
		}
	}
	var failed []string
	for _, p := range projects {
		dir := path.Clean(filepath.ToSlash(p))
		if err := checkProject(dir, root, out, run, in(dir)); err != nil {
			_, _ = fmt.Fprintf(out, "%s: %v\n", dir, err)
			failed = append(failed, dir)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d frontend package(s) failed: %s", len(failed), len(projects), strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(out, "%d frontend package(s) install, typecheck, test, build and e2e where defined\n", len(projects))
	return nil
}

// checkProject runs one package's install and scripts.
func checkProject(dir, root string, out io.Writer, run, here Exec) error {
	manifest, lock := path.Join(dir, "package.json"), path.Join(dir, "bun.lock")
	if _, err := run(nil, false, "git", "ls-files", "--error-unmatch", "--", manifest, lock); err != nil {
		return fmt.Errorf("%s and %s must both be tracked", manifest, lock)
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest)))
	if err != nil {
		return err
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(body, &pkg); err != nil {
		return fmt.Errorf("%s does not parse: %w", manifest, err)
	}
	_, _ = fmt.Fprintf(out, "bun install --frozen-lockfile in %s\n", dir)
	if _, err := here(nil, true, "bun", "install", "--frozen-lockfile"); err != nil {
		return fmt.Errorf("the frozen install failed; bun.lock does not match package.json: %w", err)
	}
	ran := 0
	for _, s := range frontendScripts {
		if _, ok := pkg.Scripts[s]; !ok {
			continue
		}
		_, _ = fmt.Fprintf(out, "bun run %s in %s\n", s, dir)
		if _, err := here(nil, true, "bun", "run", s); err != nil {
			return fmt.Errorf("bun run %s failed: %w", s, err)
		}
		ran++
	}
	if ran == 0 {
		return fmt.Errorf("%s defines none of %s, so there is nothing to check", manifest, strings.Join(frontendScripts, ", "))
	}
	return nil
}
