// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blocks writes one directory per repository, each with its block, and the
// issuer's client registry.
func blocks(t *testing.T, repos map[string]string, clients string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range repos {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(p, ".lateregate.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if clients != "" {
		p := filepath.Join(dir, "auth", "deploy", "base")
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "clients.yaml"), []byte(clients), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const issuerBlock = "identity:\n  role: issuer\n"

const coreBlock = "identity:\n  role: core\n  audience: sandboxd\n  config_prefix: CELLA\n  api_group: cella.latere.ai\n"

const serviceBlock = "identity:\n  role: service\n  audience: drive\n"

const clientBlock = "identity:\n  role: client\n  audiences: [sandboxd, drive]\n"

const noneBlock = "identity:\n  role: none\n"

const registryFile = `clients:
  - client_id: latere-cli
    client_type: public
    allowed_audiences: [$ISSUER, sandboxd]
    actor_audiences: [drive]
`

// shape is the family in shape: every repository declares a role, every
// audience is registered once and verified once, and the client presents
// audiences that exist.
func shape() map[string]string {
	return map[string]string{
		"auth":  issuerBlock,
		"cella": coreBlock,
		"cli":   clientBlock,
		"drive": serviceBlock,
		"pkg":   noneBlock,
	}
}

func family(t *testing.T, dir, expect string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Family(dir, expect, &sb)
	return sb.String(), err
}

const wantTable = `| Role | Repositories | Audience |
|---|---|---|
| issuer | auth |  |
| core | cella | sandboxd |
| service | drive | drive |
| client | cli | sandboxd, drive |
| none | pkg |  |
`

func TestIdentityFamilyInShape(t *testing.T) {
	out, err := family(t, blocks(t, shape(), registryFile), "")
	if err != nil {
		t.Fatalf("a family in shape passes: %v\n%s", err, out)
	}
	if !strings.Contains(out, wantTable) {
		t.Errorf("the layer table is derived from the blocks:\n%s", out)
	}
}

// Each way the family leaves the shape, and the finding that names it.
func TestIdentityFamily(t *testing.T) {
	cases := []struct {
		name     string
		repos    func(map[string]string) map[string]string
		clients  string
		contains string
	}{{
		name:     "a repository with no block",
		repos:    func(r map[string]string) map[string]string { r["lectio"] = ""; return r },
		contains: "lectio carries no identity block",
	}, {
		name: "an audience the registry does not list",
		repos: func(r map[string]string) map[string]string {
			r["lux"] = "identity:\n  role: core\n  audience: lux\n  config_prefix: LUX\n  api_group: lux.latere.ai\n"
			return r
		},
		contains: `lux verifies the audience "lux" and the registry does not list it`,
	}, {
		name:     "an audience nobody verifies",
		clients:  strings.Replace(registryFile, "[$ISSUER, sandboxd]", "[$ISSUER, sandboxd, toposd]", 1),
		contains: `the registry lists the audience "toposd" and no repository verifies it`,
	}, {
		name: "two repositories claiming one audience",
		repos: func(r map[string]string) map[string]string {
			r["sandbox"] = coreBlock
			return r
		},
		contains: `cella and sandbox both verify the audience "sandboxd"`,
	}, {
		name: "a client presenting an audience nobody verifies",
		repos: func(r map[string]string) map[string]string {
			r["cli"] = "identity:\n  role: client\n  audiences: [sandboxd, drive, toposd]\n"
			return r
		},
		contains: `cli presents the audience "toposd" and no repository verifies it`,
	}, {
		name: "no repository declares the issuer",
		repos: func(r map[string]string) map[string]string {
			delete(r, "auth")
			return r
		},
		contains: "no repository declares the issuer role",
	}, {
		name: "a block the loader rejects",
		repos: func(r map[string]string) map[string]string {
			r["eval"] = "identity:\n  role: gateway\n"
			return r
		},
		contains: "eval: its file could not be read",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repos := shape()
			if c.repos != nil {
				repos = c.repos(repos)
			}
			clients := registryFile
			if c.clients != "" {
				clients = c.clients
			}
			out, err := family(t, blocks(t, repos, clients), "")
			if err == nil {
				t.Fatalf("the family check must fail:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.contains) {
				t.Errorf("the finding must say %q:\n%v", c.contains, err)
			}
		})
	}
}

// The registry the issuer cannot be read from is a check that measured
// nothing, which fails rather than passing.
func TestIdentityFamilyNeedsTheRegistry(t *testing.T) {
	out, err := family(t, blocks(t, shape(), ""), "")
	if err == nil {
		t.Fatalf("a registry that is not there is not an empty one:\n%s", out)
	}
	if !strings.Contains(err.Error(), "client registry could not be read") {
		t.Errorf("the finding names what could not be read:\n%v", err)
	}
}

func TestIdentityFamilyNeedsRepositories(t *testing.T) {
	_, err := family(t, t.TempDir(), "")
	if err == nil {
		t.Fatal("a family check over nothing would pass over nothing")
	}
	if _, err := family(t, filepath.Join(t.TempDir(), "gone"), ""); err == nil {
		t.Error("a directory that is not there is an error, not an empty family")
	}
}

// The committed layer table is derived from the tree: the check fails when
// the document and the blocks disagree.
func TestIdentityFamilyDerivesTheCommittedTable(t *testing.T) {
	dir := blocks(t, shape(), registryFile)
	committed := filepath.Join(t.TempDir(), "layers.md")
	if err := os.WriteFile(committed, []byte(wantTable), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := family(t, dir, committed)
	if err != nil {
		t.Fatalf("the committed table matches the blocks: %v\n%s", err, out)
	}

	stale := strings.Replace(wantTable, "| core | cella | sandboxd |", "| core | cella, lux | sandboxd |", 1)
	if err := os.WriteFile(committed, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = family(t, dir, committed)
	if err == nil {
		t.Fatal("a committed table that differs from the blocks must fail")
	}
	if !strings.Contains(err.Error(), "at line 4") {
		t.Errorf("the finding names the line that differs:\n%v", err)
	}

	if _, err := family(t, dir, filepath.Join(t.TempDir(), "gone.md")); err == nil {
		t.Error("a committed table that is not there is a finding")
	}
}
