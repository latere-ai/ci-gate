// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/gates"
)

const (
	headSHA = "1111111111111111111111111111111111111111"
	oldSHA  = "2222222222222222222222222222222222222222"
	prevSHA = "3333333333333333333333333333333333333333"
	prevTag = "v0.38.0"
)

// api is the GitHub the guard reads in a test: the five endpoints the two
// rules touch, each answering from a scenario the test wrote.
type api struct {
	branch   string
	runs     []run            // on the default branch
	tagRuns  []run            // at the previous tag's commit
	jobs     map[int64][]job  // per run
	logs     map[int64]string // per job
	released map[string]bool  // per tag
	redirect bool             // serve a job log through a 302 that rejects the token

	seen []string
}

func (a *api) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Error(err)
		}
	}
	record := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			a.seen = append(a.seen, r.URL.Path)
			if got := r.Header.Get("Authorization"); got != "Bearer t" && !strings.HasPrefix(r.URL.Path, "/blob/") {
				t.Errorf("%s carried %q, want the bearer token", r.URL.Path, got)
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/repos/o/r", record(func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]string{"default_branch": a.branch})
	}))
	mux.HandleFunc("/repos/o/r/actions/runs", record(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("head_sha") != "" {
			write(w, runsPage{Runs: a.tagRuns})
			return
		}
		if q.Get("page") != "" && q.Get("page") != "1" {
			write(w, runsPage{})
			return
		}
		write(w, runsPage{Runs: a.runs})
	}))
	mux.HandleFunc("/repos/o/r/actions/runs/{id}/jobs", record(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		write(w, map[string][]job{"jobs": a.jobs[id]})
	}))
	mux.HandleFunc("/repos/o/r/actions/jobs/{id}/logs", record(func(w http.ResponseWriter, r *http.Request) {
		if a.redirect {
			http.Redirect(w, r, "/blob/"+r.PathValue("id"), http.StatusFound)
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		_, _ = w.Write([]byte(a.logs[id]))
	}))
	// The signed blob host rejects a forwarded token, which is the reason the
	// guard follows the redirect itself.
	mux.HandleFunc("/blob/{id}", record(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		body, ok := a.logs[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	mux.HandleFunc("/repos/o/r/releases/tags/{tag}", record(func(w http.ResponseWriter, r *http.Request) {
		if !a.released[r.PathValue("tag")] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		write(w, map[string]string{"tag_name": r.PathValue("tag")})
	}))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// gitExec answers the read-only git commands the guard runs.
func gitExec(tags string) gates.Exec {
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name != "git" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		switch strings.Join(args, " ") {
		case "remote get-url origin":
			return []byte("ssh://github.com/o/r\n"), nil
		case "tag --list --sort=-version:refname":
			return []byte(tags), nil
		case "rev-parse HEAD":
			return []byte(headSHA + "\n"), nil
		case "rev-list " + prevTag + "..HEAD", "rev-list HEAD":
			return []byte(headSHA + "\n" + oldSHA + "\n"), nil
		case "rev-list -n 1 " + prevTag:
			return []byte(prevSHA + "\n"), nil
		}
		return nil, fmt.Errorf("unexpected git %v", args)
	}
}

func guard(t *testing.T, a *api, force bool, out *strings.Builder) *Guard {
	t.Helper()
	return &Guard{
		Root:  t.TempDir(),
		API:   a.serve(t).URL,
		Token: "t",
		Run:   gitExec(prevTag + "\nv0.37.0\n"),
		Out:   out,
		Force: force,
	}
}

// greenRun is a workflow that passed on the default branch.
func greenRun(id int64, name string) run {
	return run{ID: id, Name: name, RunNumber: int(id), WorkflowID: id, HeadSHA: headSHA,
		HeadBranch: "main", Status: "completed", Conclusion: "success",
		HTMLURL: "https://github.com/o/r/actions/runs/" + strconv.FormatInt(id, 10)}
}

func redRun(id int64, name, conclusion string) run {
	r := greenRun(id, name)
	r.Conclusion = conclusion
	return r
}

// releaseRunAt is the run the previous tag itself started.
func releaseRunAt(conclusion string) run {
	return run{ID: 1201, Name: "release", RunNumber: 1201, WorkflowID: 9, HeadSHA: prevSHA,
		HeadBranch: prevTag, Status: "completed", Conclusion: conclusion,
		HTMLURL: "https://github.com/o/r/actions/runs/1201"}
}

// green is the scenario every red case starts from.
func green() *api {
	return &api{
		branch:   "main",
		runs:     []run{greenRun(1284, "ci")},
		tagRuns:  []run{releaseRunAt("success")},
		jobs:     map[int64][]job{},
		logs:     map[int64]string{},
		released: map[string]bool{prevTag: true},
	}
}

// A green window, a green release run and a published previous tag is a cut
// that proceeds, and the guard says nothing.
func TestGreenCutProceeds(t *testing.T) {
	var out strings.Builder
	a := green()
	if err := guard(t, a, false, &out).Check(); err != nil {
		t.Fatalf("a green ci refused the cut: %v", err)
	}
	if out.String() != "" {
		t.Errorf("a green guard printed %q", out.String())
	}
	if !strings.Contains(strings.Join(a.seen, " "), "/releases/tags/"+prevTag) {
		t.Error("a green release run is not evidence a Release exists; the guard must ask")
	}
}

// A failing test in the window refuses before the bar runs, names the run and
// the failing job, and hands the fix to whoever pushed.
func TestARedRunRefusesAndNamesTheCode(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "gate (cover)", Conclusion: "failure"}}
	a.logs[77] = "--- FAIL: TestSectionMissing (0.00s)\nFAIL\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	want := "ci is red\n" +
		"  ci #1284 failure, job \"gate (cover)\"\n" +
		"  https://github.com/o/r/actions/runs/1284\n" +
		"CODE: fix and push, then cut again"
	if err == nil || err.Error() != want {
		t.Fatalf("Check() = %v, want\n%s", err, want)
	}
}

// The maintainer's standing instruction: if it is red for budget reasons, ask
// them to act, and forward the phrase that says so.
func TestABudgetFailureNamesTheMaintainer(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "gate (vuln)", Conclusion: "failure"}}
	a.logs[77] = "read tcp: connection reset by peer\n" +
		"The job was not started because you have exceeded your included usage limit for GitHub Actions.\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	want := "ci is red\n" +
		"  ci #1284 failure, job \"gate (vuln)\"\n" +
		"  https://github.com/o/r/actions/runs/1284\n" +
		"BUDGET: the maintainer must act (the log names \"usage limit\")"
	if err == nil || err.Error() != want {
		t.Fatalf("Check() = %v, want\n%s", err, want)
	}
}

// A registry that answered DENIED wants the job run again, not a commit.
func TestAnInfraFailureAsksForARerun(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "publish", Conclusion: "failure"}}
	a.logs[77] = "Error: denied: requested access to the resource is denied\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	if err == nil || !strings.HasSuffix(err.Error(), "\nINFRA: re-run the job, then cut again") {
		t.Fatalf("Check() = %v, want an INFRA refusal", err)
	}
}

// The night of 2026-09-16: the release run went red after its deploy job, so
// the tag was live and the notes were not.
func TestThePreviousTagsReleaseRunIsRead(t *testing.T) {
	a := green()
	a.tagRuns = []run{releaseRunAt("failure")}
	a.jobs[1201] = []job{{ID: 88, Name: "publish", Conclusion: "failure"}}
	a.logs[88] = "gh release create: HTTP 403\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	want := "ci is red\n" +
		"  release #1201 failure, job \"publish\"\n" +
		"  https://github.com/o/r/actions/runs/1201\n" +
		"CODE: fix and push, then cut again"
	if err == nil || err.Error() != want {
		t.Fatalf("Check() = %v, want\n%s", err, want)
	}
}

// A run that finished green is evidence the workflow ended, and nothing more.
func TestAPreviousTagThatPublishedNothing(t *testing.T) {
	a := green()
	a.released = map[string]bool{}

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	want := prevTag + " deployed but published nothing\n" +
		"  release #1201 success, no GitHub Release exists for " + prevTag + "\n" +
		"  https://github.com/o/r/actions/runs/1201\n" +
		"CODE: fix and push, then cut again"
	if err == nil || err.Error() != want {
		t.Fatalf("Check() = %v, want\n%s", err, want)
	}
}

// Nothing passes vacuously: a window where every run is still in progress is
// not green, it is unknown.
func TestNothingHasRunYet(t *testing.T) {
	a := green()
	a.runs = []run{{ID: 1284, Name: "ci", HeadSHA: headSHA, Status: "in_progress"}}

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	want := "no completed run on main for " + prevTag + "..HEAD\n" +
		"  push and wait for ci, or cut with -force-red"
	if err == nil || err.Error() != want {
		t.Fatalf("Check() = %v, want\n%s", err, want)
	}
}

// A repository that has never tagged has no previous tag to read, and the
// window is its whole history.
func TestAFirstTagReadsNoPreviousTag(t *testing.T) {
	a := green()
	a.tagRuns = nil
	a.released = map[string]bool{}
	g := guard(t, a, false, &strings.Builder{})
	g.Run = gitExec("")
	if err := g.Check(); err != nil {
		t.Fatalf("a first tag refused: %v", err)
	}
	if strings.Contains(strings.Join(a.seen, " "), "/releases/tags/") {
		t.Error("with no previous tag there is no Release to ask about")
	}
}

// A cancelled run is red exactly as a failure is.
func TestACancelledRunIsRed(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "cancelled")}
	a.jobs[1284] = []job{{ID: 77, Name: "gate (race)", Conclusion: "cancelled"}}
	a.logs[77] = "The operation was canceled.\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	if err == nil || !strings.Contains(err.Error(), "ci #1284 cancelled, job \"gate (race)\"") {
		t.Fatalf("Check() = %v, want a cancelled run refused", err)
	}
}

// The log endpoint answers 302 to a host that rejects the token. Following it
// by hand, without the header, is the only way the classification reads a body.
func TestTheJobLogIsReadThroughARedirect(t *testing.T) {
	a := green()
	a.redirect = true
	a.runs = []run{redRun(1284, "ci", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "deploy", Conclusion: "failure"}}
	a.logs[77] = "No runner available matching labels: ubuntu-latest\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	if err == nil || !strings.HasSuffix(err.Error(), "BUDGET: the maintainer must act (the log names \"no runner available\")") {
		t.Fatalf("Check() = %v, want the redirected log classified", err)
	}
}

// A log the guard cannot read is not a reason to let a red cut through.
func TestAnUnreadableLogStillNamesSomebody(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "gate (lint)", Conclusion: "failure"}}
	a.redirect = true
	a.logs = map[int64]string{} // the blob host serves nothing

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	if err == nil || !strings.HasSuffix(err.Error(), "CODE: fix and push, then cut again") {
		t.Fatalf("Check() = %v, want a refusal that still names an actor", err)
	}
}

// A run with no failing job still refuses; there is simply no job name to give.
func TestARedRunWithNoFailingJob(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure")}

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	if err == nil || !strings.Contains(err.Error(), "ci #1284 failure\n") {
		t.Fatalf("Check() = %v, want a refusal with no job name", err)
	}
}

// --force-red does not skip the check, it overrides the result, and prints
// every finding with the actor it found.
func TestForceRedPrintsWhatItOverrides(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "gate (cover)", Conclusion: "failure"}}
	a.logs[77] = "--- FAIL: TestX\n"
	a.released = map[string]bool{}

	var out strings.Builder
	if err := guard(t, a, true, &out).Check(); err != nil {
		t.Fatalf("--force-red refused: %v", err)
	}
	want := "--force-red: overriding ci is red\n" +
		"  ci #1284 failure, job \"gate (cover)\"\n" +
		"  https://github.com/o/r/actions/runs/1284\n" +
		"  CODE: fix and push, then cut again\n" +
		"  release #1201 success, no GitHub Release exists for " + prevTag + "\n" +
		"  https://github.com/o/r/actions/runs/1201\n" +
		"  CODE: fix and push, then cut again\n"
	if out.String() != want {
		t.Errorf("--force-red printed\n%s\nwant\n%s", out.String(), want)
	}
}

// --force-red over a window nothing has run in prints the same sentence the
// refusal would have opened with.
func TestForceRedOverAnUnknownWindow(t *testing.T) {
	a := green()
	a.runs = nil

	var out strings.Builder
	if err := guard(t, a, true, &out).Check(); err != nil {
		t.Fatalf("--force-red refused: %v", err)
	}
	if want := "--force-red: overriding no completed run on main for " + prevTag + "..HEAD\n"; out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}
}

// Where two runs are red with different actors, the closing line names the
// fix that has to happen first.
func TestTheClosingLineNamesTheHighestActor(t *testing.T) {
	a := green()
	a.runs = []run{redRun(1284, "ci", "failure"), redRun(1290, "images", "failure")}
	a.jobs[1284] = []job{{ID: 77, Name: "gate (cover)", Conclusion: "failure"}}
	a.logs[77] = "--- FAIL: TestX\n"
	a.jobs[1290] = []job{{ID: 78, Name: "push", Conclusion: "failure"}}
	a.logs[78] = "You have exceeded your included usage limit for GitHub Actions.\n"

	var out strings.Builder
	err := guard(t, a, false, &out).Check()
	if err == nil || !strings.HasSuffix(err.Error(), "BUDGET: the maintainer must act (the log names \"usage limit\")") {
		t.Fatalf("Check() = %v, want the BUDGET line to close it", err)
	}
	if !strings.Contains(err.Error(), "images #1290") || !strings.Contains(err.Error(), "ci #1284") {
		t.Error("both red runs belong in the refusal")
	}
}

// Every remote form this organisation writes names the same repository.
func TestParseSlug(t *testing.T) {
	for _, remote := range []string{
		"ssh://github.com/latere-ai/ci-gate",
		"ssh://git@github.com/latere-ai/ci-gate.git",
		"git@github.com:latere-ai/ci-gate.git",
		"https://github.com/latere-ai/ci-gate.git",
		"https://github.com/latere-ai/ci-gate/\n",
	} {
		owner, repo, err := parseSlug(remote)
		if err != nil {
			t.Fatalf("%s: %v", remote, err)
		}
		if owner != "latere-ai" || repo != "ci-gate" {
			t.Errorf("%s -> %s/%s", remote, owner, repo)
		}
	}
	if _, _, err := parseSlug("/srv/git/bare"); err == nil {
		t.Error("a path that names no owner and repository is an error")
	}
}

// The environment first, because that is the CI case and costs no subprocess;
// `gh auth token` only when it carries nothing.
func TestAuthPrefersTheEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "from-env")
	g := &Guard{Run: func([]string, bool, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("gh must not run when the environment carries a token")
	}}
	if err := g.auth(); err != nil || g.Token != "from-env" {
		t.Fatalf("auth() = %v, token %q", err, g.Token)
	}

	_ = os.Unsetenv("GITHUB_TOKEN")
	g = &Guard{Run: func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		if name != "gh" || strings.Join(args, " ") != "auth token" {
			return nil, fmt.Errorf("unexpected %s %v", name, args)
		}
		return []byte("from-gh\n"), nil
	}}
	if err := g.auth(); err != nil || g.Token != "from-gh" {
		t.Fatalf("auth() = %v, token %q", err, g.Token)
	}

	g = &Guard{Run: func([]string, bool, string, ...string) ([]byte, error) { return nil, fmt.Errorf("not logged in") }}
	if err := g.auth(); err == nil || !strings.Contains(err.Error(), "release.require_green") {
		t.Fatalf("auth() = %v, want the refusal to name the way out", err)
	}
	g = &Guard{Run: func([]string, bool, string, ...string) ([]byte, error) { return []byte("  \n"), nil }}
	if err := g.auth(); err == nil {
		t.Error("an empty token is not a token")
	}
}

// An API that answers anything but 200 stops the cut rather than passing it.
func TestApiFailuresStopTheCut(t *testing.T) {
	for _, tc := range []struct{ name, path, want string }{
		{"the repository", "/repos/o/r", "the api answered 500"},
		{"the runs", "/repos/o/r/actions/runs", "reading the runs on main"},
		{"the release", "/repos/o/r/releases/tags/", "reading the release for"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := green()
			s := a.serve(t)
			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, s.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			})
			broken := httptest.NewServer(mux)
			t.Cleanup(broken.Close)

			g := &Guard{Root: t.TempDir(), API: broken.URL, Token: "t", Run: gitExec(prevTag + "\n"), Out: &strings.Builder{}}
			err := g.Check()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check() = %v, want %q", err, tc.want)
			}
		})
	}
}

// git that cannot answer stops the cut, named by the command that failed.
func TestGitFailuresStopTheCut(t *testing.T) {
	broken := func(fail string) gates.Exec {
		real := gitExec(prevTag + "\n")
		return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
			if strings.HasPrefix(strings.Join(args, " "), fail) {
				return nil, fmt.Errorf("fatal")
			}
			return real(env, stream, name, args...)
		}
	}
	for _, tc := range []struct{ fail, want string }{
		{"remote", "git remote get-url origin"},
		{"rev-list " + prevTag + "..HEAD", "git rev-list " + prevTag + "..HEAD"},
		{"rev-parse", "git rev-parse HEAD"},
	} {
		a := green()
		g := &Guard{Root: t.TempDir(), API: a.serve(t).URL, Token: "t", Run: broken(tc.fail), Out: &strings.Builder{}}
		err := g.Check()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Check() = %v, want %q", err, tc.want)
		}
	}
}

// With no API set the guard reads github.com; GITHUB_API_URL is what points
// it at an enterprise host or, here, at a test.
func TestTheDefaultApiIsGitHub(t *testing.T) {
	if got := (&Guard{}).base(); got != DefaultAPI {
		t.Errorf("base() = %q, want %q", got, DefaultAPI)
	}
	if got := (&Guard{API: "https://ghe.example.com/api/v3/"}).base(); got != "https://ghe.example.com/api/v3" {
		t.Errorf("base() = %q", got)
	}
}

// A window wider than one page is paged through, and a workflow's newest run
// is the one that counts even when an older one in the window is red.
func TestRunsArePagedAndTheNewestPerWorkflowWins(t *testing.T) {
	a := green()
	full := make([]run, 0, perPage)
	for i := range perPage {
		r := greenRun(int64(2000+i), "noise")
		r.HeadSHA = "not-in-the-window"
		full = append(full, r)
	}
	a.runs = full
	served := 0
	base := a.serve(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("head_sha") != "" || r.URL.Query().Get("page") == "1" {
			http.Redirect(w, r, base.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			return
		}
		served++
		w.Header().Set("Content-Type", "application/json")
		// Page two holds this workflow's newest run, then the older red one.
		newest, older := greenRun(1284, "ci"), redRun(1280, "ci", "failure")
		newest.WorkflowID, older.WorkflowID = 42, 42
		older.HeadSHA = oldSHA
		_ = json.NewEncoder(w).Encode(runsPage{Runs: []run{newest, older}})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base.URL+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	g := &Guard{Root: t.TempDir(), API: front.URL, Token: "t", Run: gitExec(prevTag + "\n"), Out: &strings.Builder{}}
	if err := g.Check(); err != nil {
		t.Fatalf("the newest run of the workflow is green: %v", err)
	}
	if served == 0 {
		t.Error("a full first page must be followed by a second")
	}
}
