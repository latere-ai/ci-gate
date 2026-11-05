// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"latere.ai/x/ci-gate/internal/gates"
)

// sinceRuns is how many successful runs one lookup reads. Thirty reaches
// past any stretch of pushes to a default branch that a retry would not
// have turned green.
const sinceRuns = "30"

// Green finds the newest commit on the default branch that the calling
// workflow passed on a push and that HEAD contains, which is the commit a
// CI run measures its change from.
//
// It never fails the run. A base it cannot have is the whole tree, so the
// second result says why in one line, and the first is empty. The run's
// environment names everything it needs: GITHUB_REPOSITORY, the calling
// workflow in GITHUB_WORKFLOW_REF, the API root in GITHUB_API_URL, and a
// token in GH_TOKEN or GITHUB_TOKEN. A run with CI set never asks gh for a
// token, since a runner has no login to read.
func Green(root string, run gates.Exec) (sha, whole string) {
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return "", "GITHUB_REPOSITORY is not set, so there is no repository to read the last green commit from"
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return "", fmt.Sprintf("GITHUB_REPOSITORY is %q, not owner/repository", repo)
	}
	file, err := workflowFile(os.Getenv("GITHUB_WORKFLOW_REF"))
	if err != nil {
		return "", err.Error()
	}
	g := &Guard{Root: root, API: os.Getenv("GITHUB_API_URL"), Run: run}
	if err := tokenFor(g); err != nil {
		return "", err.Error()
	}
	head, err := g.git("rev-parse", "HEAD")
	if err != nil || head == "" {
		return "", "git cannot name HEAD, so there is no commit to look behind"
	}

	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.read(&repoInfo, "repos", owner, name); err != nil {
		return "", err.Error()
	}
	if repoInfo.DefaultBranch == "" {
		return "", "the repository reports no default branch"
	}

	var runs runsPage
	q := url.Values{
		"branch":   {repoInfo.DefaultBranch},
		"event":    {"push"},
		"status":   {"success"},
		"per_page": {sinceRuns},
	}
	if err := g.read(&runs, "repos", owner, name, "actions", "workflows", file, "runs", q); err != nil {
		return "", err.Error()
	}
	for _, r := range runs.Runs {
		if r.HeadSHA == "" {
			continue
		}
		if r.HeadSHA != head {
			var cmp struct {
				Status string `json:"status"`
			}
			if err := g.read(&cmp, "repos", owner, name, "compare", r.HeadSHA+"..."+head); err != nil {
				return "", err.Error()
			}
			// ahead: HEAD is past the run's commit. identical is the same
			// tree position under another name. Anything else is a commit
			// HEAD does not contain.
			if cmp.Status != "ahead" && cmp.Status != "identical" {
				continue
			}
		}
		if err := have(g, r.HeadSHA); err != nil {
			return "", err.Error()
		}
		return r.HeadSHA, ""
	}
	return "", fmt.Sprintf("%s has no successful push run on %s that HEAD contains", file, repoInfo.DefaultBranch)
}

// workflowFile is the file name of the calling workflow, out of a
// GITHUB_WORKFLOW_REF such as
// owner/repo/.github/workflows/ci.yml@refs/heads/main.
func workflowFile(ref string) (string, error) {
	if ref == "" {
		return "", errors.New("GITHUB_WORKFLOW_REF is not set, so the calling workflow is unknown")
	}
	p, _, _ := strings.Cut(ref, "@")
	_, file, ok := strings.Cut(p, "/.github/workflows/")
	if !ok {
		return "", fmt.Errorf("GITHUB_WORKFLOW_REF is %q, which names no file under .github/workflows", ref)
	}
	if file == "" || strings.Contains(file, "/") {
		return "", fmt.Errorf("GITHUB_WORKFLOW_REF is %q, which names no workflow file", ref)
	}
	return file, nil
}

// tokenFor resolves the token from the environment. Off CI it falls back to
// gh, as the release guard does; on CI a missing token is the answer.
func tokenFor(g *Guard) error {
	if os.Getenv("CI") == "" {
		if err := g.auth(); err != nil {
			return errors.New("there is no GH_TOKEN or GITHUB_TOKEN, and gh has none to give")
		}
		return nil
	}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			g.Token = v
			return nil
		}
	}
	return errors.New("there is no GH_TOKEN or GITHUB_TOKEN to read the last green commit with")
}

// read gets one endpoint, whose last part may be a url.Values query, and
// folds a non-200 into an error that reads as the reason for the whole tree.
func (g *Guard) read(v any, parts ...any) error {
	var q url.Values
	var segs []string
	for _, p := range parts {
		switch p := p.(type) {
		case string:
			segs = append(segs, p)
		case url.Values:
			q = p
		}
	}
	p := endpoint(segs...)
	status, err := g.get(p, q, v)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("GitHub answered %d for GET %s", status, p)
	}
	return nil
}

// have makes sure the commit is in the checkout, fetching it at depth 1
// when it is not.
func have(g *Guard, sha string) error {
	if _, err := g.Run(nil, false, "git", "cat-file", "-e", sha+"^{commit}"); err == nil {
		return nil
	}
	if _, err := g.Run(nil, false, "git", "fetch", "--no-tags", "--depth=1", "origin", sha); err != nil {
		return fmt.Errorf("git cannot fetch %s from origin: %w", sha, err)
	}
	return nil
}
