// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"fmt"
	"slices"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// sharedPrefix is the prefix of the variables a service and a platform are
// configured with; a core is configured under the prefix its block declares.
const sharedPrefix = "AUTH_"

// audienceVariable is what a service and a platform read their audience
// from; a core reads its own, under the prefix its block declares.
const audienceVariable = sharedPrefix + "AUDIENCE"

// internalPrefix is the route that stays inside the cluster.
const internalPrefix = "/internal/"

// ruleAudience holds every deployment of a repository to naming the audience
// it verifies.
//
// A container runs this repository when the last path segment of its image,
// without the tag or the digest, is exactly a command the repository builds:
// origo runs ghcr.io/latere-ai/origo:v1 and never origo-stubs. A container
// with no image is an overlay's patch of one declared elsewhere, and is read
// as the container whose name it merges into. An init container is held only
// when it is configured as the workload is, by declaring a variable of the
// repository's own prefix: one that runs the check with the node's
// environment verifies a token, one that copies a file does not.
func ruleAudience(t *tree) (result, error) {
	if len(t.manifests) == 0 {
		return result{skip: "the tree has no deployment manifest"}, nil
	}
	names := []string{audienceVariable, audienceVariable + "S"}
	if t.cfg.Role == config.RoleCore {
		names = []string{t.cfg.ConfigPrefix + "_OIDC_AUDIENCE"}
	}
	named := workloadNames(t.manifests, t.binaries)
	prefix := ownPrefix(t.cfg)
	// A deployment is a base and its overlays, so one container is judged
	// across every file that names it: it passes when any file sets the
	// audience, and an address anywhere is a finding.
	var found []Finding
	set := map[string]bool{}
	missing := map[string][]Finding{}
	seen := map[string]bool{}
	for _, m := range t.manifests {
		if m.err != nil {
			found = append(found, at(m.rel, 1, unreadableSentence))
			continue
		}
		for _, c := range m.own(t.binaries, named, prefix) {
			seen[c.name] = true
			entry, name, ok := firstEnv(c, names)
			if !ok {
				missing[c.name] = append(missing[c.name], at(m.rel, m.lineOf("image", c.image), noAudienceSentence, names[0]))
				continue
			}
			value, _ := entry["value"].(string)
			switch {
			case strings.TrimSpace(value) == "":
				found = append(found, at(m.rel, m.lineOf("name", name), noAudienceValueSentence, name))
			case address(value):
				found = append(found, at(m.rel, m.lineOf("name", name), addressAsAudienceSentence))
			default:
				set[c.name] = true
			}
		}
	}
	for name, fs := range missing {
		if !set[name] {
			found = append(found, fs...)
		}
	}
	if len(seen) == 0 && len(found) == 0 {
		return result{skip: "no container in the deployment runs a command this repository builds"}, nil
	}
	return result{findings: found,
		note: fmt.Sprintf("%d container(s) name the audience they verify", len(seen))}, nil
}

// firstEnv is the first of names a container sets, with the name it set.
func firstEnv(c container, names []string) (map[string]any, string, bool) {
	for _, n := range names {
		if entry, ok := c.env(n); ok {
			return entry, n, true
		}
	}
	return nil, "", false
}

const unreadableSentence = "this deployment file does not parse as a document, so the deployment " +
	"rules could not read it"

const noAudienceSentence = "this container sets no %s, so it would accept a token addressed to " +
	"anything; name the audience this repository verifies"

const noAudienceValueSentence = "the %s of this container is empty, so it would accept a token " +
	"addressed to anything; name the audience this repository verifies"

const addressAsAudienceSentence = "this container names an address as its audience; the audience is " +
	"the name this repository answers to, not the address of the issuer"

// address reports whether a value names an address where a name belongs. It
// is one test rather than two, so the rule that reports it and the rule that
// leaves it alone read one definition.
func address(value string) bool {
	return strings.HasPrefix(value, "http") ||
		strings.Contains(value, ",http") || strings.Contains(value, ", http")
}

// ruleBearers holds every cross-service credential to one endpoint, and
// keeps an internal route inside the cluster.
func ruleBearers(t *tree) (result, error) {
	if len(t.manifests) == 0 {
		return result{skip: "the tree has no deployment manifest"}, nil
	}
	var found []Finding
	containers, routes := 0, 0
	for _, m := range t.manifests {
		if m.err != nil {
			found = append(found, at(m.rel, 1, unreadableSentence))
			continue
		}
		for _, c := range m.containers() {
			containers++
			seen := map[string]bool{}
			for _, e := range c.envs() {
				ref := secretRef(e)
				if ref == "" {
					continue
				}
				name, _ := e["name"].(string)
				if seen[ref] {
					found = append(found, at(m.rel, m.lineOf("name", name), sharedSecretSentence))
					continue
				}
				seen[ref] = true
			}
		}
		for host, paths := range m.routes() {
			routes++
			for _, p := range paths {
				if !reaches(p, paths) {
					continue
				}
				found = append(found, at(m.rel, m.lineOf("host", host), publicInternalSentence))
			}
		}
	}
	if containers == 0 && routes == 0 && len(found) == 0 {
		return result{skip: "the deployment has no container and no route"}, nil
	}
	return result{findings: found,
		note: fmt.Sprintf("%d container(s) and %d host(s) keep each credential and route to itself", containers, routes)}, nil
}

const sharedSecretSentence = "a second variable of this container reads the same secret key; every " +
	"credential between two services is per endpoint, so give this one its own"

const publicInternalSentence = "this host serves an internal route behind a public path; an internal " +
	"route is reachable from inside the cluster only"

// reaches reports whether p is a public path that also serves an internal
// one on the same host.
func reaches(p string, paths []string) bool {
	if strings.HasPrefix(p, internalPrefix) {
		return false
	}
	if p != "/" && !strings.HasPrefix(internalPrefix, p) {
		return false
	}
	for _, other := range paths {
		if strings.HasPrefix(other, internalPrefix) {
			return true
		}
	}
	return false
}

// container is one container of a deployment document. init marks the ones
// that run to completion before the workload starts, which the audience rule
// reads differently from the workload itself.
type container struct {
	name  string
	image string
	spec  map[string]any
	init  bool
}

// declares reports whether a container sets any variable of a prefix, which
// is how an init container says it runs with the workload's configuration.
// A variable reached through envFrom is not named here and does not count.
func (c container) declares(prefix string) bool {
	for _, e := range c.envs() {
		if n, ok := e["name"].(string); ok && strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// envs lists the environment entries of a container.
func (c container) envs() []map[string]any {
	list, ok := c.spec["env"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// env returns the entry with a name.
func (c container) env(name string) (map[string]any, bool) {
	for _, e := range c.envs() {
		if n, ok := e["name"].(string); ok && n == name {
			return e, true
		}
	}
	return nil, false
}

// secretRef renders the secret key an entry reads, or "" when it reads a
// value. Two entries rendering the same string read one key.
func secretRef(e map[string]any) string {
	from, ok := e["valueFrom"].(map[string]any)
	if !ok {
		return ""
	}
	ref, ok := from["secretKeyRef"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := ref["name"].(string)
	key, _ := ref["key"].(string)
	if name == "" || key == "" {
		return ""
	}
	return name + "/" + key
}

// workload is one container of one deployment document, carrying the name
// the document is written under. A reaper beside a server runs the same
// container name under another workload and is configured on its own, so a
// rule that holds each deployment reads the pair and not the container name
// alone.
type workload struct {
	container
	doc string
}

// workloadKey identifies one container of one document across the files
// that patch it.
type workloadKey struct{ doc, container string }

func (w workload) key() workloadKey { return workloadKey{doc: w.doc, container: w.name} }

// workloads lists every container of every document in a manifest with the
// document it is declared in, the init containers first: a container that
// verifies a token before the workload starts is a container of the
// deployment like any other.
func (m manifest) workloads() []workload {
	var out []workload
	for _, doc := range m.docs {
		name := documentName(doc)
		walkYAML(doc, func(node map[string]any) {
			for _, c := range listed(node, "initContainers", true) {
				out = append(out, workload{container: c, doc: name})
			}
			for _, c := range listed(node, "containers", false) {
				out = append(out, workload{container: c, doc: name})
			}
		})
	}
	return out
}

// documentName is the name a deployment document is written under, which is
// what an overlay's patch merges on.
func documentName(doc any) string {
	node, ok := doc.(map[string]any)
	if !ok {
		return ""
	}
	meta, ok := node["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := meta["name"].(string)
	return name
}

// containers lists every container of every document in a manifest, for the
// rules that hold a container wherever it is declared.
func (m manifest) containers() []container {
	ws := m.workloads()
	out := make([]container, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.container)
	}
	return out
}

// listed reads one container list of a pod spec.
func listed(node map[string]any, key string, init bool) []container {
	list, ok := node[key].([]any)
	if !ok {
		return nil
	}
	out := make([]container, 0, len(list))
	for _, c := range list {
		spec, ok := c.(map[string]any)
		if !ok {
			continue
		}
		image, _ := spec["image"].(string)
		name, _ := spec["name"].(string)
		out = append(out, container{name: name, image: image, spec: spec, init: init})
	}
	return out
}

// own selects the containers that run this repository, init containers
// included: the ones whose image is a command it builds, and, among those,
// the init containers configured as the workload is.
func (m manifest) own(binaries []string, named map[string]bool, prefix string) []container {
	ws := m.ownWorkloads(binaries, named, prefix)
	out := make([]container, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.container)
	}
	return out
}

// ownWorkloads is own with the document each container is declared in.
func (m manifest) ownWorkloads(binaries []string, named map[string]bool, prefix string) []workload {
	var out []workload
	for _, w := range m.workloads() {
		if !runs(w.container, binaries, named) {
			continue
		}
		if w.init && !w.declares(prefix) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// workloadNames are the names the commands of this repository run under
// across every manifest of the tree. An overlay patches a container by the
// name it merges on and carries no image, so the name is what says which
// container it is.
func workloadNames(ms []manifest, binaries []string) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		for _, c := range m.containers() {
			if c.name != "" && c.image != "" && slices.Contains(binaries, imageName(c.image)) {
				out[c.name] = true
			}
		}
	}
	return out
}

// runs reports whether a container runs a command this repository builds.
func runs(c container, binaries []string, named map[string]bool) bool {
	if c.image == "" {
		return c.name != "" && named[c.name]
	}
	return slices.Contains(binaries, imageName(c.image))
}

// imageName is the name an image was built under: the last path segment of
// the reference, without the tag and without the digest. Matching it whole
// is what tells a repository's own image from another built beside it.
func imageName(image string) string {
	ref := image
	if i := strings.IndexByte(ref, '@'); i >= 0 {
		ref = ref[:i]
	}
	ref = ref[strings.LastIndexByte(ref, '/')+1:]
	if i := strings.IndexByte(ref, ':'); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

// ownPrefix is the prefix of the variables this repository is configured
// with: a core's declared one, and the shared AUTH_ for the roles that read
// the family's variable.
func ownPrefix(cfg config.Identity) string {
	if cfg.Role == config.RoleCore && cfg.ConfigPrefix != "" {
		return cfg.ConfigPrefix + "_"
	}
	return sharedPrefix
}

// routes maps each host of an ingress to the paths it serves.
func (m manifest) routes() map[string][]string {
	out := map[string][]string{}
	for _, doc := range m.docs {
		node, ok := doc.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := node["kind"].(string); kind != "Ingress" {
			continue
		}
		spec, ok := node["spec"].(map[string]any)
		if !ok {
			continue
		}
		rules, ok := spec["rules"].([]any)
		if !ok {
			continue
		}
		for _, r := range rules {
			rule, ok := r.(map[string]any)
			if !ok {
				continue
			}
			host, _ := rule["host"].(string)
			out[host] = append(out[host], pathsOf(rule)...)
		}
	}
	return out
}

// pathsOf reads the paths of one ingress rule.
func pathsOf(rule map[string]any) []string {
	http, ok := rule["http"].(map[string]any)
	if !ok {
		return nil
	}
	list, ok := http["paths"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range list {
		entry, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if path, ok := entry["path"].(string); ok {
			out = append(out, path)
		}
	}
	return out
}

// lineOf finds the line a key and value are written on, so a finding names
// a place a reader can open. The document is read back as text because the
// decoder yields values without positions.
func (m manifest) lineOf(key, value string) int {
	want := key + ": " + value
	for i, line := range m.lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		if trimmed == want || trimmed == key+": \""+value+"\"" || trimmed == key+": '"+value+"'" {
			return i + 1
		}
	}
	return 1
}

// walkYAML visits every mapping of a decoded document.
func walkYAML(v any, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, value := range x {
			walkYAML(value, fn)
		}
	case []any:
		for _, value := range x {
			walkYAML(value, fn)
		}
	}
}
