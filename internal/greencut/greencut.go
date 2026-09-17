// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package greencut refuses a release cut while CI is red, and names who has
// to act on the red it found.
//
// The bar a cut already runs is the local one: it says the tree about to be
// tagged is sound, and nothing about what the last push to the default branch
// did. Between a tag push and the next cut there is exactly one moment where a
// machine is already looking at the repository and a person is already
// waiting, and that is the cut, so the check belongs there.
//
// Two rules. Every workflow with a completed run on the default branch in this
// tag's window must have concluded green, and so must the previous tag's
// release run; and a previous tag whose release run went green must have a
// GitHub Release to show for it, because a workflow can finish and publish
// nothing.
//
// The reads go to the REST API over net/http against an injectable base URL,
// not through `gh`. The hermetic gate strips PATH to the toolchain and the
// directories a repository declares, `gh` is in neither, and a guard whose
// tests only run where a CLI happens to be installed is the class of test this
// repository exists to prevent. One `gh auth token` call remains, behind the
// same Exec as every other subprocess, and only when the environment carries
// no token of its own.
package greencut

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"latere.ai/x/ci-gate/internal/gates"
)

// DefaultAPI is GitHub's REST root. GITHUB_API_URL overrides it, which is the
// variable every runner already sets and the one an enterprise install points
// at its own host.
const DefaultAPI = "https://api.github.com"

// runPages caps the paging over a branch's runs. A hundred runs a page and
// five pages reaches back further than any window between two tags, and the
// cap is what keeps the loop bounded when a filter matches nothing.
const runPages = 5

// Guard is one check of one repository's CI before its tag.
type Guard struct {
	// Root is the repository the cut is for; the git reads run there.
	Root string
	// API is the REST root. Empty means DefaultAPI.
	API string
	// Token authenticates the reads. Empty means GH_TOKEN, then
	// GITHUB_TOKEN, then `gh auth token`.
	Token string
	// Run carries the git and gh subprocesses.
	Run gates.Exec
	// Out is where --force-red prints what it overrides.
	Out io.Writer
	// Force cuts over the findings instead of refusing on them. It does not
	// skip the reads: a maintainer who cuts over red is told exactly what
	// they are cutting over.
	Force bool

	client *http.Client
}

// Check reads CI and returns the refusal, or nil when the cut may proceed.
func (g *Guard) Check() error {
	rep, err := g.scan()
	if err != nil {
		return err
	}
	if rep == nil {
		return nil
	}
	if g.Force {
		_, _ = io.WriteString(g.Out, rep.override())
		return nil
	}
	return errors.New(rep.refusal())
}

// finding is one red thing the guard found, rendered as the two lines a
// refusal prints for it.
type finding struct {
	summary  string
	url      string
	actor    Actor
	evidence string
}

// report is everything the guard found. A nil report is a green cut.
type report struct {
	headline string
	findings []finding
	// hint replaces the actor line where there is no failed job to classify
	// because nothing has run yet.
	hint string
}

// actor is the highest-precedence actor across the findings: where more than
// one run is red, the fix that has to happen first is the one named.
func (r report) actor() (Actor, string) {
	best, evidence := Code, ""
	for _, f := range r.findings {
		if f.actor.rank() > best.rank() {
			best, evidence = f.actor, f.evidence
		}
	}
	return best, evidence
}

func (r report) refusal() string {
	var b strings.Builder
	b.WriteString(r.headline)
	for _, f := range r.findings {
		_, _ = fmt.Fprintf(&b, "\n  %s\n  %s", f.summary, f.url)
	}
	if r.hint != "" {
		_, _ = fmt.Fprintf(&b, "\n  %s", r.hint)
		return b.String()
	}
	actor, evidence := r.actor()
	_, _ = fmt.Fprintf(&b, "\n%s", actor.Line(evidence))
	return b.String()
}

func (r report) override() string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "--force-red: overriding %s\n", r.headline)
	for _, f := range r.findings {
		_, _ = fmt.Fprintf(&b, "  %s\n  %s\n  %s\n", f.summary, f.url, f.actor.Line(f.evidence))
	}
	return b.String()
}

// scan is the whole of the two rules.
func (g *Guard) scan() (*report, error) {
	owner, repo, err := g.slug()
	if err != nil {
		return nil, err
	}
	if err := g.auth(); err != nil {
		return nil, err
	}
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if code, err := g.get(endpoint("repos", owner, repo), nil, &info); err != nil {
		return nil, err
	} else if code != http.StatusOK {
		return nil, fmt.Errorf("reading %s/%s: the api answered %d", owner, repo, code)
	}

	prev := g.previousTag()
	window, err := g.window(prev)
	if err != nil {
		return nil, err
	}
	latest, err := g.latestPerWorkflow(owner, repo, info.DefaultBranch, window)
	if err != nil {
		return nil, err
	}

	rep := &report{headline: "ci is red"}
	// Nothing passes vacuously: a window with no completed run at all is not
	// green, it is unknown. Cutting three minutes after a push, while every
	// run is still in progress, is the case this catches.
	if len(latest) == 0 {
		return &report{
			headline: fmt.Sprintf("no completed run on %s for %s", info.DefaultBranch, span(prev)),
			hint:     "push and wait for ci, or cut with -force-red",
		}, nil
	}
	for _, r := range latest {
		if !isRed(r.Conclusion) {
			continue
		}
		f, err := g.explain(owner, repo, r)
		if err != nil {
			return nil, err
		}
		rep.findings = append(rep.findings, f)
	}

	// Rule (b) and (c): the previous tag's release run, and the Release it
	// should have left behind.
	if prev != "" {
		release, ok, err := g.releaseRun(owner, repo, prev)
		if err != nil {
			return nil, err
		}
		switch {
		case ok && isRed(release.Conclusion):
			f, err := g.explain(owner, repo, release)
			if err != nil {
				return nil, err
			}
			rep.findings = append(rep.findings, f)
		case ok && release.Conclusion == "success":
			published, err := g.published(owner, repo, prev)
			if err != nil {
				return nil, err
			}
			if !published {
				if len(rep.findings) == 0 {
					rep.headline = prev + " deployed but published nothing"
				}
				rep.findings = append(rep.findings, finding{
					summary: fmt.Sprintf("%s #%d success, no GitHub Release exists for %s", release.Name, release.RunNumber, prev),
					url:     release.HTMLURL,
					actor:   Code,
				})
			}
		}
	}
	if len(rep.findings) == 0 {
		return nil, nil
	}
	return rep, nil
}

// span names the commits the window covers, for the message that says nothing
// has run over them.
func span(prev string) string {
	if prev == "" {
		return "HEAD"
	}
	return prev + "..HEAD"
}

// isRed is the two conclusions the rule names. A run still in progress has an
// empty conclusion and is neither red nor green; it is not completed, so it
// never reaches here.
func isRed(conclusion string) bool { return conclusion == "failure" || conclusion == "cancelled" }

// explain turns a red run into the finding a refusal prints, by reading the
// failing job's log and classifying it.
func (g *Guard) explain(owner, repo string, r run) (finding, error) {
	job, ok, err := g.failingJob(owner, repo, r.ID)
	if err != nil {
		return finding{}, err
	}
	summary := fmt.Sprintf("%s #%d %s", r.Name, r.RunNumber, r.Conclusion)
	log := ""
	if ok {
		summary += fmt.Sprintf(", job %q", job.Name)
		// A log that cannot be read is not a reason to let a red cut
		// through: CODE is the fallback, so the refusal still names
		// somebody.
		if body, err := g.jobLog(owner, repo, job.ID); err == nil {
			log = body
		}
	}
	actor, evidence := Classify(r.Conclusion, log)
	return finding{summary: summary, url: r.HTMLURL, actor: actor, evidence: evidence}, nil
}
