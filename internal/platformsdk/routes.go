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
// as the SDK's v0.7.0 surface has them, with its memory package beside
// them: the iterator and the Page form of a list alike. A route whose
// parameter is a kind, such as an evals definition's, names the calls of
// every kind it can be. It names a call only where the SDK has one; every
// other route is a call the SDK does not make, and the finding says to add
// it there. Refresh it from the SDK when a release adds calls.
var calls = map[string]sdkCall{
	"/v1/agents":                       {pkg: "agents"},
	"/v1/agents/sessions":              {"agents", []string{"ListSessions", "ListSessionsPage", "CreateSession"}},
	"/v1/agents/sessions/search":       {"agents", []string{"SearchSessions", "SearchSessionsPage"}},
	"/v1/agents/sessions/summary":      {"agents", []string{"GetSessionSummary"}},
	"/v1/agents/sessions/{}":           {"agents", []string{"GetSession", "UpdateSession", "DeleteSession"}},
	"/v1/agents/sessions/{}/archive":   {"agents", []string{"ArchiveSession"}},
	"/v1/agents/sessions/{}/unarchive": {"agents", []string{"UnarchiveSession"}},
	"/v1/agents/sessions/{}/end":       {"agents", []string{"EndSession"}},
	"/v1/agents/sessions/{}/resume":    {"agents", []string{"ResumeSession"}},
	"/v1/agents/sessions/{}/fork":      {"agents", []string{"ForkSession"}},
	"/v1/agents/sessions/{}/events":    {"agents", []string{"ListEvents", "ListEventsPage", "SendMessage", "Interrupt", "AnswerQuestion", "ConfirmTool", "SendToolResult"}},
	"/v1/agents/sessions/{}/stream":    {"agents", []string{"StreamEvents"}},
	"/v1/agents/sessions/{}/blobs/{}":  {"agents", []string{"ReadBlob"}},
	"/v1/agents/sessions/{}/files":     {"agents", []string{"ReadSessionFile"}},
	"/v1/agents/triggers":              {"agents", []string{"ListTriggers", "ListTriggersPage", "ListRoutines", "ListRoutinesPage"}},
	"/v1/agents/triggers/{}":           {"agents", []string{"GetTrigger", "ApplyTrigger", "DeleteTrigger", "GetRoutine", "CreateRoutine", "ChangeRoutine", "DeleteRoutine"}},
	"/v1/agents/triggers/{}/fire":      {"agents", []string{"FireTrigger", "RunRoutine"}},
	"/v1/agents/triggers/{}/firings":   {"agents", []string{"ListFirings", "ListFiringsPage"}},
	// The read of a shared conversation by its link's token, which anyone
	// holding the link makes, with a Config built WithoutCredentials.
	"/v1/shares":             {pkg: "agents"},
	"/v1/shares/{}":          {"agents", []string{"GetSharedPage", "ListSharedEvents"}},
	"/v1/shares/{}/blobs/{}": {"agents", []string{"ReadSharedBlob"}},
	"/v1/shares/{}/images":   {"web", []string{"ReadSharedImage"}},

	"/v1/account":                                {pkg: "account"},
	"/v1/account/agents/{}":                      {"account", []string{"ApplyManagedAgent"}},
	"/v1/account/effort":                         {"account", []string{"GetEffortLevels"}},
	"/v1/account/wallet":                         {"account", []string{"GetWallet"}},
	"/v1/account/wallet/checkout":                {"account", []string{"StartCheckout"}},
	"/v1/account/data":                           {"account", []string{"GetDataPlaces"}},
	"/v1/account/data/models":                    {"account", []string{"GetModelDataTerms"}},
	"/v1/account/arrival":                        {"account", []string{"RecordArrival"}},
	"/v1/account/preferences":                    {"account", []string{"GetPreferences", "UpdatePreferences"}},
	"/v1/account/preferences/network/domains/{}": {"account", []string{"AddSite", "RemoveSite"}},
	"/v1/account/preferences/bookmarks/{}/{}":    {"account", []string{"BookmarkTurn", "UnbookmarkTurn"}},
	"/v1/account/preferences/pins/{}":            {"account", []string{"PinSession", "UnpinSession"}},
	"/v1/account/preferences/pins/apps/{}":       {"account", []string{"PinApp", "UnpinApp"}},
	"/v1/account/preferences/pins/projects/{}":   {"account", []string{"PinProject", "UnpinProject"}},
	"/v1/account/projects":                       {"account", []string{"ListProjects", "ListProjectsPage", "CreateProject"}},
	"/v1/account/projects/{}":                    {"account", []string{"GetProject", "UpdateProject", "DeleteProject"}},
	"/v1/account/projects/{}/apps/{}":            {"account", []string{"AddProjectApp", "RemoveProjectApp"}},
	"/v1/account/shares":                         {"account", []string{"ListShares", "ListSharesPage", "CreateShare"}},
	"/v1/account/shares/{}":                      {"account", []string{"UpdateShare", "DeleteShare"}},
	"/v1/account/triggers/inbound":               {"account", []string{"ListTriggerInbound"}},
	// A trigger's way in, by a kind the path names last.
	"/v1/account/triggers/{}":             {"account", []string{"IssueTriggerHook", "RemoveTriggerHook", "IssueTriggerAddress", "RemoveTriggerAddress"}},
	"/v1/account/triggers/{}/hook":        {"account", []string{"IssueTriggerHook", "RemoveTriggerHook"}},
	"/v1/account/triggers/{}/address":     {"account", []string{"IssueTriggerAddress", "RemoveTriggerAddress"}},
	"/v1/account/devices":                 {"account", []string{"ListDevices"}},
	"/v1/account/devices/{}":              {"account", []string{"RenameDevice", "RemoveDevice"}},
	"/v1/account/environments/context":    {"account", []string{"GetEnvironmentsContext"}},
	"/v1/account/apps/{}/access":          {"apps", []string{"GetAccess"}},
	"/v1/account/apps/{}/access/invites":  {"apps", []string{"Invite"}},
	"/v1/account/apps/{}/access/password": {"apps", []string{"SetPassword", "RemovePassword"}},
	"/v1/account/apps/{}/access/{}":       {"apps", []string{"RevokeAccess"}},

	// What Latere remembers about a person, under the account's routes and
	// made by the SDK's memory package, as the platform's account document
	// lists them.
	"/v1/account/memory":                   {"memory", []string{"ListMemories", "AddMemory", "ForgetEverything"}},
	"/v1/account/memory/settings":          {"memory", []string{"UpdateSettings"}},
	"/v1/account/memory/forgotten":         {"memory", []string{"ListForgotten"}},
	"/v1/account/memory/forgotten/restore": {"memory", []string{"RestoreMemory"}},
	"/v1/account/memory/export":            {"memory", []string{"ExportMemory"}},
	"/v1/account/memory/import":            {"memory", []string{"ImportMemory"}},
	"/v1/account/memory/you/{}":            {"memory", []string{"GetMemory", "UpdateMemory", "DeleteMemory"}},
	"/v1/account/memory/projects/{}/{}":    {"memory", []string{"GetMemory", "UpdateMemory", "DeleteMemory"}},
	// One memory by a scope the source does not fix: you, or a project's.
	"/v1/account/memory/{}/{}": {"memory", []string{"GetMemory", "UpdateMemory", "DeleteMemory"}},
	// The memory package's base path followed by a part the source does not
	// fix. The base is a route with calls of its own, so hint's rule for a
	// capability's bare base does not reach it, and every call under it is
	// still the package's.
	"/v1/account/memory{}": {pkg: "memory"},

	"/v1/apps":                              {pkg: "apps"},
	"/v1/apps/apps":                         {"apps", []string{"ListApps", "ListAppsPage", "CreateApp"}},
	"/v1/apps/apps/gate/sessions":           {"apps", []string{"EndGateSessions"}},
	"/v1/apps/apps/{}":                      {"apps", []string{"GetApp", "UpdateApp", "DeleteApp"}},
	"/v1/apps/apps/{}/sessions":             {"apps", []string{"ListAppSessions", "ListAppSessionsPage"}},
	"/v1/apps/apps/{}/visits":               {"apps", []string{"GetAppVisits"}},
	"/v1/apps/apps/{}/frame":                {"apps", []string{"CreateFramePass"}},
	"/v1/apps/apps/{}/listing":              {"apps", []string{"GetListing", "RequestListing", "WithdrawListing"}},
	"/v1/apps/apps/{}/deploys":              {"apps", []string{"ListDeploys"}},
	"/v1/apps/apps/{}/deploys/{}":           {"apps", []string{"GetDeploy"}},
	"/v1/apps/apps/{}/deploys/{}/logs":      {"apps", []string{"ReadDeployLog", "FollowDeployLog"}},
	"/v1/apps/apps/{}/deploys/{}/thumbnail": {"apps", []string{"ReadThumbnail"}},
	"/v1/apps/apps/{}/releases":             {"apps", []string{"ListReleases"}},
	"/v1/apps/apps/{}/releases/{}":          {"apps", []string{"GetRelease"}},
	"/v1/apps/deploys":                      {pkg: "apps"},
	"/v1/apps/deploys/{}":                   {"apps", []string{"GetDeployByID"}},
	"/v1/apps/deploys/{}/app":               {"apps", []string{"GetDeployApp"}},

	"/v1/connections":                           {pkg: "connections"},
	"/v1/connections/connections":               {"connections", []string{"ListConnections", "ListConnectionsPage", "CreateConnection"}},
	"/v1/connections/connections/{}":            {"connections", []string{"GetConnection", "RenameConnection", "DeleteConnection"}},
	"/v1/connections/connections/{}/confirm":    {"connections", []string{"ConfirmConnection"}},
	"/v1/connections/connections/{}/reconnect":  {"connections", []string{"ReconnectConnection"}},
	"/v1/connections/connections/{}/credential": {"connections", []string{"ReplaceCredential"}},
	"/v1/connections/connections/{}/tools":      {"connections", []string{"ListTools"}},
	"/v1/connections/connections/{}/calls":      {"connections", []string{"ListCalls", "ListCallsPage"}},
	"/v1/connections/connectors":                {"connections", []string{"ListConnectors", "ListConnectorsPage", "CreateConnector"}},
	"/v1/connections/connectors/{}":             {"connections", []string{"GetConnector", "UpdateConnector", "DeleteConnector"}},

	"/v1/storage":                     {pkg: "storage"},
	"/v1/storage/files/{}":            {"storage", []string{"ListFiles", "ListFilesPage"}},
	"/v1/storage/files/{}/{}":         {"storage", []string{"ReadFile", "WriteFile", "StatFile", "ListFiles", "ListFilesPage", "GetDownloadURL", "MoveFile", "MoveFolder", "DeleteFile", "DeleteFolder"}},
	"/v1/storage/uploads":             {"storage", []string{"StartUpload", "UploadFile"}},
	"/v1/storage/uploads/{}":          {"storage", []string{"AbortUpload"}},
	"/v1/storage/uploads/{}/complete": {"storage", []string{"CompleteUpload"}},
	"/v1/storage/shares/with-me":      {"storage", []string{"ListSharedWithMe", "ListSharedWithMePage"}},

	"/v1/web":          {pkg: "web"},
	"/v1/web/icons/{}": {"web", []string{"GetIcon"}},
	"/v1/web/images":   {"web", []string{"ReadImage"}},

	"/v1/models":                     {pkg: "llm"},
	"/v1/models/lux/v1/generate":     {"llm", []string{"Generate", "Stream"}},
	"/v1/models/lux/v1/count_tokens": {"llm", []string{"CountTokens"}},
	"/v1/models/lux/v1/models":       {"llm", []string{"ListModels"}},

	"/v1/evals":                            {pkg: "evals"},
	"/v1/evals/metrics":                    {"evals", []string{"ListMetrics", "ListMetricsPage"}},
	"/v1/evals/metrics/{}":                 {"evals", []string{"GetMetric"}},
	"/v1/evals/metrics/{}/versions":        {"evals", []string{"ListMetricVersions", "ListMetricVersionsPage"}},
	"/v1/evals/graders":                    {"evals", []string{"ListGraders", "ListGradersPage"}},
	"/v1/evals/graders/{}":                 {"evals", []string{"GetGrader"}},
	"/v1/evals/graders/{}/versions":        {"evals", []string{"ListGraderVersions", "ListGraderVersionsPage"}},
	"/v1/evals/tasks":                      {"evals", []string{"ListTasks", "ListTasksPage"}},
	"/v1/evals/tasks/{}":                   {"evals", []string{"GetTask"}},
	"/v1/evals/tasks/{}/versions":          {"evals", []string{"ListTaskVersions", "ListTaskVersionsPage"}},
	"/v1/evals/suites":                     {"evals", []string{"ListSuites", "ListSuitesPage"}},
	"/v1/evals/suites/{}":                  {"evals", []string{"GetSuite"}},
	"/v1/evals/suites/{}/versions":         {"evals", []string{"ListSuiteVersions", "ListSuiteVersionsPage"}},
	"/v1/evals/candidates":                 {"evals", []string{"ListCandidates", "ListCandidatesPage"}},
	"/v1/evals/candidates/{}":              {"evals", []string{"GetCandidate"}},
	"/v1/evals/candidates/{}/versions":     {"evals", []string{"ListCandidateVersions", "ListCandidateVersionsPage"}},
	"/v1/evals/experiments":                {"evals", []string{"ListExperiments", "ListExperimentsPage"}},
	"/v1/evals/experiments/{}":             {"evals", []string{"GetExperiment"}},
	"/v1/evals/experiments/{}/cells":       {"evals", []string{"ListCells", "ListCellsPage"}},
	"/v1/evals/experiments/{}/comparisons": {"evals", []string{"ListExperimentComparisons", "ListExperimentComparisonsPage"}},
	"/v1/evals/experiments/{}/stream":      {"evals", []string{"StreamEvents"}},
	// A definition or an experiment by a kind the source does not fix.
	"/v1/evals/{}":             {"evals", []string{"ListMetrics", "ListGraders", "ListTasks", "ListSuites", "ListCandidates", "ListExperiments"}},
	"/v1/evals/{}/{}":          {"evals", []string{"GetMetric", "GetGrader", "GetTask", "GetSuite", "GetCandidate", "GetExperiment"}},
	"/v1/evals/{}/{}/versions": {"evals", []string{"ListMetricVersions", "ListGraderVersions", "ListTaskVersions", "ListSuiteVersions", "ListCandidateVersions"}},
}

// hint is the instead-clause of a finding on route: the SDK call that makes
// it, or the instruction to add one. A route that is a capability's base
// followed by a part the source does not fix, such as a base constant with
// a path a caller passes, names that capability's package: every call under
// it is the package's.
func hint(route string) string {
	c, ok := calls[route]
	if base, tail := strings.CutSuffix(route, unknown); !ok && tail && !strings.HasSuffix(base, "/") {
		if b, found := calls[base]; found && len(b.names) == 0 {
			c, ok = b, true
		}
	}
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
