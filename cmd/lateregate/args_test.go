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

// -h after a command is a request for the usage, not a failure.
func TestHelpAfterACommandPrintsTheUsage(t *testing.T) {
	for _, cmd := range []string{"check", "release", "init", "list"} {
		var out strings.Builder
		if err := run([]string{cmd, "-h"}, &out); err != nil {
			t.Errorf("%s -h: %v", cmd, err)
		}
		if !strings.Contains(out.String(), "Usage:") || strings.Contains(out.String(), "help requested") {
			t.Errorf("%s -h must print the usage and nothing about a flag error:\n%.200s", cmd, out.String())
		}
	}
}

func TestOnlyANonEmptyCIValueOtherThanFalseOrZeroIsCI(t *testing.T) {
	for value, want := range map[string]bool{"": false, "false": false, "0": false, "true": true, "1": true, "yes": true} {
		t.Setenv("CI", value)
		if got := isCI(); got != want {
			t.Errorf("CI=%q: isCI() = %v, want %v", value, got, want)
		}
	}
}
