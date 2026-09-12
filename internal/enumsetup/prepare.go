// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package enumsetup installs the locked frontend dependencies needed by the
// TypeScript gate. Preparation is explicit; checking never installs packages.
package enumsetup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

type installation struct {
	dir  string
	lock string
	bun  bool
}

// Prepare installs each configured project's nearest lockfile owner once.
// All projects and tracked lockfiles are checked before the first install.
func Prepare(projects []config.TypeScriptEnums, root string, out io.Writer, run gates.Exec) error {
	if len(projects) == 0 {
		return fmt.Errorf("enums.typescript names no projects to prepare")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	installs := map[string]installation{}
	for _, project := range projects {
		if !filepath.IsLocal(project.Project) {
			return fmt.Errorf("project %q must stay inside the repository", project.Project)
		}
		path := filepath.Join(root, project.Project)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("project %q is not a readable tsconfig file", project.Project)
		}
		install, err := owner(root, filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("project %s: %w", project.Project, err)
		}
		installs[install.dir] = install
	}
	dirs := make([]string, 0, len(installs))
	for dir := range installs {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		install := installs[dir]
		rel, _ := filepath.Rel(root, dir)
		lock := filepath.ToSlash(filepath.Join(rel, install.lock))
		manifest := filepath.ToSlash(filepath.Join(rel, "package.json"))
		if _, err := run(nil, false, "git", "ls-files", "--error-unmatch", "--", manifest, lock); err != nil {
			return fmt.Errorf("%s and %s must be tracked before preparation: %w", manifest, lock, err)
		}
	}
	for _, dir := range dirs {
		install := installs[dir]
		name, args := "npm", []string{"--prefix", dir, "ci"}
		if install.bun {
			name, args = "bun", []string{"install", "--frozen-lockfile", "--cwd", dir}
		}
		_, _ = fmt.Fprintf(out, "installing %s from %s\n", dir, install.lock)
		if _, err := run(nil, true, name, args...); err != nil {
			return fmt.Errorf("frozen install in %s failed: %w", dir, err)
		}
	}
	return nil
}

func owner(root, dir string) (installation, error) {
	for {
		if info, err := os.Stat(filepath.Join(dir, "package.json")); err == nil && info.Mode().IsRegular() {
			var found []string
			for _, lock := range []string{"package-lock.json", "npm-shrinkwrap.json", "bun.lock", "bun.lockb"} {
				if info, err := os.Stat(filepath.Join(dir, lock)); err == nil && info.Mode().IsRegular() {
					found = append(found, lock)
				}
			}
			if len(found) > 1 {
				return installation{}, fmt.Errorf("%s holds multiple lockfiles; keep one package manager's lockfile", dir)
			}
			if len(found) == 1 {
				lock := found[0]
				return installation{dir: dir, lock: lock, bun: lock == "bun.lock" || lock == "bun.lockb"}, nil
			}
		}
		if dir == root {
			return installation{}, fmt.Errorf("no package.json with an npm or Bun lockfile inside the repository; commit a lockfile before preparation")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return installation{}, fmt.Errorf("project directory is outside the repository")
		}
		dir = parent
	}
}
