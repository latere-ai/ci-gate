// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

const goodShip = `ship:
  image:
    name: ghcr.io/latere-ai/auth
  deploy:
    kubeconfig: op://LatereAI/auth rollout kubeconfig/kubeconfig
    namespace: latere
    deployment: auth
    url: https://auth.latere.ai
`

func TestShipDefaultsAndPresence(t *testing.T) {
	c, err := Load(write(t, goodShip))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ships() {
		t.Fatal("a ship block must make the repository ship from a workstation")
	}
	i, d := c.Ship.Image, c.Ship.Deploy
	if i.Dockerfile != "Dockerfile" || i.Context != "." || i.Platform != "linux/amd64" || i.Arch() != "amd64" {
		t.Errorf("image defaults: %+v", i)
	}
	if d.RolloutTimeout != "300s" || d.Smoke != "tools/smoke/release.sh" || d.ContainerOr() != "auth" {
		t.Errorf("deploy defaults: %+v", d)
	}
	none, err := Load(write(t, "identity:\n  role: none\n"))
	if err != nil {
		t.Fatal(err)
	}
	if none.Ships() {
		t.Error("no ship block must not ship")
	}
}

func TestShipRestatedDefaultsAreReported(t *testing.T) {
	c, err := Load(write(t, goodShip+"    smoke: tools/smoke/release.sh\n    rollout_timeout: 300s\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ship.deploy.smoke", "ship.deploy.rollout_timeout"} {
		if !slices.Contains(c.Restated, want) {
			t.Errorf("%s restates its default and is not reported: %v", want, c.Restated)
		}
	}
}

func TestShipArch(t *testing.T) {
	if got := (ShipImage{Platform: "linux/arm/v7"}).Arch(); got != "arm" {
		t.Errorf("arch = %q", got)
	}
}

func TestShipValidation(t *testing.T) {
	good := map[string]string{
		"name": "ghcr.io/latere-ai/auth", "kc": "op://LatereAI/auth rollout kubeconfig/kubeconfig",
		"ns": "latere", "dep": "auth", "url": "https://auth.latere.ai",
	}
	build := func(over map[string]string, extra string) string {
		m := map[string]string{}
		maps.Copy(m, good)
		maps.Copy(m, over)
		var b strings.Builder
		b.WriteString("ship:\n  image:\n")
		if m["name"] != "-" {
			b.WriteString("    name: " + m["name"] + "\n")
		}
		b.WriteString(extra)
		b.WriteString("  deploy:\n")
		for _, kv := range [][2]string{{"kubeconfig", "kc"}, {"namespace", "ns"}, {"deployment", "dep"}, {"url", "url"}} {
			if m[kv[1]] != "-" {
				b.WriteString("    " + kv[0] + ": " + m[kv[1]] + "\n")
			}
		}
		return b.String()
	}
	cases := []struct {
		name  string
		over  map[string]string
		extra string
		want  string
	}{
		{"missing name", map[string]string{"name": "-"}, "    context: .\n", "ship.image.name is required"},
		{"name with a tag", map[string]string{"name": "ghcr.io/latere-ai/auth:v1"}, "", "no tag or digest"},
		{"missing namespace", map[string]string{"ns": "-"}, "", "ship.deploy.namespace is required"},
		{"missing deployment", map[string]string{"dep": "-"}, "", "ship.deploy.deployment is required"},
		{"missing url", map[string]string{"url": "-"}, "", "ship.deploy.url is required"},
		{"http url", map[string]string{"url": "http://auth.latere.ai"}, "", "must be an https address"},
		{"missing kubeconfig", map[string]string{"kc": "-"}, "", "ship.deploy.kubeconfig is required"},
		{"kubeconfig not a reference", map[string]string{"kc": "/Users/me/.kube/config"}, "", "starting with op://"},
		{"short reference", map[string]string{"kc": "op://vault/item"}, "", "vault, an item and a file"},
		{"bad platform", nil, "    platform: windows/amd64\n", "not a Linux platform"},
		{"bad arch", nil, "    platform: linux/vax\n", "not a Linux platform"},
		{"version arg clash", nil, "    version_arg: VERSION\n    build_args: {VERSION: x}\n", "already fills"},
		{"bad version arg", nil, "    version_arg: 1-bad\n", "not a build argument name"},
		{"escaping dockerfile", nil, "    dockerfile: ../Dockerfile\n", "inside the repository"},
		{"empty prepare", nil, "    prepare: ['']\n", "empty command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, build(tc.over, tc.extra)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want it to say %q", err, tc.want)
			}
		})
	}
	if _, err := Load(write(t, build(nil, "    platform: linux/arm64\n    version_arg: VERSION\n    build_args: {X: y}\n    prepare: [make bin]\n"))); err != nil {
		t.Fatalf("a good block failed: %v", err)
	}
	for _, bad := range []string{"0s", "soon", "-5s"} {
		body := goodShip + "    rollout_timeout: " + bad + "\n"
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), "rollout_timeout") {
			t.Errorf("timeout %q: %v", bad, err)
		}
	}
}

func TestEmptyShipBlockShipsAndPublishesNothing(t *testing.T) {
	c, err := Load(write(t, "ship: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ships() {
		t.Fatal("`ship: {}` declares the workstation flow")
	}
	if c.Ship.Builds() || c.Ship.Deploys() {
		t.Errorf("an empty block publishes and deploys nothing: %+v", c.Ship)
	}
	if len(c.Restated) != 0 {
		t.Errorf("nothing was restated: %v", c.Restated)
	}
}

func TestShipWithNoValueIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"ship":   "ship:\n",
		"image":  "ship:\n  image:\n",
		"deploy": "ship:\n  deploy:\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, body))
			if err == nil || !strings.Contains(err.Error(), "has no value") {
				t.Fatalf("error %v, want it to say the key has no value", err)
			}
		})
	}
}

func TestShipDeployNeedsAnImage(t *testing.T) {
	body := "ship:\n  deploy:\n    kubeconfig: op://V/I/F\n    namespace: n\n    deployment: x\n    url: https://x.example\n"
	if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), "nothing to deploy") {
		t.Fatalf("error: %v", err)
	}
}

func TestShipImageAloneIsValid(t *testing.T) {
	c, err := Load(write(t, "ship:\n  image:\n    name: ghcr.io/latere-ai/x\n    context: .\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ship.Builds() || c.Ship.Deploys() || c.Ship.Image.Dockerfile != "Dockerfile" {
		t.Errorf("image-only block: %+v", c.Ship)
	}
	if !slices.Contains(c.Restated, "ship.image.context") {
		t.Errorf("a restated image default is reported: %v", c.Restated)
	}
	for _, k := range c.Restated {
		if strings.HasPrefix(k, "ship.deploy.") {
			t.Errorf("%s is reported for a deploy block that is absent", k)
		}
	}
}
