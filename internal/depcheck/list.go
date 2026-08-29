// Copyright 2026 Latere AI.
// Licensed under the MIT License.

package depcheck

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GoLister returns a Lister backed by `go list -deps`.
//
// The template prints each dependency's import path and its module, so the
// standard library (which has no module) and the main module's own packages
// are both identifiable without guessing from the path shape.
func GoLister(goBin, dir string) Lister {
	return func(goos, goarch, pkg string) ([]string, error) {
		main, err := mainModule(goBin, dir)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(goBin, "list", "-deps",
			"-f", "{{if .Module}}{{.ImportPath}} {{.Module.Path}}{{end}}", pkg)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		if goos != "" {
			cmd.Env = append(cmd.Env, "GOOS="+goos, "GOARCH="+goarch)
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		outBytes, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}

		var deps []string
		for line := range strings.SplitSeq(string(outBytes), "\n") {
			path, mod, ok := strings.Cut(strings.TrimSpace(line), " ")
			if !ok || path == "" {
				continue
			}
			if mod == main {
				continue // the module is not a dependency of itself
			}
			deps = append(deps, path)
		}
		return deps, nil
	}
}

func mainModule(goBin, dir string) (string, error) {
	cmd := exec.Command(goBin, "list", "-m")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cannot read the main module path: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
