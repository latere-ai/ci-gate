// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"slices"
	"strings"
)

// Role is the layer of the family's identity shape a repository holds.
//
// The vocabulary is closed because the gate picks a rule set from it. A value
// outside the list would select no rules, which is a gate that reports green
// over a repository nobody is checking.
type Role string

const (
	// RoleUnset is the zero value: a block that exists and names no layer.
	RoleUnset Role = ""
	// RoleIssuer mints every token and owns the client registry.
	RoleIssuer Role = "issuer"
	// RoleCore verifies one token, forwards every claim and asks one
	// authorizer. It holds no value of the company that deploys it.
	RoleCore Role = "core"
	// RolePlatform answers the cores' questions from its own tables.
	RolePlatform Role = "platform"
	// RoleService verifies that the audience is itself and decides from its
	// own state.
	RoleService Role = "service"
	// RoleBFF forwards the person's token and decides nothing.
	RoleBFF Role = "bff"
	// RoleClient holds credentials and presents one audience per product.
	RoleClient Role = "client"
	// RoleNone is a library or a tool. It is a declared role rather than an
	// absent one, so the family check lists it.
	RoleNone Role = "none"
)

// Roles is the vocabulary in layer order, which is the order the family
// table prints.
var Roles = []Role{RoleIssuer, RoleCore, RolePlatform, RoleService, RoleBFF, RoleClient, RoleNone}

// RoleList renders the vocabulary for a message.
func RoleList() string { return roleNames(Roles) }

// roleNames renders a set of roles for a message.
func roleNames(roles []Role) string {
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = string(r)
	}
	return strings.Join(names, ", ")
}

// DefaultRegistry is where the issuer keeps the client registry the family
// check reads.
const DefaultRegistry = "deploy/base/clients.yaml"

// audienceRoles verify an audience of their own, so they must name it.
var audienceRoles = []Role{RoleCore, RoleService, RolePlatform}

// imageRoles deploy a workload of their own under an image name, which is
// what identity.image declares.
var imageRoles = []Role{RoleService, RoleBFF, RoleCore}

// Identity declares which layer of the family's identity shape a repository
// is, and holds the values that layer's rules need.
//
// The block is mandatory: a repository with none fails the gate rather than
// skipping it, so a repository cannot sit outside the shape by omission.
type Identity struct {
	// Role selects the rules.
	Role Role `yaml:"role"`
	// Audience is what this repository verifies as the audience of a token
	// addressed to it. Required for core, service and platform.
	Audience string `yaml:"audience"`
	// ConfigPrefix is the prefix of the deployment variables that carry the
	// issuers, the audience and the authorizer. Required for core.
	ConfigPrefix string `yaml:"config_prefix"`
	// APIGroup is the group a core's own manifests are written under, such as
	// cella.latere.ai. The no-company-value rule exempts it, so it is
	// declared rather than guessed. Required for core.
	APIGroup string `yaml:"api_group"`
	// ClaimsPassthrough lists the files of a core that may name a claim,
	// because forwarding one is what they do.
	ClaimsPassthrough []string `yaml:"claims_passthrough"`
	// Skip lists paths the scans do not enter, as a directory or a file
	// relative to the repository root.
	Skip []string `yaml:"skip"`
	// Overlays lists the paths that hold one company's own deployment
	// overlay: the manifests a hosted installation deploys from, kept in the
	// core's tree because the tag deploys from them. The no-company-value
	// rule reads a declared overlay rather than scanning it, so the values
	// that overlay carries are the ones a document may name; every other
	// rule reads it as before. Core only.
	Overlays []string `yaml:"overlays"`
	// Image is the name this repository's workload image was built under,
	// for a repository whose image carries neither a command name under
	// cmd/ nor the module's own name. The deployment rules recognise a
	// container by the last path segment of its image, without the
	// registry, the tag and the digest, so the value here is that segment
	// alone. It is a declaration and not a rename: a repository that
	// deploys wallfacerd out of a module called wallfacer says so here.
	// Service, bff and core only.
	Image string `yaml:"image"`
	// EnvelopeExempt lists the files whose types carry the envelope's field
	// names for a reason of their own: a core's limits type, and the page a
	// list action answers with. The envelope rule does not read them. The
	// two reasons are declared here rather than written into the gate, so a
	// repository that grows a third writes it down beside them, and a
	// declared path the tree does not hold stops the run.
	EnvelopeExempt []string `yaml:"envelope_exempt"`
	// RolesOnly turns on the rule that access is by role. It is one way: a
	// tree whose history set it cannot unset it.
	RolesOnly bool `yaml:"roles_only"`
	// Registry is the client registry the family check reads. Issuer only.
	Registry string `yaml:"registry"`
	// Audiences are the product audiences a client mints for. Client only.
	Audiences []string `yaml:"audiences"`
	// BFF lists the paths that hold this repository's browser frontend: a
	// server that signs the person in, keeps the session, and forwards the
	// person's own token to the issuer's API for what the pages show. The
	// request-path rule does not read those files, because calling the
	// issuer with the person's token is what a frontend does; the API the
	// repository verifies for is still held everywhere else. Roles that
	// verify only; a repository that is only a frontend declares role bff.
	BFF []string `yaml:"bff"`
	// ReachedBy says who presents this repository's audience: "clients", the
	// default, means a registered client mints an actor token for it and the
	// family check expects the registry to list it; "services" means no
	// client acts here for a person, only service tokens or operators reach
	// it; "self-hosted" means the audience is an open core's default, which
	// only a self-hosted deployment verifies while the hosted plane is
	// another repository. Neither of the last two expects a registry row.
	// Roles that verify only.
	ReachedBy string `yaml:"reached_by"`
	// Waive maps a rule name to the decision not to hold this repository to
	// it yet. It is per rule, not per gate, so a repository behind on one
	// rule keeps the other ten running; every entry carries a reason and
	// the day it stops working, like a gate waiver.
	Waive map[string]Waiver `yaml:"waive"`
	// Present reports whether the file carried the block at all. Computed by
	// Load; not part of the file.
	Present bool `yaml:"-"`
	// Settled is the spec tree's settled status list, copied from the spec
	// section by Load so the document rules can tell a finished spec, which
	// is a record, from an open one, which describes the current system.
	Settled []string `yaml:"-"`
}

// ReachedByValues are the ways a verified audience is reached: by a
// registered client minting an actor token for it ("clients"); by service
// tokens and operators alone, with no client acting for a person
// ("services"); or only in a self-hosted deployment of an open core, whose
// hosted plane is another repository with an audience of its own
// ("self-hosted"), so the family's registry never lists the core's default.
var ReachedByValues = []string{"clients", "services", "self-hosted"}

// ReachedByClients reports whether the family check expects a client to be
// registered to mint for this repository's audience.
func (i Identity) ReachedByClients() bool { return i.ReachedBy == "" || i.ReachedBy == "clients" }

// Verifies reports whether this role verifies a token addressed to itself,
// which is the set of roles that must name an audience.
func (i Identity) Verifies() bool { return slices.Contains(audienceRoles, i.Role) }

// validate rejects a block whose role would run rules with nothing to read.
func (i Identity) validate(path string) error {
	if !i.Present {
		return nil
	}
	if i.Role == RoleUnset {
		return fmt.Errorf("%s: identity.role is empty\n"+
			"name the layer of the shape this repository is, one of %s", path, RoleList())
	}
	if !slices.Contains(Roles, i.Role) {
		return fmt.Errorf("%s: identity.role %q is not a layer of the shape\n"+
			"one of %s", path, string(i.Role), RoleList())
	}
	if i.Verifies() && strings.TrimSpace(i.Audience) == "" {
		return fmt.Errorf("%s: identity.role %q verifies a token addressed to itself and "+
			"identity.audience is empty\nname what this repository accepts as its own audience",
			path, string(i.Role))
	}
	if i.Role == RoleCore && strings.TrimSpace(i.ConfigPrefix) == "" {
		return fmt.Errorf("%s: identity.config_prefix is empty\n"+
			"a core names the prefix of its deployment variables, such as CELLA, so the "+
			"audience rule knows which variable to look for", path)
	}
	if i.Role == RoleCore && strings.TrimSpace(i.APIGroup) == "" {
		return fmt.Errorf("%s: identity.api_group is empty\n"+
			"a core names the group its own manifests are written under, so the "+
			"no-company-value rule exempts it rather than guessing at it", path)
	}
	if strings.TrimSpace(i.Registry) != "" && i.Role != RoleIssuer {
		return fmt.Errorf("%s: identity.registry is set and identity.role is %q\n"+
			"the client registry belongs to the issuer, and a registry nothing "+
			"reads is a decision with no effect", path, string(i.Role))
	}
	if i.ReachedBy != "" && !slices.Contains(ReachedByValues, i.ReachedBy) {
		return fmt.Errorf("%s: identity.reached_by %q is not a way an audience is reached\n"+
			"one of %s", path, i.ReachedBy, strings.Join(ReachedByValues, ", "))
	}
	if len(i.BFF) > 0 && !i.Verifies() {
		return fmt.Errorf("%s: identity.bff is set and identity.role is %q\n"+
			"only a role that verifies an API of its own has a frontend beside it; "+
			"a repository that is only a frontend declares role bff", path, string(i.Role))
	}
	if i.ReachedBy != "" && !i.Verifies() {
		return fmt.Errorf("%s: identity.reached_by is set and identity.role is %q\n"+
			"only a role that verifies an audience says who reaches it", path, string(i.Role))
	}
	if name := strings.TrimSpace(i.Image); name != "" {
		if !slices.Contains(imageRoles, i.Role) {
			return fmt.Errorf("%s: identity.image is set and identity.role is %q\n"+
				"only a repository that deploys a workload of its own names the image it "+
				"was built under, one of %s; a name nothing reads is a decision with no effect",
				path, string(i.Role), roleNames(imageRoles))
		}
		if strings.ContainsAny(name, "/:@") {
			return fmt.Errorf("%s: identity.image %q carries a registry, a tag or a digest\n"+
				"name the segment the image was built under and nothing else, such as wallfacerd: "+
				"that is the part of the reference the deployment rules compare", path, name)
		}
	}
	if len(i.EnvelopeExempt) > 0 && i.Role == RoleNone {
		return fmt.Errorf("%s: identity.envelope_exempt is set and identity.role is none\n"+
			"a library or a tool runs no rule of the shape, so an exemption from one "+
			"is a decision with no effect", path)
	}
	if len(i.Overlays) > 0 && i.Role != RoleCore {
		return fmt.Errorf("%s: identity.overlays is set and identity.role is %q\n"+
			"only an open core holds one company's overlay in its tree as an exception, "+
			"and a list nothing reads is a decision with no effect", path, string(i.Role))
	}
	if len(i.Audiences) > 0 && i.Role != RoleClient {
		return fmt.Errorf("%s: identity.audiences is set and identity.role is %q\n"+
			"only a client mints for a product audience, and a list nothing "+
			"reads is a decision with no effect", path, string(i.Role))
	}
	var noWhy, noDate []string
	for rule, w := range i.Waive {
		if strings.TrimSpace(w.Reason) == "" {
			noWhy = append(noWhy, rule)
		}
		if _, ok := w.UntilDate(); !ok {
			noDate = append(noDate, rule)
		}
	}
	if len(noWhy) > 0 {
		slices.Sort(noWhy)
		return fmt.Errorf("%s: identity.waive entry without a reason: %s\n"+
			"not holding a rule is a decision: write why this repository does "+
			"not hold it yet, and which work will, or delete the entry",
			path, strings.Join(noWhy, ", "))
	}
	if len(noDate) > 0 {
		slices.Sort(noDate)
		return fmt.Errorf("%s: identity.waive entry without a usable until date: %s\n"+
			"write the date the waiver stops working, as YYYY-MM-DD: a "+
			"waiver with no end is the shape being lowered permanently",
			path, strings.Join(noDate, ", "))
	}
	return nil
}
