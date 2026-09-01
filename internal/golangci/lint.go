// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package golangci

import (
	"fmt"
	"io"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Version pins golangci-lint. It was pinned in the pipeline's input, in
// this repository's Makefile and in every consumer's, and three pins agree
// only until one moves.
const Version = "v2.13.1"

// Module is the linter's main package, run through the toolchain so the
// binary is built by the module's own Go version rather than a system
// install built by whichever one was current when it was installed.
const Module = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"

// Lint renders the shared configuration and runs the linter against it.
//
// The render happens first, every time. golangci-lint with no config falls
// back to its own default linter set and lints something nobody asked for,
// and a stale rendered file lints something somebody asked for last month.
// A repository that keeps its own config is left to it, with the reason
// printed so the exception stays visible.
func Lint(root string, cfg *config.Config, goBin string, out io.Writer, run gates.Exec) error {
	reason, err := Own(root, cfg)
	if err != nil {
		return err
	}
	if reason != "" {
		_, _ = fmt.Fprintf(out, "%s is this repository's own, not generated: %s\n", Name, reason)
	} else {
		path, err := Write(root, cfg, goBin)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "wrote "+path)
	}
	if _, err := run(nil, true, goBin, "run", Module+"@"+Version, "run", "./..."); err != nil {
		return fmt.Errorf("golangci-lint %s reported findings: %w", Version, err)
	}
	_, _ = fmt.Fprintln(out, "golangci-lint "+Version+" reports nothing")
	return nil
}
