// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"fmt"
	"slices"
	"strings"
)

// Actor names who has to act on a red run. Three kinds of red want three
// different people, and a refusal that does not say which hands all three to
// whoever ran the cut.
type Actor string

// The three actors, in the order Classify considers them.
const (
	// Budget is the maintainer's alone: an exhausted Actions allowance, a
	// billing stop, no runner to be had, or an org policy that refuses the
	// job. Nobody else can move any of them.
	Budget Actor = "BUDGET"
	// Infra is a run that failed on something outside the repository and
	// outside the account: a registry refusal, a dropped connection, a
	// certificate or a name that did not resolve. Running it again is the
	// fix often enough to be the advice.
	Infra Actor = "INFRA"
	// Code is the fallback rather than a match, so an unreadable log still
	// names somebody.
	Code Actor = "CODE"
)

// redConclusions are the conclusions a cut refuses on. A run that never
// started, one that died on the clock, and one waiting for somebody to
// approve it are all runs whose answer nobody has, which is not green.
// `neutral`, `skipped` and `stale` are answers, so they are not here.
var redConclusions = []string{"failure", "cancelled", "timed_out", "startup_failure", "action_required"}

// isRed reports whether a completed run refuses the cut. A run still in
// progress has an empty conclusion and never reaches here.
func isRed(conclusion string) bool { return slices.Contains(redConclusions, conclusion) }

// testSignals are the marks a Go suite leaves in a log. They decide a
// `timed_out` run, which is infrastructure until the suite is what ran out.
var testSignals = []string{"--- fail:", "=== run ", "panic: test timed out", "*** test killed"}

// LogLines is the tail of a failing job's log Classify reads. The error is at
// the end of a log, and a job that printed a hundred thousand lines before it
// failed is a job whose first two thousand say nothing.
const LogLines = 2000

// budgetSignals are the phrases that make a red run the maintainer's. Each is
// matched case-insensitively against the run's conclusion and then its log.
var budgetSignals = []string{
	"actions minutes",
	"billing",
	"payment",
	"spending limit",
	"usage limit",
	"included quota",
	"no runner available",
	"no hosted runner",
	"runner capacity",
	"not permitted to create or approve pull requests",
	"organization policy",
	"policy does not permit",
}

// infraSignals are the phrases that make a red run worth running again. They
// are checked after every budget signal: a runner that dies mid-pull on an
// exhausted account writes both, and the maintainer is still the one who has
// to act.
var infraSignals = []string{
	"denied: ",
	"connection reset",
	"connection refused",
	"tls handshake",
	"x509:",
	"certificate",
	"no such host",
	"temporary failure in name resolution",
	"server misbehaving",
	"i/o timeout",
}

// Classify reads a red run's conclusion and the tail of its failing job's log
// and returns the actor and the evidence for it.
//
// The evidence is a phrase, not a verdict: it is what the person who forwards
// a BUDGET refusal to the maintainer forwards with it, and "trust me, it is
// budget" is not a thing to forward.
func Classify(conclusion, log string) (Actor, string) {
	c, l := strings.ToLower(conclusion), strings.ToLower(log)
	if actor, evidence, ok := match(Budget, budgetSignals, conclusion, c, l); ok {
		return actor, evidence
	}
	// A run that died on the clock is a wedged read until the log names a
	// test. A suite that ran out of time is the suite's problem, and running
	// it again only spends the clock twice. The budget signals are read
	// first, because a runner that stalls on an exhausted account times out
	// like any other.
	if c == "timed_out" {
		for _, s := range testSignals {
			if strings.Contains(l, s) {
				return Code, ""
			}
		}
		return Infra, fmt.Sprintf("the run concluded %q", conclusion)
	}
	if actor, evidence, ok := match(Infra, infraSignals, conclusion, c, l); ok {
		return actor, evidence
	}
	return Code, ""
}

// match reads one actor's signals against the conclusion and then the log, so
// the evidence says which of the two named it.
func match(actor Actor, signals []string, conclusion, c, l string) (Actor, string, bool) {
	for _, s := range signals {
		if strings.Contains(c, s) {
			return actor, fmt.Sprintf("the run concluded %q", conclusion), true
		}
	}
	for _, s := range signals {
		if strings.Contains(l, s) {
			return actor, fmt.Sprintf("the log names %q", s), true
		}
	}
	return "", "", false
}

// Line is the single line a refusal ends with.
//
// Only BUDGET carries its evidence, because that line is read by somebody who
// has to hand the problem on. The other two name the next command instead,
// because the person reading them is the person who runs it.
func (a Actor) Line(evidence string) string {
	switch a {
	case Budget:
		return string(Budget) + ": the maintainer must act (" + evidence + ")"
	case Infra:
		return string(Infra) + ": re-run the job, then cut again"
	default:
		return string(Code) + ": fix and push, then cut again"
	}
}

// rank orders two actors so a report holding several findings closes with the
// one whose fix has to happen first.
func (a Actor) rank() int {
	switch a {
	case Budget:
		return 2
	case Infra:
		return 1
	default:
		return 0
	}
}
