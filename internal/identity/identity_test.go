// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/registers"
)

// repo writes a tree at a fresh root and returns the root. Every tree has a
// go.mod, because a finding about the whole repository names it.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"go.mod": "module example.com/app\n\ngo 1.27\n"}
	maps.Copy(all, files)
	for rel, body := range all {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// noHistory answers the one-way check as a tree that never set the key. No
// test shells out to git: what the history says is an input to the rule.
func noHistory() gates.Exec {
	return func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) { return nil, nil }
}

// hadRolesOnly answers as a tree whose history set the key.
func hadRolesOnly() gates.Exec {
	return func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) { return []byte("9f1c2ab\n"), nil }
}

func run(t *testing.T, cfg config.Identity, root string, exec gates.Exec) (string, error) {
	t.Helper()
	cfg.Present = true
	var sb strings.Builder
	err := Run(cfg, root, &sb, exec, today)
	return sb.String(), err
}

// today is the clock every test runs on, so a waiver's date means one thing.
var today = time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

// A repository with no block fails, and the failure names the key and every
// role, because a repository outside the shape by omission is the gap.
func TestIdentityBlockIsRequired(t *testing.T) {
	var sb strings.Builder
	err := Run(config.Identity{}, repo(t, nil), &sb, noHistory(), today)
	if err == nil {
		t.Fatal("a repository with no block must fail")
	}
	if !strings.Contains(err.Error(), "identity.role") {
		t.Errorf("the failure names the key:\n%v", err)
	}
	for _, role := range config.Roles {
		if !strings.Contains(err.Error(), string(role)) {
			t.Errorf("the failure names the role %q:\n%v", string(role), err)
		}
	}
}

// none is a declared role. It runs no rule and says which ones it did not
// run, so the report is never silent about what was not checked.
func TestRoleNonePrintsWhatItSkipped(t *testing.T) {
	out, err := run(t, config.Identity{Role: config.RoleNone}, repo(t, map[string]string{
		"docs/auth.md": "The identity fabric mints a delegated token.\n",
	}), noHistory())
	if err != nil {
		t.Fatalf("a tool with no identity surface passes: %v\n%s", err, out)
	}
	for _, name := range Names() {
		if !strings.Contains(out, name) {
			t.Errorf("the report names the rule %q it did not run:\n%s", name, out)
		}
	}
}

// Each rule fails a tree that violates it and passes the same tree with the
// violation removed. The pass has to be a PASS and not a SKIP: a rule with
// nothing to read proves nothing about the rule.
func TestIdentityRules(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"}
	service := config.Identity{Role: config.RoleService, Audience: "drive"}
	client := config.Identity{Role: config.RoleClient, Audiences: []string{"origo", "sandboxd"}}
	settledService := service
	settledService.Settled = []string{"complete"}

	for _, c := range ruleCases(core, service, client, settledService) {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, c.cfg, repo(t, c.bad), noHistory())
			if err == nil {
				t.Fatalf("the violation must fail the %s rule:\n%s", c.rule, out)
			}
			if !strings.Contains(out, "FAIL "+c.rule) {
				t.Fatalf("the %s rule must be the one that failed:\n%s", c.rule, out)
			}
			out, err = run(t, c.cfg, repo(t, c.good), noHistory())
			if !strings.Contains(out, "PASS "+c.rule) {
				t.Fatalf("the same tree without the violation must pass the %s rule (%v):\n%s", c.rule, err, out)
			}
		})
	}
}

type ruleCase struct {
	name string
	rule string
	cfg  config.Identity
	bad  map[string]string
	good map[string]string
}

const claimsFile = `package token

import "latere.ai/x/pkg/authkit/jwt"

// Claims is what the verifier returns.
type Claims struct {
	Sub string ` + "`json:\"sub\"`" + `
	Exp int64  ` + "`json:\"exp\"`" + `
	%s
}

var _ = jwt.Verify
`

const eventsFile = `package api

import "latere.ai/x/pkg/authkit/jwt"

// event is one audit row as the API renders it.
type event struct {
	ID      int64  ` + "`json:\"id\"`" + `
	ActorID string ` + "`json:\"actor_id\"`" + `
}

var _ = jwt.Verify
`

const storeFile = `package store

// Row is one attribution record.
type Row struct {
	ID      string ` + "`json:\"id\"`" + `
	AgentID string ` + "`json:\"agent_id\"`" + `
}
`

func ruleCases(core, service, client, settledService config.Identity) []ruleCase {
	return []ruleCase{{
		name: "a core reads no claim for meaning",
		rule: "claims", cfg: core,
		bad: map[string]string{"internal/api/h.go": "package api\n\ntype Decision struct{ OrgID string }\n\n" +
			"func Allow(d Decision) bool { return d.OrgID != \"\" }\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\ntype Decision struct{ Subject string }\n\n" +
			"func Allow(d Decision) bool { return d.Subject != \"\" }\n"},
	}, {
		name: "a core reads no claim named as a string",
		rule: "claims", cfg: core,
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar key = \"is_superadmin\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nvar key = \"subject\"\n"},
	}, {
		name: "one verifier, and no second token library",
		rule: "verifier", cfg: service,
		bad: map[string]string{"internal/auth/v.go": "package auth\n\nimport _ \"github.com/ory/fosite\"\n" +
			"import _ \"latere.ai/x/pkg/authkit/jwt\"\n"},
		good: map[string]string{"internal/auth/v.go": "package auth\n\nimport _ \"latere.ai/x/pkg/authkit/jwt\"\n"},
	}, {
		name: "one verifier, and no token taken apart by hand",
		rule: "verifier", cfg: service,
		bad: map[string]string{"internal/auth/v.go": "package auth\n\nimport (\n\t\"encoding/base64\"\n\t\"strings\"\n\n" +
			"\t_ \"latere.ai/x/pkg/authkit/jwt\"\n)\n\n" +
			"func Read(tok string) []byte {\n\tparts := strings.Split(tok, \".\")\n\t" +
			"b, _ := base64.RawURLEncoding.DecodeString(parts[1])\n\treturn b\n}\n"},
		good: map[string]string{"internal/auth/v.go": "package auth\n\nimport (\n\t\"encoding/base64\"\n\n" +
			"\t_ \"latere.ai/x/pkg/authkit/jwt\"\n)\n\n" +
			"func Read(seed string) []byte {\n\tb, _ := base64.RawURLEncoding.DecodeString(seed)\n\treturn b\n}\n"},
	}, {
		name: "one contract, asked through the shared client",
		rule: "authorizer", cfg: core,
		bad: map[string]string{"internal/ask/ask.go": "package ask\n\nimport \"net/http\"\n\n" +
			"func Ask() (*http.Request, error) {\n\treturn http.NewRequest(http.MethodPost, \"/internal/authorize\", nil)\n}\n"},
		good: map[string]string{"internal/ask/ask.go": "package ask\n\nimport _ \"latere.ai/x/pkg/authz\"\n"},
	}, {
		// An import path is a string in the grammar and a dependency in the
		// file. A conformance case that posts and imports a test double
		// whose path holds the word asks nothing by hand.
		name: "an import path is not a hand-rolled ask",
		rule: "authorizer", cfg: core,
		bad: map[string]string{
			"internal/ask/ask.go": "package ask\n\nimport _ \"latere.ai/x/pkg/authz\"\n",
			"test/conformance/cases.go": "package conformance\n\nimport \"net/http\"\n\n" +
				"func Ask() (*http.Request, error) {\n\treturn http.NewRequest(http.MethodPost, \"/internal/authorize\", nil)\n}\n",
		},
		good: map[string]string{
			"internal/ask/ask.go": "package ask\n\nimport _ \"latere.ai/x/pkg/authz\"\n",
			"test/conformance/cases.go": "package conformance\n\nimport (\n\t\"net/http\"\n\n" +
				"\t_ \"example.com/app/test/stubs/authorizer\"\n)\n\n" +
				"func Probe() (*http.Request, error) {\n\treturn http.NewRequest(http.MethodPost, \"/healthz\", nil)\n}\n",
		},
	}, {
		// The same decision for the claims rule: a package whose path holds
		// a membership claim is imported, not read for meaning.
		name: "an import path is not a claim read for meaning",
		rule: "claims", cfg: core,
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar key = \"roles\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nimport _ \"example.com/app/internal/roles\"\n"},
	}, {
		// The authorizer rule catches a repository that asks in a shape of
		// its own; this one catches a repository that writes the wire shape
		// out a second time, whichever half it writes.
		name: "the envelope is declared once, not restated",
		rule: "envelope", cfg: service,
		bad:  map[string]string{"internal/ask/wire.go": askWire},
		good: map[string]string{"internal/ask/wire.go": nearWire},
	}, {
		name: "the decision is declared once, not restated",
		rule: "envelope", cfg: service,
		bad:  map[string]string{"internal/ask/wire.go": answerWire},
		good: map[string]string{"internal/ask/wire.go": nearWire},
	}, {
		name: "no issuer call on a request path",
		rule: "request-path", cfg: service,
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar teams = \"/tokeninfo\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nvar health = \"/healthz\"\n"},
	}, {
		name: "no issuer call for the members of an organization",
		rule: "request-path", cfg: service,
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar members = \"/orgs/%s/members\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nvar members = \"/teams/%s\"\n"},
	}, {
		name: "no delegation vocabulary in a document",
		rule: "delegation", cfg: service,
		bad:  map[string]string{"docs/auth.md": "The exchange follows RFC 8693.\n"},
		good: map[string]string{"docs/auth.md": "The service verifies one token.\n"},
	}, {
		// A changelog, a release note and a finished spec record what was
		// once true and may name what they retired; an open spec may not.
		name: "a record may name the mechanism it retired, an open spec may not",
		rule: "delegation", cfg: settledService,
		bad: map[string]string{
			"specs/009-open.md": "---\ntitle: x\nstatus: drafted\n---\n\nThe exchange follows RFC 8693.\n",
		},
		good: map[string]string{
			"internal/a/a.go":               "package a\n",
			"CHANGELOG.md":                  "## v1\n\n- Removed the RFC 8693 exchange.\n",
			"docs/release-notes-v0.17.0.md": "Agent tokens carry grantor_id.\n",
			"specs/008-removal.md":          "---\ntitle: x\nstatus: complete\n---\n\nRemoved the RFC 8693 exchange.\n",
			"specs/.archive/001-old.md":     "The exchange follows RFC 8693.\n",
		},
	}, {
		name: "a delegation claim beside the registered claims",
		rule: "delegation", cfg: service,
		bad:  map[string]string{"internal/token/claims.go": fmtClaims("Act string `json:\"act\"`")},
		good: map[string]string{"internal/token/claims.go": fmtClaims("Email string `json:\"email\"`")},
	}, {
		// The family's decision lets one product carry an agent identity as
		// attribution. The column is not a claim, and the same tag beside the
		// registered claims is.
		name: "an attribution column is not a claim",
		rule: "delegation", cfg: service,
		bad:  map[string]string{"internal/token/claims.go": fmtClaims("AgentID string `json:\"agent_id\"`")},
		good: map[string]string{"internal/store/row.go": storeFile},
	}, {
		// A handler that verifies tokens also renders its audit rows, and
		// the row's actor column is not a claim because the file imports the
		// verifier; the same tag beside sub and exp still is.
		name: "an attribution column in a file that verifies tokens is not a claim",
		rule: "delegation", cfg: service,
		bad:  map[string]string{"internal/api/claims.go": fmtClaims("ActorID string `json:\"actor_id\"`")},
		good: map[string]string{"internal/api/events.go": eventsFile},
	}, {
		name: "access by role, not by a flag",
		rule: "roles", cfg: withRolesOnly(service),
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar admin = \"is_superadmin\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nvar admin = \"platform_admin\"\n"},
	}, {
		// A deployment is a base and its overlays: a container missing the
		// variable in the base passes when an overlay sets it, and a service
		// may spell the variable AUTH_AUDIENCES.
		name: "an audience set in an overlay counts for the base, under either spelling",
		rule: "audience", cfg: service,
		bad: builds(map[string]string{
			"deploy/base/app.yaml": deployment(""),
			"deploy/prod/app.yaml": deployment(""),
		}),
		good: builds(map[string]string{
			"deploy/base/app.yaml": deployment(""),
			"deploy/prod/app.yaml": deployment("        - name: AUTH_AUDIENCES\n          value: drive\n"),
		}),
	}, {
		// A repository whose only main package is the module root builds
		// one binary, named after the module. The rule reads the container
		// that runs it, so a root command is held like a cmd/ one.
		name: "a command at the module root is a command this repository builds",
		rule: "audience", cfg: service,
		bad: rootBuilds(map[string]string{"deploy/base/app.yaml": deployment("")}),
		good: rootBuilds(map[string]string{
			"deploy/base/app.yaml": deployment("        - name: AUTH_AUDIENCE\n          value: drive\n"),
		}),
	}, {
		// An image built under a third name is declared, because a name a
		// rule inferred is a rule that stops checking as soon as it infers
		// wrong.
		name: "a declared image is the container this repository runs",
		rule: "audience", cfg: withImage(service, "appd"),
		bad: rootBuilds(map[string]string{"deploy/base/app.yaml": deploymentImage("registry.example.com/appd:1", "")}),
		good: rootBuilds(map[string]string{
			"deploy/base/app.yaml": deploymentImage("registry.example.com/appd:1",
				"        - name: AUTH_AUDIENCE\n          value: drive\n"),
		}),
	}, {
		name: "an explicit audience in every deployment",
		rule: "audience", cfg: core,
		bad:  builds(map[string]string{"deploy/base/app.yaml": deployment("")}),
		good: builds(map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_AUDIENCE\n          value: cella\n")}),
	}, {
		name: "the audience is a name, not the address of the issuer",
		rule: "audience", cfg: core,
		bad:  builds(map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_AUDIENCE\n          value: https://auth.example.com\n")}),
		good: builds(map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_AUDIENCE\n          value: cella\n")}),
	}, {
		// The family accepts two audiences at an open core, its own name and
		// the origin: a script holding a platform key calls the origin
		// directly and the core it reaches asks the authorizer.
		name: "the hosted overlay names this core's name and the origin",
		rule: "core-audiences", cfg: withOverlay(core),
		bad: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(publicURLEnv),
		}),
		good: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("cella,api.latere.ai")),
		}),
	}, {
		name: "two audiences and no third",
		rule: "core-audiences", cfg: withOverlay(core),
		bad: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("cella,api.latere.ai,drive")),
		}),
		good: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("cella,api.latere.ai")),
		}),
	}, {
		name: "the two audiences are two names, not one written twice",
		rule: "core-audiences", cfg: withOverlay(core),
		bad: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("cella,cella")),
		}),
		good: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("cella,api.latere.ai")),
		}),
	}, {
		// A hosted plane's own name is one the origin retires by a cutover.
		// Neither it nor any other third name is one of the two.
		name: "a hosted plane's name is neither of the two",
		rule: "core-audiences", cfg: withOverlay(core),
		bad: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("lux.latere.ai,api.latere.ai")),
		}),
		good: builds(map[string]string{
			"deploy/base/app.yaml": deployment(audienceEnv("cella")),
			"deploy/prod/app.yaml": patchFile(audienceEnv("cella,api.latere.ai")),
		}),
	}, {
		name: "a credential per endpoint",
		rule: "bearers", cfg: core,
		bad: map[string]string{"deploy/base/app.yaml": deployment(
			secretEnv("CLONE_TOKEN", "operator", "bearer") + secretEnv("KEYS_TOKEN", "operator", "bearer"))},
		good: map[string]string{"deploy/base/app.yaml": deployment(
			secretEnv("CLONE_TOKEN", "operator", "clone") + secretEnv("KEYS_TOKEN", "operator", "keys"))},
	}, {
		name: "an internal route stays inside the cluster",
		rule: "bearers", cfg: core,
		bad:  map[string]string{"deploy/base/ingress.yaml": ingress("/")},
		good: map[string]string{"deploy/base/ingress.yaml": ingress("/api/")},
	}, {
		name: "no value of one company in a core",
		rule: "no-latere-value", cfg: core,
		bad:  map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.latere.ai\n")},
		good: map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.example.com\n")},
	}, {
		// A core writes its own manifests under its own group, which is a
		// name the block declares rather than one the rule guesses at.
		// The module namespace and a contact address are the project's own
		// coordinates, wherever they appear; a hostname in a URL is not.
		name: "module paths and contact addresses are coordinates, a hostname is a value",
		rule: "no-latere-value", cfg: core,
		bad: map[string]string{"docs/install.md": "Clone https://code.latere.ai/acme/app.git to begin.\n"},
		good: map[string]string{"docs/contributing.md": "Import `latere.ai/x/pkg/httpjson`; report issues to security@latere.ai.\n" +
			"Build with -X latere.ai/x/cella/internal/version.Version=dev.\n"},
	}, {
		name: "the core's own group is not a company value",
		rule: "no-latere-value", cfg: core,
		bad:  map[string]string{"docs/pools.md": "A pool is `lux.latere.ai/v1beta1`.\n"},
		good: map[string]string{"docs/pools.md": "A pool is `cella.latere.ai/v1beta1`.\n"},
	}, {
		// The spec tree is the contributor's record and may name the hosted
		// deployment the core came from; a user document may not.
		name: "a spec may name the hosted deployment, a user document may not",
		rule: "no-latere-value", cfg: core,
		bad: map[string]string{
			"docs/install.md":           "The reference installation is https://code.latere.ai.\n",
			"specs/001-architecture.md": "The reference installation is https://code.latere.ai.\n",
		},
		good: map[string]string{
			"docs/install.md":           "Install cellad from the release archive.\n",
			"specs/001-architecture.md": "The reference installation is https://code.latere.ai.\n",
		},
	}, {
		name: "one audience per product in a client",
		rule: "client-audiences", cfg: client,
		bad: map[string]string{
			"internal/git/git.go":   "package git\n\nvar aud = \"origo\"\nvar box = \"sandboxd\"\n",
			"internal/shell/sh.go":  "package shell\n\nvar aud = \"origo\"\n",
			"internal/token/tok.go": "package token\n\nvar _ = \"none\"\n",
		},
		good: map[string]string{
			"internal/git/git.go":  "package git\n\nvar aud = \"origo\"\n",
			"internal/shell/sh.go": "package shell\n\nvar aud = \"sandboxd\"\n",
		},
	}, {
		name: "a client mints for the audiences it declares",
		rule: "client-audiences", cfg: client,
		bad: map[string]string{
			"internal/git/git.go": "package git\n\nvar aud = \"origo\"\n",
		},
		good: map[string]string{
			"internal/git/git.go":  "package git\n\nvar aud = \"origo\"\n",
			"internal/shell/sh.go": "package shell\n\nvar aud = \"sandboxd\"\n",
		},
	}, {
		name: "a live document describes the generation that exists",
		rule: "documents", cfg: service,
		bad:  map[string]string{"README.md": "Sign in with pkg/oidclogin.\n"},
		good: map[string]string{"README.md": "Sign in with the shared client.\n"},
	}}
}

// askWire is the authorizer's question written out a second time.
const askWire = `package ask

type request struct {
	Subject  string ` + "`json:\"subject\"`" + `
	Action   string ` + "`json:\"action\"`" + `
	Resource string ` + "`json:\"resource\"`" + `
}
`

// answerWire is the authorizer's decision written out a second time.
const answerWire = `package ask

type decision struct {
	Allow  bool   ` + "`json:\"allow\"`" + `
	Reason string ` + "`json:\"reason\"`" + `
	TTL    int    ` + "`json:\"ttl\"`" + `
}
`

// nearWire is every shape that shares letters with the envelope and is not
// it: the page a list action answers with, which carries no ttl because a
// page is not a verdict; a core's limits, whose ceilings are its own; and a
// request that dispatches on an action and names neither who asks nor what
// about.
const nearWire = `package ask

type refusal struct {
	Allow  bool   ` + "`json:\"allow\"`" + `
	Reason string ` + "`json:\"reason\"`" + `
}

type limits struct {
	RequestsPerMinute int ` + "`json:\"requests_per_minute\"`" + `
	TTLSeconds        int ` + "`json:\"ttl_seconds\"`" + `
}

type dispatch struct {
	Action       string   ` + "`json:\"action\"`" + `
	Actions      []string ` + "`json:\"actions\"`" + `
	Reasons      []string ` + "`json:\"reasons\"`" + `
	AllowedHosts []string ` + "`json:\"allowed_hosts\"`" + `
}
`

// verifying is the smallest file that holds the one-verifier rule, so a test
// of another rule is not also a test of that one.
const verifying = "package api\n\nimport _ \"latere.ai/x/pkg/authkit/jwt\"\n"

func withRolesOnly(cfg config.Identity) config.Identity {
	cfg.RolesOnly = true
	return cfg
}

func withImage(cfg config.Identity, image string) config.Identity {
	cfg.Image = image
	return cfg
}

// withOverlay declares the directory the hosted installation deploys from,
// which is what the core-audiences rule reads.
func withOverlay(cfg config.Identity) config.Identity {
	cfg.Overlays = []string{"deploy/prod"}
	return cfg
}

// audienceEnv is the audience entry of a core under the fixture's prefix.
func audienceEnv(value string) string {
	return "        - name: CELLA_OIDC_AUDIENCE\n          value: " + value + "\n"
}

// publicURLEnv is an overlay patching something other than the audience,
// which is the shape of an overlay that leaves the audience to the base.
const publicURLEnv = "        - name: CELLA_PUBLIC_URL\n          value: https://cella.example.com\n"

// authorizerEnv is a second entry an overlay patches, for the fixture where
// two files of one overlay patch one deployment.
const authorizerEnv = "        - name: CELLA_AUTHORIZER_URL\n" +
	"          value: http://authz-internal.example.svc/internal/cella/authorize\n"

// patchFile renders an overlay's patch of the deployment fixture's workload:
// the same workload and container name, carrying no image, which is what
// kustomize merges into the base.
func patchFile(env string) string { return workloadDoc("app", "", env) }

// workloadDoc renders one deployment document under a name of its own. An
// empty image is a patch of a container declared elsewhere.
func workloadDoc(name, image, env string) string {
	body := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name +
		"\nspec:\n  template:\n    spec:\n      containers:\n      - name: app\n"
	if image != "" {
		body += "        image: " + image + "\n"
	}
	if env != "" {
		body += "        env:\n" + env
	}
	return body
}

// twoWorkloads is a server and a reaper built from one image: one container
// name under two workload names, each configured on its own.
func twoWorkloads(env string) string {
	return workloadDoc("app", "registry.example.com/app:1", env) + "---\n" +
		workloadDoc("app-reaper", "registry.example.com/app:1", env)
}

// twoPatches patches both of them, the way one file of an overlay does.
func twoPatches(env string) string {
	return workloadDoc("app", "", env) + "---\n" + workloadDoc("app-reaper", "", env)
}

func fmtClaims(field string) string { return strings.Replace(claimsFile, "%s", field, 1) }

// builds adds the command the deployment fixtures' image names, so the
// audience rule reads that container as this repository's workload. It
// imports the verifier, because a tree with a Go file and no verifier is a
// finding of another rule and not of the one under test.
func builds(files map[string]string) map[string]string {
	files["cmd/app/main.go"] = mainFile
	return files
}

// mainFile is a command that imports the verifier and does nothing else.
const mainFile = "package main\n\nimport _ \"latere.ai/x/pkg/authkit/jwt\"\n\nfunc main() {}\n"

// rootBuilds puts the command at the module root rather than under cmd/,
// which is the shape of a repository whose only main package is the root:
// it builds one binary, named after the module, and has no cmd/ to read.
func rootBuilds(files map[string]string) map[string]string {
	files["main.go"] = mainFile
	return files
}

// deployment renders a one-container workload with the given env entries.
func deployment(env string) string { return deploymentImage("registry.example.com/app:1", env) }

// deploymentImage renders the same workload under a named image, for the
// repository whose image was built under neither a command name nor its own.
func deploymentImage(image, env string) string {
	body := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
      - name: app
        image: ` + image + `
`
	if env != "" {
		body += "        env:\n" + env
	}
	return body
}

func secretEnv(name, secret, key string) string {
	return "        - name: " + name + "\n          valueFrom:\n            secretKeyRef:\n" +
		"              name: " + secret + "\n              key: " + key + "\n"
}

func ingress(public string) string {
	return `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app
spec:
  rules:
  - host: app.example.com
    http:
      paths:
      - path: ` + public + `
        pathType: Prefix
      - path: /internal/hook
        pathType: Prefix
`
}

// A rule with nothing to read says so. Nothing here passes vacuously, which
// is the decision every gate in this repository is built on.
func TestIdentityRuleSkipsAreExplained(t *testing.T) {
	empty := repo(t, nil)
	cfgs := map[config.Role]config.Identity{
		config.RoleIssuer:   {Role: config.RoleIssuer},
		config.RoleCore:     {Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"},
		config.RolePlatform: {Role: config.RolePlatform, Audience: "platformd"},
		config.RoleService:  {Role: config.RoleService, Audience: "drive"},
		config.RoleBFF:      {Role: config.RoleBFF},
		config.RoleClient:   {Role: config.RoleClient},
	}
	seen := map[string]bool{}
	for role, cfg := range cfgs {
		out, err := run(t, cfg, empty, noHistory())
		if err != nil {
			t.Fatalf("role %s over an empty tree has nothing to find: %v\n%s", string(role), err, out)
		}
		for _, r := range rules {
			if !applies(r, role) {
				continue
			}
			seen[r.name] = true
			line := ruleLine(out, r.name)
			if !strings.HasPrefix(line, "SKIP") {
				t.Errorf("role %s, rule %s: a rule with nothing to read reports why, not a pass:\n%s",
					string(role), r.name, out)
			}
			if len(strings.Fields(line)) < 3 {
				t.Errorf("role %s, rule %s: the skip carries its reason:\n%s", string(role), r.name, line)
			}
		}
	}
	for _, r := range rules {
		if !seen[r.name] {
			t.Errorf("rule %s reached no role, so its skip was never read", r.name)
		}
	}
}

func applies(r rule, role config.Role) bool { return slices.Contains(r.roles, role) }

// frontendTree is one repository holding every shape the roles rule must tell
// apart in a browser tree. Only the two decision files are findings.
func frontendTree() map[string]string {
	return map[string]string{
		// The verifier keeps the other rules of this role quiet, so the tree
		// fails on the frontend or on nothing.
		"internal/api/h.go": verifying + "\nvar admin = \"platform_admin\"\n",

		// The decisions. A page that branches on the retired flag, and the
		// camel case the same field takes in a single-file component.
		"web/src/Nav.tsx":   "export function Nav(p: Principal) {\n  if (p.is_superadmin) return admin;\n  return null;\n}\n",
		"web/src/Menu.vue":  "<script setup lang=\"ts\">\nconst admin = props.isSuperadmin;\n</script>\n",
		"web/src/Badge.jsx": "export const Badge = (p) => (p.is_superadmin ? adminBadge : null);\n",
		"web/src/gate.mjs":  "export const gate = (p) => p.isSuperadmin;\n",
		"web/src/gate.cjs":  "module.exports = (p) => p.is_superadmin;\n",

		// A test asserting the flag confers nothing names the flag. It is told
		// by its path, never by reading the assertion.
		"web/src/Nav.test.tsx":     "it('the retired is_superadmin flag is ignored', () => {\n  expect(role({ is_superadmin: true })).toBeUndefined();\n});\n",
		"web/src/role.spec.ts":     "expect(p.isSuperadmin).toBeUndefined();\n",
		"web/src/__tests__/fix.ts": "export const person = { is_superadmin: true };\n",

		// Prose. A file says why it stopped reading the flag, in both comment
		// forms, and the sentence is not a decision.
		"web/src/role.ts": "// the token carries roles since id-09 retired the is_superadmin flag\n" +
			"/* isSuperadmin left the API\n   with it */\n" +
			"export const role = (p: Principal) => p.roles[0];\n",
		"web/src/Card.svelte": "<!-- isSuperadmin is gone;\n     read roles -->\n<p>{p.roles[0]}</p>\n",

		// A string is not prose: a property quoted in an object still decides.
		// This one is a finding too, which the caller counts.
		"web/src/keys.ts": "export const keys = [\"is_superadmin\"];\n",

		// Nobody in this repository wrote these.
		"web/dist/app.js":           "var x={is_superadmin:!0};\n",
		"web/build/app.js":          "var x={is_superadmin:!0};\n",
		"node_modules/dep/index.js": "exports.is_superadmin = true;\n",
		"web/testdata/seed.ts":      "export const seed = { is_superadmin: true };\n",
		"web/src/api.min.js":        "var a={is_superadmin:1};\n",
		"web/src/schema.ts":         "// Code generated by openapi-typescript. DO NOT EDIT.\nexport type P = { is_superadmin?: boolean };\n",

		// An archive records what was once true.
		"web/.archive/old.tsx": "if (p.is_superadmin) admin();\n",
	}
}

// A frontend decides access as surely as a handler does. Until the rule read
// one, a page branching on the retired flag passed the gate: the family's
// verification found three such frontends behind a green roles line.
func TestRolesReadsTheFrontend(t *testing.T) {
	cfg := withRolesOnly(config.Identity{Role: config.RoleService, Audience: "drive"})
	out, err := run(t, cfg, repo(t, frontendTree()), noHistory())
	if err == nil {
		t.Fatalf("a frontend that branches on the flag is a finding:\n%s", out)
	}
	if !strings.HasPrefix(ruleLine(out, "roles"), "FAIL") {
		t.Fatalf("the roles rule reports the frontend, and no other rule fails here:\n%s", out)
	}
	for _, want := range []string{
		"web/src/Nav.tsx:2:", "web/src/Menu.vue:2:", "web/src/keys.ts:1:",
		"web/src/Badge.jsx:1:", "web/src/gate.mjs:1:", "web/src/gate.cjs:1:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a decision in the frontend is a finding at %s:\n%s", want, out)
		}
	}
	// Everything else in the tree names the flag and decides nothing.
	for _, quiet := range []string{
		"Nav.test.tsx", "role.spec.ts", "__tests__", "role.ts:", "Card.svelte",
		"dist/app.js", "build/app.js", "node_modules", "testdata/seed.ts",
		"api.min.js", "schema.ts", ".archive",
	} {
		if strings.Contains(out, quiet) {
			t.Errorf("%s names the flag and decides nothing, so it is not a finding:\n%s", quiet, out)
		}
	}
}

// ignoring answers as a tree whose .gitignore names the given paths. git
// writes the ignored paths it was asked about, one per line, and exits
// non-zero when none of them is ignored.
func ignoring(paths ...string) gates.Exec {
	return func(_ []string, _ bool, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "check-ignore" {
			return nil, nil
		}
		var out strings.Builder
		for _, arg := range args[1:] {
			if slices.Contains(paths, arg) {
				out.WriteString(arg + "\n")
			}
		}
		if out.Len() == 0 {
			return nil, errors.New("exit status 1")
		}
		return []byte(out.String()), nil
	}
}

// Build residue is not the repository. One frontend tree leaves a .vue.js
// sidecar beside every component and ignores it; a file that is on a laptop
// and not in a checkout would make the gate red locally and green on the
// runner, which is the one direction a gate must never fail in.
func TestRolesReadsNoFileTheRepositoryIgnores(t *testing.T) {
	files := frontendTree()
	files["web/src/Nav.tsx"] = "export const Nav = (p: Principal) => p.roles[0];\n"
	files["web/src/Menu.vue"] = "<script setup lang=\"ts\">\nconst admin = props.roles[0];\n</script>\n"
	files["web/src/keys.ts"] = "export const keys = [\"roles\"];\n"
	files["web/src/Badge.jsx"] = "export const Badge = (p) => (p.roles[0] ? adminBadge : null);\n"
	files["web/src/gate.mjs"] = "export const gate = (p) => p.roles.includes('platform_admin');\n"
	files["web/src/gate.cjs"] = "module.exports = (p) => p.roles.includes('platform_admin');\n"
	// The residue the toolchain wrote, holding what the source no longer does.
	files["web/src/Menu.vue.js"] = "const admin = props.isSuperadmin;\n"
	root := repo(t, files)

	out, err := run(t, withRolesOnly(config.Identity{Role: config.RoleService, Audience: "drive"}), root, noHistory())
	if err == nil || !strings.Contains(out, "web/src/Menu.vue.js:1:") {
		t.Fatalf("a tree that ignores nothing reads the file:\n%s", out)
	}

	out, err = run(t, withRolesOnly(config.Identity{Role: config.RoleService, Audience: "drive"}), root, ignoring("web/src/Menu.vue.js"))
	if err != nil {
		t.Fatalf("a file the repository ignores is not read:\n%s", out)
	}
	if !strings.HasPrefix(ruleLine(out, "roles"), "PASS") {
		t.Errorf("the roles rule passes over ignored residue:\n%s", out)
	}
}

// The same tree with the three decisions taken out passes, so the rule reads
// the frontend for what it decides and not for what it mentions.
func TestRolesPassesAFrontendThatReadsRoles(t *testing.T) {
	files := frontendTree()
	files["web/src/Nav.tsx"] = "export function Nav(p: Principal) {\n  if (p.roles.includes('platform_admin')) return admin;\n  return null;\n}\n"
	files["web/src/Menu.vue"] = "<script setup lang=\"ts\">\nconst admin = props.roles.includes('platform_admin');\n</script>\n"
	files["web/src/keys.ts"] = "export const keys = [\"roles\"];\n"
	files["web/src/Badge.jsx"] = "export const Badge = (p) => (p.roles[0] ? adminBadge : null);\n"
	files["web/src/gate.mjs"] = "export const gate = (p) => p.roles.includes('platform_admin');\n"
	files["web/src/gate.cjs"] = "module.exports = (p) => p.roles.includes('platform_admin');\n"
	cfg := withRolesOnly(config.Identity{Role: config.RoleService, Audience: "drive"})
	out, err := run(t, cfg, repo(t, files), noHistory())
	if err != nil {
		t.Fatalf("a frontend that reads roles leaves the gate green:\n%s", out)
	}
	if !strings.HasPrefix(ruleLine(out, "roles"), "PASS") {
		t.Errorf("a frontend that reads roles passes:\n%s", out)
	}
	if !strings.Contains(ruleLine(out, "roles"), "file(s) decide by role") {
		t.Errorf("the roles line counts what it read:\n%s", ruleLine(out, "roles"))
	}
}

// ruleLine returns the report line for a rule.
func ruleLine(out, name string) string {
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return line
		}
	}
	return ""
}

// roles_only cannot be unset once a tree's history has set it.
func TestRolesOnlyIsOneWay(t *testing.T) {
	cfg := config.Identity{Role: config.RoleService, Audience: "drive"}
	root := repo(t, map[string]string{"internal/api/h.go": verifying})

	out, err := run(t, cfg, root, noHistory())
	if err != nil {
		t.Fatalf("a tree that never set the key is not behind: %v\n%s", err, out)
	}
	if !strings.HasPrefix(ruleLine(out, "roles"), "SKIP") {
		t.Errorf("a tree that never set the key skips the rule with its reason:\n%s", out)
	}

	out, err = run(t, cfg, root, hadRolesOnly())
	if err == nil {
		t.Fatalf("a tree whose history set the key cannot unset it:\n%s", out)
	}
	if !strings.Contains(out, "FAIL roles") || !strings.Contains(out, config.Name+":1:") {
		t.Errorf("the finding names the file the key belongs in:\n%s", out)
	}

	cfg.RolesOnly = true
	out, err = run(t, cfg, root, hadRolesOnly())
	if err != nil {
		t.Fatalf("the key still set is the shape the rule wants: %v\n%s", err, out)
	}
}

// The one-way check asks the history. A tree it cannot ask is a rule that
// cannot report, which fails rather than passing.
func TestRolesOnlyNeedsTheHistory(t *testing.T) {
	broken := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
		return nil, errNoGit{}
	}
	_, err := run(t, config.Identity{Role: config.RoleService, Audience: "drive"},
		repo(t, map[string]string{"internal/api/h.go": verifying}), broken)
	if err == nil {
		t.Fatal("a rule that cannot read the history must fail")
	}
	if !strings.Contains(err.Error(), rolesOnlyKey) {
		t.Errorf("the failure names the key it looked for:\n%v", err)
	}
}

type errNoGit struct{}

func (errNoGit) Error() string { return "exec: git: executable file not found" }

// A frontend beside an API forwards the person's own token to the issuer,
// so the files the block names as bff may name the issuer's paths; the API's
// own files may not.
func TestAFrontendBesideTheAPIMayCallTheIssuer(t *testing.T) {
	service := config.Identity{Role: config.RoleService, Audience: "sandboxd", BFF: []string{"internal/http/web"}}
	directory := "package web\n\nvar members = \"/orgs/%s/members\"\n"
	api := "package api\n\nimport _ \"latere.ai/x/pkg/authkit/jwt\"\n"
	out, err := run(t, service, repo(t, map[string]string{"internal/http/web/directory.go": directory,
		"internal/http/api/h.go": api}), noHistory())
	if err != nil || !strings.Contains(out, "PASS request-path") {
		t.Fatalf("the frontend may call the issuer with the person's token (%v):\n%s", err, out)
	}
	out, err = run(t, service, repo(t, map[string]string{"internal/http/api/h.go": api,
		"internal/http/api/directory.go": strings.Replace(directory, "package web", "package api", 1)}), noHistory())
	if err == nil || !strings.Contains(out, "FAIL request-path") {
		t.Fatalf("the API's own files are still held:\n%s", out)
	}
}

// A path the block skips is one the rules assert nothing about, and a file
// the block admits as a passthrough may name a claim, because forwarding one
// is what it does.
func TestSkipAndPassthroughAreHonored(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"}
	files := map[string]string{
		"internal/auth/claims.go": "package auth\n\nvar keys = []string{\"org_id\", \"roles\"}\n",
		"deploy/prod/app.yaml":    deployment("        - name: X\n          value: https://auth.latere.ai\n"),
	}
	out, err := run(t, core, repo(t, files), noHistory())
	if err == nil {
		t.Fatalf("without the block's admissions both are findings:\n%s", out)
	}

	core.ClaimsPassthrough = []string{"internal/auth/claims.go"}
	core.Skip = []string{"deploy/prod"}
	out, _ = run(t, core, repo(t, files), noHistory())
	if strings.Contains(out, "FAIL claims") {
		t.Errorf("a passthrough file may name a claim:\n%s", out)
	}
	if strings.Contains(out, "FAIL no-latere-value") {
		t.Errorf("a skipped path is one the rules read nothing from:\n%s", out)
	}
}

// overlayIngress is one company's own overlay: the hostname its installation
// serves on, with another address named in prose and set nowhere.
const overlayIngress = `# Latere's own values. The handbook at docs.latere.ai says why.
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: cella
spec:
  rules:
    - host: code.latere.ai
`

// A declared overlay is one company's own deployment, kept in the core's
// tree because the tag deploys from it. The rule reads that overlay for the
// addresses it sets, and one of those is the whole of what a document may
// name. Everything else is held as before: the same sentence with no overlay
// declared, a line that names the overlay's path beside another address, an
// address the overlay only mentions, the base every fork deploys, and the
// code.
func TestADeclaredOverlayIsWhatADocumentMayName(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"}
	readme := map[string]string{
		"deploy/prod/ingress.yaml": overlayIngress,
		"README.md": "Latere runs Cella at [code.latere.ai](https://code.latere.ai) for its own\n" +
			"repositories. Anyone with a cluster and a bucket can run their own.\n",
	}

	out, err := run(t, core, repo(t, readme), noHistory())
	if err == nil || !strings.Contains(out, "FAIL no-latere-value") {
		t.Fatalf("with no overlay declared the sentence is a value a fork inherits:\n%s", out)
	}
	if !strings.Contains(out, "README.md:1") {
		t.Fatalf("the finding names the sentence:\n%s", out)
	}

	core.Overlays = []string{"deploy/prod"}
	out, _ = run(t, core, repo(t, readme), noHistory())
	if !strings.Contains(out, "PASS no-latere-value") {
		t.Fatalf("a document may name the address the declared overlay serves:\n%s", out)
	}

	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{{
		name: "a sentence names the address the overlay serves",
		files: map[string]string{"docs/install.md": "The hosted installation is " +
			"https://code.latere.ai.\n"},
		want: "PASS no-latere-value",
	}, {
		name: "naming the overlay's path admits no address the overlay does not set",
		files: map[string]string{"docs/install.md": "Unlike `deploy/prod`, set your issuer " +
			"to https://auth.latere.ai.\n"},
		want: "FAIL no-latere-value",
	}, {
		name:  "an address the overlay only mentions is not one it sets",
		files: map[string]string{"docs/install.md": "The handbook is at https://docs.latere.ai.\n"},
		want:  "FAIL no-latere-value",
	}, {
		name:  "an address no overlay carries is a value",
		files: map[string]string{"docs/install.md": "The reference installation is https://elsewhere.latere.ai.\n"},
		want:  "FAIL no-latere-value",
	}, {
		name: "the base every fork deploys is held",
		files: map[string]string{"deploy/base/app.yaml": deployment(
			"        - name: CELLA_OIDC_ISSUERS\n          value: https://code.latere.ai\n")},
		want: "FAIL no-latere-value",
	}, {
		name: "the code is held",
		files: map[string]string{"internal/api/url.go": "package api\n\n" +
			"// Public is where this node is reached.\nconst Public = \"https://code.latere.ai\"\n"},
		want: "FAIL no-latere-value",
	}} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"deploy/prod/ingress.yaml": overlayIngress}
			maps.Copy(files, c.files)
			out, _ := run(t, core, repo(t, files), noHistory())
			if !strings.Contains(out, c.want) {
				t.Fatalf("want %q:\n%s", c.want, out)
			}
		})
	}
}

// An overlay the tree does not hold exempts nothing, and a declaration with
// no effect hides a typo that lowers the bar.
func TestADeclaredOverlayMustBeInTheTree(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai", Overlays: []string{"deploy/production"}}
	_, err := run(t, core, repo(t, map[string]string{"deploy/prod/ingress.yaml": overlayIngress}), noHistory())
	if err == nil || !strings.Contains(err.Error(), "deploy/production") {
		t.Fatalf("a declared overlay the tree does not hold must stop the run: %v", err)
	}
}

// A core that declares no hosted deployment overlay is held to nothing here
// and says so: lux and cella hold overlays for a cluster and for a fork, and
// neither is a hosted installation.
func TestACoreWithNoOverlayReportsWhy(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai"}
	out, _ := run(t, core, repo(t, builds(map[string]string{
		"deploy/base/app.yaml": deployment(audienceEnv("cella")),
	})), noHistory())
	line := ruleLine(out, "core-audiences")
	if !strings.HasPrefix(line, "SKIP") || !strings.Contains(line, "overlay") {
		t.Fatalf("a core with no declared overlay says why the rule did not run:\n%s", out)
	}
}

// arca and origo name one directory under skip and under overlays. The walk
// prunes it, and the rule about the hosted deployment still reads it:
// overlays is the positive declaration, this directory is the hosted
// deployment, and skip the negative one, assert nothing here.
func TestTheDeclaredOverlayIsReadThoughItIsSkipped(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai", Overlays: []string{"deploy/prod"}, Skip: []string{"deploy/prod"}}

	out, _ := run(t, core, repo(t, builds(map[string]string{
		"deploy/base/app.yaml": deployment(audienceEnv("cella")),
		"deploy/prod/app.yaml": patchFile(audienceEnv("cella,api.latere.ai")),
	})), noHistory())
	if !strings.Contains(out, "PASS core-audiences") {
		t.Fatalf("the declared overlay is read although skip names the same path:\n%s", out)
	}

	out, err := run(t, core, repo(t, builds(map[string]string{
		"deploy/base/app.yaml": deployment(audienceEnv("cella")),
		"deploy/prod/app.yaml": patchFile(publicURLEnv),
	})), noHistory())
	if err == nil || !strings.Contains(out, "FAIL core-audiences") {
		t.Fatalf("an overlay that patches no audience runs the base's one name (%v):\n%s", err, out)
	}
	if !strings.Contains(out, "deploy/prod/app.yaml:") {
		t.Errorf("the finding is at the patch, the file the second name goes in:\n%s", out)
	}
}

// Every file of one overlay patches one deployment, so a container two files
// patch is one verdict and not two. This is arca's shape: a server and a
// reaper, each patched by the authorizer file and by the public-url file,
// and neither patch naming an audience.
func TestOneFindingPerWorkloadTheOverlayPatches(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai", Overlays: []string{"deploy/prod"}, Skip: []string{"deploy/prod"}}
	out, err := run(t, core, repo(t, builds(map[string]string{
		"deploy/base/app.yaml":        twoWorkloads(audienceEnv("cella")),
		"deploy/prod/authorizer.yaml": twoPatches(authorizerEnv),
		"deploy/prod/public-url.yaml": twoPatches(publicURLEnv),
	})), noHistory())
	if err == nil {
		t.Fatalf("two workloads left at the core's own name are two findings:\n%s", out)
	}
	if line := ruleLine(out, "core-audiences"); !strings.Contains(line, "2 finding(s)") {
		t.Fatalf("one finding per workload the overlay patches, whatever number of files patch it:\n%s", out)
	}
	if n := strings.Count(out, "leaves the audience of container"); n != 2 {
		t.Errorf("want 2 findings, one per workload, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, `container "app" of "app-reaper"`) {
		t.Errorf("a reaper beside a server runs the same container name and is held too:\n%s", out)
	}
}

// What the rule reports when the overlay names one audience, and when there
// is nothing in it for the rule to read. None of the four is a pass: a rule
// that passes over an overlay it could not read reports green as the tree
// fills up.
func TestTheHostedOverlayIsReadOrTheReasonIsPrinted(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai", Overlays: []string{"deploy/prod"}, Skip: []string{"deploy/prod"}}
	for _, c := range []struct{ name, base, patch, want, says string }{{
		name:  "the origin alone is one name where two belong",
		patch: patchFile(audienceEnv("api.latere.ai")),
		want:  "FAIL", says: "lists 1 name",
	}, {
		name:  "an overlay that patches no container this repository builds",
		patch: overlayIngress,
		want:  "SKIP", says: "patches no container",
	}, {
		name:  "an overlay file nothing can parse",
		patch: "kind: Deployment\n\tname: broken\n",
		want:  "FAIL", says: "does not parse",
	}, {
		name: "neither file names an audience, which the audience rule reports",
		base: deployment(""), patch: patchFile(publicURLEnv),
		want: "SKIP", says: "names an audience",
	}} {
		t.Run(c.name, func(t *testing.T) {
			base := c.base
			if base == "" {
				base = deployment(audienceEnv("cella"))
			}
			out, _ := run(t, core, repo(t, builds(map[string]string{
				"deploy/base/app.yaml": base,
				"deploy/prod/app.yaml": c.patch,
			})), noHistory())
			line := ruleLine(out, "core-audiences")
			if !strings.HasPrefix(line, c.want) || !strings.Contains(out, c.says) {
				t.Fatalf("want %s saying %q:\n%s", c.want, c.says, out)
			}
		})
	}
}

// An address where a name belongs is the audience rule's finding, and this
// rule does not report it a second time.
func TestAnAddressInTheOverlayIsTheAudienceRulesFinding(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai", Overlays: []string{"deploy/prod"}}
	out, err := run(t, core, repo(t, builds(map[string]string{
		"deploy/base/app.yaml": twoWorkloads(audienceEnv("cella")),
		"deploy/prod/app.yaml": workloadDoc("app", "", audienceEnv("https://api.latere.ai")) + "---\n" +
			workloadDoc("app-reaper", "", audienceEnv("cella,api.latere.ai")),
	})), noHistory())
	if err == nil || !strings.Contains(out, "FAIL audience") {
		t.Fatalf("an address where a name belongs is the audience rule's finding (%v):\n%s", err, out)
	}
	if !strings.Contains(out, "PASS core-audiences") {
		t.Fatalf("the rule reads the second workload and reports the address nowhere:\n%s", out)
	}
}

// The rule runs for a core and for nothing else: a service verifies its own
// name alone, and the hosted planes are role service.
func TestOnlyACoreNamesTwoAudiences(t *testing.T) {
	service := config.Identity{Role: config.RoleService, Audience: "drive",
		Overlays: nil}
	out, _ := run(t, service, repo(t, builds(map[string]string{
		"deploy/base/app.yaml": deployment("        - name: AUTH_AUDIENCE\n          value: drive\n"),
	})), noHistory())
	if strings.Contains(out, "core-audiences") {
		t.Fatalf("the rule is a core's and runs nowhere else:\n%s", out)
	}
}

// The rule is waived per rule and per date like the others, which is what
// carries a core from the day the rule lands to the day its overlay names
// both audiences.
func TestCoreAudiencesIsWaivedLikeTheOtherRules(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA",
		APIGroup: "cella.latere.ai", Overlays: []string{"deploy/prod"},
		Waive: map[string]config.Waiver{"core-audiences": {
			Until: "2026-12-31", Reason: "the overlay names both with the core's own spec"}}}
	root := repo(t, builds(map[string]string{
		"deploy/base/app.yaml": deployment(audienceEnv("cella")),
		"deploy/prod/app.yaml": patchFile(publicURLEnv),
	}))
	out, _ := run(t, core, root, noHistory())
	if !strings.Contains(out, "WAIV core-audiences") || !strings.Contains(out, "1 finding(s)") {
		t.Fatalf("the waived rule reports under WAIV with its count:\n%s", out)
	}
	if strings.Contains(out, "FAIL core-audiences") {
		t.Fatalf("a waived rule must not fail:\n%s", out)
	}
	expired := core
	expired.Waive = map[string]config.Waiver{"core-audiences": {Until: "2026-09-12", Reason: "yesterday"}}
	out, err := run(t, expired, root, noHistory())
	if err == nil || !strings.Contains(out, "FAIL core-audiences") {
		t.Fatalf("past its date the waiver stops working (%v):\n%s", err, out)
	}
}

// A document inside an archive records what was once true, so it is not a
// description of the system that exists.
func TestArchivedDocumentsAreNotDescriptions(t *testing.T) {
	cfg := config.Identity{Role: config.RoleService, Audience: "drive"}
	out, err := run(t, cfg, repo(t, map[string]string{
		"specs/.archive/old.md": "The identity fabric mints a delegated token under RFC 8693.\n",
		"specs/now.md":          "The service verifies one token.\n",
	}), noHistory())
	if err != nil {
		t.Fatalf("an archived document is not a live one: %v\n%s", err, out)
	}
}

// A deployment file nothing can parse is reported rather than passed over.
func TestAnUnreadableManifestIsAFinding(t *testing.T) {
	cfg := config.Identity{Role: config.RoleService, Audience: "drive"}
	out, err := run(t, cfg, repo(t, map[string]string{
		"deploy/base/app.yaml": "kind: Deployment\n\tname: broken\n",
	}), noHistory())
	if err == nil {
		t.Fatalf("a manifest the rules cannot read is a rule that did not run:\n%s", out)
	}
	if !strings.Contains(out, "deploy/base/app.yaml:1:") {
		t.Errorf("the finding names the file:\n%s", out)
	}
}

// Every finding is written for whoever runs the tool. The tells are the
// registers gate's own, so the two gates hold one rule rather than two.
func TestIdentityFindingsAreUserRegister(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"}
	service := config.Identity{Role: config.RoleService, Audience: "drive"}
	client := config.Identity{Role: config.RoleClient, Audiences: []string{"origo", "sandboxd"}}
	settledService := service
	settledService.Settled = []string{"complete"}
	sentences := 0
	for _, c := range ruleCases(core, service, client, settledService) {
		out, _ := run(t, c.cfg, repo(t, c.bad), noHistory())
		for line := range strings.SplitSeq(out, "\n") {
			line = strings.TrimSpace(line)
			_, sentence, ok := cutLocation(line)
			if !ok {
				continue
			}
			sentences++
			if tells := registers.Tells(sentence); len(tells) > 0 {
				t.Errorf("%s: the sentence of a finding, after its location, is in the user's "+
					"register: %s\n%s", c.name, strings.Join(tells, "; "), sentence)
			}
		}
	}
	if sentences == 0 {
		t.Fatal("no finding was read, so nothing was checked")
	}
}

// cutLocation splits "rel:line: sentence" into its two halves.
func cutLocation(line string) (string, string, bool) {
	rel, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	num, sentence, ok := strings.Cut(rest, ": ")
	if !ok || num == "" || strings.ContainsAny(num, " \t") {
		return "", "", false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return rel, sentence, true
}

// Which containers of a manifest are this repository's workload, which is
// what the audience rule is held over. A container runs this repository when
// the name its image was built under is exactly a command the tree builds:
// an image built beside it under a longer name is another workload, and a
// document holding one container of another workload holds none of this one.
// An init container is read like the workload when it is configured like the
// workload, and passed over when it is not.
func TestTheWorkloadContainersOfAManifest(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"}
	service := config.Identity{Role: config.RoleService, Audience: "drive"}
	for _, c := range []struct {
		name     string
		cfg      config.Identity
		files    map[string]string
		want     string
		contains string
	}{{
		name: "a sidecar is not this repository, the command beside it is",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: proxy
          image: ghcr.io/example/proxy:1
        - name: cellad
          image: ghcr.io/example/cellad:1
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: cella
`),
		want: "PASS", contains: "1 container(s)",
	}, {
		name: "the command this repository builds, with no audience, is a finding",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: proxy
          image: ghcr.io/example/proxy:1
        - name: cellad
          image: ghcr.io/example/cellad:1
`),
		want: "FAIL", contains: "1 finding(s)",
	}, {
		name: "an image whose name only holds the binary is another image",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: stubs
          image: ghcr.io/example/cellad-stubs:candidate
`),
		want: "SKIP", contains: "no container in the deployment runs a command this repository builds",
	}, {
		name: "one container that is not this repository is no container of it",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: stubs
          image: ghcr.io/example/stubs:candidate
`),
		want: "SKIP", contains: "no container in the deployment runs a command this repository builds",
	}, {
		name: "the registry, the tag and the digest are not the image's name",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: cellad
          image: registry.example.com:5000/cellad@sha256:0123456789abcdef
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: cella
`),
		want: "PASS", contains: "1 container(s)",
	}, {
		name: "an init container configured as the node is held to the audience",
		cfg:  core,
		files: cmdTree("cellad", `      initContainers:
        - name: check
          image: ghcr.io/example/cellad:1
          args: [check]
          env:
            - name: CELLA_DATA_DIR
              value: /var/lib/cella
      containers:
        - name: cellad
          image: ghcr.io/example/cellad:1
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: cella
`),
		want: "FAIL", contains: "1 finding(s)",
	}, {
		name: "an init container that only copies a file verifies nothing",
		cfg:  core,
		files: cmdTree("cellad", `      initContainers:
        - name: copy
          image: ghcr.io/example/cellad:1
          command: [cp, /bin/cellad, /shared/cellad]
          env:
            - name: TZ
              value: UTC
      containers:
        - name: cellad
          image: ghcr.io/example/cellad:1
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: cella
`),
		want: "PASS", contains: "1 container(s)",
	}, {
		name: "a service's init container is held under the shared prefix",
		cfg:  service,
		files: cmdTree("drived", `      initContainers:
        - name: check
          image: ghcr.io/example/drived:1
          env:
            - name: AUTH_ISSUER
              value: https://auth.example.com
      containers:
        - name: drived
          image: ghcr.io/example/drived:1
          env:
            - name: AUTH_AUDIENCE
              value: drive
`),
		want: "FAIL", contains: "1 finding(s)",
	}, {
		name: "an overlay patches the container it names and carries no image",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: cellad
          image: ghcr.io/example/cellad:1
`, `      containers:
        - name: cellad
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: cella
`),
		want: "PASS", contains: "1 container(s)",
	}, {
		name: "an overlay's patch may name no address either",
		cfg:  core,
		files: cmdTree("cellad", `      containers:
        - name: cellad
          image: ghcr.io/example/cellad:1
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: cella
`, `      containers:
        - name: cellad
          env:
            - name: CELLA_OIDC_AUDIENCE
              value: https://auth.example.com
`),
		want: "FAIL", contains: "1 finding(s)",
	}} {
		t.Run(c.name, func(t *testing.T) {
			out, _ := run(t, c.cfg, repo(t, c.files), noHistory())
			line := ruleLine(out, "audience")
			if !strings.HasPrefix(line, c.want) || !strings.Contains(line, c.contains) {
				t.Errorf("the audience rule must report %s %q:\n%s", c.want, c.contains, out)
			}
		})
	}
}

// pod renders a Deployment from the container lists of its pod spec.
func pod(spec string) string {
	return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
` + spec
}

// cmdTree is a tree that builds one command and holds one manifest per pod
// spec: the first is the base, the second the overlay that patches it.
func cmdTree(command string, specs ...string) map[string]string {
	out := map[string]string{"cmd/" + command + "/main.go": mainFile}
	rels := []string{"deploy/base/app.yaml", "deploy/overlay/app.yaml"}
	for i, spec := range specs {
		out[rels[i]] = pod(spec)
	}
	return out
}

// The one-way check reads a real history, and a repository with no commit
// yet has none: asking the current branch alone reports that as a failure
// git cannot be told apart from git being absent.
func TestRolesOnlyReadsARealHistory(t *testing.T) {
	root := repo(t, map[string]string{"internal/api/h.go": verifying})
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Skipf("git is not available here: %v\n%s", err, out)
	}
	out, err := run(t, config.Identity{Role: config.RoleService, Audience: "drive"}, root,
		gates.OSExec(root, io.Discard))
	if err != nil {
		t.Fatalf("a repository with no commit yet has no history: %v\n%s", err, out)
	}
	if !strings.HasPrefix(ruleLine(out, "roles"), "SKIP") {
		t.Errorf("a history that never set the key skips the rule:\n%s", out)
	}
}

// A waiver is per rule: the waived rule reports under WAIV with its count
// and the gate passes, while every other rule of the role still runs.
func TestWaivedRuleReportsAndHolds(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai",
		Waive: map[string]config.Waiver{"no-latere-value": {Until: "2026-12-31", Reason: "the overlay moves with id-06"}}}
	root := repo(t, builds(map[string]string{
		"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.latere.ai\n"),
	}))
	out, err := run(t, core, root, noHistory())
	if !strings.Contains(out, "WAIV no-latere-value") || !strings.Contains(out, "1 finding(s)") {
		t.Fatalf("the waived rule must report under WAIV with its count:\n%s", out)
	}
	if strings.Contains(out, "FAIL no-latere-value") {
		t.Fatalf("a waived rule must not fail:\n%s", out)
	}
	// The other rules of the role still ran: the audience rule reads the
	// same manifest and fails on it.
	if err == nil || !strings.Contains(out, "FAIL audience") {
		t.Fatalf("the rules beside the waived one must still run (%v):\n%s", err, out)
	}
	// A waiver whose rule already holds says the waiver can go.
	clean := repo(t, map[string]string{
		"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.example.com\n"),
	})
	out, _ = run(t, core, clean, noHistory())
	if !strings.Contains(out, "the waiver can go") {
		t.Fatalf("a waiver with nothing to waive must say so:\n%s", out)
	}
}

// Past its date a waiver stops working and the failure names it.
func TestExpiredWaiverFails(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai",
		Waive: map[string]config.Waiver{"no-latere-value": {Until: "2026-09-12", Reason: "yesterday"}}}
	root := repo(t, map[string]string{
		"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.latere.ai\n"),
	})
	out, err := run(t, core, root, noHistory())
	if err == nil || !strings.Contains(out, "FAIL no-latere-value") || !strings.Contains(out, "waiver expired 2026-09-12") {
		t.Fatalf("an expired waiver must fail and name itself (%v):\n%s", err, out)
	}
	// The day named is inclusive: a waiver until today still holds.
	core.Waive["no-latere-value"] = config.Waiver{Until: "2026-09-13", Reason: "today"}
	if out, _ := run(t, core, root, noHistory()); !strings.Contains(out, "WAIV no-latere-value") {
		t.Fatalf("a waiver until today must hold today:\n%s", out)
	}
}

// A waiver that names no rule, or a rule the role never runs, is a decision
// with no effect, and the gate refuses it rather than let a typo lower the
// bar.
func TestWaiverMustNameARuleTheRoleRuns(t *testing.T) {
	root := repo(t, map[string]string{"internal/a/a.go": "package a\n"})
	unknown := config.Identity{Role: config.RoleService, Audience: "drive",
		Waive: map[string]config.Waiver{"verifiers": {Until: "2026-12-31", Reason: "typo"}}}
	if _, err := run(t, unknown, root, noHistory()); err == nil || !strings.Contains(err.Error(), "which is not a rule") {
		t.Fatalf("an unknown rule name must be refused: %v", err)
	}
	idle := config.Identity{Role: config.RoleService, Audience: "drive",
		Waive: map[string]config.Waiver{"claims": {Until: "2026-12-31", Reason: "a core rule on a service"}}}
	if _, err := run(t, idle, root, noHistory()); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("a waiver for a rule the role does not run must be refused: %v", err)
	}
	// The two audiences are a core's, so a service waiving them waives
	// nothing, and the gate refuses the entry rather than reading it as a
	// name it does not know.
	planes := config.Identity{Role: config.RoleService, Audience: "lux.latere.ai",
		Waive: map[string]config.Waiver{"core-audiences": {Until: "2026-12-31", Reason: "a core rule on a plane"}}}
	if _, err := run(t, planes, root, noHistory()); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("a hosted plane cannot waive a core's rule: %v", err)
	}
}

// A bff forwards the person's token and verifies nothing itself, so the
// verifier rule asks nothing of its imports; a service is still held to one.
func TestABFFNeedsNoVerifierOfItsOwn(t *testing.T) {
	root := repo(t, map[string]string{"internal/web/w.go": "package web\n"})
	bff := config.Identity{Role: config.RoleBFF}
	if out, _ := run(t, bff, root, noHistory()); !strings.Contains(out, "PASS verifier") {
		t.Fatalf("a bff with no verifier import must pass the verifier rule:\n%s", out)
	}
	service := config.Identity{Role: config.RoleService, Audience: "drive"}
	if out, _ := run(t, service, root, noHistory()); !strings.Contains(out, "FAIL verifier") {
		t.Fatalf("a service with no verifier import must fail the verifier rule:\n%s", out)
	}
}

// The envelope rule's exemptions are declared. A type that carries the
// envelope's field names for a reason of its own is named in the block, and
// a name the tree does not hold stops the run rather than passing over.
func TestEnvelopeExemptionsAreDeclared(t *testing.T) {
	cfg := config.Identity{Role: config.RoleService, Audience: "drive"}
	files := map[string]string{
		"internal/ask/wire.go": answerWire,
		// A file left outside the exemption, so the exempt tree passes the
		// rule rather than skipping it: a rule with nothing to read proves
		// nothing about the rule.
		"internal/api/api.go": verifying,
	}

	out, err := run(t, cfg, repo(t, files), noHistory())
	if err == nil || !strings.Contains(out, "FAIL envelope") {
		t.Fatalf("a type that restates the decision is a finding:\n%s", out)
	}

	exempt := cfg
	exempt.EnvelopeExempt = []string{"internal/ask/wire.go"}
	out, err = run(t, exempt, repo(t, files), noHistory())
	if !strings.Contains(out, "PASS envelope") {
		t.Fatalf("a declared exemption is not read (%v):\n%s", err, out)
	}

	missing := cfg
	missing.EnvelopeExempt = []string{"internal/ask/absent.go"}
	_, err = run(t, missing, repo(t, files), noHistory())
	if err == nil || !strings.Contains(err.Error(), "which this tree does not hold") {
		t.Fatalf("an exemption the tree does not hold stops the run:\n%v", err)
	}
}

// The shared package is where the envelope is declared, so the rule does not
// hold it to importing its own declaration.
func TestEnvelopeSkipsTheSharedModule(t *testing.T) {
	root := repo(t, map[string]string{
		"go.mod":              "module latere.ai/x/pkg\n\ngo 1.27\n",
		"authz/authz.go":      answerWire,
		"internal/api/api.go": verifying,
	})
	out, err := run(t, config.Identity{Role: config.RoleService, Audience: "drive"}, root, noHistory())
	if err != nil {
		t.Fatalf("the shared package declares the envelope: %v\n%s", err, out)
	}
	if line := ruleLine(out, "envelope"); !strings.HasPrefix(line, "SKIP") {
		t.Errorf("the rule says why it did not run here:\n%s", line)
	}
}
