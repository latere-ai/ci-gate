// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package platformsdk

import (
	"regexp"
	"slices"
	"strings"
)

// SDK is the Go SDK's module path. A directory whose go.mod declares it, or
// a module under it, is the SDK's own source, where the routes belong, and
// is not read.
const SDK = "latere.ai/x/sdk-go"

// Host is the platform origin's host. A request addressed to it is a call to
// the platform, whatever its path.
const Host = "api.latere.ai"

// segments are the first segments under /v1 that the platform origin serves,
// one per capability, as its mount table names them. A path under /v1 whose
// first segment is not here, such as a local service's /v1/status, is not a
// platform route.
var segments = []string{
	"account", "agents", "apps", "connections", "environments", "evals",
	"models", "parsing", "repos", "shares", "storage", "web",
}

// sdkCall is what a finding tells the reader to use instead: an SDK
// package, and the calls it makes on the route. No names means the route is
// a package's base, and the package is the answer.
type sdkCall struct {
	pkg   string
	names []string
}

// calls maps a route, as normalize writes it, to the SDK calls that make it,
// as the SDK's v0.5.0 surface has them. It names a call only where the SDK
// has one; every other route is a call the SDK does not make yet, and the
// finding says to add it there. Refresh it from the SDK before the release
// that turns this gate on in a client.
var calls = map[string]sdkCall{
	"/v1/agents":                                {pkg: "agents"},
	"/v1/agents/sessions":                       {"agents", []string{"ListSessions", "CreateSession"}},
	"/v1/agents/sessions/search":                {"agents", []string{"SearchSessions"}},
	"/v1/agents/sessions/{}":                    {"agents", []string{"GetSession", "UpdateSession", "DeleteSession"}},
	"/v1/agents/sessions/{}/archive":            {"agents", []string{"ArchiveSession"}},
	"/v1/agents/sessions/{}/unarchive":          {"agents", []string{"UnarchiveSession"}},
	"/v1/agents/sessions/{}/end":                {"agents", []string{"EndSession"}},
	"/v1/agents/sessions/{}/resume":             {"agents", []string{"ResumeSession"}},
	"/v1/agents/sessions/{}/fork":               {"agents", []string{"ForkSession"}},
	"/v1/agents/sessions/{}/events":             {"agents", []string{"ListEvents", "SendMessage", "Interrupt", "AnswerQuestion", "ConfirmTool", "SendToolResult"}},
	"/v1/agents/sessions/{}/stream":             {"agents", []string{"StreamEvents"}},
	"/v1/agents/sessions/{}/blobs/{}":           {"agents", []string{"ReadBlob"}},
	"/v1/agents/sessions/{}/files":              {"agents", []string{"ReadSessionFile"}},
	"/v1/agents/triggers":                       {"agents", []string{"ListTriggers", "ListRoutines"}},
	"/v1/agents/triggers/{}":                    {"agents", []string{"GetTrigger", "ApplyTrigger", "DeleteTrigger", "GetRoutine", "CreateRoutine", "ChangeRoutine", "DeleteRoutine"}},
	"/v1/agents/triggers/{}/fire":               {"agents", []string{"FireTrigger", "RunRoutine"}},
	"/v1/agents/triggers/{}/firings":            {"agents", []string{"ListFirings"}},
	"/v1/account":                               {pkg: "account"},
	"/v1/account/agents/{}":                     {"account", []string{"ApplyManagedAgent"}},
	"/v1/account/effort":                        {"account", []string{"GetEffortLevels"}},
	"/v1/account/wallet":                        {"account", []string{"GetWallet"}},
	"/v1/account/preferences":                   {"account", []string{"GetPreferences"}},
	"/v1/account/preferences/pins/{}":           {"account", []string{"PinSession", "UnpinSession"}},
	"/v1/account/devices":                       {"account", []string{"ListDevices"}},
	"/v1/account/devices/{}":                    {"account", []string{"RenameDevice", "RemoveDevice"}},
	"/v1/account/environments/context":          {"account", []string{"GetEnvironmentsContext"}},
	"/v1/account/apps/{}/access":                {"apps", []string{"GetAccess"}},
	"/v1/account/apps/{}/access/invites":        {"apps", []string{"Invite"}},
	"/v1/account/apps/{}/access/password":       {"apps", []string{"SetPassword", "RemovePassword"}},
	"/v1/account/apps/{}/access/{}":             {"apps", []string{"RevokeAccess"}},
	"/v1/apps":                                  {pkg: "apps"},
	"/v1/apps/apps":                             {"apps", []string{"ListApps", "CreateApp"}},
	"/v1/apps/apps/{}":                          {"apps", []string{"GetApp", "UpdateApp", "DeleteApp"}},
	"/v1/apps/apps/{}/deploys":                  {"apps", []string{"ListDeploys"}},
	"/v1/apps/apps/{}/deploys/{}":               {"apps", []string{"GetDeploy"}},
	"/v1/apps/apps/{}/deploys/{}/logs":          {"apps", []string{"ReadDeployLog", "FollowDeployLog"}},
	"/v1/apps/apps/{}/releases":                 {"apps", []string{"ListReleases"}},
	"/v1/apps/apps/{}/releases/{}":              {"apps", []string{"GetRelease"}},
	"/v1/connections":                           {pkg: "connections"},
	"/v1/connections/connections":               {"connections", []string{"ListConnections", "CreateConnection"}},
	"/v1/connections/connections/{}":            {"connections", []string{"GetConnection", "RenameConnection", "DeleteConnection"}},
	"/v1/connections/connections/{}/confirm":    {"connections", []string{"ConfirmConnection"}},
	"/v1/connections/connections/{}/reconnect":  {"connections", []string{"ReconnectConnection"}},
	"/v1/connections/connections/{}/credential": {"connections", []string{"ReplaceCredential"}},
	"/v1/connections/connections/{}/tools":      {"connections", []string{"ListTools"}},
	"/v1/connections/connections/{}/calls":      {"connections", []string{"ListCalls"}},
	"/v1/connections/connectors":                {"connections", []string{"ListConnectors", "CreateConnector"}},
	"/v1/connections/connectors/{}":             {"connections", []string{"GetConnector", "UpdateConnector", "DeleteConnector"}},
	"/v1/storage":                               {pkg: "storage"},
	"/v1/storage/files/{}":                      {"storage", []string{"ListFiles"}},
	"/v1/storage/files/{}/{}":                   {"storage", []string{"ReadFile", "WriteFile", "StatFile", "ListFiles", "GetDownloadURL"}},
	"/v1/storage/uploads":                       {"storage", []string{"StartUpload", "UploadFile"}},
	"/v1/storage/uploads/{}":                    {"storage", []string{"AbortUpload"}},
	"/v1/storage/uploads/{}/complete":           {"storage", []string{"CompleteUpload"}},
	"/v1/models":                                {pkg: "llm"},
	"/v1/models/lux/v1/generate":                {"llm", []string{"Generate", "Stream"}},
	"/v1/models/lux/v1/count_tokens":            {"llm", []string{"CountTokens"}},
	"/v1/models/lux/v1/models":                  {"llm", []string{"ListModels"}},
}

// hint is the instead-clause of a finding on route: the SDK call that makes
// it, or the instruction to add one.
func hint(route string) string {
	c, ok := calls[route]
	if !ok {
		return "add it to " + SDK
	}
	pkg := SDK + "/" + c.pkg
	switch n := len(c.names); n {
	case 0:
		return "use " + pkg
	case 1:
		return "use " + c.names[0] + " in " + pkg
	default:
		return "use " + strings.Join(c.names[:n-1], ", ") + " or " + c.names[n-1] + " in " + pkg
	}
}

var (
	// template is a path parameter written as a route template, {id}.
	template = regexp.MustCompile(`\{[^{}/]*\}`)
	// verb is a fmt verb left in a constant, as a format string kept for
	// later carries it.
	verb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)
	// unknowns is a run of parts the scan cannot know, which stand for one.
	unknowns = regexp.MustCompile(`(\{\})+`)
)

// normalize writes a path the way findings, the call table and the allow
// list compare it: every part the scan cannot know, a route template's
// parameter and a fmt verb alike, as {}, and no trailing slash.
func normalize(p string) string {
	p = template.ReplaceAllString(p, unknown)
	p = verb.ReplaceAllString(p, unknown)
	p = unknowns.ReplaceAllString(p, unknown)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// target is what a string value addresses.
type target struct {
	// route is the path the value names, normalized; empty when it names
	// neither a platform route nor the platform host.
	route string
	// api reports that route is under a segment the platform serves.
	api bool
	// host reports that the value names the platform host itself.
	host bool
}

// schemes are the URL schemes a call to the platform is written with.
var schemes = []string{"https://", "http://", "wss://", "ws://"}

// address reads what a value addresses. The value names a route when it
// starts with one, after a scheme and the platform host, or after a part the
// scan cannot know, such as a base URL read from configuration. A value
// that starts with anything else, a sentence that mentions a route among
// them, addresses nothing.
func address(v value) target {
	s := normalize(v.render())
	var t target
	if rest, ok := strings.CutPrefix(s, unknown); ok {
		s = rest
	} else if rest, ok := cutScheme(s); ok {
		end := strings.IndexAny(rest, "/?{")
		if end < 0 {
			end = len(rest)
		}
		if hostname(rest[:end]) != Host {
			return target{}
		}
		t.host = true
		s = rest[end:]
	}
	path, _, _ := strings.Cut(s, "?")
	path = normalize(path)
	t.api = isRoute(path)
	if t.api || (t.host && path != "") {
		t.route = path
	}
	return t
}

// cutScheme strips a URL scheme from s, reporting whether it carried one.
func cutScheme(s string) (string, bool) {
	for _, scheme := range schemes {
		if len(s) >= len(scheme) && strings.EqualFold(s[:len(scheme)], scheme) {
			return s[len(scheme):], true
		}
	}
	return s, false
}

// hostname is an authority's host, without user information or a port.
func hostname(authority string) string {
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		authority = authority[i+1:]
	}
	if i := strings.LastIndex(authority, ":"); i >= 0 && !strings.Contains(authority[i:], "]") {
		authority = authority[:i]
	}
	return strings.ToLower(authority)
}

// isRoute reports whether a path is under a segment the platform serves.
func isRoute(path string) bool {
	rest, ok := strings.CutPrefix(path, "/v1/")
	if !ok {
		return false
	}
	if end := strings.IndexAny(rest, "/{"); end >= 0 {
		rest = rest[:end]
	}
	return slices.Contains(segments, rest)
}

// allows reports the allow entries that cover route: the entry itself, or a
// prefix of it that ends at a segment boundary.
func allows(route string, allow map[string]string) []string {
	var keys []string
	for key := range allow {
		k := normalize(key)
		if route == k || strings.HasPrefix(route, k+"/") {
			keys = append(keys, key)
		}
	}
	return keys
}
