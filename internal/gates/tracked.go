// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"io"
	"os"
	"path/filepath"
)

// TrackedFiles lists the files git tracks under root that match pathspecs, as
// slash paths relative to root, outside any directory named in skip. Files the
// index lists but the working tree no longer holds are left out.
//
// The scans that read source take their file list from here rather than from a
// walk, so a laptop with a scratch file or a git-ignored build directory reads
// the same set a clean CI checkout does. what names the list in the error
// when git cannot produce it.
func TrackedFiles(root, what string, skip []string, pathspecs ...string) ([]string, error) {
	files, err := trackedFiles(OSExec(root, io.Discard), what, skip, pathspecs...)
	if err != nil {
		return nil, err
	}
	present := files[:0]
	for _, f := range files {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			present = append(present, f)
		}
	}
	return present, nil
}
