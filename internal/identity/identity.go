// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package identity gates the shape every repository of the family
// integrates identity with.
//
// The family took one decision about identity: the issuer issues identity
// and membership, an open core verifies one token, forwards every claim and
// asks one authorizer, a service verifies that the audience is itself and
// decides from its own state, nobody calls the issuer on a request path,
// there is one hop mechanism and no delegation in a token, and access is by
// role. Those rules held once because a person grepped for them, and three
// generations of documents accumulated the same way.
//
// So the rules run on every push. A repository declares which layer of the
// shape it is in .lateregate.yaml, the role selects the rules, and each rule
// is a scan of the tree: non-test Go files, deployment manifests, and
// documents outside an archive. Nothing here runs a service; the behavioural
// half of the shape is the conformance packages a repository runs as tests.
//
// A repository with no block fails rather than skipping, because a
// repository outside the shape by omission is the gap the gate exists for. A
// rule with nothing to scan reports why, because a rule that passes over an
// empty tree reports green as the tree fills up.
package identity

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Finding is one place the tree leaves the shape.
//
// The location is the file and the line; the sentence says what to do and is
// written in the register of somebody who runs the tool, so it names no
// import path, no package-qualified identifier and no file path of its own.
type Finding struct {
	Rel      string
	Line     int
	Sentence string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.Rel, f.Line, f.Sentence) }

// at builds a finding at a location.
func at(rel string, line int, format string, args ...any) Finding {
	return Finding{Rel: rel, Line: line, Sentence: fmt.Sprintf(format, args...)}
}

// result is what one rule reports. A rule reports exactly one of the three:
// nothing to scan and why, what it read, or what it found.
type result struct {
	skip     string
	note     string
	findings []Finding
}

// rule is one row of the family's rule table.
type rule struct {
	name  string
	roles []config.Role
	run   func(*tree) (result, error)
}

// verifies are the roles that verify a token addressed to themselves.
var verifies = []config.Role{config.RoleCore, config.RoleService, config.RolePlatform, config.RoleBFF}

// serving are the roles that answer requests from a person's token.
var serving = []config.Role{config.RoleService, config.RolePlatform, config.RoleBFF}

// integrating is every role but none: a repository with an identity surface.
var integrating = []config.Role{
	config.RoleIssuer, config.RoleCore, config.RolePlatform,
	config.RoleService, config.RoleBFF, config.RoleClient,
}

// rules is the table of spec 019, in the order the gate reports them. Each
// row names the family rule it holds.
var rules = []rule{
	{name: "claims", roles: []config.Role{config.RoleCore}, run: ruleClaims},
	{name: "verifier", roles: verifies, run: ruleVerifier},
	{name: "authorizer", roles: []config.Role{config.RoleCore}, run: ruleAuthorizer},
	{name: "request-path", roles: serving, run: ruleRequestPath},
	{name: "delegation", roles: integrating, run: ruleDelegation},
	{name: "roles", roles: integrating, run: ruleRoles},
	{name: "audience", roles: []config.Role{config.RoleCore, config.RoleService, config.RolePlatform}, run: ruleAudience},
	{name: "bearers", roles: []config.Role{config.RoleIssuer, config.RolePlatform, config.RoleCore}, run: ruleBearers},
	{name: "no-latere-value", roles: []config.Role{config.RoleCore}, run: ruleNoCompanyValue},
	{name: "client-audiences", roles: []config.Role{config.RoleClient}, run: ruleClientAudiences},
	{name: "documents", roles: integrating, run: ruleDocuments},
}

// Names lists every rule, for the message a repository with no block reads.
// checkWaivers refuses a waiver that names no rule, or a rule the role does
// not run: a waiver with no effect hides a typo, and a typo that lowers the
// bar is the failure this binary is against.
func checkWaivers(cfg config.Identity) error {
	var unknown, idle []string
	for name := range cfg.Waive {
		r, ok := find(name)
		switch {
		case !ok:
			unknown = append(unknown, name)
		case !slices.Contains(r.roles, cfg.Role):
			idle = append(idle, name)
		}
	}
	slices.Sort(unknown)
	slices.Sort(idle)
	if len(unknown) > 0 {
		return fmt.Errorf("%s: identity.waive names %s, which is not a rule\nrules: %s",
			config.Name, strings.Join(unknown, ", "), strings.Join(Names(), ", "))
	}
	if len(idle) > 0 {
		return fmt.Errorf("%s: identity.waive names %s, which role %s does not run; "+
			"a waiver with no effect is a decision with no effect, so delete it",
			config.Name, strings.Join(idle, ", "), string(cfg.Role))
	}
	return nil
}

// find is the rule of that name.
func find(name string) (rule, bool) {
	for _, r := range rules {
		if r.name == name {
			return r, true
		}
	}
	return rule{}, false
}

func Names() []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.name
	}
	return out
}

// Run applies the rules of the declared role to the tree.
func Run(cfg config.Identity, root string, out io.Writer, exec gates.Exec, now time.Time) error {
	if !cfg.Present {
		return fmt.Errorf("%s has no identity block, so no rule of the shape runs here\n"+
			"declare which layer this repository is under identity.role, one of %s; "+
			"a library or a tool declares none, which is a role and not an absence",
			config.Name, config.RoleList())
	}
	if cfg.Role == config.RoleNone {
		_, _ = fmt.Fprintf(out, "identity: role none, no identity surface to hold\n"+
			"not run: %s\n", strings.Join(Names(), ", "))
		return nil
	}
	if err := checkWaivers(cfg); err != nil {
		return err
	}
	t, err := scan(cfg, root, exec)
	if err != nil {
		return err
	}
	applied := 0
	var failed []string
	for _, r := range rules {
		if !slices.Contains(r.roles, cfg.Role) {
			continue
		}
		applied++
		res, runErr := r.run(t)
		if runErr != nil {
			return runErr
		}
		w, waived := cfg.Waive[r.name]
		expired := ""
		if waived {
			// The date is inclusive, as a gate waiver's is: until 1 November
			// covers all of 1 November.
			until, _ := w.UntilDate()
			if now.Before(until.AddDate(0, 0, 1)) {
				_, _ = fmt.Fprintf(out, "%-4s %-17s until %s: %s (%d finding(s))\n",
					"WAIV", r.name, w.Until, w.Reason, len(res.findings))
				if len(res.findings) == 0 {
					_, _ = fmt.Fprintf(out, "  the rule holds already; the waiver can go\n")
				}
				continue
			}
			expired = "; the waiver expired " + w.Until + ": " + w.Reason
		}
		switch {
		case len(res.findings) > 0:
			failed = append(failed, r.name)
			_, _ = fmt.Fprintf(out, "%-4s %-17s %d finding(s)%s\n", "FAIL", r.name, len(res.findings), expired)
			for _, f := range sorted(res.findings) {
				_, _ = fmt.Fprintln(out, "  "+f.String())
			}
		case res.skip != "":
			_, _ = fmt.Fprintf(out, "%-4s %-17s %s\n", "SKIP", r.name, res.skip)
		default:
			_, _ = fmt.Fprintf(out, "%-4s %-17s %s\n", "PASS", r.name, res.note)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d identity rule(s) failed for role %s: %s",
			len(failed), applied, string(cfg.Role), strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(out, "role %s holds %d rule(s) of the shape\n", string(cfg.Role), applied)
	return nil
}

// sorted orders findings by location, so two runs over one tree print the
// same report.
func sorted(f []Finding) []Finding {
	out := slices.Clone(f)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rel != out[j].Rel {
			return out[i].Rel < out[j].Rel
		}
		return out[i].Line < out[j].Line
	})
	return out
}
