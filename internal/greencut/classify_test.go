// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Every row of the classification table, with the line a real run wrote.
func TestClassifyTable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		conclusion string
		log        string
		want       Actor
		evidence   string
	}{{
		name:       "actions minutes",
		conclusion: "failure",
		log:        "The job was not started because you have exceeded your included usage limit for GitHub Actions.",
		want:       Budget, evidence: `the log names "usage limit"`,
	}, {
		name:       "org policy refusal",
		conclusion: "failure",
		log:        "remote: Permission denied to github-actions[bot].\nGitHub Actions is not permitted to create or approve pull requests.",
		want:       Budget, evidence: `the log names "not permitted to create or approve pull requests"`,
	}, {
		name:       "no runner",
		conclusion: "failure",
		log:        "No runner available matching labels: ubuntu-latest",
		want:       Budget, evidence: `the log names "no runner available"`,
	}, {
		name:       "billing stop",
		conclusion: "failure",
		log:        "Recent account payments have failed or your spending limit needs to be updated. Check the billing page.",
		want:       Budget, evidence: `the log names "billing"`,
	}, {
		name:       "registry denied",
		conclusion: "failure",
		log:        "Error: denied: requested access to the resource is denied",
		want:       Infra, evidence: `the log names "denied: "`,
	}, {
		name:       "connection reset",
		conclusion: "failure",
		log:        "read tcp 10.1.2.3:52344->140.82.113.3:443: connection reset by peer",
		want:       Infra, evidence: `the log names "connection reset"`,
	}, {
		name:       "certificate",
		conclusion: "failure",
		log:        "Get \"https://proxy.golang.org/\": x509: certificate signed by unknown authority",
		want:       Infra, evidence: `the log names "x509:"`,
	}, {
		name:       "dns",
		conclusion: "failure",
		log:        "dial tcp: lookup ghcr.io on 127.0.0.53:53: no such host",
		want:       Infra, evidence: `the log names "no such host"`,
	}, {
		name:       "a failing test",
		conclusion: "failure",
		log:        "--- FAIL: TestCutRefusesADirtyTree (0.00s)\n    changelog_test.go:88: the working tree is not clean\nFAIL",
		want:       Code,
	}, {
		name:       "an empty log still names somebody",
		conclusion: "cancelled",
		log:        "",
		want:       Code,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got, evidence := Classify(tc.conclusion, tc.log)
			if got != tc.want {
				t.Errorf("Classify = %s, want %s", got, tc.want)
			}
			if evidence != tc.evidence {
				t.Errorf("evidence = %q, want %q", evidence, tc.evidence)
			}
		})
	}
}

// A runner that dies mid-pull on an exhausted account writes both signals.
// The maintainer is still the one who has to act.
func TestBudgetBeatsInfra(t *testing.T) {
	log := "read tcp: connection reset by peer\nThe job was stopped: usage limit reached"
	got, evidence := Classify("failure", log)
	if got != Budget {
		t.Fatalf("Classify = %s, want %s", got, Budget)
	}
	if !strings.Contains(evidence, "usage limit") {
		t.Errorf("evidence = %q, want the budget phrase", evidence)
	}
}

// The task's rule reads the run's conclusion as well as the log, so a run
// that is red before any job wrote a line still classifies.
func TestTheConclusionIsReadToo(t *testing.T) {
	got, evidence := Classify("failure: no runner available", "")
	if got != Budget {
		t.Fatalf("Classify = %s, want %s", got, Budget)
	}
	if want := `the run concluded "failure: no runner available"`; evidence != want {
		t.Errorf("evidence = %q, want %q", evidence, want)
	}
	if got, _ := Classify("connection reset by the runner", ""); got != Infra {
		t.Errorf("Classify = %s, want %s", got, Infra)
	}
}

// The three lines a refusal ends with, byte for byte.
func TestActorLines(t *testing.T) {
	for _, tc := range []struct {
		actor    Actor
		evidence string
		want     string
	}{
		{Budget, `the log names "usage limit"`, `BUDGET: the maintainer must act (the log names "usage limit")`},
		{Infra, "", "INFRA: re-run the job, then cut again"},
		{Code, "", "CODE: fix and push, then cut again"},
	} {
		if got := tc.actor.Line(tc.evidence); got != tc.want {
			t.Errorf("%s.Line = %q, want %q", tc.actor, got, tc.want)
		}
	}
	if Budget.rank() <= Infra.rank() || Infra.rank() <= Code.rank() {
		t.Error("the ranks must order BUDGET above INFRA above CODE")
	}
}

// The error is at the end of a log, so the cap keeps the end.
func TestLastLinesKeepsTheEnd(t *testing.T) {
	var b strings.Builder
	for i := range 5000 {
		_, _ = fmt.Fprintf(&b, "line %d\n", i)
	}
	got, err := lastLines(strings.NewReader(b.String()), LogLines)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "\n") + 1; n != LogLines {
		t.Errorf("kept %d lines, want %d", n, LogLines)
	}
	if !strings.HasSuffix(got, "line 4999") {
		t.Error("the tail dropped the end of the log")
	}
	if strings.Contains(got, "line 100\n") {
		t.Error("the tail kept the start of the log")
	}

	// A log shorter than the cap is returned whole, with or without a final
	// newline.
	for _, in := range []string{"a\nb\nc", "a\nb\nc\n"} {
		got, err := lastLines(strings.NewReader(in), LogLines)
		if err != nil {
			t.Fatal(err)
		}
		if got != "a\nb\nc" {
			t.Errorf("lastLines(%q) = %q", in, got)
		}
	}
	if _, err := lastLines(errReader{}, LogLines); err == nil {
		t.Error("a stream that fails mid-read is an error, not a short log")
	}
}

// errReader fails on the first read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
