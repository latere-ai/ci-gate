// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package golangci

import (
	"fmt"
	"slices"

	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"latere.ai/x/ci-gate/internal/config"
)

func mustRender(t *testing.T, module string, disable []string, sl *config.Sloglint) string {
	t.Helper()
	got, err := Render(module, disable, sl, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRenderCarriesTheModulePath(t *testing.T) {
	got := mustRender(t, "github.com/latere-ai/tgo", nil, nil)
	if !strings.Contains(got, "- github.com/latere-ai/tgo") {
		t.Errorf("the module path is the one thing that differs per repo:\n%s", got)
	}
	if !strings.Contains(got, "Do not edit") {
		t.Error("a generated file must say so")
	}
	// A reader who finds the file wrong needs to know it is generated, that
	// committing it is a mistake, and where the real source is.
	for _, want := range []string{"do not commit", "gitignored", "ci-gate", ".lateregate.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("the header must mention %q:\n%s", want, got)
		}
	}
}

// The modernize linter and the toolchain's fixers are the same analyzers, so
// both read one list.
func TestRenderDisablesWhatTheConfigDisables(t *testing.T) {
	got := mustRender(t, "m", []string{"newexpr", "errorsastype"}, nil)
	for _, f := range []string{"- newexpr", "- errorsastype"} {
		if !strings.Contains(got, f) {
			t.Errorf("%s missing:\n%s", f, got)
		}
	}
}

func TestRenderOmitsTheModernizeBlockWhenNothingIsDisabled(t *testing.T) {
	// govet carries a disable list of its own, so the assertion is on the
	// modernize block rather than on the word appearing anywhere.
	if got := mustRender(t, "m", nil, nil); strings.Contains(got, "modernize:") {
		t.Errorf("an empty list should produce no modernize settings block:\n%s", got)
	}
}

// The shared set is the org's bar. Each of these was carried by one
// repository's own config before this file existed, and a repository may add
// to the set through golangci.extra but never subtract from it.
func TestRenderCarriesTheSharedBar(t *testing.T) {
	got := mustRender(t, "m", nil, nil)
	for what, want := range map[string]string{
		"repeated findings are not capped": "max-same-issues: 0",
		"a test helper is not a leak":      "bodyclose",
		"a linter's findings are not cut":  "max-issues-per-linter: 0",
		"unchecked type assertions":        "check-type-assertions: true",
		"every vet analyzer":               "enable-all: true",
		"staticcheck beyond the default":   "- -ST1000",
		"the deprecated io helper":         "pkg: io/ioutil",
		"the superseded random source":     "pkg: math/rand",
		"generated code is not ours":       "generated: lax",
		"a frontend's vendored Go":         "node_modules",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the shared set must gate %s (%q missing):\n%s", what, want, got)
		}
	}
}

// slog would be caught by the "log" denial's prefix match unless it is
// allowed by name, and a repository that lost structured logging that way
// would find out one finding at a time.
func TestTheStructuredLoggerSurvivesTheLogDenial(t *testing.T) {
	got := mustRender(t, "m", nil, nil)
	if !strings.Contains(got, "- log/slog") {
		t.Errorf("log/slog must be allowed by name:\n%s", got)
	}
	if !strings.Contains(got, "pkg: log\n") {
		t.Errorf("the unstructured logger must still be denied:\n%s", got)
	}
}

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Regenerating on every run is what makes drift impossible: a hand edit does
// not survive the next Write, so there is nothing to detect.
func TestWriteOverwritesAHandEdit(t *testing.T) {
	root := repo(t, nil)
	cfg := &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}
	if _, err := Write(root, cfg, "go"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, Name)
	first, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte("version: \"2\"\n# hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, cfg, "go"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != string(first) {
		t.Error("a hand edit must not survive regeneration")
	}
	if strings.Contains(string(again), "hand edit") {
		t.Error("the edit is still there")
	}
}

// Changing the disabled list moves every repository on the next run, which is
// the point of one source.
func TestWriteFollowsTheDisabledList(t *testing.T) {
	root := repo(t, nil)
	if _, err := Write(root, &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}, "go"); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr", "errorsastype"}}}, "go"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, Name))
	if !strings.Contains(string(got), "errorsastype") {
		t.Errorf("the second render should carry the new list:\n%s", got)
	}
}

// A generated file that is also committed is two sources of truth that agree
// until one is edited, which is the shape this design exists to avoid.
func TestWriteRefusesATrackedFile(t *testing.T) {
	root := repo(t, nil)
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"add", ".golangci.yml"},
	} {
		if len(args) == 2 && args[0] == "add" {
			if err := os.WriteFile(filepath.Join(root, Name), []byte("version: \"2\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if err := cmd.Run(); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}
	_, err := Write(root, &config.Config{}, "go")
	if err == nil {
		t.Fatal("writing over a tracked file must fail")
	}
	for _, want := range []string{"tracked by git", "gitignored", "git rm --cached"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should say %q, got %v", want, err)
		}
	}
}

func TestAMissingModuleIsAnError(t *testing.T) {
	if _, err := ModulePath("go", t.TempDir()); err == nil {
		t.Fatal("a directory with no module must be an error")
	}
	if _, err := ModulePath("no-such-go-binary", "."); err == nil {
		t.Fatal("a missing toolchain must be an error")
	}
}

func TestWriteReportsAnUnwritablePath(t *testing.T) {
	root := repo(t, nil)
	if err := os.Mkdir(filepath.Join(root, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, &config.Config{}, "go"); err == nil {
		t.Fatal("an unwritable path must be an error")
	}
}

// The set is the point: errcheck across four repositories surfaced 452
// discarded errors and several real bugs, so a render that quietly loses a
// linter would undo that.
func TestRenderCarriesTheWholeLinterSet(t *testing.T) {
	got := mustRender(t, "m", nil, nil)
	for _, linter := range []string{"errcheck", "govet", "ineffassign", "staticcheck", "unused", "modernize", "depguard"} {
		if !strings.Contains(got, "- "+linter) {
			t.Errorf("%s missing from the rendered set:\n%s", linter, got)
		}
	}
	if !strings.Contains(got, "default: none") {
		t.Error("the set must be explicit, not inherited from a golangci-lint default that can move")
	}
}

// depguard's settings must survive even when no modernize fixer is disabled,
// because the two share the settings block.
func TestTheTestifyBanSurvivesAnEmptyDisableList(t *testing.T) {
	got := mustRender(t, "m", nil, nil)
	if !strings.Contains(got, "no-testify") || !strings.Contains(got, "stretchr/testify") {
		t.Errorf("the testify ban is org policy, not per repo:\n%s", got)
	}
}

// A repository with real policy of its own keeps its config, and says so. The
// alternative is an exception nobody declared, which reads the same as one
// nobody got around to.
func TestAnOwnedConfigIsReportedAndNotOverwritten(t *testing.T) {
	root := repo(t, map[string]string{Name: "version: \"2\"\n# hand written\n"})
	cfg := &config.Config{Golangci: config.Golangci{Own: "a 50-word spelling policy"}}
	reason, err := Own(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "a 50-word spelling policy" {
		t.Errorf("reason = %q", reason)
	}
	body, _ := os.ReadFile(filepath.Join(root, Name))
	if !strings.Contains(string(body), "hand written") {
		t.Error("an owned config must not be touched")
	}
}

// A declared exception pointing at nothing is a repository that lost its
// config and did not notice.
func TestAnOwnedConfigThatIsMissingFails(t *testing.T) {
	cfg := &config.Config{Golangci: config.Golangci{Own: "reasons"}}
	if _, err := Own(repo(t, nil), cfg); err == nil {
		t.Fatal("claiming to own a file that does not exist must fail")
	}
}

func TestNoClaimMeansGenerate(t *testing.T) {
	reason, err := Own(repo(t, nil), &config.Config{})
	if err != nil || reason != "" {
		t.Errorf("Own = %q, %v; want the generate path", reason, err)
	}
}

// The scope is the whole reason sloglint is config rather than template: every
// repository's request path is its own.
//
// Asserted against the parsed document rather than the text, because the
// marshaller's key order is its business and not the contract.
func TestSloglintIsRenderedWithItsScope(t *testing.T) {
	doc := parseRendered(t, mustRender(t, "m", nil, &config.Sloglint{
		Context: "all", RequestPaths: "internal/(handler|server)/",
	}))
	linters := doc["linters"].(map[string]any)
	if !slices.Contains(anyStrings(linters["enable"]), "sloglint") {
		t.Errorf("sloglint not enabled: %v", linters["enable"])
	}
	if got := linters["settings"].(map[string]any)["sloglint"].(map[string]any)["context"]; got != "all" {
		t.Errorf("context = %v, want all", got)
	}
	scope := ruleWith(t, linters, "path-except")
	if scope["path-except"] != "internal/(handler|server)/" {
		t.Errorf("scope = %v", scope["path-except"])
	}
	if !slices.Contains(anyStrings(scope["linters"]), "sloglint") {
		t.Errorf("the exclusion must name sloglint: %v", scope["linters"])
	}
}

// sandbox exempts a sweeper and a store that live under its request path but
// do not serve requests, so a second exclusion has to survive.
func TestSloglintCarriesFurtherExemptPaths(t *testing.T) {
	doc := parseRendered(t, mustRender(t, "m", nil, &config.Sloglint{
		Context:      "scope",
		RequestPaths: "internal/http/(web|api)/",
		Exempt:       []string{`internal/http/api/(runs_sweeper|runs_store)\.go`},
	}))
	linters := doc["linters"].(map[string]any)
	var exempt any
	for _, r := range linters["exclusions"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if slices.Contains(anyStrings(m["linters"]), "sloglint") && m["path"] != nil {
			exempt = m["path"]
		}
	}
	if exempt != `internal/http/api/(runs_sweeper|runs_store)\.go` {
		t.Errorf("exempt path = %v", exempt)
	}
	if got := linters["settings"].(map[string]any)["sloglint"].(map[string]any)["context"]; got != "scope" {
		t.Errorf("context = %v, want scope", got)
	}
}

// A repository whose lint policy is genuinely its own keeps it in
// .lateregate.yaml rather than a second file. Maps merge, enable appends.
func TestExtraMergesOverTheSharedSet(t *testing.T) {
	got, err := Render("m", nil, nil, map[string]any{
		"linters": map[string]any{
			"enable": []any{"bodyclose", "gosec"},
			"settings": map[string]any{
				"gosec": map[string]any{"excludes": []any{"G301"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	linters := parseRendered(t, got)["linters"].(map[string]any)
	enabled := anyStrings(linters["enable"])
	for _, want := range []string{"errcheck", "modernize", "bodyclose", "gosec"} {
		if !slices.Contains(enabled, want) {
			t.Errorf("%s missing; enable appends, it does not replace: %v", want, enabled)
		}
	}
	settings := linters["settings"].(map[string]any)
	if _, ok := settings["depguard"]; !ok {
		t.Error("merging a settings key must not drop the shared ones")
	}
	if got := settings["gosec"].(map[string]any)["excludes"]; len(anyStrings(got)) != 1 {
		t.Errorf("gosec excludes = %v", got)
	}
}

func parseRendered(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("rendered config does not parse: %v\n%s", err, s)
	}
	return doc
}

func anyStrings(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		out = append(out, fmt.Sprintf("%v", e))
	}
	return out
}

// A repository that does not serve requests gets no sloglint, and no rule
// mentioning it -- the shared test-file exclusion stays, because that one is
// not about sloglint at all.
func TestNoSloglintMeansNoSloglintRule(t *testing.T) {
	doc := parseRendered(t, mustRender(t, "m", nil, nil))
	linters := doc["linters"].(map[string]any)
	if slices.Contains(anyStrings(linters["enable"]), "sloglint") {
		t.Error("sloglint enabled without configuration")
	}
	if _, ok := linters["settings"].(map[string]any)["sloglint"]; ok {
		t.Error("sloglint settings emitted without configuration")
	}
	for _, r := range linters["exclusions"].(map[string]any)["rules"].([]any) {
		if slices.Contains(anyStrings(r.(map[string]any)["linters"]), "sloglint") {
			t.Errorf("a sloglint exclusion was emitted without configuration: %v", r)
		}
	}
}

// ruleWith returns the first exclusion rule carrying a key, so the tests do not
// depend on the order rules happen to be emitted in.
func ruleWith(t *testing.T, linters map[string]any, key string) map[string]any {
	t.Helper()
	for _, r := range linters["exclusions"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if _, ok := m[key]; ok {
			return m
		}
	}
	t.Fatalf("no exclusion rule with %q", key)
	return nil
}

// Tests are excluded from the linters that fire on deliberate error paths and
// constructed fixture paths, for every repository rather than one.
func TestTestFilesAreExcludedFromTheDeliberateErrorLinters(t *testing.T) {
	linters := parseRendered(t, mustRender(t, "m", nil, nil))["linters"].(map[string]any)
	rule := ruleWith(t, linters, "path")
	if rule["path"] != `_test\.go` {
		t.Errorf("path = %v", rule["path"])
	}
	for _, want := range []string{"errcheck", "noctx", "errchkjson"} {
		if !slices.Contains(anyStrings(rule["linters"]), want) {
			t.Errorf("%s should be excluded in tests: %v", want, rule["linters"])
		}
	}
}
