// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tcImport   = "package testdb\n\nimport (\n\t\"context\"\n\n\t\"github.com/testcontainers/testcontainers-go\"\n)\n"
	reapImport = "package testdb\n\nimport \"latere.ai/x/ci-gate/reap\"\n"
)

func TestContainerLabelsPassesAPackageThatImportsReap(t *testing.T) {
	var sb strings.Builder
	root := repo(t, map[string]string{
		"internal/testdb/testdb.go": tcImport,
		"internal/testdb/reap.go":   reapImport,
		"main.go":                   "package main\n",
	})
	if err := ContainerLabels(root, &sb); err != nil {
		t.Fatalf("a package that imports reap should pass: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "imports reap: internal/testdb") {
		t.Errorf("the pass should name the packages it judged:\n%s", sb.String())
	}
}

func TestContainerLabelsReadsTestFiles(t *testing.T) {
	// cella starts its containers from _test.go files with no helper, and an
	// external test package shares the directory of the package it tests.
	var sb strings.Builder
	root := repo(t, map[string]string{
		"store/store.go":       "package store\n",
		"store/store_test.go":  "package store_test\n\nimport \"github.com/testcontainers/testcontainers-go/modules/postgres\"\n",
		"store/labels_test.go": "package store_test\n\nimport \"latere.ai/x/ci-gate/reap\"\n",
		"cmd/d/d_test.go":      "package main\n\nimport tc \"github.com/testcontainers/testcontainers-go\"\n",
	})
	err := ContainerLabels(root, &sb)
	if err == nil {
		t.Fatalf("a test file that imports testcontainers without reap should fail:\n%s", sb.String())
	}
	if !strings.Contains(sb.String(), "cmd/d/d_test.go:3: imports github.com/testcontainers/testcontainers-go,") {
		t.Errorf("the finding should name the test file and line:\n%s", sb.String())
	}
	if strings.Contains(sb.String(), "store/") {
		t.Errorf("a package whose sibling test file imports reap was reported:\n%s", sb.String())
	}
	if !strings.Contains(err.Error(), "1 package imports testcontainers-go without reap") {
		t.Errorf("the verdict should count packages: %v", err)
	}
}

func TestContainerLabelsNamesEachPackageOnce(t *testing.T) {
	var sb strings.Builder
	root := repo(t, map[string]string{
		"a/a.go":      tcImport,
		"a/a_test.go": "package testdb\n\nimport \"github.com/testcontainers/testcontainers-go/network\"\n",
		"b/b.go":      "package b\n\nimport \"github.com/testcontainers/testcontainers-go/wait\"\n",
		"c/c.go":      reapImport, // reap with nothing to label is no finding
	})
	err := ContainerLabels(root, &sb)
	if err == nil || !strings.Contains(err.Error(), "2 packages import") {
		t.Fatalf("two packages without reap should fail as two: %v\n%s", err, sb.String())
	}
	lines := strings.Split(strings.TrimSpace(sb.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "a/a.go:6:") || !strings.Contains(lines[1], "b/b.go:3:") {
		t.Errorf("each package should be named once, at its first file:\n%s", sb.String())
	}
}

func TestContainerLabelsReadsOnlyThePackagesOfThisTree(t *testing.T) {
	// An agent's worktree under the repository is another checkout, on its
	// own version of the contract; the go command ignores testdata, vendor,
	// and names that start with a dot or an underscore.
	var sb strings.Builder
	root := repo(t, map[string]string{
		"pkg.go":                              "package pkg\n",
		".worktrees/old/internal/db/db.go":    tcImport,
		"wt/.git":                             "gitdir: /elsewhere\n",
		"wt/internal/db/db.go":                tcImport,
		"testdata/fixture.go":                 tcImport,
		"vendor/github.com/x/y.go":            tcImport,
		"node_modules/flatted/golang/f.go":    tcImport,
		"_scratch/s.go":                       tcImport,
		"internal/db/_ignored.go":             tcImport,
		"internal/db/.hidden.go":              tcImport,
		"internal/db/README.md":               "import \"github.com/testcontainers/testcontainers-go\"\n",
		"internal/db/broken.go":               "package db\n\nimport (\n",
		"internal/db/db.go":                   "package db\n",
		"internal/db/commented.go":            "package db\n\n// import \"github.com/testcontainers/testcontainers-go\"\n",
		"internal/db/string.go":               "package db\n\nconst m = \"github.com/testcontainers/testcontainers-go\"\n",
		"internal/db/notcontainers/n.go":      "package n\n\nimport \"github.com/testcontainers/testcontainers-gold\"\n",
		"internal/db/notcontainers/n_test.go": "package n\n",
	})
	if err := ContainerLabels(root, &sb); err != nil {
		t.Fatalf("nothing outside this tree's packages should count: %v\n%s", err, sb.String())
	}
	if !strings.Contains(sb.String(), "no package imports testcontainers-go") {
		t.Errorf("a tree with no subject should say so rather than claim a pass:\n%s", sb.String())
	}
	ok, err := ImportsTestcontainers(root)
	if err != nil || ok {
		t.Errorf("ImportsTestcontainers = %v, %v; want no subject", ok, err)
	}
}

func TestContainerLabelsFailsOnNoGoFiles(t *testing.T) {
	var sb strings.Builder
	root := repo(t, map[string]string{"README.md": "# nothing\n"})
	err := ContainerLabels(root, &sb)
	if err == nil || !strings.Contains(err.Error(), "vacuously") {
		t.Fatalf("a tree with no Go files should fail rather than pass: %v", err)
	}
}

func TestImportsTestcontainersFindsTheSubject(t *testing.T) {
	root := repo(t, map[string]string{"x_test.go": "package x\n\nimport _ \"github.com/testcontainers/testcontainers-go\"\n"})
	ok, err := ImportsTestcontainers(root)
	if err != nil || !ok {
		t.Fatalf("ImportsTestcontainers = %v, %v; want a subject", ok, err)
	}
}

func TestContainerLabelsReportsAnUnreadableTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if err := ContainerLabels(root, io.Discard); err == nil {
		t.Fatal("a root that does not exist should fail")
	}
	if _, err := ImportsTestcontainers(root); err == nil {
		t.Fatal("a root that does not exist should fail")
	}
}
