// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// tempVars are the names a process reads to find the temporary directory.
// All three are set: Go and the C library read TMPDIR, Windows tools read TMP
// and TEMP, and a suite that shells out to either has to land in the sandbox
// or the gate measures the wrong directory.
var tempVars = []string{"TMPDIR", "TMP", "TEMP"}

// TempDir runs the test suite against an empty temporary directory and fails
// on anything that survives it.
//
// A test that calls os.MkdirTemp and does not remove the result leaks it for
// the life of the machine. Nothing fails when it happens, the suite stays
// green, and the cost is invisible until a disk fills: one Go repository
// leaked 168GB across three sites over a few months, and the first symptom was
// a laptop with 8.2GB free on a 926GB volume. Two of the three sites carried a
// comment claiming the directory was removed by the process that made it.
//
// The gate is dynamic rather than a source scan because the leak is a property
// of the process tree, not of the source. A suite that shells out to a
// compiler, a container runtime or a package manager leaks through those too,
// and no amount of reading the caller's code sees it. That also makes the
// check language-agnostic: cfg.Command names whatever runs the suite.
func TempDir(cfg config.TempDir, argv []string, out io.Writer, run Exec) error {
	if len(argv) == 0 {
		argv = cfg.Argv()
	}
	sandbox, err := os.MkdirTemp("", "lateregate-tempdir")
	if err != nil {
		return fmt.Errorf("making the sandbox: %w", err)
	}
	// A gate that leaks while checking for leaks is worse than no gate. This
	// runs whatever the suite did, including when it failed.
	defer func() { _ = os.RemoveAll(sandbox) }()

	before, err := touchedAt(sandbox)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "TMPDIR=%s\n", sandbox)
	_, runErr := run(envWithTemp(os.Environ(), sandbox), true, argv[0], argv[1:]...)

	after, err := touchedAt(sandbox)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(sandbox)
	if err != nil {
		return fmt.Errorf("reading the sandbox: %w", err)
	}

	// An empty sandbox does not distinguish a clean run from one that never
	// used it. A suite launched through a wrapper that resets the environment,
	// or one whose runner has its own temporary directory, would report a
	// perfect score having proved nothing. The directory's modification time
	// moves when an entry is created or removed, so a run that touched it at
	// all is visible even when it cleaned up perfectly after itself.
	if len(entries) == 0 && !after.After(before) {
		return fmt.Errorf("nothing ever wrote to %s, so the run did not use it\n"+
			"the gate cannot tell a clean suite from one that ignored TMPDIR: "+
			"check that %q inherits the environment", sandbox, strings.Join(argv, " "))
	}

	leaked, allowed := classify(entries, cfg, sandbox)
	for _, a := range allowed {
		_, _ = fmt.Fprintf(out, "  allowed %s: %s\n", a.name, a.why)
	}
	if len(leaked) > 0 {
		for _, l := range leaked {
			_, _ = fmt.Fprintf(out, "  %s (%s)\n", l.name, humanSize(l.size))
		}
		return fmt.Errorf("%d entr%s survived the test run, %s in all\n"+
			"a directory left under TMPDIR is never deleted: use the test "+
			"framework's own temporary directory, or remove it from the "+
			"function that outlives every test",
			len(leaked), plural(len(leaked)), humanSize(totalSize(leaked)))
	}
	// The suite's own verdict comes second. A failing suite that also leaked
	// should report the leak, because a red suite gets re-run and a leak that
	// only shows on a green one is a leak nobody sees.
	if runErr != nil {
		return fmt.Errorf("the test run failed: %w", runErr)
	}
	_, _ = fmt.Fprintf(out, "nothing survived the test run\n")
	return nil
}

// survivor is one entry left under the sandbox.
type survivor struct {
	name string
	why  string
	size int64
}

// classify splits what survived into what the config admits and what it does
// not, each sorted by name so the report is stable between runs.
func classify(entries []os.DirEntry, cfg config.TempDir, dir string) (leaked, allowed []survivor) {
	for _, e := range entries {
		s := survivor{name: e.Name(), size: treeSize(filepath.Join(dir, e.Name()))}
		if why, ok := cfg.AllowedFor(s.name); ok {
			s.why = why
			allowed = append(allowed, s)
			continue
		}
		leaked = append(leaked, s)
	}
	sort.Slice(leaked, func(i, j int) bool { return leaked[i].name < leaked[j].name })
	sort.Slice(allowed, func(i, j int) bool { return allowed[i].name < allowed[j].name })
	return leaked, allowed
}

// touchedAt is the modification time of the sandbox itself, which moves when
// an entry is created or removed inside it.
func touchedAt(dir string) (time.Time, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat %s: %w", dir, err)
	}
	return fi.ModTime(), nil
}

// envWithTemp points every temporary-directory variable at dir, replacing any
// the caller already had.
func envWithTemp(env []string, dir string) []string {
	out := make([]string, 0, len(env)+len(tempVars))
	for _, kv := range env {
		keep := true
		for _, v := range tempVars {
			if strings.HasPrefix(kv, v+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	for _, v := range tempVars {
		out = append(out, v+"="+dir)
	}
	return out
}

// treeSize is the number of bytes a survivor holds. The size is what makes a
// leak worth acting on: a report of 432 directories says less than one of
// 160GB.
func treeSize(path string) int64 {
	var total int64
	// The callback never returns an error -- an unreadable entry is skipped,
	// because a size is best-effort -- so WalkDir's own error is always nil.
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

func totalSize(ss []survivor) int64 {
	var total int64
	for _, s := range ss {
		total += s.size
	}
	return total
}

// humanSize renders bytes the way df does, because the number is read by a
// person deciding whether a leak matters.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
