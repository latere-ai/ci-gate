// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"latere.ai/x/ci-gate/internal/changelog"
)

// run is the part of a workflow run the two rules read.
type run struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	RunNumber  int    `json:"run_number"`
	WorkflowID int64  `json:"workflow_id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

// runsPage is one page of the runs endpoint.
type runsPage struct {
	Runs []run `json:"workflow_runs"`
}

// job is the part of a run's job the classification reads.
type job struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// perPage is the largest page the runs and jobs endpoints serve.
const perPage = 100

// base is the REST root this guard reads.
func (g *Guard) base() string {
	if g.API != "" {
		return strings.TrimSuffix(g.API, "/")
	}
	return DefaultAPI
}

// httpClient names its transport rather than leaving it nil. This is a tool
// and not a service: there is no inbound trace to continue, and the field is
// written so the otel-client gate reads a decision instead of an omission.
func (g *Guard) httpClient() *http.Client {
	if g.client == nil {
		g.client = &http.Client{Transport: http.DefaultTransport}
	}
	return g.client
}

// auth resolves the token once: the environment first, because that is the CI
// case and costs no subprocess, then `gh auth token`.
func (g *Guard) auth() error {
	if g.Token != "" {
		return nil
	}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			g.Token = v
			return nil
		}
	}
	out, err := g.Run(nil, false, "gh", "auth", "token")
	if err != nil {
		return fmt.Errorf("no GH_TOKEN or GITHUB_TOKEN, and `gh auth token` failed: %w\n"+
			"the cut reads CI through the GitHub API before it tags: authenticate gh, export a token, "+
			"or set release.require_green: false and write why", err)
	}
	if g.Token = strings.TrimSpace(string(out)); g.Token == "" {
		return errors.New("`gh auth token` printed nothing; authenticate gh or export GH_TOKEN")
	}
	return nil
}

// endpoint joins and escapes the path segments of a REST call.
func endpoint(parts ...string) string {
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = url.PathEscape(p)
	}
	return strings.Join(escaped, "/")
}

// get reads one endpoint. It returns the status code rather than an error for
// a non-200, because a 404 is an answer the caller acts on and not a failure.
func (g *Guard) get(p string, q url.Values, v any) (int, error) {
	u := g.base() + "/" + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+g.Token)
	resp, err := g.httpClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s: %w", p, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			return resp.StatusCode, fmt.Errorf("GET %s: %w", p, err)
		}
	}
	return resp.StatusCode, nil
}

// slug reads the owner and repository the cut is for out of origin.
func (g *Guard) slug() (string, string, error) {
	remote, err := g.git("remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("git remote get-url origin: %w", err)
	}
	return parseSlug(remote)
}

// parseSlug reads owner and repository out of every remote form this
// organisation writes: ssh://github.com/o/r, git@github.com:o/r.git and
// https://github.com/o/r.git.
func parseSlug(remote string) (string, string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(remote), "/")
	s = strings.TrimSuffix(s, ".git")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	s = strings.Replace(s, ":", "/", 1)
	parts := strings.Split(s, "/")
	// The first segment is the host, so a local path or a bare directory is
	// told apart from a remote rather than read as one.
	if len(parts) < 3 || !strings.Contains(parts[0], ".") || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", "", fmt.Errorf("origin is %q, which names no host, owner and repository the guard can read", remote)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

// git runs one read-only git command in the repository being cut.
func (g *Guard) git(args ...string) (string, error) {
	out, err := g.Run(nil, false, "git", args...)
	return strings.TrimSpace(string(out)), err
}

// previousTag is the newest release tag in the repository. A repository that
// has never tagged has none, and the two rules that read one are skipped.
func (g *Guard) previousTag() string {
	out, err := g.git("tag", "--list", "--sort=-version:refname")
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(out, "\n") {
		if tag := strings.TrimSpace(line); changelog.IsReleaseTag(tag) {
			return tag
		}
	}
	return ""
}

// window is HEAD and its ancestors back to the previous tag: the commits the
// section under `## Unreleased` describes. HEAD is added explicitly, because a
// HEAD that is already the previous tag leaves the range empty.
func (g *Guard) window(prev string) (map[string]bool, error) {
	spec := "HEAD"
	if prev != "" {
		spec = prev + "..HEAD"
	}
	out, err := g.git("rev-list", spec)
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s: %w", spec, err)
	}
	w := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if sha := strings.TrimSpace(line); sha != "" {
			w[sha] = true
		}
	}
	head, err := g.git("rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	w[head] = true
	return w, nil
}

// latestPerWorkflow is the newest completed run of each workflow on branch at
// a commit in the window.
//
// The set of workflows comes from the runs the API returns, not from the files
// under .github/workflows: a workflow that triggers only on tags has no run on
// the default branch, and demanding one would refuse every cut forever.
func (g *Guard) latestPerWorkflow(owner, repo, branch string, window map[string]bool) ([]run, error) {
	seen := map[int64]bool{}
	var out []run
	for page := 1; page <= runPages; page++ {
		q := url.Values{
			"branch":   {branch},
			"per_page": {strconv.Itoa(perPage)},
			"page":     {strconv.Itoa(page)},
		}
		var body runsPage
		code, err := g.get(endpoint("repos", owner, repo, "actions", "runs"), q, &body)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf("reading the runs on %s: the api answered %d", branch, code)
		}
		for _, r := range body.Runs {
			if r.Status != "completed" || !window[r.HeadSHA] || seen[r.WorkflowID] {
				continue
			}
			seen[r.WorkflowID] = true
			out = append(out, r)
		}
		if len(body.Runs) < perPage {
			break
		}
	}
	return out, nil
}

// releaseRun is the completed run at the previous tag's commit whose head ref
// is that tag: the run the tag itself started.
func (g *Guard) releaseRun(owner, repo, tag string) (run, bool, error) {
	sha, err := g.git("rev-list", "-n", "1", tag)
	if err != nil || sha == "" {
		return run{}, false, nil
	}
	var body runsPage
	q := url.Values{"head_sha": {sha}, "per_page": {strconv.Itoa(perPage)}}
	code, err := g.get(endpoint("repos", owner, repo, "actions", "runs"), q, &body)
	if err != nil {
		return run{}, false, err
	}
	if code != http.StatusOK {
		return run{}, false, fmt.Errorf("reading the runs at %s: the api answered %d", tag, code)
	}
	for _, r := range body.Runs {
		if r.Status == "completed" && r.HeadBranch == tag {
			return r, true, nil
		}
	}
	return run{}, false, nil
}

// published reports whether a GitHub Release exists for tag. A run that
// finished green is evidence the workflow ended, and nothing more.
func (g *Guard) published(owner, repo, tag string) (bool, error) {
	code, err := g.get(endpoint("repos", owner, repo, "releases", "tags", tag), nil, nil)
	if err != nil {
		return false, err
	}
	switch code {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("reading the release for %s: the api answered %d", tag, code)
}

// failingJob is the first job of a run that did not pass. Its name is what the
// refusal prints, and its log is what the classification reads.
func (g *Guard) failingJob(owner, repo string, runID int64) (job, bool, error) {
	var body struct {
		Jobs []job `json:"jobs"`
	}
	p := endpoint("repos", owner, repo, "actions", "runs", strconv.FormatInt(runID, 10), "jobs")
	code, err := g.get(p, url.Values{"per_page": {strconv.Itoa(perPage)}}, &body)
	if err != nil {
		return job{}, false, err
	}
	if code != http.StatusOK {
		return job{}, false, nil
	}
	for _, j := range body.Jobs {
		if isRed(j.Conclusion) {
			return j, true, nil
		}
	}
	return job{}, false, nil
}

// jobLog reads the tail of one job's log.
//
// The endpoint answers 302 to a signed blob host that rejects a forwarded
// Authorization header, so the redirect is followed here and the header is
// dropped on the hop. Letting the default client follow it carries the header
// across and fails only against the real API, which is the one place it cannot
// be seen.
func (g *Guard) jobLog(owner, repo string, jobID int64) (string, error) {
	p := endpoint("repos", owner, repo, "actions", "jobs", strconv.FormatInt(jobID, 10), "logs")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, g.base()+"/"+p, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.Token)

	client := *g.httpClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	if loc := resp.Header.Get("Location"); loc != "" && resp.StatusCode/100 == 3 {
		// Location may be relative, so it is resolved against the request
		// that produced it rather than parsed on its own.
		next, perr := resp.Request.URL.Parse(loc)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if perr != nil {
			return "", perr
		}
		blob, err := http.NewRequestWithContext(context.Background(), http.MethodGet, next.String(), nil)
		if err != nil {
			return "", err
		}
		if resp, err = client.Do(blob); err != nil {
			return "", err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("reading the log of job %d: the api answered %d", jobID, resp.StatusCode)
	}
	return lastLines(resp.Body, LogLines)
}

// lastLines returns the final n lines of a stream, holding no more than n of
// them at a time. The error is at the end of a log, and a job that printed a
// hundred thousand lines before it failed is a job whose first two thousand
// say nothing.
func lastLines(r io.Reader, n int) (string, error) {
	ring, count := make([]string, n), 0
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			ring[count%n] = strings.TrimRight(line, "\n")
			count++
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	if count <= n {
		return strings.Join(ring[:count], "\n"), nil
	}
	out := make([]string, 0, n)
	for i := count - n; i < count; i++ {
		out = append(out, ring[i%n])
	}
	return strings.Join(out, "\n"), nil
}
