// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
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
		name: "no issuer call on a request path",
		rule: "request-path", cfg: service,
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar teams = \"/tokeninfo\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nvar health = \"/healthz\"\n"},
	}, {
		name: "no issuer call for the members of an organisation",
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

// verifying is the smallest file that holds the one-verifier rule, so a test
// of another rule is not also a test of that one.
const verifying = "package api\n\nimport _ \"latere.ai/x/pkg/authkit/jwt\"\n"

func withRolesOnly(cfg config.Identity) config.Identity {
	cfg.RolesOnly = true
	return cfg
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

// deployment renders a one-container workload with the given env entries.
func deployment(env string) string {
	body := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
      - name: app
        image: registry.example.com/app:1
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
func TestSkipAndPassthroughAreHonoured(t *testing.T) {
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
// addresses it sets, so a document may say where the installation runs and
// which path it deploys from. Everything else is held as before: the same
// sentence with no overlay declared, an address the overlay only mentions,
// the base every fork deploys, and the code.
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
		name: "a sentence that names the overlay's path is about that overlay",
		files: map[string]string{"docs/install.md": "The hosted installation deploys from " +
			"`deploy/prod`, which serves https://code.latere.ai.\n"},
		want: "PASS no-latere-value",
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
