// Copyright 2026 Latere AI.
// Licensed under the MIT License.

// Package depcheck gates what a package's build is allowed to reach.
//
// A dependency arriving is not a failure. A dependency arriving that nobody
// decided on is, and the first symptom is otherwise a slower build. The gate
// exists so a subtree that promises a small footprint keeps promising it: one
// upstream import lands on the next `go get`, and nothing else notices.
//
// The build list is taken with `go list -deps` rather than read from go.mod,
// because a module graph says what *could* be reached and the claim being
// defended is about what is. That distinction is also why pinning a tool
// dependency in go.mod does not trip this gate: a tool is never imported by
// the packages it checks.
package depcheck

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// Lister returns the import paths a package's build reaches on one platform,
// excluding the standard library and the module's own packages.
type Lister func(goos, goarch, pkg string) ([]string, error)

// Run checks every gated package on every configured platform.
func Run(cfg config.Depcheck, out io.Writer, list Lister) error {
	if len(cfg.Packages) == 0 {
		fmt.Fprintln(out, "depcheck: no gated packages configured, nothing to check")
		return nil
	}
	platforms := cfg.Platforms
	if len(platforms) == 0 {
		platforms = []string{""} // the host's own platform
	}

	var problems []string
	for _, pkg := range slices.Sorted(maps.Keys(cfg.Packages)) {
		g := cfg.Packages[pkg]
		reached := map[string]bool{} // import path -> seen
		used := map[string]bool{}    // allowed prefix -> matched something
		for _, p := range platforms {
			goos, goarch, err := split(p)
			if err != nil {
				return err
			}
			deps, err := list(goos, goarch, pkg)
			if err != nil {
				// A package that does not build is a build error, and the
				// build is another gate's job.
				return fmt.Errorf("go list -deps %s on %s: %w", pkg, label(p), err)
			}
			for _, d := range deps {
				reached[d] = true
				if prefix, ok := allowedBy(g.Allow, d); ok {
					used[prefix] = true
					continue
				}
				problems = append(problems, fmt.Sprintf(
					"%s reaches %s on %s, which %s does not admit",
					pkg, d, label(p), decisionOf(g)))
			}
		}
		fmt.Fprintf(out, "  %-52s %d package(s) reached\n", short(pkg), len(reached))

		// A stale allowance sits there admitting whatever later moves under
		// it, so an entry the build no longer reaches is a failure too.
		for _, prefix := range slices.Sorted(maps.Keys(g.Allow)) {
			if !used[prefix] {
				problems = append(problems, fmt.Sprintf(
					"%s allows %s, which its build does not reach; a stale allowance "+
						"admits whatever later moves under it", pkg, prefix))
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		for _, p := range problems {
			fmt.Fprintln(out, "  "+p)
		}
		return fmt.Errorf("%d dependency problem(s)", len(problems))
	}
	fmt.Fprintf(out, "\nevery gated package reaches only what it admits (%d package(s), %d platform(s))\n",
		len(cfg.Packages), len(platforms))
	return nil
}

// allowedBy reports the prefix admitting an import path.
//
// The prefix must end at a path boundary, so a sibling module whose path
// starts with the same bytes is a different module.
func allowedBy(allow map[string]string, dep string) (string, bool) {
	for prefix := range allow {
		if dep == prefix || strings.HasPrefix(dep, strings.TrimSuffix(prefix, "/")+"/") {
			return prefix, true
		}
	}
	return "", false
}

func decisionOf(g config.Gated) string {
	if g.Decision == "" {
		return "its allowlist"
	}
	return g.Decision
}

func split(platform string) (goos, goarch string, err error) {
	if platform == "" {
		return "", "", nil
	}
	goos, goarch, ok := strings.Cut(platform, "/")
	if !ok || goos == "" || goarch == "" {
		return "", "", fmt.Errorf("depcheck: platform %q is not GOOS/GOARCH", platform)
	}
	return goos, goarch, nil
}

func label(platform string) string {
	if platform == "" {
		return "this host"
	}
	return platform
}

func short(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 && len(pkg) > 44 {
		return "..." + pkg[i:]
	}
	return pkg
}
