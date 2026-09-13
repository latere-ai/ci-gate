// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"latere.ai/x/ci-gate/internal/config"
)

// issuerAudience is the audience every client may ask for, which is the
// issuer itself, and the name the registry writes it under. Neither is a
// product audience, so the check reads past both.
var issuerAudience = []string{"$ISSUER", "auth"}

// repository is one repository's block as the family check reads it.
type repository struct {
	name string
	dir  string
	cfg  config.Identity
	err  error
}

// registry is the issuer's client registry: which audiences a token may be
// minted for, per client.
type registry struct {
	Clients []struct {
		ClientID string `yaml:"client_id"`
		// AllowedAudiences are the audiences a client's own token may carry.
		AllowedAudiences []string `yaml:"allowed_audiences"`
		// ActorAudiences are the audiences an actor token may be minted for
		// on that client's behalf. The key arrives with the family's id-03,
		// so a registry without it is read for the other one alone.
		ActorAudiences []string `yaml:"actor_audiences"`
	} `yaml:"clients"`
}

// Family checks the part of the shape that is only visible with every
// repository in view, and prints the layer table the blocks derive.
//
// dir holds one directory per repository, which the family workflow gathers
// by checkout. expect, when set, is the committed copy of the table: the
// document is then derived from the tree rather than maintained beside it.
func Family(dir string, expect string, out io.Writer) error {
	repos, err := readFamily(dir)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return fmt.Errorf("no repository under %s, so the family check would pass over nothing\n"+
			"give the directory the checkouts are gathered in", dir)
	}
	findings := crossCheck(repos)
	table := layerTable(repos)
	_, _ = fmt.Fprint(out, table)
	if expect != "" {
		if f := compareTable(expect, table); f != "" {
			findings = append(findings, f)
		}
	}
	if len(findings) > 0 {
		return fmt.Errorf("%d finding(s) across %d repositories:\n- %s",
			len(findings), len(repos), strings.Join(findings, "\n- "))
	}
	_, _ = fmt.Fprintf(out, "\n%d repositories, each with a declared role and an audience the registry lists\n", len(repos))
	return nil
}

// readFamily reads the block of every repository under dir.
func readFamily(dir string) ([]repository, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading the checkouts to compare: %w", err)
	}
	var out []repository
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		r := repository{name: e.Name(), dir: filepath.Join(dir, e.Name())}
		cfg, loadErr := config.Load(r.dir)
		if loadErr != nil {
			r.err = loadErr
		} else {
			r.cfg = cfg.Identity
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// crossCheck is every rule that needs more than one repository in view.
func crossCheck(repos []repository) []string {
	var findings []string
	verified := map[string][]string{}
	minted := map[string][]string{}
	issuers := 0
	var registered []string
	for _, r := range repos {
		switch {
		case r.err != nil:
			findings = append(findings, fmt.Sprintf("%s: its file could not be read: %v", r.name, r.err))
			continue
		case !r.cfg.Present:
			findings = append(findings, r.name+" carries no identity block, so no rule of the shape runs "+
				"there; declare its layer, one of "+config.RoleList())
			continue
		}
		if r.cfg.Verifies() {
			verified[r.cfg.Audience] = append(verified[r.cfg.Audience], r.name)
		}
		for _, a := range r.cfg.Audiences {
			minted[a] = append(minted[a], r.name)
		}
		if r.cfg.Role == config.RoleIssuer {
			issuers++
			names, err := readRegistry(r)
			if err != nil {
				findings = append(findings, fmt.Sprintf("%s: its client registry could not be read: %v", r.name, err))
				continue
			}
			registered = append(registered, names...)
		}
	}
	if issuers == 0 {
		findings = append(findings, "no repository declares the issuer role, so there is no client "+
			"registry to check the audiences against")
	}
	for _, a := range sortedKeys(verified) {
		if len(verified[a]) > 1 {
			findings = append(findings, fmt.Sprintf("%s and %s both verify the audience %q; one audience "+
				"belongs to one repository, or a token minted for either reaches both",
				strings.Join(verified[a][:len(verified[a])-1], ", "), verified[a][len(verified[a])-1], a))
		}
		if issuers > 0 && !slices.Contains(registered, a) {
			findings = append(findings, fmt.Sprintf("%s verifies the audience %q and the registry does not "+
				"list it, so nothing can be minted for it", strings.Join(verified[a], ", "), a))
		}
	}
	for _, a := range slices.Compact(slices.Sorted(slices.Values(registered))) {
		if len(verified[a]) == 0 {
			findings = append(findings, fmt.Sprintf("the registry lists the audience %q and no repository "+
				"verifies it; a token can be minted for a reader that does not exist", a))
		}
	}
	for _, a := range sortedKeys(minted) {
		if len(verified[a]) == 0 {
			findings = append(findings, fmt.Sprintf("%s presents the audience %q and no repository verifies "+
				"it, so the credential is refused wherever it is sent", strings.Join(minted[a], ", "), a))
		}
	}
	return findings
}

// readRegistry reads the audiences the issuer's registry admits, both the
// ones a client's own token may carry and the ones an actor token may be
// minted for. The issuer itself is not a product audience.
func readRegistry(r repository) ([]string, error) {
	body, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(r.cfg.Registry)))
	if err != nil {
		return nil, err
	}
	var reg registry
	if err := yaml.Unmarshal(body, &reg); err != nil {
		return nil, fmt.Errorf("%s: %w", r.cfg.Registry, err)
	}
	var out []string
	for _, c := range reg.Clients {
		for _, a := range append(slices.Clone(c.AllowedAudiences), c.ActorAudiences...) {
			if !slices.Contains(issuerAudience, a) {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// layerTable renders the layer table from the blocks, so the document that
// carries it is derived from the tree.
func layerTable(repos []repository) string {
	var b strings.Builder
	b.WriteString("| Role | Repositories | Audience |\n|---|---|---|\n")
	for _, role := range config.Roles {
		var names, audiences []string
		for _, r := range repos {
			if r.err != nil || !r.cfg.Present || r.cfg.Role != role {
				continue
			}
			names = append(names, r.name)
			if r.cfg.Audience != "" {
				audiences = append(audiences, r.cfg.Audience)
			}
			audiences = append(audiences, r.cfg.Audiences...)
		}
		if len(names) == 0 {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", string(role), strings.Join(names, ", "), strings.Join(audiences, ", "))
	}
	return b.String()
}

// compareTable reports the first line at which the committed table and the
// derived one differ.
func compareTable(path, table string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("the committed layer table could not be read: %v", err)
	}
	want := strings.Split(strings.TrimSpace(table), "\n")
	got := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(body), "\r\n", "\n")), "\n")
	for i := range max(len(want), len(got)) {
		w, g := lineAt(want, i), lineAt(got, i)
		if strings.TrimSpace(w) == strings.TrimSpace(g) {
			continue
		}
		return fmt.Sprintf("the committed layer table differs from the one the blocks derive, at line %d: "+
			"it reads %q and the blocks say %q", i+1, g, w)
	}
	return ""
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func sortedKeys(m map[string][]string) []string {
	return slices.Sorted(slices.Values(keysOf(m)))
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
