// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A repo adopting only the required targets writes no config at all, so a
// missing file has to be a success with defaults rather than an error.
func TestAMissingFileIsTheDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("no config should not be an error: %v", err)
	}
	if c.Cover.Threshold != DefaultThreshold {
		t.Errorf("threshold = %v, want %v", c.Cover.Threshold, DefaultThreshold)
	}
	if c.Spec.Dir != DefaultSpecDir {
		t.Errorf("spec.dir = %q, want %q; whether the gate applies is asked of git, not of this", c.Spec.Dir, DefaultSpecDir)
	}
}

func TestLoadReadsEverySection(t *testing.T) {
	dir := write(t, `
cover:
  threshold: 85.5
  trim_prefix: github.com/latere-ai/llmops/
  exempt:
    internal/harness: shells out to a real binary
spec:
  dir: specs
  status: [draft, partial, complete]
  require: [title, status]
  index: specs/README.md
  wikilinks: true
  exclude: [README.md]
hermetic:
  allow: [/usr/bin, /bin]
modernize:
  disable: [newexpr]
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cover.Threshold != 85.5 {
		t.Errorf("threshold = %v", c.Cover.Threshold)
	}
	if why, ok := c.Cover.ExemptFor("github.com/latere-ai/llmops/internal/harness"); !ok || why == "" {
		t.Errorf("ExemptFor = %q, %v; want the reason", why, ok)
	}
	if !c.Spec.Wikilinks || c.Spec.Index != "specs/README.md" {
		t.Errorf("spec section not read: %+v", c.Spec)
	}
	if !c.Spec.IsExcluded("README.md") {
		t.Error("README.md should be excluded")
	}
	if len(c.Hermetic.Allow) != 2 || len(c.Modernize.Disable) != 1 {
		t.Errorf("hermetic/modernize not read: %+v %+v", c.Hermetic, c.Modernize)
	}
}

// The invariant both original implementations stated in comments and got for
// free from a Go map literal: an exemption cannot exist without a reason.
// Moving the data to YAML turns that into a validation rule, so it has to
// fail rather than warn.
func TestAnExemptionWithoutAReasonIsRejected(t *testing.T) {
	dir := write(t, "cover:\n  exempt:\n    internal/thing: \"\"\n    internal/other: \"   \"\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("an exemption with no reason must fail the load")
	}
	for _, want := range []string{"internal/other", "internal/thing", "a decision"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestAThresholdOutsideAPercentageIsRejected(t *testing.T) {
	for _, v := range []string{"-1", "101"} {
		if _, err := Load(write(t, "cover:\n  threshold: "+v+"\n")); err == nil {
			t.Errorf("threshold %s should be rejected", v)
		}
	}
}

// A typo in a key silently disabling a gate is the failure mode this guards:
// `exmept:` would parse as an empty config and report green.
func TestAnUnknownKeyIsRejected(t *testing.T) {
	if _, err := Load(write(t, "cover:\n  exmept:\n    a: b\n")); err == nil {
		t.Fatal("an unknown key must fail rather than silently disable a gate")
	}
}

func TestMalformedYAMLIsAnError(t *testing.T) {
	if _, err := Load(write(t, "cover:\n\tthreshold: [\n")); err == nil {
		t.Fatal("malformed YAML must be an error")
	}
}

func TestAnUnreadableFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a directory in place of the config must be an error")
	}
}

func TestAnEmptyVocabularyAllowsAnyStatus(t *testing.T) {
	if !(Spec{}).AllowsStatus("anything") {
		t.Error("no configured vocabulary should allow any status")
	}
	s := Spec{Status: []string{"draft"}}
	if !s.AllowsStatus("draft") || s.AllowsStatus("shipped") {
		t.Error("a configured vocabulary should be closed")
	}
}

func TestExemptForMissesAnUnlistedPackage(t *testing.T) {
	c := Cover{Exempt: map[string]string{"internal/a": "why"}}
	if _, ok := c.ExemptFor("mod/internal/b"); ok {
		t.Error("internal/b is not exempt")
	}
}

// Admitting a dependency is a decision, in the same way exempting a package
// from the coverage floor is.
func TestADepcheckAllowanceWithoutAReasonIsRejected(t *testing.T) {
	dir := write(t, "depcheck:\n  packages:\n    example.com/m/server:\n      allow:\n        golang.org/x/text: \"\"\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("an allowance with no reason must fail the load")
	}
	for _, want := range []string{"example.com/m/server", "golang.org/x/text", "a decision"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDepcheckIsRead(t *testing.T) {
	c, err := Load(write(t, `
depcheck:
  platforms: [linux/amd64, darwin/arm64]
  packages:
    example.com/m/server:
      decision: 009-D14
      allow:
        golang.org/x/text: Unicode normalisation
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Depcheck.Platforms) != 2 {
		t.Errorf("platforms = %v", c.Depcheck.Platforms)
	}
	g, ok := c.Depcheck.Packages["example.com/m/server"]
	if !ok || g.Decision != "009-D14" || g.Allow["golang.org/x/text"] == "" {
		t.Errorf("packages = %+v", c.Depcheck.Packages)
	}
}

// An entry here says a directory may outlive the test run. Nearly nothing
// should, so the entry has to carry the argument for itself.
func TestATempDirAllowanceWithoutAReasonIsRejected(t *testing.T) {
	dir := write(t, "tempdir:\n  allow:\n    go-build: \"\"\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("an allowance with no reason must fail the load")
	}
	for _, want := range []string{"go-build", "nobody will delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestTempDirIsRead(t *testing.T) {
	c, err := Load(write(t, `
tempdir:
  command: [make, test-all]
  allow:
    go-build: the toolchain cache a -work run keeps on purpose
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.TempDir.Argv(), " "); got != "make test-all" {
		t.Errorf("Argv = %q, want the configured command", got)
	}
	// Prefix matching, because a temporary name carries a random suffix.
	if why, ok := c.TempDir.AllowedFor("go-build4413"); !ok || why == "" {
		t.Errorf("AllowedFor(go-build4413) = %q, %v; want the reason", why, ok)
	}
	if _, ok := c.TempDir.AllowedFor("nanogo-corpus77"); ok {
		t.Error("an unlisted prefix must not be admitted")
	}
}

func TestLicenseIsRead(t *testing.T) {
	c, err := Load(write(t, `
license:
  spdx: AGPL-3.0-or-later
  holder: Latere AI
  extensions: ['.go', '.ts']
  skip: [dist]
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.License.SPDX != "AGPL-3.0-or-later" || c.License.Holder != "Latere AI" {
		t.Errorf("license = %+v", c.License)
	}
	if got := c.License.Exts(); len(got) != 2 || got[0] != ".go" {
		t.Errorf("extensions = %v", got)
	}
	if len(c.License.Skip) != 1 || c.License.Skip[0] != "dist" {
		t.Errorf("skip = %v", c.License.Skip)
	}
}

// Unset means Go, which is what a Go repository needs and the only thing this
// tool can assume about a tree.
func TestAnUnsetExtensionListIsGoOnly(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := c.License.Exts(); len(got) != 1 || got[0] != ".go" {
		t.Errorf("extensions = %v, want [.go]", got)
	}
}

// A file type the scanner has no comment marker for would be walked and never
// matched, which reads as a clean pass over files nobody checked.
func TestAFileTypeWithoutLineCommentsIsRejected(t *testing.T) {
	_, err := Load(write(t, "license:\n  spdx: MIT\n  extensions: ['.go', '.css', '.html']\n"))
	if err == nil {
		t.Fatal("a file type the gate cannot read should be rejected at load")
	}
	if !strings.Contains(err.Error(), ".css") || !strings.Contains(err.Error(), ".html") {
		t.Errorf("both bad types should be named: %v", err)
	}
}

// names go through the same table, so an unlisted one fails at load rather
// than being walked past.
func TestAnUnknownWholeNameIsRejected(t *testing.T) {
	_, err := Load(write(t, "license:\n  spdx: MIT\n  names: [Makefile, Rakefile]\n"))
	if err == nil {
		t.Fatal("a whole name the gate cannot read should be rejected at load")
	}
	if !strings.Contains(err.Error(), "Rakefile") || strings.Contains(err.Error(), "Makefile") {
		t.Errorf("only the unknown name should be rejected: %v", err)
	}
}

// A repository that only names extension-less files still means those, not
// the Go default.
func TestNamesAloneDoNotFallBackToGo(t *testing.T) {
	c, err := Load(write(t, "license:\n  spdx: MIT\n  names: [Makefile]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.License.Exts(); len(got) != 0 {
		t.Errorf("extensions = %v, want none", got)
	}
	if p, ok := c.License.CommentFor("Makefile"); !ok || p != "#" {
		t.Errorf("CommentFor(Makefile) = %q, %v", p, ok)
	}
	if _, ok := c.License.CommentFor("main.go"); ok {
		t.Error("a .go file should not be checked when only names are listed")
	}
}

func TestTheCommentMarkerFollowsTheFileType(t *testing.T) {
	c, err := Load(write(t, "license:\n  spdx: MIT\n  extensions: ['.go', '.sh']\n"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"a.go": "//", "b.sh": "#"} {
		if p, ok := c.License.CommentFor(name); !ok || p != want {
			t.Errorf("CommentFor(%s) = %q, %v; want %q", name, p, ok, want)
		}
	}
}

// A readiness rule with nothing to settle into fires on every started spec
// that has a dependency. A gate that always fails is turned off within the
// day, so the config is refused instead.
func TestStartedWithoutSettledIsRefused(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  started: [complete]\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("started without settled must not load")
	}
	if !strings.Contains(err.Error(), "spec.settled is empty") {
		t.Errorf("the error must name the missing key, got %q", err)
	}
}

// A status the vocabulary does not list can never match, so the rule covers
// fewer specs than reading it suggests.
func TestAReadinessStatusOutsideTheVocabularyIsRefused(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  status: [draft, complete]\n"+
		"  started: [shipped]\n  settled: [complete]\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("a status outside the vocabulary must not load")
	}
	if !strings.Contains(err.Error(), "shipped") {
		t.Errorf("the error must name the unknown status, got %q", err)
	}
}

func TestReadinessLoadsWhenBothAreSet(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  status: [draft, complete]\n"+
		"  numbered: true\n  started: [complete]\n  settled: [complete]\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Spec.Numbered || len(c.Spec.Started) != 1 {
		t.Errorf("the rule keys must survive the load, got %+v", c.Spec)
	}
}

// Not running a gate is a decision, so it carries its reason. D3.
func TestAWaiverWithoutAReasonIsRejected(t *testing.T) {
	dir := write(t, "waive:\n  cover:\n    reason: \"\"\n    until: 2026-11-01\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("a waiver with no reason must fail the load")
	}
	for _, want := range []string{"cover", "does not run it yet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A reason alone becomes wallpaper. The date is what makes retiring the
// waivers one at a time something the tool checks.
func TestAWaiverWithoutADateIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, until string }{
		{"absent", ""},
		{"not a date", "soon"},
		{"wrong format", "01/11/2026"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := write(t, "waive:\n  cover:\n    reason: needs Postgres\n    until: \""+tc.until+"\"\n")
			if _, err := Load(dir); err == nil {
				t.Fatalf("until %q must fail the load: a waiver with no end lowers the bar permanently", tc.until)
			}
		})
	}
}

func TestAWaiverIsRead(t *testing.T) {
	c, err := Load(write(t, `
waive:
  cover:
    reason: the suite needs Postgres and MinIO beside it
    until: 2026-11-01
`))
	if err != nil {
		t.Fatal(err)
	}
	w, ok := c.Waive["cover"]
	if !ok {
		t.Fatal("the waiver must survive the load")
	}
	if _, ok := w.UntilDate(); !ok {
		t.Errorf("until %q must parse", w.Until)
	}
}

// The old key. A repository still carrying it must be told, not silently
// run with the waiver dropped.
func TestTheOldContractKeyIsRejected(t *testing.T) {
	dir := write(t, "contract:\n  exempt:\n    cover:\n      reason: x\n      until: 2026-11-01\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("contract.exempt was renamed to waive; the old key must fail the load")
	}
	if !strings.Contains(err.Error(), "top-level waive map") {
		t.Errorf("the error must say where the key went, got %v", err)
	}
}

// A nil slice is a key the file did not write; an empty one is a key set to
// nothing. Unset takes the shared default and [] is a decision to run every
// fixer. The two must stay distinguishable.
func TestModernizeDisableDefaultsWhenUnsetAndNotWhenEmpty(t *testing.T) {
	unset, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(unset.Modernize.Disable, DefaultDisabledFixers) {
		t.Errorf("unset disable = %v, want the default %v", unset.Modernize.Disable, DefaultDisabledFixers)
	}
	empty, err := Load(write(t, "modernize:\n  disable: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Modernize.Disable) != 0 {
		t.Errorf("disable: [] must run every fixer, got %v", empty.Modernize.Disable)
	}
	if len(empty.Restated) != 0 {
		t.Errorf("[] is not the default, so nothing is restated, got %v", empty.Restated)
	}
}

// Every consumer wrote the same five lines. They are defaults now, and a
// file that still writes them is reported so the line goes away before the
// next default change makes it wrong.
func TestARestatedDefaultIsRecorded(t *testing.T) {
	dir := write(t, `
cover:
  threshold: 90.0
modernize:
  disable: [errorsastype, newexpr]
spec:
  dir: specs
  require: [status, title]
  index: specs/README.md
golangci:
  sloglint:
    context: scope
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cover.threshold", "modernize.disable", "spec.dir", "spec.require", "spec.index", "golangci.sloglint"}
	if !sameSet(c.Restated, want) {
		t.Errorf("restated = %v, want %v", c.Restated, want)
	}

	// A decision that differs from the default is not restating it.
	c, err = Load(write(t, "spec:\n  require: [title, status, created]\ngolangci:\n  sloglint:\n    context: all\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Restated) != 0 {
		t.Errorf("a value that differs from the default is not restated, got %v", c.Restated)
	}
}

// The spec defaults apply so a tree with specs needs no spec section; the
// index defaults only when the file exists, because an index that is
// configured and absent fails the lint.
func TestSpecDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Dir != DefaultSpecDir || !sameSet(c.Spec.Require, DefaultSpecRequire) {
		t.Errorf("spec defaults = dir %q require %v", c.Spec.Dir, c.Spec.Require)
	}
	if c.Spec.Index != "" {
		t.Errorf("no README exists, so no index defaults; got %q", c.Spec.Index)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultSpecIndex), []byte("# Specs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Index != DefaultSpecIndex {
		t.Errorf("index = %q, want %q when the file exists", c.Spec.Index, DefaultSpecIndex)
	}
	if c.Golangci.Sloglint == nil || c.Golangci.Sloglint.Context != DefaultSloglintContext {
		t.Errorf("sloglint must default to %q everywhere, got %+v", DefaultSloglintContext, c.Golangci.Sloglint)
	}
}

// An archive nobody can be sent to is read on every run and files nothing,
// which reports green while every finished spec sits at the root.
func TestAnArchiveWithNoTerminalStatusIsRefused(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  archive:\n    dir: .archive\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("an archive with no terminal status must not load")
	}
	if !strings.Contains(err.Error(), "spec.archive.statuses is empty") {
		t.Errorf("the error must name the missing key, got %q", err)
	}
}

// The mirror: statuses declared terminal with no archive to send them to.
func TestTerminalStatusesWithoutAnArchiveDirAreRefused(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  status: [draft, complete]\n"+
		"  archive:\n    statuses: [complete]\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("terminal statuses with no archive dir must not load")
	}
	if !strings.Contains(err.Error(), "spec.archive.dir is empty") {
		t.Errorf("the error must name the missing key, got %q", err)
	}
}

// A terminal status no spec can hold sends nothing to the archive.
func TestATerminalStatusOutsideTheVocabularyIsRefused(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  status: [draft, complete]\n"+
		"  archive:\n    dir: .archive\n    statuses: [retired]\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("a terminal status outside the vocabulary must not load")
	}
	if !strings.Contains(err.Error(), "retired") {
		t.Errorf("the error must name the unknown status, got %q", err)
	}
}

func TestTheArchiveRuleLoadsWhenBothAreSet(t *testing.T) {
	dir := write(t, "spec:\n  dir: specs\n  status: [draft, complete, superseded]\n"+
		"  archive:\n    dir: .archive\n    statuses: [complete, superseded]\n")
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Spec.Archive.Enabled() || !c.Spec.Archive.IsTerminal("superseded") {
		t.Errorf("the archive keys must survive the load, got %+v", c.Spec.Archive)
	}
	if c.Spec.Archive.IsTerminal("draft") {
		t.Error("a working status must not read as terminal")
	}
}

func TestRaceTimeoutMustBeAPositiveDuration(t *testing.T) {
	for _, bad := range []string{"soon", "-5m", "0"} {
		if _, err := Load(write(t, "race:\n  timeout: "+bad+"\n")); err == nil {
			t.Errorf("race.timeout %q must fail the load", bad)
		}
	}
	c, err := Load(write(t, "race:\n  timeout: 45m\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Race.Timeout != "45m" {
		t.Errorf("timeout = %q", c.Race.Timeout)
	}
}
