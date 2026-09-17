// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"fmt"
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
	for _, actor := range []struct {
		name    Actor
		signals []string
	}{{Budget, budgetSignals}, {Infra, infraSignals}} {
		for _, s := range actor.signals {
			if strings.Contains(c, s) {
				return actor.name, fmt.Sprintf("the run concluded %q", conclusion)
			}
		}
		for _, s := range actor.signals {
			if strings.Contains(l, s) {
				return actor.name, fmt.Sprintf("the log names %q", s)
			}
		}
	}
	return Code, ""
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
