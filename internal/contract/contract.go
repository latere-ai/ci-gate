// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package contract gates the gate set.
//
// Every other gate here holds [ci-gate specs/001] D4 about its own input: a
// check that measured nothing fails. None of them holds it about the set of
// checks. The shared pipeline probes a consumer's Makefile and runs whichever
// targets it finds, so a repository with no `cover` target is not gated on
// coverage and the run still reports success. The absence is printed into a
// step log and never becomes a failure.
//
// This package is D4 one level up: a repository that does not hold a required
// gate fails, and the only way past is a dated exemption.
package contract

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Required is the gate set every consumer holds, as make targets.
//
// It is compiled in rather than configured because the bar belongs to the
// organisation: a consumer that can edit the list can lower it, and then the
// list records what each repository felt like doing. A consumer declares only
// which of these it is behind on, with a reason and a date.
//
// Two gates are deliberately absent. `license` because one repository of
// eighteen holds it, and requiring it would produce seventeen exemptions,
// which declares an intention rather than sets a bar. `validate` because its
// content is per repository -- an instrumented-transport grep in one,
// something else in the next -- so requiring the target would not mean the
// repositories run the same check.
var Required = []string{
	"fmt-check",
	"test",
	"test-hermetic",
	"test-race",
	"cover",
	"lint",
	"lint-config",
	"lint-modernize",
	"spec-lint",
}

// target matches a rule in make's database: a target at the start of a line.
var target = regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+):`)

// Run fails on any required gate the repository neither holds nor exempts.
//
// now is a parameter so the expiry is testable without waiting for a date.
func Run(cfg config.Contract, out io.Writer, exec gates.Exec, now time.Time) error {
	held, err := targets(exec)
	if err != nil {
		return err
	}

	for t := range cfg.Exempt {
		if !slices.Contains(Required, t) {
			return fmt.Errorf("%s: contract exempts %q, which is not a required gate\n"+
				"required: %s\n"+
				"an exemption for a gate nobody requires hides a typo in the name "+
				"of a gate somebody does",
				config.Name, t, strings.Join(Required, ", "))
		}
	}

	var missing, expired []string
	waived := map[string]config.Waiver{}
	for _, t := range Required {
		if held[t] {
			continue
		}
		w, ok := cfg.Exempt[t]
		if !ok {
			missing = append(missing, t)
			continue
		}
		// The date is validated at load, so it parses here. It names a day,
		// not an instant, and it is inclusive: the exemption dies when the
		// day after it begins.
		until, _ := w.UntilDate()
		if !now.Before(until.AddDate(0, 0, 1)) {
			expired = append(expired, fmt.Sprintf("%s (expired %s: %s)", t, w.Until, w.Reason))
			continue
		}
		waived[t] = w
	}

	for _, t := range sortedKeys(waived) {
		_, _ = fmt.Fprintf(out, "waived: %s until %s (%s)\n", t, waived[t].Until, waived[t].Reason)
	}

	// Both lists are reported together. A repository behind on four gates
	// should learn that in one run, not one gate per push.
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("no make target, and no exemption: %s", strings.Join(missing, ", ")))
	}
	if len(expired) > 0 {
		sort.Strings(expired)
		problems = append(problems, fmt.Sprintf("exemption ran out: %s", strings.Join(expired, "; ")))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s\n"+
			"the shared pipeline runs the targets it finds, so a gate with no "+
			"target is a gate that silently does not run: add the target, or "+
			"record in %s why this repository does not hold it yet and by when",
			strings.Join(problems, "\n"), config.Name)
	}

	_, _ = fmt.Fprintf(out, "%d required gates held, %d waived\n", len(Required)-len(waived), len(waived))
	return nil
}

// targets reads make's rule database.
//
// It is the rule database rather than `make -n <target>` because that form
// succeeds when a FILE of the target's name exists: on a case-insensitive
// filesystem a LICENSE file answers for a `license` target, and a dist/
// directory answers for `dist`. Both were live in this organisation and both
// reported a gate as held that no Makefile defined.
//
// The output is read to completion. Matching it through a pipe that closes
// early kills make with SIGPIPE, which exits 141 and reads as "no targets".
func targets(exec gates.Exec) (map[string]bool, error) {
	// make -n exits non-zero when the database is fine but a default goal
	// fails to build, so the error is deliberately not fatal: the database
	// is printed either way, and an empty one is caught below.
	db, _ := exec(nil, false, "make", "-np")
	held := map[string]bool{}
	for _, m := range target.FindAllStringSubmatch(string(db), -1) {
		held[m[1]] = true
	}
	if len(held) == 0 {
		return nil, fmt.Errorf("make printed no rule database: this gate cannot " +
			"tell a repository with no targets from make failing to run, and a " +
			"check that cannot tell those apart passes vacuously")
	}
	return held, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
