// Copyright 2026 Latere AI.
// Licensed under the MIT License.

package speclint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
)

// CheckVocabulary reports frontmatter keys holding a value outside their
// closed set. `status` has its own rule; this closes the others a tree
// defines, such as a layer or an owner.
func CheckVocabulary(cfg config.Spec, specs []Spec) []string {
	var out []string
	for _, key := range slices.Sorted(maps.Keys(cfg.Vocabulary)) {
		allowed := cfg.Vocabulary[key]
		for _, s := range specs {
			v, ok := s.frontmatter[key]
			if !ok || v == nil {
				continue // presence is the job of `require`
			}
			got := strings.TrimSpace(fmt.Sprintf("%v", v))
			if !slices.Contains(allowed, got) {
				out = append(out, fmt.Sprintf("%s: %s %q is not one of %s",
					s.Name, key, got, strings.Join(allowed, "|")))
			}
		}
	}
	return out
}

// CheckStatusRequires reports specs whose status and frontmatter disagree.
//
// The rule runs in both directions on purpose. A spec at the status without
// the field is the obvious half; a spec carrying the field at another status
// is the half that keeps the pair exhaustive, so the field cannot be left
// behind when the status moves on.
func CheckStatusRequires(cfg config.Spec, specs []Spec) []string {
	var out []string
	for _, status := range slices.Sorted(maps.Keys(cfg.StatusRequires)) {
		rule := cfg.StatusRequires[status]
		if rule.Field == "" {
			continue
		}
		var re *regexp.Regexp
		if rule.Match != "" {
			var err error
			if re, err = regexp.Compile(rule.Match); err != nil {
				out = append(out, fmt.Sprintf("status_requires.%s.match is not a valid pattern: %v", status, err))
				continue
			}
		}
		for _, s := range specs {
			value, present := fieldText(s, rule.Field)
			switch {
			case s.Status == status && !present:
				out = append(out, fmt.Sprintf("%s: status is %s with no %s", s.Name, status, rule.Field))
			case s.Status != status && present:
				out = append(out, fmt.Sprintf("%s: has %s but status is %q", s.Name, rule.Field, s.Status))
			case s.Status == status && re != nil && !re.MatchString(value):
				msg := fmt.Sprintf("%s: %s does not match what %s requires", s.Name, rule.Field, status)
				if rule.Hint != "" {
					msg += "; " + rule.Hint
				}
				out = append(out, msg)
			}
		}
	}
	return out
}

// fieldText returns a frontmatter value flattened to text, and whether it
// holds anything. A list joins with spaces so one pattern can match across it.
func fieldText(s Spec, key string) (string, bool) {
	v, ok := s.frontmatter[key]
	if !ok || v == nil {
		return "", false
	}
	if list, ok := v.([]any); ok {
		var parts []string
		for _, e := range list {
			parts = append(parts, strings.TrimSpace(fmt.Sprintf("%v", e)))
		}
		joined := strings.TrimSpace(strings.Join(parts, " "))
		return joined, joined != ""
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", v))
	return text, text != ""
}

// CheckSections reports specs missing a heading they are required to carry.
//
// A spec that claims to be built and does not say what happened leaves the
// claim unchecked, which is how a tree ends up with a status column nobody
// trusts.
func CheckSections(cfg config.Spec, specs []Spec) []string {
	var out []string
	for _, s := range specs {
		if slices.Contains(cfg.SectionExempt, s.Name) {
			continue
		}
		want := slices.Clone(cfg.RequireSection)
		want = append(want, cfg.RequireSectionByStatus[s.Status]...)
		for _, heading := range want {
			if !hasHeading(s.body, heading) {
				out = append(out, fmt.Sprintf("%s: no %q section", s.Name, heading))
			}
		}
	}
	return out
}

// hasHeading matches a Markdown heading by name.
//
// The level is ignored so a tree can promote or demote a section without the
// rule following it, and a leading section number is stripped so `## 8.
// Outcome` satisfies a rule that asks for "Outcome". A document that numbers
// its sections is not a document missing them.
func hasHeading(body, heading string) bool {
	want := headingText(heading)
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		if strings.EqualFold(headingText(line), want) {
			return true
		}
	}
	return false
}

// sectionNumber matches the "8." or "8)" a numbered heading starts with.
var sectionNumber = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*[.)]?\s+`)

func headingText(line string) string {
	t := strings.TrimSpace(strings.TrimLeft(line, "# "))
	return strings.TrimSpace(sectionNumber.ReplaceAllString(t, ""))
}

// CheckScopedIDs reports identifiers carrying a number other than their own
// spec's. A mismatch means a record was copied between specs, which silently
// gives two different decisions one name.
//
// The pattern's first group is the number the id claims; it is compared with
// the leading digits of the file name.
func CheckScopedIDs(cfg config.Spec, specs []Spec) []string {
	if cfg.ScopedIDs == "" {
		return nil
	}
	re, err := regexp.Compile(cfg.ScopedIDs)
	if err != nil {
		return []string{fmt.Sprintf("scoped_ids is not a valid pattern: %v", err)}
	}
	var out []string
	for _, s := range specs {
		own := leadingDigits(s.Name)
		if own == "" {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(s.body, -1) {
			if len(m) < 2 || m[1] == own {
				continue
			}
			out = append(out, fmt.Sprintf("%s: id %s belongs to spec %s", s.Name, strings.TrimSpace(m[0]), m[1]))
		}
	}
	return out
}

func leadingDigits(name string) string {
	for i, r := range name {
		if r < '0' || r > '9' {
			return name[:i]
		}
	}
	return name
}

// CheckTables reports table rows whose cell count disagrees with their header.
//
// A pipe inside a cell splits the row, and Markdown breaks it inside a code
// span just the same: `[a | b]` renders as two cells and shifts every column
// after it. Nothing else notices -- the build passes, the document renders,
// and the table says something other than what was written. The fix at a call
// site is an escaped pipe, which this counts as content.
func CheckTables(specs []Spec) []string {
	var out []string
	for _, s := range specs {
		var header string
		var headerAt, width int
		inFence := false
		for i, line := range strings.Split(s.body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence || !isTableRow(line) {
				header, width = "", 0
				continue
			}
			n := tableCells(line)
			if header == "" {
				header, headerAt, width = line, i, n
				continue
			}
			if isSeparator(line) || n == width {
				continue
			}
			out = append(out, fmt.Sprintf("%s: table row %d has %d cells; its header on line %d has %d",
				s.Name, i+1, n, headerAt+1, width))
		}
	}
	return out
}

// tableCells counts the cells in a table row. The row is bounded by a leading
// and a trailing pipe, so splitting on unescaped pipes gives the cells plus one
// empty at each end. Go's regexp has no lookbehind, so the escape is hidden
// before the split.
func tableCells(row string) int {
	hidden := strings.ReplaceAll(row, `\|`, "\x00")
	return len(strings.Split(strings.TrimSpace(hidden), "|")) - 2
}
