// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package golangci

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// zeroSHA is what git passes for a ref that does not exist on one side.
const zeroSHA = "0000000000000000000000000000000000000000"

// Prepush runs golangci-lint over the packages a push changes.
//
// in carries the lines git hands a pre-push hook on stdin, one per ref:
// local ref, local sha, remote ref, remote sha. For each branch ref the
// pushed commit is diffed against the remote's commit, or against the merge
// base with origin/main when the remote has no such ref yet, and the Go
// files in that diff name the packages. Tag refs are ignored: a tag points
// at a commit that was already pushed on a branch. A push that changes no
// Go file runs nothing.
//
// The linter is the full gate's linter on a subset: the same pinned
// version against the same rendered config, so a package this passes is a
// package CI passes. A push is rare and already waits on the network, and
// this run does not take the linter's machine-wide lock, so a lint in
// another checkout neither blocks it nor is blocked by it.
func Prepush(root string, cfg *config.Config, goBin string, in io.Reader, out io.Writer, run gates.Exec) error {
	pkgs := map[string]bool{}
	refs := 0
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		localRef, localSHA, remoteSHA := fields[0], fields[1], fields[3]
		if !strings.HasPrefix(localRef, "refs/heads/") || localSHA == zeroSHA {
			continue
		}
		refs++
		base := remoteSHA
		if base == zeroSHA {
			mb, err := run(nil, false, "git", "merge-base", "origin/main", localSHA)
			if err != nil {
				return fmt.Errorf("the remote has no %s yet and origin/main is not a merge base to diff against: %w", localRef, err)
			}
			base = strings.TrimSpace(string(mb))
		}
		changed, err := run(nil, false, "git", "diff", "--name-only", "--diff-filter=ACMR", "-z", base, localSHA, "--", "*.go")
		if err != nil {
			return fmt.Errorf("listing the Go files %s changes: %w", localRef, err)
		}
		for f := range strings.SplitSeq(string(changed), "\x00") {
			if f == "" {
				continue
			}
			dir := path.Dir(f)
			// A package under testdata is outside ./..., which the full gate
			// reads, so the hook must not hold it to more.
			if slices.Contains(strings.Split(dir, "/"), "testdata") {
				continue
			}
			// A file under a nested module (its own go.mod below the root)
			// is not a package of the main module either: `go run ... ./dir`
			// from the root fails with "main module does not contain
			// package", and the full gate never reads it. A spike tool or an
			// example with its own dependencies lives there on purpose.
			if inNestedModule(root, dir) {
				continue
			}
			if dir == "." {
				pkgs["."] = true
				continue
			}
			pkgs["./"+dir] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading the pushed refs: %w", err)
	}
	if refs == 0 {
		_, _ = fmt.Fprintln(out, "no branch pushed; nothing to lint")
		return nil
	}
	if len(pkgs) == 0 {
		_, _ = fmt.Fprintln(out, "this push changes no Go file; nothing to lint")
		return nil
	}
	patterns := make([]string, 0, len(pkgs))
	for p := range pkgs {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	_, _ = fmt.Fprintf(out, "linting %d package(s) this push changes\n", len(patterns))
	// The linter takes a machine-wide lock so two full runs do not fight
	// over one cache. A hook that waits on, or dies from, a lint running in
	// another checkout is the hook people bypass; this run is a subset in a
	// session that is already waiting, so it opts out of the lock.
	return lint(root, cfg, goBin, out, run, []string{"--allow-parallel-runners"}, patterns)
}

// inNestedModule reports whether dir, a slash path relative to root, sits
// under a go.mod other than the root's, walking up from dir to the root.
func inNestedModule(root, dir string) bool {
	for dir != "." && dir != "" && dir != "/" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir), "go.mod")); err == nil {
			return true
		}
		dir = path.Dir(dir)
	}
	return false
}
