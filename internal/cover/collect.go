// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
	"fmt"
	"io"
	"path/filepath"

	"latere.ai/x/ci-gate/internal/gates"
)

// Profile is the file the collecting run writes, at the repository root so
// the pipeline can upload it and a developer can open it with go tool cover.
const Profile = "coverage.out"

// Collect runs the suite instrumented and returns the profile's path.
//
// -coverpkg=./... is what makes the profile honest across packages: without
// it a statement is credited only to the package whose tests ran it, and a
// package exercised entirely through another's tests reads as untested.
// -covermode=atomic is the mode the race detector accepts, so the same flags
// work when a repository runs its own tiers.
//
// Four repositories held four versions of this command, one of which wrote
// the profile to a name the gate then did not read.
func Collect(goBin, root string, out io.Writer, run gates.Exec) (string, error) {
	_, err := run(nil, true, goBin, "test", "./...",
		"-covermode=atomic", "-coverpkg=./...", "-coverprofile="+Profile)
	if err != nil {
		return "", fmt.Errorf("the instrumented test run failed: %w", err)
	}
	return filepath.Join(root, Profile), nil
}
