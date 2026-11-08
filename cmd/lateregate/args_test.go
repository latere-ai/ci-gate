// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

// A command word after a leading flag is not the command. The bar must not
// run and pass for a command that never ran.
func TestACommandAfterALeadingFlagIsRefused(t *testing.T) {
	t.Setenv("CI", "")
	root := t.TempDir()
	for _, argv := range [][]string{
		{"-C", root, "release", "v1.2.3"},
		{"-all", "release", "v1.2.3"},
		{"-since", "green", "deploy"},
		{"-json", "list"},
		{"-C", root, "contract"},
		{"check", "-C", root, "extra"},
	} {
		var out strings.Builder
		err := run(argv, &out)
		if err == nil {
			t.Errorf("%v exited 0 and ran %q as check", argv, out.String())
			continue
		}
		if !strings.Contains(err.Error(), "a command comes before its flags") || !strings.Contains(err.Error(), "lateregate release -C DIR v1.2.3") {
			t.Errorf("%v: the message must say a command comes first and show how, got %v", argv, err)
		}
	}
}

func TestTheRefusalNamesTheArgument(t *testing.T) {
	err := run([]string{"-C", t.TempDir(), "release-notes", "v1.0.0"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), `"release-notes"`) {
		t.Fatalf("want the argument named, got %v", err)
	}
}
