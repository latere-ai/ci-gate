// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"io"
	"path"
	"slices"
	"sort"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/license"
)

// GoimportsModule and GoimportsVersion pin the import grouper the hook runs.
// It is run through the toolchain at a pinned version, like golangci-lint,
// rather than taken as a dependency of this module: one binary invocation
// costs nothing after the first build, and the dependency graph is gated.
// The linter's goimports check reads the same rule from the rendered config
// with the module path as the local prefix, so this pin moves with that one.
const (
	GoimportsModule  = "golang.org/x/tools/cmd/goimports"
	GoimportsVersion = "v0.49.0"
)

// Hook runs the pre-commit checks over the staged Go files: every gate that
// is a file scan, then the modernizers on the packages holding the files.
//
// In order: gofmt, goimports grouping with module as the local prefix, the
// licence notice when the repository declared one, the outbound-HTTP
// instrumentation rule, then go fix. Each of the four scans reads only the
// staged files with no type-check and no network, so the hook stays a few
// seconds. golangci-lint is not here; Prepush runs it once per push.
//
// It reads modernize.disable and license from the same config the full
// gates read. Eighteen repositories carried a copy of this logic with the
// disabled fixers written into the script, which is the config duplicated
// in a place no gate checks.
//
// Nothing staged is a pass: a commit that touches no Go file has nothing
// for this hook to say. A file under testdata is held to gofmt, goimports,
// and the licence notice, and to nothing that reads it as a package.
func Hook(cfg *config.Config, root, module, goBin string, out io.Writer, run Exec) error {
	staged, err := run(nil, false, "git", "diff", "--cached", "--name-only", "--diff-filter=ACM", "-z", "--", "*.go")
	if err != nil {
		return fmt.Errorf("listing staged Go files: %w", err)
	}
	files := nulSeparated(string(staged))
	if len(files) == 0 {
		_, _ = fmt.Fprintln(out, "no Go files staged")
		return nil
	}

	listed, err := run(nil, false, "gofmt", append([]string{"-l"}, files...)...)
	if err != nil {
		return fmt.Errorf("gofmt: %w", err)
	}
	if unformatted := nonEmptyLines(string(listed)); len(unformatted) > 0 {
		for _, f := range unformatted {
			_, _ = fmt.Fprintln(out, "  "+f)
		}
		return fmt.Errorf("%d staged file(s) are not gofmt-formatted; run gofmt -w on them and re-stage", len(unformatted))
	}

	args := []string{"run", GoimportsModule + "@" + GoimportsVersion}
	if module != "" {
		args = append(args, "-local", module)
	}
	args = append(args, "-l")
	grouped, err := run(nil, false, goBin, append(args, files...)...)
	if err != nil {
		return fmt.Errorf("goimports: %w", err)
	}
	if ungrouped := nonEmptyLines(string(grouped)); len(ungrouped) > 0 {
		for _, f := range ungrouped {
			_, _ = fmt.Fprintln(out, "  "+f)
		}
		fix := "goimports -w"
		if module != "" {
			fix = "goimports -local " + module + " -w"
		}
		return fmt.Errorf("%d staged file(s) have imports the linter would regroup; run %s on them and re-stage", len(ungrouped), fix)
	}

	if strings.TrimSpace(cfg.License.SPDX) != "" {
		bad, err := license.Files(cfg.License, root, files)
		if err != nil {
			return fmt.Errorf("license: %w", err)
		}
		if len(bad) > 0 {
			for _, b := range bad {
				_, _ = fmt.Fprintln(out, "  "+b)
			}
			return fmt.Errorf("%d staged file(s) without the declared %s notice\n%s",
				len(bad), cfg.License.SPDX, license.Want(cfg.License, "//"))
		}
	}

	var scanned []string
	pkgs := map[string]bool{}
	for _, f := range files {
		dir := path.Dir(f)
		// A package under testdata is outside ./..., so the full gates never
		// read it and the hook must not hold it to more.
		if slices.Contains(strings.Split(dir, "/"), "testdata") {
			continue
		}
		scanned = append(scanned, f)
		if dir == "." {
			pkgs["."] = true
			continue
		}
		pkgs["./"+dir] = true
	}
	if found := OtelClientFiles(root, scanned); len(found) > 0 {
		for _, f := range found {
			_, _ = fmt.Fprintln(out, "  "+f)
		}
		return fmt.Errorf("%d uninstrumented outbound HTTP client(s) in the staged files", len(found))
	}
	if len(pkgs) == 0 {
		_, _ = fmt.Fprintln(out, "the staged Go files are formatted and sit under testdata")
		return nil
	}
	patterns := make([]string, 0, len(pkgs))
	for p := range pkgs {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	return modernize(cfg.Modernize, goBin, out, run, patterns, func() ([]string, error) { return patterns, nil })
}

// Staged is the pre-commit script every repository installs. It delegates,
// so the checks it runs are the ones the binary holds and not a copy of them.
const Staged = `#!/bin/sh
# pre-commit: the staged Go files are gofmt-formatted, group their imports
# the way the linter wants, carry the licence notice, build no outbound HTTP
# client without a trace, and hold no code the standard library already
# covers. The checks live in lateregate; this file only calls it.
# Install with: git config core.hooksPath .githooks
exec go tool lateregate hook
`

// Prepush is the pre-push script every repository installs. Git hands the
// pushed refs to a pre-push hook on stdin, once, so the script captures them
// and a repository that adds its own check below the delegation reads $refs
// rather than an already-drained stdin.
const Prepush = `#!/bin/sh
# pre-push: a release tag is refused unless CHANGELOG.md at its commit has a
# section for it, then golangci-lint runs over the packages this push
# changes, so a finding is seen before CI rather than as a fix commit after
# it. The checks live in lateregate; this file only calls it. Refs arrive on
# stdin, once: a repository that adds its own check below reads $refs.
refs=$(cat)
printf '%s\n' "$refs" | go tool lateregate prepush || exit 1
`

// HookInvocation and PrepushInvocation are the lines a hook script must
// carry to count as the shared one.
const (
	HookInvocation    = "lateregate hook"
	PrepushInvocation = "lateregate prepush"
)

// IsSharedHook reports whether a pre-commit script delegates to the binary.
func IsSharedHook(script string) bool { return Delegates(script, HookInvocation) }

// Delegates reports whether a hook script carries invocation on a line that
// is not a comment. The test is the delegation line, not the bytes, so a
// repository may add lines of its own around it.
func Delegates(script, invocation string) bool {
	for line := range strings.SplitSeq(script, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, invocation) {
			return true
		}
	}
	return false
}
