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
// Two rules, and a third thing that is read and reported. Every workflow with
// a completed run on the default branch in this tag's window must have
// concluded green; and a previous tag whose release run went green must have a
// GitHub Release to show for it, because a workflow can finish and publish
// nothing.
//
// What is reported and not refused on is a previous tag whose release run went
// red. That run rolled out the tag before this one: it built an image,
// deployed it, smoked the deployment and published the notes, and a failure in
// any of that is a fact about a release that already happened. The tree being
// tagged now is judged by the branch runs over the window, which are the runs
// that read it. Refusing on the previous rollout stops the tag that repairs
// it, which is the common case rather than the rare one: a service that
// crash-loops on the version it just deployed is repaired by deploying the
// next one, and a deployment held at zero replicas fails its smoke at every
// tag forever. Both of those were real, and both reached for the maintainer's
// escape hatch, which is a thing that stops meaning anything once the ordinary
// path runs through it.
//
// A green release run that left no GitHub Release stays a refusal. That one is
// a discrepancy nobody can see: the workflow claimed success and produced
// nothing, so the next tag claims the same and produces the same. A red run
// publishing nothing is that run's own visible consequence and says nothing
// further.
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

// Check reads CI, prints what it found and did not refuse on, and returns the
// refusal, or nil when the cut may proceed.
func (g *Guard) Check() error {
	rep, err := g.scan()
	if err != nil {
		return err
	}
	if rep == nil {
		return nil
	}
	if len(rep.warnings) > 0 {
		_, _ = io.WriteString(g.Out, rep.warning())
	}
	if !rep.refuses() {
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

// report is everything the guard found. A nil report is a green cut, and a
// report holding warnings alone is a cut that proceeds with something said.
type report struct {
	headline string
	findings []finding
	// warnings are what the guard found and does not refuse on, with the
	// sentence that opens them. A rollout that failed is evidence about a
	// release that already happened, and the cut being asked for is often
	// the repair, so it is printed and the exit code stays zero.
	warned   string
	warnings []finding
	// hint replaces the actor line where there is no failed job to classify
	// because nothing has run yet.
	hint string
}

// refuses reports whether the report holds something the cut stops on: a red
// run over the commits being tagged, a previous tag that published nothing,
// or a window nothing has run in. Warnings are not in the list.
func (r report) refuses() bool { return len(r.findings) > 0 || r.hint != "" }

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

// warning renders what the guard read and let through, in the shape a
// refusal uses: the run, its URL, and the line naming who acts on it.
func (r report) warning() string {
	var b strings.Builder
	b.WriteString(r.warned)
	for _, f := range r.warnings {
		_, _ = fmt.Fprintf(&b, "\n  %s\n  %s\n  %s", f.summary, f.url, f.actor.Line(f.evidence))
	}
	b.WriteString("\n")
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

	// The previous tag's release run, and the Release it should have left
	// behind. A red run is a warning; a green run with nothing published is
	// the refusal.
	if prev != "" {
		release, ok, err := g.releaseRun(owner, repo, prev)
		if err != nil {
			return nil, err
		}
		switch {
		case ok && isRed(release.Conclusion):
			// A Release run carries a rollout: it builds an image, deploys
			// it, smokes the deployment and publishes the notes. A failure
			// anywhere in it is evidence about the previous tag going out,
			// not about the tree being tagged now, and the tag being cut is
			// frequently what repairs it. What the tree being tagged is
			// judged on is the branch runs above, so this is read, printed
			// and let through.
			f, err := g.explain(owner, repo, release)
			if err != nil {
				return nil, err
			}
			rep.warned = fmt.Sprintf("%s rolled out red, and this cut is not refused on it", prev)
			rep.warnings = append(rep.warnings, f)
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
	if len(rep.findings) == 0 && len(rep.warnings) == 0 {
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
