// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package speclint

import (
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func parse(t *testing.T, name, src string) Spec {
	t.Helper()
	s, err := Parse(name, src)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func joined(problems []string) string { return strings.Join(problems, "\n") }

func TestVocabularyClosesOtherFrontmatterKeys(t *testing.T) {
	cfg := config.Spec{Vocabulary: map[string][]string{"layer": {"engine", "api"}}}
	specs := []Spec{
		parse(t, "001-a.md", "---\ntitle: a\nstatus: draft\nlayer: engine\n---\nbody\n"),
		parse(t, "002-b.md", "---\ntitle: b\nstatus: draft\nlayer: kitchen\n---\nbody\n"),
		parse(t, "003-c.md", "---\ntitle: c\nstatus: draft\n---\nbody\n"),
	}
	got := joined(CheckVocabulary(cfg, specs))
	if !strings.Contains(got, `002-b.md: layer "kitchen" is not one of engine|api`) {
		t.Errorf("a value outside the set must be reported:\n%s", got)
	}
	if strings.Contains(got, "001-a.md") || strings.Contains(got, "003-c.md") {
		t.Errorf("a valid value, and an absent key, must both pass:\n%s", got)
	}
}

// The rule runs both ways: the field must be there at that status, and gone at
// any other, so it cannot be left behind when the status moves on.
func TestStatusRequiresRunsInBothDirections(t *testing.T) {
	cfg := config.Spec{StatusRequires: map[string]config.StatusRule{
		"blocked": {Field: "blocked_on"},
	}}
	specs := []Spec{
		parse(t, "001-a.md", "---\nstatus: blocked\n---\nbody\n"),
		parse(t, "002-b.md", "---\nstatus: draft\nblocked_on:\n  - accel#12\n---\nbody\n"),
		parse(t, "003-c.md", "---\nstatus: blocked\nblocked_on:\n  - accel#12\n---\nbody\n"),
	}
	got := joined(CheckStatusRequires(cfg, specs))
	if !strings.Contains(got, "001-a.md: status is blocked with no blocked_on") {
		t.Errorf("missing field:\n%s", got)
	}
	if !strings.Contains(got, `002-b.md: has blocked_on but status is "draft"`) {
		t.Errorf("stale field:\n%s", got)
	}
	if strings.Contains(got, "003-c.md") {
		t.Errorf("the correct pair must pass:\n%s", got)
	}
}

// A field can be filled in with something that is not a record anyone can act
// on, so the value gets a pattern and the failure carries the hint.
func TestStatusRequiresChecksTheValue(t *testing.T) {
	cfg := config.Spec{StatusRequires: map[string]config.StatusRule{
		"blocked": {Field: "blocked_on", Match: "accel#[0-9]+", Hint: "want an accel#N reference"},
	}}
	specs := []Spec{
		parse(t, "001-a.md", "---\nstatus: blocked\nblocked_on:\n  - specs/010.md\n---\nbody\n"),
		parse(t, "002-b.md", "---\nstatus: blocked\nblocked_on:\n  - accel#12\n---\nbody\n"),
	}
	got := joined(CheckStatusRequires(cfg, specs))
	if !strings.Contains(got, "001-a.md") || !strings.Contains(got, "want an accel#N reference") {
		t.Errorf("a value that matches nothing must fail with the hint:\n%s", got)
	}
	if strings.Contains(got, "002-b.md") {
		t.Errorf("a durable reference must pass:\n%s", got)
	}
}

func TestAnInvalidStatusPatternIsReported(t *testing.T) {
	cfg := config.Spec{StatusRequires: map[string]config.StatusRule{
		"blocked": {Field: "x", Match: "("},
	}}
	if got := joined(CheckStatusRequires(cfg, nil)); !strings.Contains(got, "not a valid pattern") {
		t.Errorf("a broken pattern must be reported, not ignored: %q", got)
	}
}

func TestSectionsAreRequiredGloballyAndByStatus(t *testing.T) {
	cfg := config.Spec{
		RequireSection:         []string{"Decision record"},
		RequireSectionByStatus: map[string][]string{"complete": {"Outcome"}},
		SectionExempt:          []string{"000-decisions.md"},
	}
	specs := []Spec{
		parse(t, "000-decisions.md", "---\nstatus: record\n---\nbody\n"),
		parse(t, "001-a.md", "---\nstatus: draft\n---\n## Decision record\n"),
		parse(t, "002-b.md", "---\nstatus: complete\n---\n## Decision record\n"),
		parse(t, "003-c.md", "---\nstatus: complete\n---\n## Decision record\n## 8. Outcome\n"),
	}
	got := joined(CheckSections(cfg, specs))
	if strings.Contains(got, "000-decisions.md") {
		t.Errorf("an exempt file must be skipped:\n%s", got)
	}
	if strings.Contains(got, "001-a.md") {
		t.Errorf("a draft owes no Outcome:\n%s", got)
	}
	if !strings.Contains(got, `002-b.md: no "Outcome" section`) {
		t.Errorf("a complete spec must say what happened:\n%s", got)
	}
	// A document that numbers its sections is not a document missing them.
	if strings.Contains(got, "003-c.md") {
		t.Errorf("a numbered heading must satisfy the rule:\n%s", got)
	}
}

// A mismatched id means a record was copied between specs, which silently
// gives two different decisions one name.
func TestScopedIDsMustCarryTheirOwnSpecNumber(t *testing.T) {
	cfg := config.Spec{ScopedIDs: `\| (\d{3})-D(\d+) \|`}
	specs := []Spec{
		parse(t, "009-server.md", "---\nstatus: draft\n---\n| 009-D14 | a decision |\n| 002-D10 | copied |\n"),
	}
	got := joined(CheckScopedIDs(cfg, specs))
	if strings.Contains(got, "009-D14") {
		t.Errorf("the spec's own id must pass:\n%s", got)
	}
	if !strings.Contains(got, "id | 002-D10 | belongs to spec 002") {
		t.Errorf("a foreign id must be reported:\n%s", got)
	}
}

func TestScopedIDsIsOffByDefault(t *testing.T) {
	if got := CheckScopedIDs(config.Spec{}, []Spec{parse(t, "1.md", "---\nstatus: draft\n---\n| 002-D1 |\n")}); got != nil {
		t.Errorf("no pattern configured should check nothing, got %v", got)
	}
}

func TestAnInvalidScopedIDPatternIsReported(t *testing.T) {
	if got := joined(CheckScopedIDs(config.Spec{ScopedIDs: "("}, nil)); !strings.Contains(got, "not a valid pattern") {
		t.Errorf("a broken pattern must be reported: %q", got)
	}
}

// A pipe inside a cell shifts every column after it, and nothing else notices:
// the build passes and the document renders.
func TestATableRowThatDisagreesWithItsHeaderIsReported(t *testing.T) {
	body := "---\nstatus: draft\n---\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n| 1 | 2 | 3 |\n"
	got := joined(CheckTables([]Spec{parse(t, "001-a.md", body)}))
	if !strings.Contains(got, "has 3 cells") {
		t.Errorf("the wide row must be reported:\n%s", got)
	}
	if strings.Count(got, "001-a.md") != 1 {
		t.Errorf("the good row must not be:\n%s", got)
	}
}

// The fix at a call site is an escaped pipe, which counts as content.
func TestAnEscapedPipeIsContent(t *testing.T) {
	body := "---\nstatus: draft\n---\n\n| a | b |\n| --- | --- |\n| `x \\| y` | 2 |\n"
	if got := CheckTables([]Spec{parse(t, "001-a.md", body)}); got != nil {
		t.Errorf("an escaped pipe must not split the row: %v", got)
	}
}

func TestATableInACodeFenceIsNotATable(t *testing.T) {
	body := "---\nstatus: draft\n---\n\n```\n| a | b |\n| 1 |\n```\n"
	if got := CheckTables([]Spec{parse(t, "001-a.md", body)}); got != nil {
		t.Errorf("a fenced example is not a table: %v", got)
	}
}

func registerCfg() config.Spec {
	return config.Spec{Register: config.Register{
		File: "010-c.md", Define: `(?m)^\| (C\d+) \|`,
		Cite: `010 (C\d+)|\b(C\d+)\]\(010-c\.md\)`, Sequential: true, Prefix: "C",
	}}
}

func registerSpecs(t *testing.T, table, citing string) []Spec {
	t.Helper()
	return []Spec{
		parse(t, "010-c.md", "---\nstatus: record\n---\n"+table),
		parse(t, "011-d.md", "---\nstatus: draft\n---\n"+citing),
	}
}

func TestAWellFormedRegisterPasses(t *testing.T) {
	specs := registerSpecs(t, "| C1 | a |\n| C2 | b |\n", "see 010 C2\n")
	if got := CheckRegister(registerCfg(), specs); got != nil {
		t.Errorf("a complete register with a live citation should pass: %v", got)
	}
}

// A gap means a row was deleted, and every number after it now means something
// other than what a reader citing it meant.
func TestAGapInTheRegisterIsReported(t *testing.T) {
	specs := registerSpecs(t, "| C1 | a |\n| C3 | c |\n", "")
	got := joined(CheckRegister(registerCfg(), specs))
	if !strings.Contains(got, "no C2") {
		t.Errorf("the gap must be named:\n%s", got)
	}
}

func TestADuplicateRegisterRowIsReported(t *testing.T) {
	specs := registerSpecs(t, "| C1 | a |\n| C1 | again |\n", "")
	if got := joined(CheckRegister(registerCfg(), specs)); !strings.Contains(got, "appears twice") {
		t.Errorf("a repeated id must be reported:\n%s", got)
	}
}

func TestACitationOfAMissingRowIsReported(t *testing.T) {
	specs := registerSpecs(t, "| C1 | a |\n", "as 010 C7 says\n")
	got := joined(CheckRegister(registerCfg(), specs))
	if !strings.Contains(got, "011-d.md cites register row C7") {
		t.Errorf("a dangling citation must be reported:\n%s", got)
	}
}

// One pattern carries several citation shapes as alternatives, so the first
// non-empty group is the id.
func TestTheSecondCitationShapeAlsoResolves(t *testing.T) {
	specs := registerSpecs(t, "| C1 | a |\n", "see [C9](010-c.md)\n")
	if got := joined(CheckRegister(registerCfg(), specs)); !strings.Contains(got, "C9") {
		t.Errorf("the link form must be read too:\n%s", got)
	}
}

// The register is not a citation of itself: its own rows name every id.
func TestTheRegisterDoesNotCiteItself(t *testing.T) {
	specs := registerSpecs(t, "| C1 | a, see 010 C1 |\n", "")
	if got := CheckRegister(registerCfg(), specs); got != nil {
		t.Errorf("the owning spec must be skipped: %v", got)
	}
}

func TestAnEmptyRegisterIsReported(t *testing.T) {
	specs := registerSpecs(t, "no table here\n", "")
	if got := joined(CheckRegister(registerCfg(), specs)); !strings.Contains(got, "defines no rows") {
		t.Errorf("a register that matches nothing must fail, not pass:\n%s", got)
	}
}

func TestAMissingRegisterFileIsReported(t *testing.T) {
	cfg := registerCfg()
	cfg.Register.File = "999-gone.md"
	if got := joined(CheckRegister(cfg, registerSpecs(t, "| C1 | a |\n", ""))); !strings.Contains(got, "not a spec in the tree") {
		t.Errorf("a register pointing at nothing must be reported:\n%s", got)
	}
}

func TestRegisterIsOffByDefault(t *testing.T) {
	if got := CheckRegister(config.Spec{}, nil); got != nil {
		t.Errorf("no register configured should check nothing, got %v", got)
	}
}

func TestInvalidRegisterPatternsAreReported(t *testing.T) {
	cfg := registerCfg()
	cfg.Register.Define = "("
	if got := joined(CheckRegister(cfg, nil)); !strings.Contains(got, "not a valid pattern") {
		t.Errorf("a broken define pattern must be reported: %q", got)
	}
	cfg = registerCfg()
	cfg.Register.Cite = "("
	if got := joined(CheckRegister(cfg, registerSpecs(t, "| C1 | a |\n", ""))); !strings.Contains(got, "not a valid pattern") {
		t.Errorf("a broken cite pattern must be reported: %q", got)
	}
}

// A status meaning "the outcome is recorded over there" is a claim about
// another file, which the section rule cannot see.
func TestAStatusMustBeLinkedFromItsRecord(t *testing.T) {
	cfg := config.Spec{StatusLinkedFrom: map[string]string{"complete": "011-seq.md"}}
	specs := []Spec{
		parse(t, "011-seq.md", "---\nstatus: record\n---\nsee [008](008-a.md)\n"),
		parse(t, "008-a.md", "---\nstatus: complete\n---\nbody\n"),
		parse(t, "009-b.md", "---\nstatus: complete\n---\nbody\n"),
		parse(t, "010-c.md", "---\nstatus: draft\n---\nbody\n"),
	}
	got := joined(CheckStatusLinked(cfg, specs))
	if strings.Contains(got, "008-a.md") {
		t.Errorf("a linked spec must pass:\n%s", got)
	}
	if !strings.Contains(got, "009-b.md: status is complete and 011-seq.md does not link it") {
		t.Errorf("an unlinked complete spec must be reported:\n%s", got)
	}
	if strings.Contains(got, "010-c.md") {
		t.Errorf("a draft owes no link:\n%s", got)
	}
}

func TestAMissingLinkFileIsReported(t *testing.T) {
	cfg := config.Spec{StatusLinkedFrom: map[string]string{"complete": "999-gone.md"}}
	if got := joined(CheckStatusLinked(cfg, nil)); !strings.Contains(got, "not a spec in the tree") {
		t.Errorf("a rule pointing at nothing must be reported: %q", got)
	}
}

func markerCfg() config.Spec {
	return config.Spec{Marker: config.Marker{
		Pattern:  `\*\*Not built\.?\*\*:?\s+(\S+)`,
		Required: []string{"implemented", "complete"},
		Expect:   map[string]string{"complete": "^Nothing"},
		Reject:   map[string]string{"implemented": "^Nothing"},
	}}
}

// The rule runs both ways so the pair stays exhaustive: a finished spec left
// at the earlier status hides that it is finished.
func TestTheMarkerDiscriminatesTwoStatuses(t *testing.T) {
	specs := []Spec{
		parse(t, "001-a.md", "---\nstatus: complete\n---\n**Not built.** Nothing in scope.\n"),
		parse(t, "002-b.md", "---\nstatus: complete\n---\n**Not built.** Three things.\n"),
		parse(t, "003-c.md", "---\nstatus: implemented\n---\n**Not built.** Nothing in scope.\n"),
		parse(t, "004-d.md", "---\nstatus: implemented\n---\n**Not built.** Two things.\n"),
	}
	got := joined(CheckMarker(markerCfg(), specs))
	if strings.Contains(got, "001-a.md") || strings.Contains(got, "004-d.md") {
		t.Errorf("the two correct pairings must pass:\n%s", got)
	}
	if !strings.Contains(got, `002-b.md: status is complete and the paragraph opens with "Three"`) {
		t.Errorf("complete with open work must be reported:\n%s", got)
	}
	if !strings.Contains(got, "003-c.md") {
		t.Errorf("implemented with nothing open must be reported; it is finished:\n%s", got)
	}
}

func TestAMissingMarkerIsReportedOnlyWhereRequired(t *testing.T) {
	specs := []Spec{
		parse(t, "001-a.md", "---\nstatus: complete\n---\nno paragraph here\n"),
		parse(t, "002-b.md", "---\nstatus: draft\n---\nno paragraph here\n"),
	}
	got := joined(CheckMarker(markerCfg(), specs))
	if !strings.Contains(got, "001-a.md: status is complete and the required paragraph is missing") {
		t.Errorf("a required marker must be reported:\n%s", got)
	}
	if strings.Contains(got, "002-b.md") {
		t.Errorf("a draft owes no paragraph:\n%s", got)
	}
}

func TestMarkerIsOffByDefault(t *testing.T) {
	if got := CheckMarker(config.Spec{}, nil); got != nil {
		t.Errorf("no pattern configured should check nothing, got %v", got)
	}
}

func TestInvalidMarkerPatternsAreReported(t *testing.T) {
	for name, mutate := range map[string]func(*config.Marker){
		"pattern": func(m *config.Marker) { m.Pattern = "(" },
		"expect":  func(m *config.Marker) { m.Expect = map[string]string{"complete": "("} },
		"reject":  func(m *config.Marker) { m.Reject = map[string]string{"implemented": "("} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := markerCfg()
			mutate(&cfg.Marker)
			if got := joined(CheckMarker(cfg, nil)); !strings.Contains(got, "not a valid pattern") {
				t.Errorf("a broken %s must be reported: %q", name, got)
			}
		})
	}
}
