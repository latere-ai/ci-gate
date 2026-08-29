// Copyright 2026 Latere AI.
// Licensed under the Apache License, Version 2.0.

package speclint

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// tree writes a spec directory. Keys are file names under specs/, except
// "README.md" which is the index.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "specs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func spec(status string, extra ...string) string {
	return "---\ntitle: A spec\nstatus: " + status + "\n" +
		strings.Join(extra, "\n") + "\n---\n\n# Body\n"
}

func index(rows ...string) string {
	return "# Specs\n\n| # | Spec | Status | Scope |\n|---|---|---|---|\n" +
		strings.Join(rows, "\n") + "\n"
}

func cfg() config.Spec {
	return config.Spec{
		Dir:     "specs",
		Status:  []string{"draft", "partial", "complete"},
		Require: []string{"title", "status"},
		Index:   "specs/README.md",
	}
}

func run(t *testing.T, c config.Spec, root string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(c, root, &sb)
	return sb.String(), err
}

func TestAConsistentTreePasses(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"002-b.md":  spec("complete", "depends_on:\n  - 001-a.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](002-b.md) | complete | y |"),
	})
	out, err := run(t, cfg(), root)
	if err != nil {
		t.Fatalf("a consistent tree should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 specs consistent") {
		t.Errorf("report should count the specs:\n%s", out)
	}
}

// The drift this package exists for: the index said draft while the spec had
// moved on. It was found on the first run against a real tree.
func TestTheIndexMustAgreeWithTheSpec(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("partial"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	out, err := run(t, cfg(), root)
	if err == nil {
		t.Fatal("an index disagreeing with a spec must fail")
	}
	if !strings.Contains(out, `row says 001-a.md is "draft"; the spec says "partial"`) {
		t.Errorf("the report should name both values:\n%s", out)
	}
}

// A spec document cites other specs from prose tables. Treating any table row
// with a link as an index row reported drift that was not there, so the index
// table is found by its Status header instead.
func TestAProseTableIsNotTheIndex(t *testing.T) {
	body := index("| 001 | [A](001-a.md) | partial | x |") +
		"\n## Open questions\n\n| Spec | Question |\n|---|---|\n" +
		"| 001 | see [A](001-a.md), which reports 3 tok/s |\n"
	root := tree(t, map[string]string{"001-a.md": spec("partial"), "README.md": body})
	if out, err := run(t, cfg(), root); err != nil {
		t.Fatalf("a prose table must not be read as index rows: %v\n%s", err, out)
	}
}

func TestASpecMissingFromTheIndexIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"002-b.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	out, _ := run(t, cfg(), root)
	if !strings.Contains(out, "002-b.md is not listed") {
		t.Errorf("a spec absent from the index is invisible and must be reported:\n%s", out)
	}
}

func TestAnIndexRowToNothingIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 009 | [Gone](009-gone.md) | draft | x |"),
	})
	out, _ := run(t, cfg(), root)
	if !strings.Contains(out, "009-gone.md, which is not a spec") {
		t.Errorf("a row pointing at nothing must be reported:\n%s", out)
	}
}

func TestARequiredKeyMustBePresentAndNonEmpty(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  "---\nstatus: draft\n---\n\nbody\n",
		"002-b.md":  "---\ntitle: \nstatus: draft\n---\n\nbody\n",
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](002-b.md) | draft | x |"),
	})
	out, err := run(t, cfg(), root)
	if err == nil {
		t.Fatal("missing and empty required keys must fail")
	}
	if !strings.Contains(out, "001-a.md: frontmatter has no title") {
		t.Errorf("a missing key must be named:\n%s", out)
	}
	if !strings.Contains(out, "002-b.md: title is empty") {
		t.Errorf("an empty key must be named:\n%s", out)
	}
}

func TestAStatusOutsideTheVocabularyIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("shipped"),
		"README.md": index("| 001 | [A](001-a.md) | shipped | x |"),
	})
	out, _ := run(t, cfg(), root)
	if !strings.Contains(out, `status "shipped" is not one of draft|partial|complete`) {
		t.Errorf("a status outside the vocabulary must be reported:\n%s", out)
	}
}

func TestADanglingDependencyIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft", "depends_on:\n  - 099-gone.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	out, _ := run(t, cfg(), root)
	if !strings.Contains(out, "depends_on 099-gone.md, which does not exist") {
		t.Errorf("a dangling edge must be reported:\n%s", out)
	}
}

// A spec may depend on one in another repository, and this check cannot see
// that repository, so a path is left alone rather than reported as dangling.
func TestACrossRepoDependencyIsLeftAlone(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft", "depends_on:\n  - ../../tgo/specs/010-conformance.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	if out, err := run(t, cfg(), root); err != nil {
		t.Fatalf("a cross-repo edge must not be reported: %v\n%s", err, out)
	}
}

func TestACycleIsReported(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft", "depends_on:\n  - 002-b.md"),
		"002-b.md":  spec("draft", "depends_on:\n  - 001-a.md"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](002-b.md) | draft | x |"),
	})
	out, err := run(t, cfg(), root)
	if err == nil {
		t.Fatal("a cycle must fail")
	}
	if !strings.Contains(out, "dependency cycle:") {
		t.Errorf("the cycle must be named:\n%s", out)
	}
}

func TestWikilinksResolve(t *testing.T) {
	c := cfg()
	c.Wikilinks = true
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft") + "\nSee [[002-b]] and [[099-gone]].\n",
		"002-b.md":  spec("draft"),
		"README.md": index("| 001 | [A](001-a.md) | draft | x |", "| 002 | [B](002-b.md) | draft | x |"),
	})
	out, _ := run(t, c, root)
	if strings.Contains(out, "[[002-b]]") {
		t.Errorf("a resolving wikilink must not be reported:\n%s", out)
	}
	if !strings.Contains(out, "[[099-gone]] resolves to nothing") {
		t.Errorf("a dangling wikilink must be reported:\n%s", out)
	}
}

func TestWikilinksAreOffByDefault(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft") + "\nSee [[099-gone]].\n",
		"README.md": index("| 001 | [A](001-a.md) | draft | x |"),
	})
	if out, err := run(t, cfg(), root); err != nil {
		t.Fatalf("wikilinks should not be checked unless asked: %v\n%s", err, out)
	}
}

func TestNoSpecDirIsANoop(t *testing.T) {
	out, err := run(t, config.Spec{}, t.TempDir())
	if err != nil {
		t.Fatalf("an unconfigured spec-lint should do nothing: %v", err)
	}
	if !strings.Contains(out, "nothing to check") {
		t.Errorf("it should say so:\n%s", out)
	}
}

// A lint that checks nothing reports green forever.
func TestAnEmptyTreeIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, cfg(), root); err == nil {
		t.Fatal("an empty spec tree must fail rather than pass vacuously")
	}
}

func TestAnIndexWithNoRowsIsAnError(t *testing.T) {
	root := tree(t, map[string]string{
		"001-a.md":  spec("draft"),
		"README.md": "# Specs\n\nNo table here.\n",
	})
	_, err := run(t, cfg(), root)
	if err == nil || !strings.Contains(err.Error(), "has the table shape changed") {
		t.Fatalf("an index with no rows must be an error, got %v", err)
	}
}

func TestAMissingIndexIsAnError(t *testing.T) {
	root := tree(t, map[string]string{"001-a.md": spec("draft")})
	if _, err := run(t, cfg(), root); err == nil {
		t.Fatal("a configured index that does not exist must be an error")
	}
}

func TestParseRejectsBadFrontmatter(t *testing.T) {
	for name, src := range map[string]string{
		"none":         "# No frontmatter\n",
		"unterminated": "---\ntitle: x\n",
		"not yaml":     "---\ntitle: [\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("x.md", src); err == nil {
				t.Errorf("%q must not parse", src)
			}
		})
	}
}

// A Windows checkout turns LF into CRLF, and a parser matching on "\n" leaves
// a stray "\r" on every value.
func TestParseHandlesCRLF(t *testing.T) {
	s, err := Parse("x.md", "---\r\ntitle: A spec\r\nstatus: draft\r\n---\r\n\r\nbody\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "draft" || s.Title != "A spec" {
		t.Errorf("CRLF frontmatter parsed as %q/%q", s.Title, s.Status)
	}
}

func TestExcludedFilesAreNotSpecs(t *testing.T) {
	c := cfg()
	c.Exclude = []string{"CONTRIBUTING.md"}
	root := tree(t, map[string]string{
		"001-a.md":        spec("draft"),
		"CONTRIBUTING.md": "# Not a spec\n",
		"README.md":       index("| 001 | [A](001-a.md) | draft | x |"),
	})
	if out, err := run(t, c, root); err != nil {
		t.Fatalf("an excluded file must not be linted: %v\n%s", err, out)
	}
}

func TestAnUnreadableSpecIsAnError(t *testing.T) {
	root := tree(t, map[string]string{"README.md": index()})
	if err := os.Mkdir(filepath.Join(root, "specs", "001-a.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Run(cfg(), root, io.Discard); err == nil {
		t.Fatal("an unreadable spec must be an error")
	}
}

// Without a configured vocabulary the index status cannot be judged, but the
// links still must resolve.
func TestNoVocabularySkipsStatusComparison(t *testing.T) {
	c := cfg()
	c.Status = nil
	root := tree(t, map[string]string{
		"001-a.md":  spec("whatever"),
		"README.md": index("| 001 | [A](001-a.md) | anything | x |"),
	})
	if out, err := run(t, c, root); err != nil {
		t.Fatalf("no vocabulary should skip the status check: %v\n%s", err, out)
	}
}

func TestARowShorterThanTheStatusColumnIsReported(t *testing.T) {
	body := "| # | Spec | Status |\n|---|---|---|\n| 001 | [A](001-a.md) |\n"
	root := tree(t, map[string]string{"001-a.md": spec("draft"), "README.md": body})
	out, _ := run(t, cfg(), root)
	if !strings.Contains(out, "has no status cell") {
		t.Errorf("a truncated row must be reported:\n%s", out)
	}
}
