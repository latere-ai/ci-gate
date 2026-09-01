// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package speclint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// archiveCfg is cfg() with the archive rules on, `complete` terminal.
func archiveCfg() config.Spec {
	c := cfg()
	c.Archive = config.Archive{Dir: ".archive", Statuses: []string{"complete"}}
	return c
}

// archived writes files into the tree's archive. tree() only writes the root.
func archived(t *testing.T, root string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "specs", ".archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestATerminalSpecAtTheRootFails(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"002-b.md":  spec("complete"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](002-b.md) | complete | y |"),
	})
	archived(t, root, nil)
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("a finished spec beside the live ones should fail:\n%s", out)
	}
	if !strings.Contains(out, `002-b.md: status "complete" is terminal`) {
		t.Errorf("the report should name the spec and its status:\n%s", out)
	}
	if !strings.Contains(out, "belongs in .archive/") {
		t.Errorf("the report should name where it belongs:\n%s", out)
	}
	if strings.Contains(out, "001-a.md: status") {
		t.Errorf("a spec still being written should not be reported:\n%s", out)
	}
}

// The half that keeps the pair exhaustive: a spec retired while its work was
// still open is the same drift in the other direction.
func TestAWorkingSpecInTheArchiveFails(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("partial")})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("a spec retired before its work closed should fail:\n%s", out)
	}
	if !strings.Contains(out, `.archive/002-b.md: status "partial" is not terminal`) {
		t.Errorf("the report should name the file and its status:\n%s", out)
	}
}

func TestAConsistentArchivePasses(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft", "depends_on:\n  - specs/.archive/002-b.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("complete")})
	out, err := run(t, archiveCfg(), root)
	if err != nil {
		t.Fatalf("a tree that files its finished specs should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 specs consistent, 1 archived") {
		t.Errorf("the report should count both sides:\n%s", out)
	}
}

// The vocabulary rule never reached the archive, which is how one tree ended
// up with fifteen distinct archived statuses, one of them a sentence.
func TestAnArchivedSpecIsHeldToTheVocabulary(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{
		"002-b.md": spec("archived (superseded by 034; the premise was removed)"),
	})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("free text in an archived status should fail:\n%s", out)
	}
	if !strings.Contains(out, "is not one of draft|partial|complete") {
		t.Errorf("the report should name the vocabulary:\n%s", out)
	}
}

func TestAnArchivedSpecIsHeldToTheRequiredKeys(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": "---\nstatus: complete\n---\n\n# Body\n"})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("an archived spec with no title should fail:\n%s", out)
	}
	if !strings.Contains(out, "002-b.md: frontmatter has no title") {
		t.Errorf("the report should name the missing key:\n%s", out)
	}
}

// One file written before the frontmatter convention existed must not abort
// the run, or it hides every other finding in the report.
func TestAnUnparseableArchivedSpecIsReportedNotFatal(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("complete"),
		"README.md": index("| 001 | [A](001-a.md) | complete | x |"),
	})
	archived(t, root, map[string]string{"002-b.md": "# Just a heading, no frontmatter\n"})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("an unparseable archived spec should fail:\n%s", out)
	}
	if !strings.Contains(out, ".archive/002-b.md: no frontmatter") {
		t.Errorf("the parse failure should be a problem line:\n%s", out)
	}
	// The point of not aborting: the other finding is still in the report.
	if !strings.Contains(out, `001-a.md: status "complete" is terminal`) {
		t.Errorf("one bad file should not hide the rest of the report:\n%s", out)
	}
}

func TestAnArchivedSpecMustBeListedInTheIndex(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("complete")})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("an archived spec nobody can find should fail:\n%s", out)
	}
	if !strings.Contains(out, ".archive/002-b.md is not listed") {
		t.Errorf("the report should name the unlisted spec:\n%s", out)
	}
}

func TestAnIndexRowIntoTheArchiveMustResolve(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-gone.md) | archived | y |"),
	})
	archived(t, root, nil)
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("a row pointing at nothing should fail:\n%s", out)
	}
	if !strings.Contains(out, "row links to .archive/002-gone.md, which is not there") {
		t.Errorf("the report should name the missing target:\n%s", out)
	}
}

// The index writes the archive row as a location, not as the frontmatter's
// status. Comparing the two would force every tree onto one label vocabulary.
func TestAnArchiveRowsStatusCellIsNotCompared(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived (superseded) | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("complete")})
	out, err := run(t, archiveCfg(), root)
	if err != nil {
		t.Fatalf("an archive row's label is not a status claim: %v\n%s", err, out)
	}
}

// Retiring a spec rewrites every edge that named it, which is exactly when a
// path is mistyped.
func TestADependsOnEdgeIntoTheArchiveMustResolve(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft", "depends_on:\n  - specs/.archive/002-typo.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("complete")})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("an edge into the archive that resolves to nothing should fail:\n%s", out)
	}
	if !strings.Contains(out, "depends_on specs/.archive/002-typo.md, which is not in .archive/") {
		t.Errorf("the report should name the unresolved edge:\n%s", out)
	}
}

// An edge to another repository is still left alone: this check cannot see
// that repository.
func TestACrossRepoEdgeIsStillIgnored(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft", "depends_on:\n  - ../other/specs/009-x.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	archived(t, root, nil)
	if out, err := run(t, archiveCfg(), root); err != nil {
		t.Fatalf("an edge into another repository should be left alone: %v\n%s", err, out)
	}
}

// A citation resolves through the number, so retiring a spec must not free it.
func TestANumberIsNotReusedAcrossTheBoundary(t *testing.T) {
	c := archiveCfg()
	c.Numbered = true
	root := tree(t, map[string]string{
		"002-live.md": spec("draft"),
		"README.md":   index("| 002 | [L](002-live.md) | draft | x |", "| 002 | [G](.archive/002-gone.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-gone.md": spec("complete")})
	out, err := run(t, c, root)
	if err == nil {
		t.Fatalf("a number shared across the boundary should fail:\n%s", out)
	}
	if !strings.Contains(out, "number 002 is used by") {
		t.Errorf("the report should name the reused number:\n%s", out)
	}
}

// A wikilink to a spec that has since been retired still resolves: the archive
// keeps the file and its number.
func TestAWikilinkResolvesIntoTheArchive(t *testing.T) {
	c := archiveCfg()
	c.Wikilinks = true
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft") + "\nSee [[002-b]].\n",
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("complete")})
	if out, err := run(t, c, root); err != nil {
		t.Fatalf("a link to a retired spec should resolve: %v\n%s", err, out)
	}
}

// Rules about work in progress do not reach the archive: a record written
// before a rule existed cannot be made to satisfy it without rewriting history.
func TestTheSectionRulesDoNotReachTheArchive(t *testing.T) {
	c := archiveCfg()
	c.RequireSection = []string{"Overview"}
	root := tree(t, map[string]string{
		"001-a.md":  "---\ntitle: A spec\nstatus: draft\n---\n\n## Overview\n",
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](.archive/002-b.md) | archived | y |"),
	})
	archived(t, root, map[string]string{"002-b.md": spec("complete")})
	if out, err := run(t, c, root); err != nil {
		t.Fatalf("an archived record is not held to the section rules: %v\n%s", err, out)
	}
}

// A tree that has not adopted the rules behaves exactly as it did before.
func TestAnUnconfiguredArchiveIsNotRead(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("complete"),
		"README.md": index("| 001 | [A](001-a.md) | complete | x |"),
	})
	archived(t, root, map[string]string{"002-b.md": "not a spec at all\n"})
	out, err := run(t, cfg(), root)
	if err != nil {
		t.Fatalf("without spec.archive.dir the archive is invisible: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 specs consistent") || strings.Contains(out, "archived") {
		t.Errorf("the report should not mention an archive nobody configured:\n%s", out)
	}
}

// A tree that has retired nothing has no directory, because git cannot track
// an empty one. Adoption must not require inventing a file to put in it.
func TestAMissingArchiveDirectoryIsTheEmptyArchive(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	out, err := run(t, archiveCfg(), root)
	if err != nil {
		t.Fatalf("an archive nothing has been filed into yet should pass: %v\n%s", err, out)
	}
}

// The forward half still holds without the directory, so a tree cannot adopt
// the rule and have it quietly assert nothing.
func TestATerminalSpecFailsEvenWithNoArchiveDirectory(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("complete"),
		"README.md": index("| 001 | [A](001-a.md) | complete | x |"),
	})
	out, err := run(t, archiveCfg(), root)
	if err == nil {
		t.Fatalf("a terminal spec should fail whether or not the archive exists:\n%s", out)
	}
	if !strings.Contains(out, "belongs in .archive/") {
		t.Errorf("the report should name where it belongs:\n%s", out)
	}
}
