// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"slices"
	"strings"
	"testing"
)

// A block that is absent and a block that is empty are different decisions,
// and the gate reports on the difference.
func TestIdentityPresenceIsRecorded(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Identity.Present {
		t.Error("a missing file carries no block")
	}
	c, err = Load(write(t, "cover:\n  threshold: 85.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Identity.Present {
		t.Error("a file without the key carries no block")
	}
	c, err = Load(write(t, "identity:\n  role: none\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Identity.Present {
		t.Error("a file with the key carries the block")
	}
	if c.Identity.Role != RoleNone {
		t.Errorf("role = %q, want none", string(c.Identity.Role))
	}
}

func TestIdentityReadsEveryField(t *testing.T) {
	c, err := Load(write(t, `
identity:
  role: core
  audience: sandboxd
  config_prefix: CELLA
  api_group: cella.latere.ai
  claims_passthrough: [internal/auth/claims.go]
  skip: [deploy/prod]
  roles_only: true
`))
	if err != nil {
		t.Fatal(err)
	}
	i := c.Identity
	if i.Role != RoleCore || i.Audience != "sandboxd" || i.ConfigPrefix != "CELLA" {
		t.Errorf("role, audience, prefix = %q, %q, %q", string(i.Role), i.Audience, i.ConfigPrefix)
	}
	if i.APIGroup != "cella.latere.ai" {
		t.Errorf("api_group = %q", i.APIGroup)
	}
	if !slices.Equal(i.ClaimsPassthrough, []string{"internal/auth/claims.go"}) {
		t.Errorf("claims_passthrough = %v", i.ClaimsPassthrough)
	}
	if !slices.Equal(i.Skip, []string{"deploy/prod"}) {
		t.Errorf("skip = %v", i.Skip)
	}
	if !i.RolesOnly {
		t.Error("roles_only = false, want true")
	}
	if !i.Verifies() {
		t.Error("a core verifies an audience of its own")
	}
}

// The issuer's registry defaults, and a file that restates the default is
// reported by contract rather than accepted silently.
func TestIdentityRegistryDefaults(t *testing.T) {
	c, err := Load(write(t, "identity:\n  role: issuer\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Identity.Registry != DefaultRegistry {
		t.Errorf("registry = %q, want %q", c.Identity.Registry, DefaultRegistry)
	}
	if slices.Contains(c.Restated, "identity.registry") {
		t.Error("a defaulted key is not a restated one")
	}
	c, err = Load(write(t, "identity:\n  role: issuer\n  registry: "+DefaultRegistry+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Restated, "identity.registry") {
		t.Errorf("restated = %v, want identity.registry", c.Restated)
	}
}

// Each rejection names the key and what to write instead: a block the gate
// cannot act on is one that reports green over a repository nobody checks.
func TestIdentityValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no role", "identity:\n  audience: lux\n", "identity.role is empty"},
		{"unknown role", "identity:\n  role: gateway\n", "is not a layer of the shape"},
		{"core without audience", "identity:\n  role: core\n  config_prefix: C\n  api_group: c.latere.ai\n", "identity.audience is empty"},
		{"service without audience", "identity:\n  role: service\n", "identity.audience is empty"},
		{"platform without audience", "identity:\n  role: platform\n", "identity.audience is empty"},
		{"core without prefix", "identity:\n  role: core\n  audience: lux\n  api_group: l.latere.ai\n", "identity.config_prefix is empty"},
		{"core without api group", "identity:\n  role: core\n  audience: lux\n  config_prefix: LUX\n", "identity.api_group is empty"},
		{"registry off the issuer", "identity:\n  role: service\n  audience: drive\n  registry: deploy/base/x.yaml\n", "identity.registry is set"},
		{"audiences off a client", "identity:\n  role: bff\n  audiences: [origo]\n", "identity.audiences is set"},
		{"waiver without a reason", "identity:\n  role: service\n  audience: drive\n  waive:\n    verifier: {until: 2026-12-31}\n", "identity.waive entry without a reason"},
		{"waiver without a date", "identity:\n  role: service\n  audience: drive\n  waive:\n    verifier: {reason: later}\n", "identity.waive entry without a usable until date"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, c.body))
			if err == nil {
				t.Fatal("must be rejected")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error must say %q:\n%v", c.want, err)
			}
		})
	}
}

// Every role the vocabulary lists loads with the values that role needs.
func TestIdentityAcceptsEveryRole(t *testing.T) {
	bodies := map[Role]string{
		RoleIssuer:   "identity:\n  role: issuer\n",
		RoleCore:     "identity:\n  role: core\n  audience: origo\n  config_prefix: ORIGO\n  api_group: origo.latere.ai\n",
		RolePlatform: "identity:\n  role: platform\n  audience: platformd\n",
		RoleService:  "identity:\n  role: service\n  audience: drive\n",
		RoleBFF:      "identity:\n  role: bff\n",
		RoleClient:   "identity:\n  role: client\n  audiences: [origo, sandboxd]\n",
		RoleNone:     "identity:\n  role: none\n",
	}
	for _, r := range Roles {
		body, ok := bodies[r]
		if !ok {
			t.Fatalf("role %q has no case; the vocabulary grew and this test did not", string(r))
		}
		c, err := Load(write(t, body))
		if err != nil {
			t.Fatalf("role %q: %v", string(r), err)
		}
		if c.Identity.Role != r {
			t.Errorf("role = %q, want %q", string(c.Identity.Role), string(r))
		}
	}
	if !strings.Contains(RoleList(), "issuer, core, platform") {
		t.Errorf("the vocabulary prints in layer order: %s", RoleList())
	}
}
