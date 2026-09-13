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
	err := Run(cfg, root, &sb, exec)
	return sb.String(), err
}

// A repository with no block fails, and the failure names the key and every
// role, because a repository outside the shape by omission is the gap.
func TestIdentityBlockIsRequired(t *testing.T) {
	var sb strings.Builder
	err := Run(config.Identity{}, repo(t, nil), &sb, noHistory())
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

	for _, c := range ruleCases(core, service, client) {
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

const storeFile = `package store

// Row is one attribution record.
type Row struct {
	ID      string ` + "`json:\"id\"`" + `
	AgentID string ` + "`json:\"agent_id\"`" + `
}
`

func ruleCases(core, service, client config.Identity) []ruleCase {
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
		name: "access by role, not by a flag",
		rule: "roles", cfg: withRolesOnly(service),
		bad:  map[string]string{"internal/api/h.go": "package api\n\nvar admin = \"is_superadmin\"\n"},
		good: map[string]string{"internal/api/h.go": "package api\n\nvar admin = \"platform_admin\"\n"},
	}, {
		name: "an explicit audience in every deployment",
		rule: "audience", cfg: core,
		bad:  map[string]string{"deploy/base/app.yaml": deployment("")},
		good: map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_AUDIENCE\n          value: cella\n")},
	}, {
		name: "the audience is a name, not the address of the issuer",
		rule: "audience", cfg: core,
		bad:  map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_AUDIENCE\n          value: https://auth.example.com\n")},
		good: map[string]string{"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_AUDIENCE\n          value: cella\n")},
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
		name: "the core's own group is not a company value",
		rule: "no-latere-value", cfg: core,
		bad:  map[string]string{"docs/pools.md": "A pool is `lux.latere.ai/v1beta1`.\n"},
		good: map[string]string{"docs/pools.md": "A pool is `cella.latere.ai/v1beta1`.\n"},
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
	sentences := 0
	for _, c := range ruleCases(core, service, client) {
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

// A deployment with more than one container is read by which of them runs a
// command this repository builds; a single-workload manifest is read whole.
func TestTheContainerThatRunsThisRepository(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai"}
	sidecar := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
      - name: proxy
        image: registry.example.com/proxy:1
      - name: cellad
        image: registry.example.com/cellad:1
%s`
	files := map[string]string{
		"cmd/cellad/main.go": "package main\n\nfunc main() {}\n",
		"deploy/base/app.yaml": strings.Replace(sidecar, "%s",
			"        env:\n        - name: CELLA_OIDC_AUDIENCE\n          value: cella\n", 1),
	}
	out, _ := run(t, core, repo(t, files), noHistory())
	if !strings.Contains(out, "PASS audience") || !strings.Contains(ruleLine(out, "audience"), "1 container(s)") {
		t.Errorf("the sidecar is not this repository, so one container was read:\n%s", out)
	}

	files["deploy/base/app.yaml"] = strings.Replace(sidecar, "%s", "", 1)
	out, _ = run(t, core, repo(t, files), noHistory())
	if !strings.Contains(out, "FAIL audience") {
		t.Errorf("a container of this repository with no audience is a finding:\n%s", out)
	}
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
