// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

// uncachedNote is what a run prints when it turns the test cache off.
const uncachedNote = "the last run of this repository's suite left something behind, so this run " +
	"passes -count=1 and every package runs again instead of replaying its cached result; " +
	"the first run that leaves nothing turns the cache back on"

// leakMarker is the path of the file that records that the last run of the
// repository's suite left a leak, beside the sandbox's lock file. It is empty
// where the sandbox is not shared between runs: a fresh sandbox per run has
// no cached result to replay.
//
// The sandbox path is the same on every run so that a package whose inputs
// did not change replays its cached result. A package that leaks passes its
// test, so its result is cached too, and the replay creates nothing: the leak
// is reported once and the same tree is green on the next run. The marker is
// what tells the next run to run every package again.
func leakMarker(root string) (string, error) {
	if !lockable {
		return "", nil
	}
	name, err := sandboxName(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(os.TempDir(), name+".leaked"), nil
}

// markerPresent reports whether the marker exists. A marker that cannot be
// read is treated as present: running every package again costs time, and
// trusting the cache over an unreadable record could hide a leak.
func markerPresent(marker string) bool {
	if marker == "" {
		return false
	}
	_, err := os.Stat(marker)
	return !errors.Is(err, os.ErrNotExist)
}

// settleMarker records the run's verdict in the marker: a leak writes it, a
// run without one removes it. A marker that cannot be written is reported,
// because the next run would then replay the leak as clean.
func settleMarker(marker string, leaked bool, out io.Writer) string {
	if marker == "" {
		return ""
	}
	if leaked {
		body := []byte("the last run left a leak; the next run bypasses the test cache\n")
		if err := os.WriteFile(marker, body, 0o600); err != nil {
			return fmt.Sprintf("recording the leak in %s: %v; without it the next run replays "+
				"cached results and passes, so remove the leak and run again", marker, err)
		}
		return ""
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(out, "removing %s: %v; the next run bypasses the test cache again\n", marker, err)
	}
	return ""
}

// uncached is argv with -count=1 after the test subcommand, for a command
// that is go test, and whether it could be added.
func uncached(argv []string) ([]string, bool) {
	if len(argv) < 2 || filepath.Base(argv[0]) != "go" || argv[1] != "test" {
		return argv, false
	}
	return slices.Insert(slices.Clone(argv), 2, "-count=1"), true
}
