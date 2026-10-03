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
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// zeroSHA is what git passes for a ref that does not exist on one side.
const zeroSHA = "0000000000000000000000000000000000000000"

// Prepush runs golangci-lint over the packages a push changes.
//
// in carries the lines git hands a pre-push hook on stdin, one per ref:
// local ref, local sha, remote ref, remote sha. A line whose remote ref is a
// branch (refs/heads/) and whose local sha is not zero is linted, whatever
// the local ref is called: `git push origin HEAD:main` from a worktree, and
// the release cut's own push, name the local ref HEAD, and a sha pushed
// directly names itself. The pushed commit is diffed against the remote's
// commit, or against the merge base with origin/main when the remote has no
// such branch yet, and the Go files in that diff name the packages. With no
// merge base either, as on the first push to an empty remote, every Go file
// the pushed commit holds names them. A push to a tag is ignored, since a tag
// points at a commit already pushed on a branch, and so is a deletion, which
// pushes nothing. A push that changes no Go file runs nothing.
//
// The linter is the full gate's linter on a subset: the same pinned
// version against the same rendered config, so a package this passes is a
// package CI passes. A push is rare and already waits on the network, and
// this run does not take the linter's machine-wide lock, so a lint in
// another checkout neither blocks it nor is blocked by it.
//
// A dated waiver of the lint gate covers the hook too, until the day it
// names: the full gate reports WAIV lint and runs nothing, and a hook that
// held the same tree to more would refuse every push touching a package
// the waiver was written for. now is the day the waiver is read against.
func Prepush(root string, cfg *config.Config, goBin string, in io.Reader, out io.Writer, run gates.Exec, now time.Time) error {
	if w, ok := cfg.Waive["lint"]; ok {
		// Validated at load, so it parses; inclusive, like the full gate.
		until, _ := w.UntilDate()
		if now.Before(until.AddDate(0, 0, 1)) {
			_, _ = fmt.Fprintf(out, "lint is waived until %s; nothing to lint\n", w.Until)
			return nil
		}
	}
	pkgs := map[string]bool{}
	refs := 0
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		localSHA, remoteRef, remoteSHA := fields[1], fields[2], fields[3]
		if !strings.HasPrefix(remoteRef, "refs/heads/") || localSHA == zeroSHA {
			continue
		}
		refs++
		files, err := pushedGoFiles(out, run, localSHA, remoteRef, remoteSHA)
		if err != nil {
			return err
		}
		for _, f := range files {
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

// pushedGoFiles lists the Go files one pushed ref brings to the remote, as
// slash paths relative to the repository root.
//
// A branch the remote has is diffed against the remote's commit, and a branch
// it does not have yet against the merge base with origin/main. When there is
// no merge base either, the remote holds nothing the pushed history stands
// on: the first push to an empty remote, or a history that shares no commit
// with origin/main. Everything the pushed commit holds is then new to the
// remote, so every Go file in its tree is listed. Refusing that push would
// leave a new repository no way through its own hook.
func pushedGoFiles(out io.Writer, run gates.Exec, localSHA, remoteRef, remoteSHA string) ([]string, error) {
	base := remoteSHA
	if base == zeroSHA {
		mb, err := run(nil, false, "git", "merge-base", "origin/main", localSHA)
		if err != nil {
			_, _ = fmt.Fprintf(out, "the remote has no %s and origin/main is no merge base (%v); linting every Go file the push carries\n", remoteRef, err)
			tree, err := run(nil, false, "git", "ls-tree", "-r", "--name-only", "-z", localSHA)
			if err != nil {
				return nil, fmt.Errorf("listing the files the push to %s carries: %w", remoteRef, err)
			}
			return goFiles(string(tree)), nil
		}
		base = strings.TrimSpace(string(mb))
	}
	changed, err := run(nil, false, "git", "diff", "--name-only", "--diff-filter=ACMR", "-z", base, localSHA, "--", "*.go")
	if err != nil {
		return nil, fmt.Errorf("listing the Go files the push to %s changes: %w", remoteRef, err)
	}
	return goFiles(string(changed)), nil
}

// goFiles picks the Go files out of a NUL-separated path list.
func goFiles(list string) []string {
	var files []string
	for f := range strings.SplitSeq(list, "\x00") {
		if strings.HasSuffix(f, ".go") {
			files = append(files, f)
		}
	}
	return files
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
