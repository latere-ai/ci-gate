// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"strings"
	"testing"
)

// A run at HEAD that has not completed is not an answer, and an older green
// run of the same workflow does not stand in for it.
func TestAnUnfinishedRunAtHeadRefusesOverAnOlderGreenRun(t *testing.T) {
	for _, status := range []string{"queued", "in_progress", "waiting", "pending", "requested"} {
		t.Run(status, func(t *testing.T) {
			a := green()
			pending := run{ID: 1290, Name: "ci", RunNumber: 1290, WorkflowID: 42, HeadSHA: headSHA,
				HeadBranch: "main", Status: status, HTMLURL: "https://github.com/o/r/actions/runs/1290"}
			older := greenRun(1280, "ci")
			older.WorkflowID = 42
			older.HeadSHA = oldSHA
			a.runs = []run{pending, older}

			err := guard(t, a, false, &strings.Builder{}).Check()
			want := "ci has not finished at HEAD\n" +
				"  ci #1290 is " + status + " at HEAD\n" +
				"  https://github.com/o/r/actions/runs/1290\n" +
				"wait for them to finish, or cut with -force-red"
			if err == nil || err.Error() != want {
				t.Fatalf("Check() = %v, want\n%s", err, want)
			}
		})
	}
}

// A workflow re-running at HEAD after a canceled attempt is still running,
// whatever an older commit's run concluded.
func TestARerunAtHeadRefusesWhileItRuns(t *testing.T) {
	a := green()
	rerun := run{ID: 2001, Name: "e2e", RunNumber: 2001, WorkflowID: 7, HeadSHA: headSHA,
		HeadBranch: "main", Status: "in_progress", HTMLURL: "https://github.com/o/r/actions/runs/2001"}
	failed := redRun(2000, "e2e", "cancelled")
	failed.WorkflowID = 7
	older := greenRun(1999, "e2e")
	older.WorkflowID = 7
	older.HeadSHA = oldSHA
	a.runs = []run{greenRun(1284, "ci"), rerun, failed, older}

	err := guard(t, a, false, &strings.Builder{}).Check()
	if err == nil || !strings.Contains(err.Error(), "e2e #2001 is in_progress at HEAD") {
		t.Fatalf("Check() = %v, want the running e2e refused", err)
	}
}

// A run that is not finished at a commit older than HEAD is not read: only
// HEAD's own runs are waited for.
func TestAnUnfinishedRunBelowHeadIsPassedOver(t *testing.T) {
	a := green()
	stale := run{ID: 1270, Name: "ci", RunNumber: 1270, WorkflowID: 42, HeadSHA: oldSHA,
		HeadBranch: "main", Status: "in_progress"}
	older := greenRun(1260, "ci")
	older.WorkflowID = 42
	older.HeadSHA = oldSHA
	a.runs = []run{greenRun(1284, "ci"), stale, older}

	if err := guard(t, a, false, &strings.Builder{}).Check(); err != nil {
		t.Fatalf("Check() = %v, want a run below HEAD not waited for", err)
	}
}

// -force-red prints the waiting run with the findings it overrides.
func TestForceRedPrintsTheRunsItDoesNotWaitFor(t *testing.T) {
	a := green()
	pending := run{ID: 1290, Name: "ci", RunNumber: 1290, WorkflowID: 42, HeadSHA: headSHA,
		HeadBranch: "main", Status: "queued", HTMLURL: "https://github.com/o/r/actions/runs/1290"}
	older := greenRun(1280, "ci")
	older.WorkflowID = 42
	older.HeadSHA = oldSHA
	a.runs = []run{pending, older}

	var out strings.Builder
	if err := guard(t, a, true, &out).Check(); err != nil {
		t.Fatalf("Check() = %v, want -force-red to cut", err)
	}
	want := "--force-red: overriding ci has not finished at HEAD\n" +
		"  ci #1290 is queued at HEAD\n" +
		"  https://github.com/o/r/actions/runs/1290\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

// A red run and a running one are both named, and the red run's actor line
// closes the refusal.
func TestARedRunAndARunningOneAreBothNamed(t *testing.T) {
	a := green()
	pending := run{ID: 1290, Name: "e2e", RunNumber: 1290, WorkflowID: 42, HeadSHA: headSHA,
		HeadBranch: "main", Status: "in_progress", HTMLURL: "https://github.com/o/r/actions/runs/1290"}
	a.runs = []run{redRun(1284, "ci", "failure"), pending}

	err := guard(t, a, false, &strings.Builder{}).Check()
	if err == nil || !strings.HasPrefix(err.Error(), "ci is red\n") ||
		!strings.Contains(err.Error(), "e2e #1290 is in_progress at HEAD") ||
		!strings.HasSuffix(err.Error(), "CODE: fix and push, then cut again") {
		t.Fatalf("Check() = %v, want both named", err)
	}
}
