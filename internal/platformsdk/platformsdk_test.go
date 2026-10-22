// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package platformsdk

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// module writes a module at a fresh root with the given files and returns
// the root.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"go.mod": "module example.com/client\n\ngo 1.27\n"}
	maps.Copy(all, files)
	for rel, src := range all {
		if src == "" {
			continue
		}
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, cfg config.PlatformViaSDK, root string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(cfg, root, &sb)
	return sb.String(), err
}

// lines is the findings of a run, one per line, without the indent.
func lines(out string) []string {
	var got []string
	for l := range strings.SplitSeq(out, "\n") {
		if s, ok := strings.CutPrefix(l, "  "); ok {
			got = append(got, s)
		}
	}
	return got
}

// expect fails unless the run failed with exactly the findings want, in
// order.
func expect(t *testing.T, out string, err error, want ...string) {
	t.Helper()
	got := lines(out)
	if len(want) == 0 {
		if err != nil || len(got) > 0 {
			t.Fatalf("want a pass, got %v\n%s", err, out)
		}
		return
	}
	if err == nil {
		t.Fatalf("want %d finding(s), got a pass\n%s", len(want), out)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// platformPkg declares the prefixes a client keeps beside its calls, the
// shape the gate exists to retire: a version root that is no route, and
// capability prefixes built on it.
const platformPkg = `package platform

const (
	Root    = "/v1"
	Agents  = Root + "/agents"
	Storage = Root + "/storage"
)
`

// A route written as one literal is a finding, named with the SDK call
// that makes it.
func TestALiteralRouteIsAFinding(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

var sessions = "/v1/agents/sessions"
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:3: platform route /v1/agents/sessions in a string; use ListSessions or CreateSession in latere.ai/x/sdk-go/agents")
	if !strings.Contains(err.Error(), "1 platform route(s) outside latere.ai/x/sdk-go") {
		t.Errorf("the summary counts the findings: %v", err)
	}
}

// A concatenation is one finding at its start, not one per operand, and
// the parts the source does not fix are written as {}.
func TestAConcatenationIsOneFinding(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

func fork(id string) string {
	return "/v1/agents" + "/sessions/" + id + "/fork"
}
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:4: platform route /v1/agents/sessions/{}/fork in a string; use ForkSession in latere.ai/x/sdk-go/agents")
}

// A prefix constant is a finding where it is declared and wherever another
// package builds a route from it; the version root alone is no route.
func TestAPrefixConstantIsFollowedAcrossPackages(t *testing.T) {
	root := module(t, map[string]string{
		"internal/platform/platform.go": platformPkg,
		"internal/sessions/sessions.go": `package sessions

import (
	p "example.com/client/internal/platform"
	"example.com/client/internal/platform/other"
)

func blob(id, digest string) string {
	return p.Agents + "/sessions/" + id + "/blobs/" + digest
}

func upload() string { return other.Missing + p.Storage + "/uploads" }
`,
		"internal/platform/other/other.go": "package other\n\nvar Missing = \"x\"\n",
	})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"internal/platform/platform.go:5: platform route /v1/agents in a string; use latere.ai/x/sdk-go/agents",
		"internal/platform/platform.go:6: platform route /v1/storage in a string; use latere.ai/x/sdk-go/storage",
		"internal/sessions/sessions.go:9: platform route /v1/agents/sessions/{}/blobs/{} in a string; use ReadBlob in latere.ai/x/sdk-go/agents",
		"internal/sessions/sessions.go:12: platform route /v1/storage/uploads in a string; use StartUpload or UploadFile in latere.ai/x/sdk-go/storage")
}

// fmt.Sprintf is read with its arguments: a prefix constant passed for a
// verb fills it, and a leading verb is a base the scan cannot know.
func TestSprintfIsReadWithItsArguments(t *testing.T) {
	root := module(t, map[string]string{
		"internal/platform/platform.go": platformPkg,
		"c.go": `package client

import (
	"fmt"

	"example.com/client/internal/platform"
)

func session(id string) string { return fmt.Sprintf("%s/sessions/%s", platform.Agents, id) }

func wallet(base string) string { return fmt.Sprintf("%s/v1/account/wallet?x=%d", base, 1) }

func percent() string { return fmt.Sprintf("/v1/web/images?q=100%%") }

func indexed(id string) string { return fmt.Sprintf("%[1]s/v1/web", id) }

func unfinished() string { return fmt.Sprintf("/v1/web/icons/%", 1) }
`,
	})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:9: platform route /v1/agents/sessions/{} in a string; use GetSession, UpdateSession or DeleteSession in latere.ai/x/sdk-go/agents",
		"c.go:11: platform route /v1/account/wallet in a string; use GetWallet in latere.ai/x/sdk-go/account",
		"c.go:13: platform route /v1/web/images in a string; add it to latere.ai/x/sdk-go",
		"c.go:15: platform route /v1/web in a string; add it to latere.ai/x/sdk-go",
		"c.go:17: platform route /v1/web/icons/% in a string; add it to latere.ai/x/sdk-go",
		"internal/platform/platform.go:5: platform route /v1/agents in a string; use latere.ai/x/sdk-go/agents",
		"internal/platform/platform.go:6: platform route /v1/storage in a string; use latere.ai/x/sdk-go/storage")
}

// A url.URL is a finding by its Path when that is a route, and by its Host
// when it names the platform; the Path's literal is not reported again.
func TestAURLLiteralIsAFinding(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

import u "net/url"

func app(host, slug string) string {
	return (&u.URL{Scheme: "https", Host: host, Path: "/v1/apps/apps/" + slug}).String()
}

func origin(p string) string {
	return (&u.URL{Scheme: "https", Host: "api.latere.ai:443", Path: p}).String()
}

func both(slug string) string {
	return (&u.URL{Host: "api.latere.ai", Path: "/v1/apps/apps/" + slug}).String()
}

func local() string { return (&u.URL{Scheme: "http", Host: "127.0.0.1:8080", Path: "/callback"}).String() }

func positional() u.URL { return u.URL{} }
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:6: url.URL for platform route /v1/apps/apps/{}; use GetApp, UpdateApp or DeleteApp in latere.ai/x/sdk-go/apps",
		"c.go:10: url.URL addressed to the platform origin, api.latere.ai; add it to latere.ai/x/sdk-go",
		"c.go:14: url.URL for platform route /v1/apps/apps/{}; use GetApp, UpdateApp or DeleteApp in latere.ai/x/sdk-go/apps")
}

// A net/http request is a finding by its URL: to a platform route, or to
// the platform host on any path. The URL's literal is not reported again.
func TestARawRequestToThePlatformIsAFinding(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

import (
	"context"
	nethttp "net/http"
)

func wallet(ctx context.Context, base string) (*nethttp.Request, error) {
	return nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, base+"/v1/account/wallet", nil)
}

func anything(p string) (*nethttp.Response, error) { return nethttp.Get("https://api.latere.ai" + p) }

func root() (*nethttp.Response, error) { return nethttp.Head("https://api.latere.ai") }

func short() (*nethttp.Request, error) { return nethttp.NewRequest(nethttp.MethodGet) }
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:9: http.NewRequestWithContext to platform route /v1/account/wallet; use GetWallet in latere.ai/x/sdk-go/account",
		"c.go:12: http.Get to the platform origin, api.latere.ai; add it to latere.ai/x/sdk-go",
		"c.go:14: http.Head to the platform origin, api.latere.ai; add it to latere.ai/x/sdk-go")
}

// HTTP that is not the platform is not a finding: another host, a local
// service's own /v1 routes, a base URL passed to the SDK, a route in a
// sentence, a dialect door under another prefix, and a format whose
// constant part is not a route.
func TestHTTPThatIsNotThePlatformPasses(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

import (
	"fmt"
	"net/http"
	"net/url"
)

const (
	DefaultURL = "https://api.latere.ai"
	Audience   = "api.latere.ai"
	appURL     = "https://example.com/v1/apps"
	door       = "/openai"
	usage      = "Apps API base URL, including its /v1/apps path (default " + appURL + ")"
)

func device(path string) (*http.Request, error) {
	return http.NewRequest(http.MethodPost, "http://device"+"/v1/status"+path, nil)
}

func sessionHosts(id string) string { return "/v1/sessions/" + url.PathEscape(id) + "/hosts" }

func elsewhere() (*http.Response, error) { return http.Get("https://example.com/v1/agents/sessions") }

func models(base string) string { return base + door + "/v1/models" }

func message(err error) error { return fmt.Errorf("GET /v1/agents/sessions: %w", err) }

func notAString() int { return 1 + 2 }

func version() string { return "/v1" }

func nearly() string { return "/v1/agentsx" }

var _ = []string{DefaultURL, Audience, usage}
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err)
	if !strings.Contains(out, "every platform call goes through latere.ai/x/sdk-go, across 1 Go file(s)") {
		t.Errorf("a pass says what it read:\n%s", out)
	}
}

// fmt.Sprintf's dynamic format is the one place a route literal is read on
// its own again: the format is not fixed, so its argument is a string of its
// own.
func TestAnArgumentOfAnUnfixedFormatIsReadOnItsOwn(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

import "fmt"

func dynamic(f string) string { return fmt.Sprintf(f, "/v1/agents/sessions") }
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:5: platform route /v1/agents/sessions in a string; use ListSessions or CreateSession in latere.ai/x/sdk-go/agents")
}

// An allowed route passes and is counted; an allowance no finding needs
// fails, naming the entry.
func TestTheAllowListCoversARouteAndMustBeNeeded(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

const local = "/v1/models/openai/v1/models"

var shared = "/v1/models"
`})
	allow := map[string]string{"/v1/models": "the local model server shares the path"}
	out, err := run(t, config.PlatformViaSDK{Allow: allow}, root)
	expect(t, out, err)
	if !strings.Contains(out, "2 route(s) allowed by platform_via_sdk.allow") {
		t.Errorf("a pass counts what the allow list covered:\n%s", out)
	}

	allow["/v1/agents/{id}"] = "nothing writes this"
	out, err = run(t, config.PlatformViaSDK{Allow: allow}, root)
	if err == nil || !strings.Contains(err.Error(), "platform_via_sdk.allow names /v1/agents/{id}, which no finding needs") {
		t.Fatalf("an unneeded allowance must fail, naming it: %v\n%s", err, out)
	}
	if strings.Contains(err.Error(), "platform route(s) outside") {
		t.Errorf("an allowed route is not counted as a finding: %v", err)
	}
}

// An entry covers the routes under it at a segment boundary, and only
// those.
func TestAnAllowanceCoversASegmentBoundary(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

var icons = "/v1/web/icons/" + host()
var images = "/v1/web/images"

func host() string { return "" }
`})
	out, err := run(t, config.PlatformViaSDK{Allow: map[string]string{"/v1/web/icons": "a public read"}}, root)
	expect(t, out, err, "c.go:4: platform route /v1/web/images in a string; add it to latere.ai/x/sdk-go")
}

// Test files are not read: a test that serves a route to an httptest
// server is the SDK's shape, not a client's copy of it.
func TestTestFilesAreExempt(t *testing.T) {
	root := module(t, map[string]string{
		"c.go":      "package client\n",
		"c_test.go": "package client\n\nvar route = \"/v1/agents/sessions\"\n",
	})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err)
}

// The SDK's own source, as a nested module or vendored, and the pruned and
// skipped directories, are not read.
func TestTheSDKAndPrunedDirectoriesAreExempt(t *testing.T) {
	route := "package x\n\nvar route = \"/v1/agents/sessions\"\n"
	root := module(t, map[string]string{
		"c.go":                       "package client\n",
		"sdk/go.mod":                 "module latere.ai/x/sdk-go\n\ngo 1.26\n",
		"sdk/agents/agents.go":       route,
		"sdk/examples/go.mod":        "module latere.ai/x/sdk-go/examples\n",
		"sdk/examples/main.go":       route,
		"vendor/latere.ai/x/a/a.go":  route,
		"testdata/a.go":              route,
		".claude/worktrees/w/a.go":   route,
		"node_modules/x/a.go":        route,
		"generated/a.go":             route,
		"broken.go":                  "package client\n\nfunc {",
		"tools/go.mod":               "// no module line\n",
		"tools/tools.go":             "package tools\n",
		"README.md":                  "/v1/agents/sessions\n",
		"internal/empty/placeholder": "",
	})
	out, err := run(t, config.PlatformViaSDK{Skip: []string{"generated"}}, root)
	expect(t, out, err)
}

// A tree with nothing to read fails rather than passing over nothing: all
// tests, or the SDK itself.
func TestNothingToReadFails(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"only tests": {"c_test.go": "package client\n"},
		"the SDK":    {"go.mod": "module latere.ai/x/sdk-go\n", "agents/agents.go": "package agents\n"},
	} {
		t.Run(name, func(t *testing.T) {
			root := module(t, files)
			_, err := run(t, config.PlatformViaSDK{}, root)
			if err == nil || !strings.Contains(err.Error(), "would pass vacuously") {
				t.Fatalf("want a vacuous-pass failure, got %v", err)
			}
		})
	}
}

// A name declared in a function, a struct literal's key, a selector's
// member and a field tag do not read as the package constant they share a
// name with; a constant declared in a function is reported once, where its
// value is written.
func TestNamesThatAreNotTheConstantAreNotRead(t *testing.T) {
	root := module(t, map[string]string{
		"internal/platform/platform.go": platformPkg,
		"c.go": `package client

import (
	"example.com/client/internal/platform"
)

const Agents = "/v1/agents"

type T struct {
	Agents string ` + "`json:\"/v1/agents\"`" + `
}

func shadowed(Agents string) string { return Agents + "/x" }

func key() T { return T{Agents: "x"} }

func member(t T) string { return t.Agents }

func local() string {
	const p = "/v1/storage/uploads"
	return p + "/x"
}

func importShadowed(platform T) string { return platform.Agents }

func lit() string {
	f := func(Agents string) string { return Agents }
	return f("")
}

type Agents2 int
`,
	})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:7: platform route /v1/agents in a string; use latere.ai/x/sdk-go/agents",
		"c.go:20: platform route /v1/storage/uploads in a string; use StartUpload or UploadFile in latere.ai/x/sdk-go/storage",
		"internal/platform/platform.go:5: platform route /v1/agents in a string; use latere.ai/x/sdk-go/agents",
		"internal/platform/platform.go:6: platform route /v1/storage in a string; use latere.ai/x/sdk-go/storage")
}

// The folding follows the language: a constant with no value repeats the
// one before it, a typed constant and a conversion to string are read, and
// a cycle, which the compiler refuses, ends.
func TestConstantsFoldAsTheLanguageDoes(t *testing.T) {
	root := module(t, map[string]string{"c.go": `package client

type Route string

const (
	Wallet Route = "/v1/account/wallet"
	Again
)

const (
	a = b
	b = a
	n = 5
)

func again() string { return string(Again) }

func cycle() string { return a + "/v1/agents" }

func number() int { return n + 1 }

func paren() string { return ("/v1/account" + "/effort") }

var _ = string(n)
`})
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c.go:6: platform route /v1/account/wallet in a string; use GetWallet in latere.ai/x/sdk-go/account",
		"c.go:16: platform route /v1/account/wallet in a string; use GetWallet in latere.ai/x/sdk-go/account",
		"c.go:18: platform route /v1/agents in a string; use latere.ai/x/sdk-go/agents",
		"c.go:22: platform route /v1/account/effort in a string; use GetEffortLevels in latere.ai/x/sdk-go/account")
}

// A tree without a go.mod still tells its packages apart by directory.
func TestATreeWithoutAModuleResolvesByDirectory(t *testing.T) {
	root := t.TempDir()
	for rel, src := range map[string]string{
		"p/p.go": "package p\n\nconst Agents = \"/v1/agents\"\n",
		"c/c.go": "package c\n\nimport \"p\"\n\nvar x = p.Agents + \"/triggers\"\n",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := run(t, config.PlatformViaSDK{}, root)
	expect(t, out, err,
		"c/c.go:5: platform route /v1/agents/triggers in a string; use ListTriggers or ListRoutines in latere.ai/x/sdk-go/agents",
		"p/p.go:3: platform route /v1/agents in a string; use latere.ai/x/sdk-go/agents")
}

// A root that cannot be read is an error, not a pass.
func TestAnUnreadableRootFails(t *testing.T) {
	_, err := run(t, config.PlatformViaSDK{}, filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "scanning for platform routes") {
		t.Fatalf("want a scan error, got %v", err)
	}
}

// A go.mod that cannot be read stops the scan rather than passing over its
// module.
func TestAnUnreadableGoModFails(t *testing.T) {
	root := module(t, map[string]string{"c.go": "package client\n"})
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub", "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, config.PlatformViaSDK{}, root)
	if err == nil || !strings.Contains(err.Error(), "scanning for platform routes") {
		t.Fatalf("want a scan error, got %v", err)
	}
}

func TestAddressReadsSchemesHostsAndQueries(t *testing.T) {
	cases := []struct {
		in    string
		route string
		api   bool
		host  bool
	}{
		{"/v1/agents/sessions?limit=1", "/v1/agents/sessions", true, false},
		{"/v1/agents/sessions/{id}/fork", "/v1/agents/sessions/{}/fork", true, false},
		{"/v1/agents/sessions/", "/v1/agents/sessions", true, false},
		{"wss://api.latere.ai/v1/environments/x/attach", "/v1/environments/x/attach", true, true},
		{"HTTPS://user@API.latere.ai:443/v1/repos", "/v1/repos", true, true},
		{"https://api.latere.ai", "", false, true},
		{"https://api.latere.ai/healthz", "/healthz", false, true},
		{"https://auth.latere.ai/v1/account", "", false, false},
		{"https://[::1]:8080/v1/agents", "", false, false},
		{"%s/v1/evals/%s", "/v1/evals/{}", true, false},
		{"Open /v1/agents in the console", "", false, false},
		{"/v1", "", false, false},
	}
	for _, c := range cases {
		got := address(value{{text: c.in, known: true}})
		if got.route != c.route || got.api != c.api || got.host != c.host {
			t.Errorf("address(%q) = %+v, want route %q api %v host %v", c.in, got, c.route, c.api, c.host)
		}
	}
}

func TestHintNamesTheCallOrTheSDK(t *testing.T) {
	for route, want := range map[string]string{
		"/v1/agents":                  "use latere.ai/x/sdk-go/agents",
		"/v1/account/wallet":          "use GetWallet in latere.ai/x/sdk-go/account",
		"/v1/agents/sessions":         "use ListSessions or CreateSession in latere.ai/x/sdk-go/agents",
		"/v1/agents/sessions/{}":      "use GetSession, UpdateSession or DeleteSession in latere.ai/x/sdk-go/agents",
		"/v1/account/wallet/checkout": "add it to latere.ai/x/sdk-go",
	} {
		if got := hint(route); got != want {
			t.Errorf("hint(%q) = %q, want %q", route, got, want)
		}
	}
}

// Every route the call table names is one the gate can report, written the
// way normalize writes it, so no entry is unreachable.
func TestTheCallTableIsWrittenAsFindingsAre(t *testing.T) {
	for route, c := range calls {
		if !isRoute(route) || normalize(route) != route {
			t.Errorf("%s is not a route as a finding prints it", route)
		}
		if c.pkg == "" {
			t.Errorf("%s names no package", route)
		}
	}
}
