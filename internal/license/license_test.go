// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package license

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

const notice = "// SPDX-FileCopyrightText: 2026 Latere AI\n" +
	"// SPDX-License-Identifier: AGPL-3.0-or-later\n\n"

func cfg() config.License {
	return config.License{SPDX: "AGPL-3.0-or-later", Holder: "Latere AI"}
}

// repo writes a tree with a LICENSE at its root, which every case but the
// one testing its absence needs.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["LICENSE"] = "GNU AFFERO GENERAL PUBLIC LICENSE\n"
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, c config.License, files map[string]string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(c, repo(t, files), &sb)
	return sb.String(), err
}

func TestANoticedTreePasses(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go":      notice + "package a\n",
		"b/b.go":    notice + "// Package b does something.\npackage b\n",
		"c.txt":     "no notice here\n",
		"README.md": "```go\n// no notice here either\n```\n",
	})
	if err != nil {
		t.Fatalf("a noticed tree should pass: %v", err)
	}
	if !strings.Contains(out, "AGPL-3.0-or-later declared on 2 file(s)") {
		t.Errorf("the count should cover only the configured extensions:\n%s", out)
	}
}

func TestAMissingNoticeFailsAndNamesTheFile(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go":   notice + "package a\n",
		"b/b.go": "package b\n",
	})
	if err == nil {
		t.Fatal("a file with no notice should fail the gate")
	}
	if !strings.Contains(out, "b/b.go") || strings.Contains(out, "a.go:") {
		t.Errorf("only the file without a notice should be named:\n%s", out)
	}
	if !strings.Contains(err.Error(), "SPDX-FileCopyrightText") {
		t.Errorf("the failure should show the shape that was wanted: %v", err)
	}
}

func TestAStaleIdentifierFailsWithBothValues(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: 2026 Latere AI\n" +
			"// SPDX-License-Identifier: MIT\n\npackage a\n",
	})
	if err == nil {
		t.Fatal("an identifier that is not the declared one should fail")
	}
	if !strings.Contains(out, `"MIT"`) || !strings.Contains(out, `"AGPL-3.0-or-later"`) {
		t.Errorf("the failure should print what was found and what was declared:\n%s", out)
	}
}

func TestAWrongHolderFails(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: 2026 Someone Else\n" +
			"// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n",
	})
	if err == nil {
		t.Fatal("a holder that is not the declared one should fail")
	}
	if !strings.Contains(out, `"Someone Else"`) {
		t.Errorf("the failure should print the holder it found:\n%s", out)
	}
}

// The notice touching the doc comment is the failure this gate exists for as
// much as a missing notice: it compiles, it reviews clean, and it puts the
// licence text at the top of every page on pkg.go.dev.
func TestAnUnseparatedNoticeBecomesThePackageDoc(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: 2026 Latere AI\n" +
			"// SPDX-License-Identifier: AGPL-3.0-or-later\n" +
			"// Package a does something.\npackage a\n",
	})
	if err == nil {
		t.Fatal("a notice running into the doc comment should fail")
	}
	if !strings.Contains(out, "line 3 is not blank") {
		t.Errorf("the failure should say what is wrong:\n%s", out)
	}
}

func TestAYearRangePassesAndAMissingYearFails(t *testing.T) {
	if _, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: 2024-2026 Latere AI\n" +
			"// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n",
	}); err != nil {
		t.Errorf("a year range should pass: %v", err)
	}
	out, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: Latere AI\n" +
			"// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n",
	})
	if err == nil {
		t.Fatal("a notice with no year should fail")
	}
	if !strings.Contains(out, "not a year") {
		t.Errorf("the failure should say the year is missing:\n%s", out)
	}
}

func TestABuildConstraintBetweenNoticeAndPackagePasses(t *testing.T) {
	if _, err := run(t, cfg(), map[string]string{
		"a.go": notice + "//go:build linux\n\npackage a\n",
	}); err != nil {
		t.Errorf("a build constraint below the notice should pass: %v", err)
	}
}

func TestATwoLineFilePasses(t *testing.T) {
	if _, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: 2026 Latere AI\n" +
			"// SPDX-License-Identifier: AGPL-3.0-or-later\n",
	}); err != nil {
		t.Errorf("a file that ends at the notice should pass: %v", err)
	}
}

func TestAnUndeclaredRepositoryFails(t *testing.T) {
	_, err := run(t, config.License{Holder: "Latere AI"}, map[string]string{
		"a.go": notice + "package a\n",
	})
	if err == nil {
		t.Fatal("a repository that declares no licence should fail, not pass")
	}
	if !strings.Contains(err.Error(), "license.spdx") {
		t.Errorf("the failure should name the field to fill in: %v", err)
	}
}

func TestAMissingLicenseFileFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(notice+"package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := Run(cfg(), root, &sb); err == nil {
		t.Fatal("a notice pointing at terms that are not in the tree should fail")
	}
}

func TestAScanThatMatchedNothingFails(t *testing.T) {
	_, err := run(t, cfg(), map[string]string{"README.md": "no source here\n"})
	if err == nil {
		t.Fatal("a scan that read no file should fail rather than pass vacuously")
	}
	if !strings.Contains(err.Error(), "vacuously") {
		t.Errorf("the failure should say why: %v", err)
	}
}

func TestTheExtensionListReplacesTheGoDefault(t *testing.T) {
	c := cfg()
	c.Extensions = []string{".ts", ".tsx"}
	out, err := run(t, c, map[string]string{
		"a.go":         "package a\n", // not checked: .go is not in the list
		"src/a.ts":     notice + "export const a = 1\n",
		"src/b.tsx":    notice + "export const B = () => null\n",
		"dist/c.ts":    notice + "export const c = 1\n",
		"src/skip.mjs": "export const d = 1\n",
	})
	if err != nil {
		t.Fatalf("a noticed TypeScript tree should pass: %v", err)
	}
	if !strings.Contains(out, "on 3 file(s)") {
		t.Errorf("only the listed extensions should be counted:\n%s", out)
	}
}

func TestASkippedDirectoryIsNotScanned(t *testing.T) {
	c := cfg()
	c.Skip = []string{"dist"}
	out, err := run(t, c, map[string]string{
		"a.go":                notice + "package a\n",
		"dist/gen.go":         "package gen\n",
		"node_modules/x/x.go": "package x\n",
	})
	if err != nil {
		t.Fatalf("generated output should not fail the gate: %v", err)
	}
	if !strings.Contains(out, "on 1 file(s)") {
		t.Errorf("the skipped directories should not be counted:\n%s", out)
	}
}

func TestAHeaderWithNeitherYearNorHolderFails(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText:\n" +
			"// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n",
	})
	if err == nil {
		t.Fatal("a copyright tag with nothing after it should fail")
	}
	if !strings.Contains(out, "no year or holder") {
		t.Errorf("the failure should say what is missing:\n%s", out)
	}
}

func TestASecondLineThatIsNotTheIdentifierFails(t *testing.T) {
	out, err := run(t, cfg(), map[string]string{
		"a.go": "// SPDX-FileCopyrightText: 2026 Latere AI\n" +
			"// All rights reserved.\n\npackage a\n",
	})
	if err == nil {
		t.Fatal("a notice with no identifier should fail")
	}
	if !strings.Contains(out, "no "+IdentifierTag+" on line 2") {
		t.Errorf("the failure should name the missing tag:\n%s", out)
	}
}

// A walk that cannot read part of the tree has not checked it, and a gate
// that reports a pass over files it never opened is the vacuous case again.
func TestAnUnreadableTreeFailsRatherThanReportingAPass(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable directory anyway")
	}
	root := repo(t, map[string]string{"a.go": notice + "package a\n"})
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	var sb strings.Builder
	if err := Run(cfg(), root, &sb); err == nil {
		t.Fatal("a directory the walk cannot enter should fail the gate")
	}
}

func TestAnUnreadableFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable file anyway")
	}
	root := repo(t, map[string]string{"a.go": notice + "package a\n"})
	locked := filepath.Join(root, "b.go")
	if err := os.WriteFile(locked, []byte(notice+"package b\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	var sb strings.Builder
	if err := Run(cfg(), root, &sb); err == nil {
		t.Fatal("a file the gate cannot open should fail rather than be skipped")
	}
}

const shNotice = "# SPDX-FileCopyrightText: 2026 Latere AI\n" +
	"# SPDX-License-Identifier: AGPL-3.0-or-later\n\n"

func shellCfg() config.License {
	c := cfg()
	c.Extensions = []string{".go", ".sh"}
	return c
}

func TestAShellScriptUsesItsOwnCommentMarker(t *testing.T) {
	out, err := run(t, shellCfg(), map[string]string{
		"a.go":     notice + "package a\n",
		"s/one.sh": shNotice + "set -e\n",
	})
	if err != nil {
		t.Fatalf("a script with a # notice should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "on 2 file(s)") {
		t.Errorf("both file types should be counted:\n%s", out)
	}
}

// The Go marker in a shell script is a comment there too, so nothing breaks
// and nothing scans: exactly the silent pass the gate exists to refuse.
func TestTheWrongMarkerForTheFileTypeFails(t *testing.T) {
	out, err := run(t, shellCfg(), map[string]string{"s/one.sh": notice + "set -e\n"})
	if err == nil {
		t.Fatal("a // notice in a shell script should fail")
	}
	if !strings.Contains(out, "no "+CopyrightTag+" on line 1") {
		t.Errorf("report:\n%s", out)
	}
}

// The kernel only honours #! on line 1, so a notice pushed above it would
// make the script unexecutable. It moves below instead.
func TestTheNoticeSitsBelowAShebang(t *testing.T) {
	if _, err := run(t, shellCfg(), map[string]string{
		"s/one.sh": "#!/bin/sh\n" + shNotice + "set -e\n",
	}); err != nil {
		t.Errorf("a notice below a shebang should pass: %v", err)
	}
}

func TestAShebangWithNoNoticeUnderItFails(t *testing.T) {
	out, err := run(t, shellCfg(), map[string]string{"s/one.sh": "#!/bin/sh\nset -e\n"})
	if err == nil {
		t.Fatal("a script with a shebang and no notice should fail")
	}
	if !strings.Contains(out, "on line 2") {
		t.Errorf("the failure should count from below the shebang:\n%s", out)
	}
}

func TestAShebangShiftsTheBlankLineCheckToo(t *testing.T) {
	out, err := run(t, shellCfg(), map[string]string{
		"s/one.sh": "#!/bin/sh\n# SPDX-FileCopyrightText: 2026 Latere AI\n" +
			"# SPDX-License-Identifier: AGPL-3.0-or-later\nset -e\n",
	})
	if err == nil {
		t.Fatal("a notice running into the script below it should fail")
	}
	if !strings.Contains(out, "line 4 is not blank") {
		t.Errorf("report:\n%s", out)
	}
}

func TestAWholeNameIsCheckedAlongsideExtensions(t *testing.T) {
	c := cfg()
	c.Extensions = []string{".go"}
	c.Names = []string{"Makefile"}
	out, err := run(t, c, map[string]string{
		"a.go":     notice + "package a\n",
		"Makefile": shNotice + "all:\n\t@true\n",
	})
	if err != nil {
		t.Fatalf("a named file with a notice should pass: %v\n%s", err, out)
	}
	if !strings.Contains(out, "on 2 file(s)") {
		t.Errorf("the named file should be counted:\n%s", out)
	}
}

// Write puts the notice on files that have none, below a shebang, and leaves
// a file whose notice disagrees with the declaration to a person.
func TestWriteAddsTheNoticeWhereItIsMissing(t *testing.T) {
	c := cfg()
	c.Extensions = []string{".go", ".sh"}
	root := repo(t, map[string]string{
		"a/a.go":     "package a\n",
		"run.sh":     "#!/bin/sh\nset -eu\n",
		"b/ok.go":    "// SPDX-FileCopyrightText: 2025 Latere AI\n// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage b\n",
		"c/wrong.go": "// SPDX-FileCopyrightText: 2025 Someone Else\n// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage c\n",
	})
	var sb strings.Builder
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	err := Write(c, root, &sb, now)
	if err == nil || !strings.Contains(err.Error(), "disagrees with the declaration") {
		t.Fatalf("a wrong notice is reported, not rewritten: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a", "a.go"))
	want := "// SPDX-FileCopyrightText: 2026 Latere AI\n// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n"
	if string(got) != want {
		t.Errorf("a.go after write:\n%s", got)
	}
	got, _ = os.ReadFile(filepath.Join(root, "run.sh"))
	want = "#!/bin/sh\n# SPDX-FileCopyrightText: 2026 Latere AI\n# SPDX-License-Identifier: AGPL-3.0-or-later\n\nset -eu\n"
	if string(got) != want {
		t.Errorf("run.sh keeps its shebang on line 1:\n%s", got)
	}
	got, _ = os.ReadFile(filepath.Join(root, "c", "wrong.go"))
	if !strings.Contains(string(got), "Someone Else") {
		t.Error("a wrong holder must not be rewritten")
	}
	if !strings.Contains(sb.String(), "written on 2 file(s)") {
		t.Errorf("report:\n%s", sb.String())
	}

	// Fix the wrong one by hand, and the gate passes on the tree Write made.
	if err := os.WriteFile(filepath.Join(root, "c", "wrong.go"), []byte("// SPDX-FileCopyrightText: 2025 Latere AI\n// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(c, root, &strings.Builder{}); err != nil {
		t.Fatalf("after write the gate passes: %v", err)
	}
	// A second write changes nothing.
	sb.Reset()
	if err := Write(c, root, &sb, now); err != nil || !strings.Contains(sb.String(), "written on 0 file(s)") {
		t.Errorf("second write: %v\n%s", err, sb.String())
	}
}

func TestWriteNeedsBothDeclarations(t *testing.T) {
	for _, c := range []config.License{{SPDX: "MIT"}, {Holder: "Latere AI"}} {
		if err := Write(c, t.TempDir(), &strings.Builder{}, time.Now()); err == nil {
			t.Errorf("%+v must be refused: nothing here is guessed", c)
		}
	}
}

// A file git does not track is not the repository's to notice: another
// change's work in progress sitting in the tree fails nothing.
func TestAnUntrackedFileIsNotChecked(t *testing.T) {
	root := repo(t, map[string]string{
		"a/a.go": "// SPDX-FileCopyrightText: 2026 Latere AI\n// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n",
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "a/a.go", "LICENSE"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if outb, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, outb)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a", "wip_test.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := Run(cfg(), root, &sb); err != nil {
		t.Fatalf("an untracked file must not fail the gate: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "declared on 1 file(s)") {
		t.Errorf("only the tracked file is counted:\n%s", sb.String())
	}
	// Write leaves it alone too.
	if err := Write(cfg(), root, &strings.Builder{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "a", "wip_test.go"))
	if string(body) != "package a\n" {
		t.Error("write must not touch an untracked file")
	}
}

// git lists an untracked directory once, not its files, and a nested
// checkout is another tree entirely. Neither is written into.
func TestUntrackedDirectoriesAndNestedCheckoutsAreLeftAlone(t *testing.T) {
	root := repo(t, map[string]string{
		"a/a.go": "// SPDX-FileCopyrightText: 2026 Latere AI\n// SPDX-License-Identifier: AGPL-3.0-or-later\n\npackage a\n",
	})
	git := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if outb, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, outb)
		}
	}
	git(root, "init", "-q")
	git(root, "add", "a/a.go", "LICENSE")
	// An untracked directory holding a Go file.
	if err := os.MkdirAll(filepath.Join(root, "scratch", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scratch", "x", "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A nested checkout, tracked or not, is a different tree.
	nested := filepath.Join(root, ".claude", "worktrees", "wt")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	git(nested, "init", "-q")
	if err := os.WriteFile(filepath.Join(nested, "n.go"), []byte("package n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(cfg(), root, &strings.Builder{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "scratch", "x", "x.go"), filepath.Join(nested, "n.go")} {
		body, _ := os.ReadFile(p)
		if strings.Contains(string(body), "SPDX") {
			t.Errorf("%s must not be written into", p)
		}
	}
	var sb strings.Builder
	if err := Run(cfg(), root, &sb); err != nil {
		t.Fatalf("neither file is the repository's: %v\n%s", err, sb.String())
	}
}
