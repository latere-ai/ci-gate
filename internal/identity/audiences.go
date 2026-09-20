// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"fmt"
	"strings"
)

// originAudience is the second audience every open core of the family
// accepts. A script holding a platform key calls the origin directly and the
// core it reaches asks the authorizer, so the boundary between two cores is
// the authorizer's and not the audience's.
const originAudience = "api.latere.ai"

// acceptedAudiences is how many a core accepts: its own name and the origin.
const acceptedAudiences = 2

// effective is the audience one container of the hosted deployment runs, and
// the place a reader opens to change it.
type effective struct {
	rel   string
	line  int
	value string
	set   bool
}

// ruleCoreAudiences holds a core's hosted deployment to the two audiences
// the family accepts, its own name and the origin.
//
// The value that deployment runs is the overlay's where the overlay patches
// it and the base's otherwise, because that is what kustomize merges. Every
// file of one overlay patches one deployment, so a container two files patch
// is one verdict and not two.
//
// The overlay is read from the block's declaration rather than from the
// walk, because a repository that declares one usually skips it as well:
// overlays is the positive declaration, this directory is the hosted
// deployment, and skip the negative one, assert nothing here.
func ruleCoreAudiences(t *tree) (result, error) {
	if len(clean(t.cfg.Overlays)) == 0 {
		return result{skip: "this repository declares no hosted deployment overlay"}, nil
	}
	variable := t.cfg.ConfigPrefix + "_OIDC_AUDIENCE"
	prefix := ownPrefix(t.cfg)
	// An overlay patches a container by name and carries no image, so the
	// names are read across the base and the overlay together.
	all := make([]manifest, 0, len(t.manifests)+len(t.overlayManifests))
	all = append(all, t.manifests...)
	all = append(all, t.overlayManifests...)
	named := workloadNames(all, t.binaries)

	base := map[workloadKey]string{}
	for _, m := range t.manifests {
		// A tree that does not skip its declared overlay reads those files
		// twice; the base is what stands outside it.
		if m.err != nil || t.overlay(m.rel) {
			continue
		}
		for _, w := range m.ownWorkloads(t.binaries, named, prefix) {
			if entry, ok := w.env(variable); ok {
				value, _ := entry["value"].(string)
				base[w.key()] = value
			}
		}
	}

	var found []Finding
	var order []workloadKey
	patched := map[workloadKey]*effective{}
	for _, m := range t.overlayManifests {
		if m.err != nil {
			found = append(found, at(m.rel, 1, unreadableSentence))
			continue
		}
		for _, w := range m.ownWorkloads(t.binaries, named, prefix) {
			k := w.key()
			e, ok := patched[k]
			if !ok {
				e = &effective{rel: m.rel, line: m.lineOf("name", w.doc)}
				patched[k] = e
				order = append(order, k)
			}
			entry, ok := w.env(variable)
			if !ok {
				continue
			}
			// The files of one overlay merge in the order kustomize reads
			// them, so the last value written is the one it runs.
			value, _ := entry["value"].(string)
			e.rel, e.line, e.value, e.set = m.rel, m.lineOf("name", variable), value, true
		}
	}

	held := 0
	for _, k := range order {
		e := patched[k]
		value, known := e.value, e.set
		if !known {
			value, known = base[k]
		}
		// Neither file names an audience, which the audience rule reports
		// about the container that sets none.
		if !known {
			continue
		}
		f, judged := judge(t.cfg.Audience, k, value, e.rel, e.line)
		if !judged {
			continue
		}
		held++
		found = append(found, f...)
	}

	switch {
	case len(found) > 0:
		return result{findings: found}, nil
	case len(order) == 0:
		return result{skip: "the declared overlay patches no container this repository builds"}, nil
	case held == 0:
		return result{skip: "no container the declared overlay patches names an audience " +
			"the overlay or the base sets"}, nil
	}
	return result{note: fmt.Sprintf(
		"%d container(s) of the hosted deployment name this repository's own name and the origin", held)}, nil
}

// judge reads one effective value, and reports whether it was read at all: a
// value the audience rule reports as an address is that rule's finding and
// is not reported here a second time.
func judge(audience string, k workloadKey, value, rel string, line int) ([]Finding, bool) {
	if address(value) {
		return nil, false
	}
	entries := splitAudiences(value)
	switch {
	case len(entries) == 1 && entries[0] == audience:
		return []Finding{at(rel, line, ownNameOnlySentence, where(k))}, true
	case len(entries) != acceptedAudiences:
		return []Finding{at(rel, line, audienceCountSentence, where(k), count(len(entries)))}, true
	case entries[0] == entries[1]:
		return []Finding{at(rel, line, audienceTwiceSentence, where(k))}, true
	}
	for _, e := range entries {
		if e != audience && e != originAudience {
			return []Finding{at(rel, line, strangeAudienceSentence, where(k), e)}, true
		}
	}
	return nil, true
}

// splitAudiences reads a set written as one value, the way a core that
// accepts a set reads it: split on commas, each entry trimmed.
func splitAudiences(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// where names the container a finding is about. The document's name stands
// beside the container's, so a reaper running the same container name as the
// server is a finding of its own.
func where(k workloadKey) string {
	if k.doc == "" || k.doc == k.container {
		return fmt.Sprintf("container %q", k.container)
	}
	return fmt.Sprintf("container %q of %q", k.container, k.doc)
}

// count renders how many names a value lists, so a sentence about one name
// reads as one.
func count(n int) string {
	if n == 1 {
		return "1 name"
	}
	return fmt.Sprintf("%d names", n)
}

const ownNameOnlySentence = "the hosted overlay leaves the audience of %s at this repository's own " +
	"name, so a token addressed to api.latere.ai stops here; name both, separated by a comma"

const audienceCountSentence = "the audience of %s lists %s, and a hosted deployment names two, this " +
	"repository's own name and api.latere.ai"

const audienceTwiceSentence = "the audience of %s names one audience twice, and a hosted deployment " +
	"names two, this repository's own name and api.latere.ai"

const strangeAudienceSentence = "the audience of %s names %q, which is neither this repository's own " +
	"name nor api.latere.ai"
