// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"io"
	"strings"
	"testing"
)

func TestCgoFreeFindsAnImportFollowedByAComment(t *testing.T) {
	root := repo(t, map[string]string{
		"a.go": "package a\n\n// #include <stdio.h>\nimport \"C\" // nolint\n",
		"b.go": "package a\n\nimport (\n\t\"C\" // cgo\n)\n",
		"c.go": "package a\n\nimport    c \"C\"\n",
	})
	var sb strings.Builder
	if err := CgoFree(root, &sb, nil); err == nil {
		t.Fatal("cgo-free passed files that import \"C\" with a comment or spacing")
	}
	for _, want := range []string{"a.go:4", "b.go:4", "c.go:3"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("output does not name %s:\n%s", want, sb.String())
		}
	}
}

func TestCgoFreeFailsOnAFileItCannotParse(t *testing.T) {
	root := repo(t, map[string]string{"a.go": "package a\n", "bad.go": "package a\n\nimport (\n"})
	err := CgoFree(root, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "bad.go") {
		t.Fatalf("err = %v, want the unparsable file named", err)
	}
}
func TestCgoFreeLeavesNodeModulesOut(t *testing.T) {
	root := repo(t, map[string]string{
		"ok.go":                     "package a\n",
		"web/node_modules/dep/x.go": "package x\n\nimport \"C\"\n",
	})
	if err := CgoFree(root, io.Discard, nil); err != nil {
		t.Fatalf("cgo-free read a dependency's Go source under node_modules: %v", err)
	}
}
