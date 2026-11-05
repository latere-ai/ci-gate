// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/gates"
)

const (
	older = "a000000000000000000000000000000000000001"
	newer = "a000000000000000000000000000000000000002"
	other = "a000000000000000000000000000000000000003"
)

// forge serves the three endpoints a lookup reads. status maps a commit to
// what compare says of it relative to HEAD.
func forge(t *testing.T, runs []string, status map[string]string, fail map[string]int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, key, body string) {
		if code := fail[key]; code != 0 {
			http.Error(w, "no", code)
			return
		}
		_, _ = fmt.Fprint(w, body)
	}
	mux.HandleFunc("GET /repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		reply(w, "repo", `{"default_branch":"trunk"}`)
	})
	mux.HandleFunc("GET /repos/o/r/actions/workflows/ci.yml/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("branch") != "trunk" || q.Get("event") != "push" || q.Get("status") != "success" || q.Get("per_page") != "30" {
			http.Error(w, "wrong query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		b := []string{`{"head_sha":""}`}
		for _, sha := range runs {
			b = append(b, fmt.Sprintf(`{"head_sha":%q}`, sha))
		}
		reply(w, "runs", `{"workflow_runs":[`+strings.Join(b, ",")+`]}`)
	})
	mux.HandleFunc("GET /repos/o/r/compare/{pair}", func(w http.ResponseWriter, r *http.Request) {
		sha, head, _ := strings.Cut(r.PathValue("pair"), "...")
		if head != headSHA {
			http.Error(w, "wrong head", http.StatusBadRequest)
			return
		}
		reply(w, "compare", fmt.Sprintf(`{"status":%q}`, status[sha]))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// env is a runner's environment for the repository the forge serves.
func env(t *testing.T, api string) {
	t.Helper()
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("GITHUB_WORKFLOW_REF", "o/r/.github/workflows/ci.yml@refs/heads/main")
	t.Setenv("GITHUB_API_URL", api)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "tok")
}

// checkout answers git for a clone that holds the commits in have; fetched
// collects the commits it was asked to fetch.
func checkout(have map[string]bool, fetched *[]string, fetchErr error) gates.Exec {
	return func(_ []string, _ bool, name string, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case name != "git":
			return nil, fmt.Errorf("unexpected command %q", name)
		case cmd == "rev-parse HEAD":
			return []byte(headSHA + "\n"), nil
		case strings.HasPrefix(cmd, "cat-file -e "):
			if have[strings.TrimSuffix(strings.TrimPrefix(cmd, "cat-file -e "), "^{commit}")] {
				return nil, nil
			}
			return nil, errors.New("missing")
		case strings.HasPrefix(cmd, "fetch --no-tags --depth=1 origin "):
			*fetched = append(*fetched, strings.TrimPrefix(cmd, "fetch --no-tags --depth=1 origin "))
			return nil, fetchErr
		}
		return nil, fmt.Errorf("unexpected git %v", args)
	}
}

func TestGreenIsTheNewestAncestor(t *testing.T) {
	// The newest run is on a commit HEAD does not contain; the next is.
	s := forge(t, []string{other, newer, older},
		map[string]string{other: "diverged", newer: "ahead", older: "ahead"}, nil)
	env(t, s.URL)
	var fetched []string
	sha, whole := Green(t.TempDir(), checkout(map[string]bool{newer: true}, &fetched, nil))
	if sha != newer || whole != "" {
		t.Fatalf("Green = %q, %q; want %s", sha, whole, newer)
	}
	if len(fetched) != 0 {
		t.Errorf("a commit the checkout holds is not fetched: %v", fetched)
	}
}

func TestGreenAcceptsHeadAndIdentical(t *testing.T) {
	s := forge(t, []string{headSHA}, nil, nil)
	env(t, s.URL)
	if sha, whole := Green(t.TempDir(), checkout(map[string]bool{headSHA: true}, nil, nil)); sha != headSHA {
		t.Errorf("a green HEAD is its own base: %q, %q", sha, whole)
	}
	s = forge(t, []string{older}, map[string]string{older: "identical"}, nil)
	env(t, s.URL)
	if sha, _ := Green(t.TempDir(), checkout(map[string]bool{older: true}, nil, nil)); sha != older {
		t.Errorf("identical is an ancestor: %q", sha)
	}
}

func TestGreenFetchesAMissingCommit(t *testing.T) {
	s := forge(t, []string{older}, map[string]string{older: "ahead"}, nil)
	env(t, s.URL)
	var fetched []string
	sha, whole := Green(t.TempDir(), checkout(nil, &fetched, nil))
	if sha != older || whole != "" || len(fetched) != 1 || fetched[0] != older {
		t.Fatalf("Green = %q, %q, fetched %v", sha, whole, fetched)
	}
	sha, whole = Green(t.TempDir(), checkout(nil, &fetched, errors.New("denied")))
	if sha != "" || !strings.Contains(whole, "cannot fetch "+older) {
		t.Errorf("a failed fetch is the whole tree: %q, %q", sha, whole)
	}
}

// Every way a base can be unavailable checks the whole tree and says why.
func TestGreenFallsBackToTheWholeTree(t *testing.T) {
	good := map[string]string{older: "ahead"}
	have := func() gates.Exec { return checkout(map[string]bool{older: true, newer: true}, new([]string), nil) }
	for _, c := range []struct {
		name  string
		runs  []string
		fail  map[string]int
		setup func(*testing.T)
		run   gates.Exec
		want  string
	}{
		{name: "no runs", want: "no successful push run"},
		{name: "none an ancestor", runs: []string{other}, want: "no successful push run"},
		{name: "forbidden", runs: []string{older}, fail: map[string]int{"repo": 403}, want: "GitHub answered 403"},
		{name: "runs forbidden", runs: []string{older}, fail: map[string]int{"runs": 403}, want: "GitHub answered 403"},
		{name: "compare fails", runs: []string{older}, fail: map[string]int{"compare": 404}, want: "GitHub answered 404"},
		{name: "no repository", runs: []string{older}, setup: func(t *testing.T) { t.Setenv("GITHUB_REPOSITORY", "") }, want: "GITHUB_REPOSITORY is not set"},
		{name: "malformed repository", runs: []string{older}, setup: func(t *testing.T) { t.Setenv("GITHUB_REPOSITORY", "r") }, want: "not owner/repository"},
		{name: "no workflow", runs: []string{older}, setup: func(t *testing.T) { t.Setenv("GITHUB_WORKFLOW_REF", "") }, want: "GITHUB_WORKFLOW_REF is not set"},
		{name: "no token", runs: []string{older}, setup: func(t *testing.T) { t.Setenv("GITHUB_TOKEN", "") }, want: "no GH_TOKEN or GITHUB_TOKEN"},
		{name: "no head", runs: []string{older}, run: func([]string, bool, string, ...string) ([]byte, error) { return nil, errors.New("x") }, want: "cannot name HEAD"},
		{name: "unreachable", runs: []string{older}, setup: func(t *testing.T) { t.Setenv("GITHUB_API_URL", "http://127.0.0.1:1") }, want: "GET repos/o/r"},
		{name: "bad json", runs: []string{older}, setup: func(t *testing.T) { t.Setenv("GITHUB_API_URL", garbage(t)) }, want: "GET repos/o/r"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := forge(t, c.runs, good, c.fail)
			env(t, s.URL)
			if c.setup != nil {
				c.setup(t)
			}
			run := c.run
			if run == nil {
				run = have()
			}
			sha, whole := Green(t.TempDir(), run)
			if sha != "" || !strings.Contains(whole, c.want) {
				t.Errorf("Green = %q, %q; want the whole tree because %q", sha, whole, c.want)
			}
		})
	}
}

func garbage(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "{") }))
	t.Cleanup(s.Close)
	return s.URL
}

func TestGreenWithoutABranch(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "{}") }))
	t.Cleanup(s.Close)
	env(t, s.URL)
	if sha, whole := Green(t.TempDir(), checkout(nil, nil, nil)); sha != "" || !strings.Contains(whole, "no default branch") {
		t.Errorf("Green = %q, %q", sha, whole)
	}
}

// Off CI the token may come from gh, as the release guard reads it.
func TestGreenAsksGhOffCI(t *testing.T) {
	s := forge(t, []string{older}, map[string]string{older: "ahead"}, nil)
	env(t, s.URL)
	t.Setenv("CI", "")
	t.Setenv("GITHUB_TOKEN", "")
	git := checkout(map[string]bool{older: true}, nil, nil)
	withGh := func(e []string, st bool, name string, args ...string) ([]byte, error) {
		if name == "gh" {
			return []byte("tok\n"), nil
		}
		return git(e, st, name, args...)
	}
	if sha, whole := Green(t.TempDir(), withGh); sha != older {
		t.Errorf("a token from gh: %q, %q", sha, whole)
	}
	if sha, whole := Green(t.TempDir(), git); sha != "" || !strings.Contains(whole, "gh has none") {
		t.Errorf("no token anywhere: %q, %q", sha, whole)
	}
	// On CI gh is never asked.
	t.Setenv("CI", "true")
	if sha, _ := Green(t.TempDir(), withGh); sha != "" {
		t.Error("on CI a missing token is the answer; gh is not consulted")
	}
}

func TestWorkflowFile(t *testing.T) {
	for ref, want := range map[string]string{
		"o/r/.github/workflows/ci.yml@refs/heads/main": "ci.yml",
		"o/r/.github/workflows/ci.yml":                 "ci.yml",
	} {
		if got, err := workflowFile(ref); err != nil || got != want {
			t.Errorf("%s: %q, %v", ref, got, err)
		}
	}
	for _, ref := range []string{"", "o/r/ci.yml@main", "o/r/.github/workflows/@main", "o/r/.github/workflows/a/b.yml@main"} {
		if got, err := workflowFile(ref); err == nil {
			t.Errorf("%q: %q, want an error", ref, got)
		}
	}
}
