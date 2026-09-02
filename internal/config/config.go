// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package config reads .lateregate.yaml, the one file a consumer repo uses
// to configure every gate.
//
// One file rather than one per check, because a repository has a single
// quality bar and splitting it across files makes the bar hard to read. The
// file lives in the consumer repo rather than in this one: a gate that needs
// a second checkout to know its own threshold is a gate that only runs in CI,
// and the whole point is that it runs on a laptop first.
//
// Every section is optional. A repo that only wants the required targets
// needs no file at all, and Load returns defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// Name is the file every consumer repo carries at its root.
const Name = ".lateregate.yaml"

// DefaultThreshold is the per-package coverage floor when a repo does not
// set one. Ninety, because that is the bar both repos this tool was
// extracted from already held.
const DefaultThreshold = 90.0

// Config is the whole of .lateregate.yaml.
type Config struct {
	Cover     Cover     `yaml:"cover"`
	Spec      Spec      `yaml:"spec"`
	Hermetic  Hermetic  `yaml:"hermetic"`
	Race      Race      `yaml:"race"`
	Modernize Modernize `yaml:"modernize"`
	Depcheck  Depcheck  `yaml:"depcheck"`
	Golangci  Golangci  `yaml:"golangci"`
	CgoFree   CgoFree   `yaml:"cgo_free"`
	TempDir   TempDir   `yaml:"tempdir"`
	License   License   `yaml:"license"`

	OtelClient OtelClient `yaml:"otel_client"`

	// Waive maps a gate name to the decision not to run it yet. It is the
	// only way a gate that applies to this repository does not run, and
	// every entry is temporary by construction.
	Waive map[string]Waiver `yaml:"waive"`

	// Restated lists the keys the file set to their own default. A key that
	// restates a default is a line the next default change makes wrong, so
	// the drift report names it. Computed by Load; not part of the file.
	Restated []string `yaml:"-"`
}

// DefaultDisabledFixers are the go fix modernizers every repository turns
// off. newexpr rewrites pointer helpers to new(v) and inlines call sites
// unevenly, leaving the helper unused and giving an untyped constant the
// wrong type; errorsastype rewrites errors.As to errors.AsType[T], whose
// constraint a local non-error interface target does not satisfy, so the
// result does not compile. Both re-propose on every run.
var DefaultDisabledFixers = []string{"newexpr", "errorsastype"}

// DefaultSpecDir is where a spec tree lives. The gate applies when git
// tracks files under it.
const DefaultSpecDir = "specs"

// DefaultSpecRequire are the frontmatter keys every spec carries.
var DefaultSpecRequire = []string{"title", "status"}

// DefaultSpecIndex is the index a tree keeps when it keeps one.
const DefaultSpecIndex = "specs/README.md"

// DefaultSloglintContext is sloglint's setting for every package: where a
// context is in hand, the *Context variant is right whether or not the
// package serves requests, so the default is unscoped.
const DefaultSloglintContext = "scope"

// Waiver is one gate a repository does not run yet.
//
// Both fields are mandatory and the reason is only half of it. A reason alone
// becomes wallpaper: seventeen well-argued waivers is not a bar, it is a bar
// written down and abandoned. The date is what makes retiring them one at a
// time a thing the tool checks rather than a thing somebody remembers.
type Waiver struct {
	Reason string `yaml:"reason"`
	// Until is the last day the exemption works, as YYYY-MM-DD, and it is
	// inclusive: "until: 2026-11-01" covers all of 1 November. Somebody
	// writing that means they have until then, and a date that quietly meant
	// the day before would be read wrong by everyone who writes one.
	//
	// After it the gate runs and fails on its own terms, so the reason for
	// the work is in the gate's own output rather than in a date.
	Until string `yaml:"until"`
}

// UntilDate parses Until. The zero time means it did not parse, which
// validate rejects before any gate reads it.
func (w Waiver) UntilDate() (time.Time, bool) {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(w.Until))
	return t, err == nil
}

// License configures the per-file licence notice gate.
//
// There is no default for any of it. Every other gate here defaults to
// something sensible so a repository adopts it without config, but a licence
// guessed on a repository's behalf would be printed into every file it has,
// and a wrong identifier in 300 files is worse than none. The declaration is
// the point: the gate asserts that a person decided, and that the tree agrees
// with what they decided.
type License struct {
	// SPDX is the identifier every file must carry, e.g. "MIT" or
	// "AGPL-3.0-or-later". Unset makes the gate fail rather than pass.
	SPDX string `yaml:"spdx"`
	// Holder is the copyright holder the notice names, matched literally.
	// One holder per repository, because the field that decides who may
	// relicense is not one to leave to whoever wrote the file.
	Holder string `yaml:"holder"`
	// Extensions are the file types checked. Empty means Go only, which is
	// what a Go repository needs and the only thing this tool can assume.
	Extensions []string `yaml:"extensions"`
	// Names are files checked by their whole name rather than an extension,
	// for the ones that have none: Makefile, Dockerfile, a hook script.
	Names []string `yaml:"names"`
	// Skip names directories the scan does not enter, besides .git and
	// node_modules. Generated output belongs here; source does not.
	Skip []string `yaml:"skip"`
}

// LineComment maps a file type to the marker its line comments start with.
// A type outside this table is rejected by Load rather than skipped at scan
// time, so a repository cannot believe it is checking files that it is not.
var LineComment = map[string]string{
	".go": "//", ".ts": "//", ".tsx": "//", ".js": "//", ".mjs": "//",
	".cjs": "//", ".jsx": "//", ".rs": "//", ".java": "//", ".kt": "//",
	".swift": "//", ".c": "//", ".h": "//", ".cc": "//", ".cpp": "//",
	".hpp": "//", ".proto": "//", ".scss": "//",

	".sh": "#", ".bash": "#", ".zsh": "#", ".py": "#", ".rb": "#",
	".pl": "#", ".yaml": "#", ".yml": "#", ".toml": "#", ".tf": "#",
	"Makefile": "#", "Dockerfile": "#", "Justfile": "#",
}

// Exts is the extension list with the Go default applied.
func (l License) Exts() []string {
	if len(l.Extensions) == 0 && len(l.Names) == 0 {
		return []string{".go"}
	}
	return l.Extensions
}

// CommentFor reports the comment marker for a file name, and whether the file
// is checked at all. A whole name is matched before an extension, so a
// Dockerfile is found and a Dockerfile.dev is not unless it is listed too.
func (l License) CommentFor(name string) (string, bool) {
	if slices.Contains(l.Names, name) {
		return LineComment[name], true
	}
	ext := filepath.Ext(name)
	if !slices.Contains(l.Exts(), ext) {
		return "", false
	}
	return LineComment[ext], true
}

// Checked names every file type the gate looks at, for the message it prints
// when the scan matched nothing.
func (l License) Checked() []string {
	return append(slices.Clone(l.Exts()), l.Names...)
}

// Cover configures the per-package coverage gate.
type Cover struct {
	// Threshold is the floor every non-exempt package clears.
	Threshold float64 `yaml:"threshold"`
	// Exempt maps a package suffix to the reason it does not have to clear
	// the floor. The value is the reason, so an entry cannot exist without
	// one: a package exempted without a reason is a package nobody decided
	// to exempt. Load rejects an empty value.
	Exempt map[string]string `yaml:"exempt"`
	// TrimPrefix is dropped from package paths in the report so the output
	// fits a terminal. Defaults to the module path.
	TrimPrefix string `yaml:"trim_prefix"`
}

// Spec configures the spec-tree linter.
type Spec struct {
	// Dir holds the spec files. Empty disables spec-lint entirely.
	Dir string `yaml:"dir"`
	// Status is the closed vocabulary a spec's status must come from. Empty
	// means any value passes.
	Status []string `yaml:"status"`
	// Require lists frontmatter keys every spec must carry with a non-empty
	// value.
	Require []string `yaml:"require"`
	// Index is a Markdown file whose table rows must agree with the specs
	// they link to. Empty disables the index checks.
	Index string `yaml:"index"`
	// Wikilinks turns on [[name]] resolution against Dir.
	Wikilinks bool `yaml:"wikilinks"`
	// Exclude lists file names in Dir that are not specs.
	Exclude []string `yaml:"exclude"`
	// Vocabulary closes further frontmatter keys the way Status closes
	// `status`: a key here must hold one of the listed values.
	Vocabulary map[string][]string `yaml:"vocabulary"`
	// StatusRequires ties a frontmatter key to a status. The key must be
	// present when the spec has that status and absent otherwise, which makes
	// the status carry information rather than decorate the file.
	StatusRequires map[string]StatusRule `yaml:"status_requires"`
	// RequireSection lists headings every spec must carry.
	RequireSection []string `yaml:"require_section"`
	// RequireSectionByStatus lists headings a spec must carry once it reaches
	// a given status. A spec that claims to be built and does not say what
	// happened leaves the claim unchecked.
	RequireSectionByStatus map[string][]string `yaml:"require_section_by_status"`
	// SectionExempt names files the section rules do not apply to.
	SectionExempt []string `yaml:"section_exempt"`
	// ScopedIDs matches identifiers that belong to the spec they appear in.
	// A mismatched id means a record was copied between specs, which silently
	// gives two different decisions one name.
	ScopedIDs string `yaml:"scoped_ids"`
	// Tables turns on the table well-formedness check.
	Tables bool `yaml:"tables"`
	// Register gates a table of identifiers that one spec defines and the
	// rest of the tree cites.
	Register Register `yaml:"register"`
	// StatusLinkedFrom requires a spec at a status to be linked from a named
	// file. A status that means "the outcome is recorded over there" is not
	// checked by the section rule alone.
	StatusLinkedFrom map[string]string `yaml:"status_linked_from"`
	// Marker gates a paragraph whose content has to differ between two
	// statuses, which is what makes the pair exhaustive in both directions.
	Marker Marker `yaml:"status_marker"`
	// Numbered requires every spec file to be named NNN-name.md, and no two
	// to carry the same number. The number is what every citation resolves
	// through, so reusing one silently repoints the citations that exist.
	Numbered bool `yaml:"numbered"`
	// Started names the statuses at which work on a spec has begun. A spec at
	// one of them whose dependency has not reached Settled was built ahead of
	// the design it depends on, and the tree then records an ordering that
	// never happened. Empty turns the rule off.
	Started []string `yaml:"started"`
	// Settled names the statuses at which a dependency no longer blocks. A
	// superseded spec settles because its work moved to the spec that
	// replaced it, which carries its own edges.
	Settled []string `yaml:"settled"`
	// Archive describes where finished specs go and which statuses send them
	// there.
	Archive Archive `yaml:"archive"`
}

// Archive describes the subdirectory a tree retires finished specs into.
//
// Every tree here already has one and no rule ever reached it: Load globs
// Dir/*.md, so a subdirectory is invisible, and the statuses inside drifted
// into free text because the vocabulary check never saw them. One tree holds
// fifteen distinct archived statuses, one of them a sentence.
type Archive struct {
	// Dir is the archive, relative to Dir. Empty disables the archive rules,
	// the way an empty Index disables the index rules.
	Dir string `yaml:"dir"`
	// Statuses are the ones that mean the work is over, so the spec belongs
	// in the archive rather than beside the specs still being built.
	//
	// There is no default. What is terminal is a property of the tree's own
	// vocabulary: `implemented` means "shipped, follow-on work outstanding"
	// in one tree here and "done" in another, and a guess would move specs
	// somebody deliberately kept at the root.
	Statuses []string `yaml:"statuses"`
}

// Enabled reports whether the archive rules run.
func (a Archive) Enabled() bool { return a.Dir != "" }

// IsTerminal reports whether a status means the spec belongs in the archive.
func (a Archive) IsTerminal(status string) bool {
	return slices.Contains(a.Statuses, status)
}

// Marker gates a paragraph that a status must carry, and what it must say.
//
// Two statuses that mean different things but read the same leave a reader
// guessing, and a spec sitting at the earlier one hides that it is finished.
// The pattern's first group is the text compared against Expect and Reject.
type Marker struct {
	// Pattern matches the paragraph; group 1 is the text under test.
	Pattern string `yaml:"pattern"`
	// Required lists the statuses that must carry it at all.
	Required []string `yaml:"required"`
	// Expect maps a status to a pattern group 1 must match.
	Expect map[string]string `yaml:"expect"`
	// Reject maps a status to a pattern group 1 must not match. Go's regexp
	// has no negative lookahead, so the two directions are separate fields.
	Reject map[string]string `yaml:"reject"`
}

// Register describes an id table one spec owns.
//
// A tree that numbers something -- conformance rows, requirements, risks --
// and cites those numbers from elsewhere has two failures nothing else sees:
// a gap, which means a row was deleted and the numbers after it now mean
// something different, and a citation of a row that does not exist.
type Register struct {
	// File is the spec that defines the rows. Empty disables the check.
	File string `yaml:"file"`
	// Define matches a defining row; its first group is the id.
	Define string `yaml:"define"`
	// Cite matches a citation elsewhere in the tree. The first non-empty
	// group is the id, so one pattern can carry several citation shapes.
	Cite string `yaml:"cite"`
	// Sequential requires the ids to run from 1 with no gaps, given Prefix.
	Sequential bool `yaml:"sequential"`
	// Prefix is the letter the numbers carry, e.g. "C" for C1, C2.
	Prefix string `yaml:"prefix"`
}

// StatusRule is one status-to-frontmatter-key rule.
type StatusRule struct {
	// Field is the frontmatter key the status requires.
	Field string `yaml:"field"`
	// Match, when set, is a pattern the field's value must satisfy. It exists
	// because a field can be filled in with something that is not a record
	// anyone can act on.
	Match string `yaml:"match"`
	// Hint is added to the failure so a reader learns what a good value looks
	// like rather than only that theirs was rejected.
	Hint string `yaml:"hint"`
}

// Hermetic configures the PATH-stripped test run.
type Hermetic struct {
	// Allow lists directories kept on PATH besides the Go toolchain's own.
	// Empty is the strictest setting and the default; a repo whose tests
	// legitimately need a system tool names the directory here, which makes
	// the dependency visible instead of ambient.
	Allow []string `yaml:"allow"`
}

// Race configures the race-detector run.
type Race struct {
	// Timeout is the go test -timeout for the race run, as a duration such as
	// 45m. Empty leaves the toolchain's default. It is a budget a repository
	// sets for itself rather than a guard against a hung test: the detector
	// costs an order of magnitude on arithmetic-heavy suites, and a suite that
	// legitimately needs 40 minutes under it should say so here rather than
	// in a recipe nobody else runs.
	Timeout string `yaml:"timeout"`
}

// Depcheck configures the dependency-footprint gate.
type Depcheck struct {
	// Platforms are the GOOS/GOARCH pairs the build list is taken on. What a
	// repository claims to build for is what has to be checked: a dependency
	// reached only on darwin is invisible to a linux-only gate. Empty means
	// the host's own platform.
	Platforms []string `yaml:"platforms"`
	// Packages maps an import path to what its build may reach.
	Packages map[string]Gated `yaml:"packages"`
}

// Gated is one package's allowlist and the decision that owns it.
type Gated struct {
	// Decision names the record that argues for this list, so a failure sends
	// a reader to the argument rather than to the config.
	Decision string `yaml:"decision"`
	// Allow maps an import-path prefix to why the build may reach it. A prefix
	// rather than an exact package: a dependency's own internal subpackages
	// are its business, and pinning each would make an upstream refactor a
	// failure here without any new dependency.
	Allow map[string]string `yaml:"allow"`
}

// Golangci configures the lint-config gate.
type Golangci struct {
	// Sloglint turns on log-trace correlation, scoped to the packages that
	// serve requests. It is here rather than in the shared template because
	// the scope is the one part no template can know: every repository's
	// request path is its own.
	Sloglint *Sloglint `yaml:"sloglint"`
	// Extra is merged into the rendered configuration, so a repository whose
	// lint policy is genuinely its own does not need a second file to hold it.
	// Maps merge key by key, linters.enable appends to the shared set, and any
	// other list replaces.
	//
	// The reasoning goes here, beside the values. That is the trade: the
	// rendered file becomes machine output nobody reads, and the argument for
	// a gosec exclusion or a spelling rule lives where someone editing it will
	// look.
	Extra map[string]any `yaml:"extra"`
	// Own declares that this repository keeps its own .golangci.yml, and why.
	//
	// The shared config exists because most repositories had nothing
	// repo-specific to say and four identical copies of saying it. A few do
	// have something to say -- a revive rule set, a gosec exclusion with a
	// reason, a spelling policy -- and rendering those from here would either
	// lose them or turn this file into a second golangci-lint config.
	//
	// So the exception is allowed and declared. An undeclared exception is
	// indistinguishable from nobody having got around to it, which is what
	// this field fixes: the reason is in the repo, and lint-config checks the
	// file is really there rather than silently doing nothing.
	Own string `yaml:"own"`
}

// Sloglint configures the log-trace correlation linter.
//
// Every slog call on a request path must use the *Context variant, so the
// otelslog bridge can attach a trace and span id to the record. Off the
// request path -- startup, shutdown, background work -- there is no trace to
// correlate with and a plain call is right, which is why this is scoped
// rather than global.
type Sloglint struct {
	// Context is sloglint's own setting: "all" requires the *Context variant
	// everywhere in scope, "scope" only where a context is already in hand.
	Context string `yaml:"context"`
	// RequestPaths is a regexp for the packages that serve requests. Only
	// those are checked.
	RequestPaths string `yaml:"request_paths"`
	// Exempt lists further paths inside RequestPaths that are not request
	// handling -- a sweeper or a store that happens to live there.
	Exempt []string `yaml:"exempt"`
}

// CgoFree configures the cgo-free gate.
type CgoFree struct {
	// Skip names directories the scan does not enter, besides .git and
	// testdata. Keep it short: a directory skipped here is one the promise
	// does not cover.
	Skip []string `yaml:"skip"`
}

// OtelClient configures the outbound-client instrumentation gate.
type OtelClient struct {
	// Skip names directories the scan does not enter, besides .git,
	// .claude, testdata and node_modules. A directory skipped here is one whose
	// outbound calls nobody is asserting anything about, so keep it short.
	// Prefer skipping a build-tagged harness over a directory of real code.
	Skip []string `yaml:"skip"`
}

// TempDir configures the temporary-directory leak gate.
type TempDir struct {
	// Command is the test run the gate watches. Empty means `go test ./...`.
	//
	// It is configurable because the property has nothing to do with Go: a
	// process that makes a directory under TMPDIR and exits without removing
	// it has leaked it, whatever language it was written in. A repo whose
	// suite is pytest or cargo names its own runner here.
	//
	// Name the run that exercises the most code. A leak the gate never
	// executes is a leak it reports as absent, and the slow suites are
	// exactly the ones that build caches worth gigabytes.
	Command []string `yaml:"command"`

	// Allow maps the prefix of a surviving entry to why it may survive.
	//
	// The value is the reason, as in cover.exempt, and Load rejects an empty
	// one. Almost nothing belongs here: a directory that outlives the run is
	// a directory nobody will ever delete. The honest entry is a run that
	// deliberately keeps its work, such as a `go build -work` under test.
	Allow map[string]string `yaml:"allow"`
}

// AllowedFor reports whether a surviving entry was admitted, and why.
// Matching is by prefix because a temporary name carries a random suffix.
func (t TempDir) AllowedFor(name string) (string, bool) {
	for prefix, why := range t.Allow {
		if strings.HasPrefix(name, prefix) {
			return why, true
		}
	}
	return "", false
}

// Argv is the command the gate runs, with the default applied.
func (t TempDir) Argv() []string {
	if len(t.Command) == 0 {
		return []string{"go", "test", "./..."}
	}
	return t.Command
}

// Modernize configures the `go fix` gate.
type Modernize struct {
	// Disable names fixers to turn off. A repo that disables a fixer is
	// making a decision, and the gate verifies the fixer still exists before
	// trusting the flag: `go fix` rejects an unknown -name=false, and a
	// rejected flag would make this check pass silently.
	Disable []string `yaml:"disable"`
}

// Load reads Name from dir. A missing file is not an error: it yields the
// defaults, so a repo adopts the required targets without writing config.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, Name)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults(&Config{}, dir), nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.UnmarshalWithOptions(data, &c, yaml.Strict()); err != nil {
		// The waiver map used to live under contract.exempt. A repository
		// still carrying it is told where it went rather than only that the
		// key is unknown.
		if strings.Contains(err.Error(), `unknown field "contract"`) {
			return nil, fmt.Errorf("%s: %w\ncontract.exempt is now the top-level waive map, keyed by gate name (cover, race, lint, ...)", path, err)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.validate(path); err != nil {
		return nil, err
	}
	return defaults(&c, dir), nil
}

// defaults fills in every value a repository does not have to decide, and
// records the ones the file decided anyway.
//
// A nil slice is a key the file did not write; an empty one is a key set to
// nothing. The two differ: `modernize.disable: []` is a decision to run every
// fixer, and unset is the shared default.
func defaults(c *Config, dir string) *Config {
	if c.Cover.Threshold == DefaultThreshold {
		c.Restated = append(c.Restated, "cover.threshold")
	}
	if c.Cover.Threshold == 0 {
		c.Cover.Threshold = DefaultThreshold
	}
	if c.Modernize.Disable == nil {
		c.Modernize.Disable = slices.Clone(DefaultDisabledFixers)
	} else if sameSet(c.Modernize.Disable, DefaultDisabledFixers) {
		c.Restated = append(c.Restated, "modernize.disable")
	}
	if c.Spec.Dir == DefaultSpecDir {
		c.Restated = append(c.Restated, "spec.dir")
	}
	if c.Spec.Dir == "" {
		c.Spec.Dir = DefaultSpecDir
	}
	if c.Spec.Require == nil {
		c.Spec.Require = slices.Clone(DefaultSpecRequire)
	} else if sameSet(c.Spec.Require, DefaultSpecRequire) {
		c.Restated = append(c.Restated, "spec.require")
	}
	if c.Spec.Index == DefaultSpecIndex {
		c.Restated = append(c.Restated, "spec.index")
	}
	if c.Spec.Index == "" {
		if _, err := os.Stat(filepath.Join(dir, DefaultSpecIndex)); err == nil {
			c.Spec.Index = DefaultSpecIndex
		}
	}
	if sl := c.Golangci.Sloglint; sl == nil {
		c.Golangci.Sloglint = &Sloglint{Context: DefaultSloglintContext}
	} else if sl.Context == DefaultSloglintContext && sl.RequestPaths == "" && len(sl.Exempt) == 0 {
		c.Restated = append(c.Restated, "golangci.sloglint")
	}
	return c
}

// sameSet reports whether two lists hold the same names in any order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	sort.Strings(x)
	sort.Strings(y)
	return slices.Equal(x, y)
}

// validate rejects a config that would make a gate weaker than it looks.
func (c *Config) validate(path string) error {
	var bad []string
	for pkg, why := range c.Cover.Exempt {
		if strings.TrimSpace(why) == "" {
			bad = append(bad, pkg)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("%s: exempt without a reason: %s\n"+
			"an exemption is a decision: write why the package does not have "+
			"to clear the floor, or delete the entry",
			path, strings.Join(bad, ", "))
	}
	var noReason []string
	for pkg, g := range c.Depcheck.Packages {
		for prefix, why := range g.Allow {
			if strings.TrimSpace(why) == "" {
				noReason = append(noReason, pkg+" -> "+prefix)
			}
		}
	}
	if len(noReason) > 0 {
		sort.Strings(noReason)
		return fmt.Errorf("%s: depcheck allowance without a reason: %s\n"+
			"admitting a dependency is a decision: write why the build may reach "+
			"it, or delete the entry",
			path, strings.Join(noReason, ", "))
	}
	var noWhy, noDate []string
	for gate, w := range c.Waive {
		if strings.TrimSpace(w.Reason) == "" {
			noWhy = append(noWhy, gate)
		}
		if _, ok := w.UntilDate(); !ok {
			noDate = append(noDate, gate)
		}
	}
	if len(noWhy) > 0 {
		sort.Strings(noWhy)
		return fmt.Errorf("%s: waiver without a reason: %s\n"+
			"not running a gate is a decision: write why this repository does "+
			"not run it yet, or delete the entry",
			path, strings.Join(noWhy, ", "))
	}
	if len(noDate) > 0 {
		sort.Strings(noDate)
		return fmt.Errorf("%s: waiver without a usable until date: %s\n"+
			"write the date the waiver stops working, as YYYY-MM-DD: a "+
			"waiver with no end is the bar being lowered permanently",
			path, strings.Join(noDate, ", "))
	}
	var unowned []string
	for prefix, why := range c.TempDir.Allow {
		if strings.TrimSpace(why) == "" {
			unowned = append(unowned, prefix)
		}
	}
	if len(unowned) > 0 {
		sort.Strings(unowned)
		return fmt.Errorf("%s: tempdir allowance without a reason: %s\n"+
			"a directory that outlives the test run is one nobody will delete: "+
			"write why this one may, or delete the entry",
			path, strings.Join(unowned, ", "))
	}
	// `started` without `settled` makes every dependency unsettled, so the
	// rule fires on every spec that has one. A gate that always fails is
	// turned off within the day, and turning it off is what it deserves.
	if len(c.Spec.Started) > 0 && len(c.Spec.Settled) == 0 {
		return fmt.Errorf("%s: spec.started is set and spec.settled is empty, so no "+
			"dependency could ever be closed and every started spec would fail\n"+
			"name the statuses at which a dependency stops blocking", path)
	}
	// An archive nobody can be sent to is a directory the lint reads and never
	// files anything into, which reports green while every finished spec sits
	// at the root.
	if c.Spec.Archive.Dir != "" && len(c.Spec.Archive.Statuses) == 0 {
		return fmt.Errorf("%s: spec.archive.dir is set and spec.archive.statuses is empty, "+
			"so no spec could ever belong in the archive\n"+
			"name the statuses that mean the work is over", path)
	}
	if c.Spec.Archive.Dir == "" && len(c.Spec.Archive.Statuses) > 0 {
		return fmt.Errorf("%s: spec.archive.statuses is set and spec.archive.dir is empty, "+
			"so the archive rules never run\n"+
			"name the directory finished specs retire into", path)
	}
	// A status listed here that the vocabulary does not have never matches,
	// so the rule silently covers fewer specs than it reads as covering.
	if len(c.Spec.Status) > 0 {
		var unknown []string
		for _, s := range append(slices.Clone(c.Spec.Started), c.Spec.Settled...) {
			if !slices.Contains(c.Spec.Status, s) {
				unknown = append(unknown, s)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fmt.Errorf("%s: spec.started/spec.settled name %s, which spec.status "+
				"does not list\na status no spec can hold matches nothing, so the rule "+
				"would cover less than it appears to",
				path, strings.Join(slices.Compact(unknown), ", "))
		}
		var notInVocab []string
		for _, s := range c.Spec.Archive.Statuses {
			if !slices.Contains(c.Spec.Status, s) {
				notInVocab = append(notInVocab, s)
			}
		}
		if len(notInVocab) > 0 {
			sort.Strings(notInVocab)
			return fmt.Errorf("%s: spec.archive.statuses name %s, which spec.status does "+
				"not list\na terminal status no spec can hold sends nothing to the "+
				"archive, so the rule would cover less than it appears to",
				path, strings.Join(slices.Compact(notInVocab), ", "))
		}
	}
	var unreadable []string
	for _, ext := range append(slices.Clone(c.License.Extensions), c.License.Names...) {
		if _, ok := LineComment[ext]; !ok {
			unreadable = append(unreadable, ext)
		}
	}
	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		return fmt.Errorf("%s: license file types the gate cannot read a notice from: %s\n"+
			"it looks for a line comment at the top of the file, and knows no "+
			"marker for these; one scanned with the wrong marker never matches",
			path, strings.Join(unreadable, ", "))
	}
	if tm := strings.TrimSpace(c.Race.Timeout); tm != "" {
		d, err := time.ParseDuration(tm)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s: race.timeout %q is not a positive duration such as 45m", path, c.Race.Timeout)
		}
	}
	if t := c.Cover.Threshold; t < 0 || t > 100 {
		return fmt.Errorf("%s: cover.threshold %.1f is not a percentage", path, t)
	}
	return nil
}

// ExemptFor reports whether a package is exempt, and why. Matching is by
// suffix so a config does not have to repeat the module path.
func (c Cover) ExemptFor(pkg string) (string, bool) {
	for suffix, why := range c.Exempt {
		if strings.HasSuffix(pkg, suffix) {
			return why, true
		}
	}
	return "", false
}

// AllowsStatus reports whether s is in the configured vocabulary. An empty
// vocabulary allows everything.
func (s Spec) AllowsStatus(v string) bool {
	if len(s.Status) == 0 {
		return true
	}
	return slices.Contains(s.Status, v)
}

// IsExcluded reports whether a file name in Dir is not a spec.
func (s Spec) IsExcluded(name string) bool {
	return slices.Contains(s.Exclude, name)
}
