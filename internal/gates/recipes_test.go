// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func joined(c call) string { return c.name + " " + strings.Join(c.args, " ") }

// vet and the suite are one gate, in that order: a package vet rejects is
// one whose tests were never worth reading.
func TestTestRunsVetThenTheSuite(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := Test("go", &sb, fake(t, &calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || joined(calls[0]) != "go vet ./..." || joined(calls[1]) != "go test ./..." {
		t.Fatalf("ran %v", calls)
	}
	for _, c := range calls {
		if !c.stream {
			t.Errorf("%s must stream to the terminal", joined(c))
		}
	}
}

func TestTestStopsAtAVetFailure(t *testing.T) {
	var calls []call
	err := Test("go", &strings.Builder{}, fake(t, &calls, errors.New("vet: bad")))
	if err == nil || !strings.Contains(err.Error(), "go vet failed") {
		t.Fatalf("want the vet failure, got %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("the suite must not run after vet fails; ran %v", calls)
	}
}

func TestTestReportsASuiteFailure(t *testing.T) {
	var calls []call
	err := Test("go", &strings.Builder{}, fake(t, &calls, "", errors.New("exit 1")))
	if err == nil || !strings.Contains(err.Error(), "test suite failed") {
		t.Fatalf("want the suite failure, got %v", err)
	}
}

// The detector needs cgo whatever the shipped binary is built with, so the
// run forces it on, and only this run.
func TestRaceForcesCgoOnForTheRun(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	var calls []call
	if err := Race("go", &strings.Builder{}, fake(t, &calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || joined(calls[0]) != "go test -race ./..." {
		t.Fatalf("ran %v", calls)
	}
	if !hasEnv(calls[0].env, "CGO_ENABLED=1") {
		t.Errorf("CGO_ENABLED=1 must be set for the race run; env has %v", calls[0].env)
	}
	for _, kv := range calls[0].env {
		if kv == "CGO_ENABLED=0" {
			t.Error("the inherited CGO_ENABLED=0 must be replaced, not shadowed")
		}
	}
}

func TestRaceReportsAFailure(t *testing.T) {
	var calls []call
	err := Race(config.Race{}, "go", &strings.Builder{}, fake(t, &calls, errors.New("race detected")))
	if err == nil || !strings.Contains(err.Error(), "not race-clean") {
		t.Fatalf("want the race failure, got %v", err)
	}
}

// One pinned version, run through the toolchain, against every package.
func TestVulnRunsThePinnedScanner(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := Vuln("go", &sb, fake(t, &calls)); err != nil {
		t.Fatal(err)
	}
	want := "go run golang.org/x/vuln/cmd/govulncheck@" + VulnVersion + " ./..."
	if len(calls) != 1 || joined(calls[0]) != want {
		t.Fatalf("ran %v, want %q", calls, want)
	}
	if !strings.Contains(sb.String(), "no reachable vulnerability") {
		t.Errorf("report:\n%s", sb.String())
	}
}

func TestVulnReportsAFinding(t *testing.T) {
	var calls []call
	err := Vuln("go", &strings.Builder{}, fake(t, &calls, errors.New("exit 3")))
	if err == nil || !strings.Contains(err.Error(), "reachable vulnerability") {
		t.Fatalf("want the finding, got %v", err)
	}
}

func TestWithoutKeyDropsEveryMatch(t *testing.T) {
	got := withoutKey([]string{"A=1", "CGO_ENABLED=0", "B=2", "CGO_ENABLED=1", "CGO_ENABLEDX=3"}, "CGO_ENABLED")
	if strings.Join(got, " ") != "A=1 B=2 CGO_ENABLEDX=3" {
		t.Errorf("got %v", got)
	}
}

// A repository that budgets more time for the detector says so in config,
// and the budget reaches go test as -timeout.
func TestRacePassesTheConfiguredTimeout(t *testing.T) {
	var calls []call
	if err := Race(config.Race{Timeout: "45m"}, "go", &strings.Builder{}, fake(t, &calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || joined(calls[0]) != "go test -race -timeout 45m ./..." {
		t.Errorf("ran %v", calls)
	}
}
