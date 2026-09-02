// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// A commit that touches no Go file has nothing for the hook to say.
func TestHookPassesWithNothingStaged(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := Hook(config.Modernize{}, "go", &sb, fake(t, &calls, "")); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !strings.HasPrefix(joined(calls[0]), "git diff --cached") {
		t.Fatalf("ran %v", calls)
	}
	if !strings.Contains(sb.String(), "no Go files staged") {
		t.Errorf("report:\n%s", sb.String())
	}
}

func TestHookFailsOnAnUnformattedStagedFile(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := Hook(config.Modernize{}, "go", &sb, fake(t, &calls,
		"a/a.go\x00b/b.go\x00", // staged
		"b/b.go\n",             // gofmt -l
	))
	if err == nil || !strings.Contains(err.Error(), "not gofmt-formatted") {
		t.Fatalf("want the format failure, got %v", err)
	}
	if !strings.Contains(sb.String(), "b/b.go") {
		t.Errorf("the file must be named:\n%s", sb.String())
	}
	if len(calls) != 2 {
		t.Errorf("go fix must not run on unformatted files; ran %v", calls)
	}
}

// The modernizers run over the packages holding the staged files, with the
// fixers the config disables turned off: the same config the full gate
// reads, so the hook cannot disagree with it.
func TestHookModernizesTheStagedPackagesWithTheConfiguredFixersOff(t *testing.T) {
	var calls []call
	var sb strings.Builder
	cfg := config.Modernize{Disable: []string{"newexpr"}}
	err := Hook(cfg, "go", &sb, fake(t, &calls,
		"internal/a/a.go\x00internal/a/b.go\x00main.go\x00", // staged
		"",                    // gofmt -l: clean
		"    newexpr  desc\n", // go tool fix help
		"",                    // go fix -diff: no patch
	))
	if err != nil {
		t.Fatal(err)
	}
	last := joined(calls[len(calls)-1])
	if last != "go fix -diff -newexpr=false . ./internal/a" {
		t.Errorf("go fix ran as %q", last)
	}
}

func TestHookReportsAPatchWithTheCommandToApplyIt(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := Hook(config.Modernize{}, "go", &sb, fake(t, &calls,
		"x/x.go\x00",
		"",
		"--- a/x/x.go\n+++ b/x/x.go\n",
	))
	if err == nil || !strings.Contains(err.Error(), "go fix -diff ./x") {
		t.Fatalf("the failure must say how to apply the fix to the same scope, got %v", err)
	}
	if !strings.Contains(sb.String(), "+++ b/x/x.go") {
		t.Errorf("the patch must be printed:\n%s", sb.String())
	}
}

func TestHookReportsGitFailing(t *testing.T) {
	var calls []call
	err := Hook(config.Modernize{}, "go", &strings.Builder{}, fake(t, &calls, errors.New("not a git repository")))
	if err == nil || !strings.Contains(err.Error(), "listing staged") {
		t.Fatalf("got %v", err)
	}
}

// The shared hook is recognised by its delegation line, not by its bytes,
// so a repository may add lines of its own.
func TestIsSharedHook(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   bool
	}{
		{"the shipped script", Staged, true},
		{"with extra lines", "#!/bin/sh\n./scripts/own-check.sh\ngo tool lateregate hook\n", true},
		{"only in a comment", "#!/bin/sh\n# lateregate hook used to be here\ngofmt -l .\n", false},
		{"the old hand-rolled hook", "#!/bin/sh\ngofmt -l $files\ngo fix -diff -newexpr=false ./...\n", false},
	} {
		if got := IsSharedHook(tc.script); got != tc.want {
			t.Errorf("%s: IsSharedHook = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A generator under testdata is outside ./..., which the full gate reads;
// the hook holds it to gofmt and nothing more.
func TestHookLeavesTestdataPackagesToGofmtOnly(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := Hook(config.Modernize{}, "go", &sb, fake(t, &calls,
		"tok/testdata/gen/main.go\x00", // staged
		"",                             // gofmt -l: clean
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Errorf("go fix must not run on a testdata package; ran %v", calls)
	}
	if !strings.Contains(sb.String(), "under testdata") {
		t.Errorf("report:\n%s", sb.String())
	}
}
