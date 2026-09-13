// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"fmt"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// audienceVariable is what a service and a platform read their audience
// from; a core reads its own, under the prefix its block declares.
const audienceVariable = "AUTH_AUDIENCE"

// internalPrefix is the route that stays inside the cluster.
const internalPrefix = "/internal/"

// ruleAudience holds every deployment of a repository to naming the audience
// it verifies.
//
// A container runs this repository when its image names one of the commands
// the repository builds, or when the document holds exactly one container,
// which is the shape of a single-workload manifest.
func ruleAudience(t *tree) (result, error) {
	if len(t.manifests) == 0 {
		return result{skip: "the tree has no deployment manifest"}, nil
	}
	name := audienceVariable
	if t.cfg.Role == config.RoleCore {
		name = t.cfg.ConfigPrefix + "_OIDC_AUDIENCE"
	}
	var found []Finding
	checked := 0
	for _, m := range t.manifests {
		if m.err != nil {
			found = append(found, at(m.rel, 1, unreadableSentence))
			continue
		}
		for _, c := range m.own(t.binaries) {
			checked++
			line := m.lineOf("image", c.image)
			entry, ok := c.env(name)
			if !ok {
				found = append(found, at(m.rel, line, noAudienceSentence, name))
				continue
			}
			value, _ := entry["value"].(string)
			switch {
			case strings.TrimSpace(value) == "":
				found = append(found, at(m.rel, m.lineOf("name", name), noAudienceValueSentence, name))
			case strings.HasPrefix(value, "http"):
				found = append(found, at(m.rel, m.lineOf("name", name), addressAsAudienceSentence))
			}
		}
	}
	if checked == 0 && len(found) == 0 {
		return result{skip: "no container in the deployment runs a command this repository builds"}, nil
	}
	return result{findings: found,
		note: fmt.Sprintf("%d container(s) name the audience they verify", checked)}, nil
}

const unreadableSentence = "this deployment file does not parse as a document, so the deployment " +
	"rules could not read it"

const noAudienceSentence = "this container sets no %s, so it would accept a token addressed to " +
	"anything; name the audience this repository verifies"

const noAudienceValueSentence = "the %s of this container is empty, so it would accept a token " +
	"addressed to anything; name the audience this repository verifies"

const addressAsAudienceSentence = "this container names an address as its audience; the audience is " +
	"the name this repository answers to, not the address of the issuer"

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

// container is one container of a deployment document.
type container struct {
	image string
	spec  map[string]any
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

// containers lists every container of every document in a manifest.
func (m manifest) containers() []container {
	var out []container
	for _, doc := range m.docs {
		walkYAML(doc, func(node map[string]any) {
			list, ok := node["containers"].([]any)
			if !ok {
				return
			}
			for _, c := range list {
				spec, ok := c.(map[string]any)
				if !ok {
					continue
				}
				image, _ := spec["image"].(string)
				out = append(out, container{image: image, spec: spec})
			}
		})
	}
	return out
}

// own selects the containers that run this repository: the ones whose image
// names a command it builds, or the only container there is.
func (m manifest) own(binaries []string) []container {
	all := m.containers()
	if len(all) == 1 {
		return all
	}
	var out []container
	for _, c := range all {
		for _, b := range binaries {
			if b != "" && strings.Contains(c.image, b) {
				out = append(out, c)
				break
			}
		}
	}
	return out
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
