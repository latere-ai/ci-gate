// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"fmt"
	"io"
	"os"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// VulnVersion pins govulncheck. One version, in one place, moved by one
// commit: a `@latest` in a Makefile is a different scanner on every run.
const VulnVersion = "v1.7.0"

// Test runs go vet and the suite. The two are one gate because a package
// vet rejects is a package whose tests were never worth reading.
//
// This is the recipe every repository held in its Makefile as `test`, and
// most of them held it the same way. Owning it here is what makes the ones
// that did not stop being different.
func Test(goBin string, out io.Writer, run Exec) error {
	if _, err := run(nil, true, goBin, "vet", "./..."); err != nil {
		return fmt.Errorf("go vet failed: %w", err)
	}
	if _, err := run(nil, true, goBin, "test", "./..."); err != nil {
		return fmt.Errorf("the test suite failed: %w", err)
	}
	_, _ = fmt.Fprintln(out, "vet and the suite pass")
	return nil
}

// Race runs the suite under the race detector.
//
// The detector needs cgo, which the shipped binary may not: this is about
// finding a race in the tests, not about what is compiled for release, so
// CGO_ENABLED is forced on for this run alone. Five repositories held five
// different timeouts for this; the toolchain's own default applies unless
// the repository budgets more in race.timeout.
func Race(cfg config.Race, goBin string, out io.Writer, run Exec) error {
	env := append(withoutKey(os.Environ(), "CGO_ENABLED"), "CGO_ENABLED=1")
	args := []string{"test", "-race"}
	if t := strings.TrimSpace(cfg.Timeout); t != "" {
		args = append(args, "-timeout", t)
	}
	args = append(args, "./...")
	if _, err := run(env, true, goBin, args...); err != nil {
		return fmt.Errorf("the suite is not race-clean: %w", err)
	}
	_, _ = fmt.Fprintln(out, "the suite is race-clean")
	return nil
}

// Vuln runs govulncheck at the pinned version against every package.
//
// It reports only the vulnerable symbols the build actually reaches, so a
// finding is a call path and not a module in the graph, and the fix is a
// bump that has to happen rather than one that might.
func Vuln(goBin string, out io.Writer, run Exec) error {
	if _, err := run(nil, true, goBin, "run", "golang.org/x/vuln/cmd/govulncheck@"+VulnVersion, "./..."); err != nil {
		return fmt.Errorf("govulncheck found a reachable vulnerability: %w", err)
	}
	_, _ = fmt.Fprintln(out, "no reachable vulnerability")
	return nil
}

// withoutKey drops every KEY= entry from an environment.
func withoutKey(env []string, key string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if len(kv) > len(key) && kv[:len(key)+1] == key+"=" {
			continue
		}
		out = append(out, kv)
	}
	return out
}
