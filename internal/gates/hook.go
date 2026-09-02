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
)

// Hook runs the pre-commit checks over the staged Go files: gofmt on the
// files, and the modernizers on the packages holding them.
//
// It is scoped to what is staged so the hook stays a few seconds rather
// than a whole-module type-check, and it reads modernize.disable from the
// same config the full gate reads. Eighteen repositories carried a copy of
// this logic with the disabled fixers written into the script, which is
// the config duplicated in a place no gate checks.
//
// Nothing staged is a pass: a commit that touches no Go file has nothing
// for this hook to say.
func Hook(cfg config.Modernize, goBin string, out io.Writer, run Exec) error {
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

	pkgs := map[string]bool{}
	for _, f := range files {
		dir := path.Dir(f)
		// A package under testdata is outside ./..., so the full gate never
		// reads it and the hook must not hold it to more.
		if slices.Contains(strings.Split(dir, "/"), "testdata") {
			continue
		}
		if dir == "." {
			pkgs["."] = true
			continue
		}
		pkgs["./"+dir] = true
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
	return modernize(cfg, goBin, out, run, patterns, func() ([]string, error) { return patterns, nil })
}

// Staged is the hook script every repository installs. It delegates, so the
// checks it runs are the ones the binary holds and not a copy of them.
const Staged = `#!/bin/sh
# pre-commit: the staged Go files are gofmt-formatted and hold no code the
# standard library already covers. The checks live in lateregate; this file
# only calls it. Install with: git config core.hooksPath .githooks
exec go tool lateregate hook
`

// HookInvocation is the line a pre-commit hook must carry to count as the
// shared one.
const HookInvocation = "lateregate hook"

// IsSharedHook reports whether a hook script delegates to the binary.
func IsSharedHook(script string) bool {
	for line := range strings.SplitSeq(script, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, HookInvocation) {
			return true
		}
	}
	return false
}
