// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// A commit that touches no Go file has nothing for the hook to say.
func TestHookPassesWithNothingStaged(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := Hook(&config.Config{}, t.TempDir(), "example.com/m", "go", &sb, fake(t, &calls, "")); err != nil {
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
	err := Hook(&config.Config{}, t.TempDir(), "example.com/m", "go", &sb, fake(t, &calls,
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
	cfg := &config.Config{Modernize: config.Modernize{Disable: []string{"newexpr"}}}
	err := Hook(cfg, t.TempDir(), "example.com/m", "go", &sb, fake(t, &calls,
		"internal/a/a.go\x00internal/a/b.go\x00main.go\x00", // staged
		"",                    // gofmt -l: clean
		"",                    // goimports -l: grouped
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
	err := Hook(&config.Config{}, t.TempDir(), "example.com/m", "go", &sb, fake(t, &calls,
		"x/x.go\x00",
		"", // gofmt
		"", // goimports
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
	err := Hook(&config.Config{}, t.TempDir(), "", "go", &strings.Builder{}, fake(t, &calls, errors.New("not a git repository")))
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
// the hook holds it to the file scans and nothing more.
func TestHookLeavesTestdataPackagesToGofmtOnly(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := Hook(&config.Config{}, t.TempDir(), "example.com/m", "go", &sb, fake(t, &calls,
		"tok/testdata/gen/main.go\x00", // staged
		"",                             // gofmt -l: clean
		"",                             // goimports -l: grouped
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Errorf("go fix must not run on a testdata package; ran %v", calls)
	}
	if !strings.Contains(sb.String(), "under testdata") {
		t.Errorf("report:\n%s", sb.String())
	}
}

// stage writes rel under a temp root and returns the root, so a scan that
// reads the staged file has something to read.
func stage(t *testing.T, rel, src string) string {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The grouping rule is the linter's goimports check with the module path as
// the local prefix, run on the staged files with the same flag, so the hook
// fails exactly the file CI would.
func TestHookFailsOnImportsTheLinterWouldRegroup(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := Hook(&config.Config{}, t.TempDir(), "example.com/m", "go", &sb, fake(t, &calls,
		"a/a.go\x00", // staged
		"",           // gofmt -l: clean
		"a/a.go\n",   // goimports -l: would regroup
	))
	if err == nil || !strings.Contains(err.Error(), "goimports -local example.com/m -w") {
		t.Fatalf("the failure must say how to regroup, got %v", err)
	}
	if !strings.Contains(sb.String(), "a/a.go") {
		t.Errorf("the file must be named:\n%s", sb.String())
	}
	got := joined(calls[2])
	if want := "go run " + GoimportsModule + "@" + GoimportsVersion + " -local example.com/m -l a/a.go"; got != want {
		t.Errorf("goimports ran as %q, want %q", got, want)
	}
	if len(calls) != 3 {
		t.Errorf("nothing runs after the grouping failure; ran %v", calls)
	}
}

// Outside a module there is no local prefix, and the flag is left off
// rather than passed empty.
func TestHookRunsGoimportsWithoutALocalPrefixOutsideAModule(t *testing.T) {
	var calls []call
	if err := Hook(&config.Config{}, t.TempDir(), "", "go", &strings.Builder{}, fake(t, &calls, "a.go\x00", "", "", "")); err != nil {
		t.Fatal(err)
	}
	if got := joined(calls[2]); got != "go run "+GoimportsModule+"@"+GoimportsVersion+" -l a.go" {
		t.Errorf("goimports ran as %q", got)
	}
}

// The licence check is the gate's own, on the staged files, when the
// repository declared a licence. It prints the same finding and the same
// shape the gate prints, so the fix is the same.
func TestHookFailsOnAStagedFileWithoutTheNotice(t *testing.T) {
	cfg := &config.Config{License: config.License{SPDX: "MIT", Holder: "Latere AI", Extensions: []string{".go"}}}
	root := stage(t, "a/a.go", "package a\n")
	var calls []call
	var sb strings.Builder
	err := Hook(cfg, root, "example.com/m", "go", &sb, fake(t, &calls, "a/a.go\x00", "", ""))
	if err == nil || !strings.Contains(err.Error(), "without the declared MIT notice") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(sb.String(), "a/a.go: no SPDX-FileCopyrightText: on line 1") {
		t.Errorf("the gate's own finding must be printed:\n%s", sb.String())
	}

	root = stage(t, "a/a.go", "// SPDX-FileCopyrightText: 2026 Latere AI\n// SPDX-License-Identifier: MIT\n\npackage a\n")
	calls = nil
	if err := Hook(cfg, root, "example.com/m", "go", &sb, fake(t, &calls, "a/a.go\x00", "", "", "")); err != nil {
		t.Fatalf("a noticed file passes: %v", err)
	}
}

// No declared licence, no licence check: the gate does not apply either.
func TestHookSkipsTheNoticeWhenNoLicenceIsDeclared(t *testing.T) {
	root := stage(t, "a/a.go", "package a\n")
	var calls []call
	if err := Hook(&config.Config{}, root, "example.com/m", "go", &strings.Builder{}, fake(t, &calls, "a/a.go\x00", "", "", "")); err != nil {
		t.Fatal(err)
	}
}

// The instrumentation rule is the gate's own, on the staged non-test files.
func TestHookFailsOnAStagedUninstrumentedClient(t *testing.T) {
	const src = "package a\n\nimport \"net/http\"\n\nvar c = &http.Client{}\n"
	root := stage(t, "a/a.go", src)
	var calls []call
	var sb strings.Builder
	err := Hook(&config.Config{}, root, "example.com/m", "go", &sb, fake(t, &calls, "a/a.go\x00", "", ""))
	if err == nil || !strings.Contains(err.Error(), "uninstrumented outbound HTTP client") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(sb.String(), "a/a.go:5:") {
		t.Errorf("the file and line must be named:\n%s", sb.String())
	}
	if len(calls) != 3 {
		t.Errorf("go fix must not run after the instrumentation failure; ran %v", calls)
	}

	// The same content in a test file is outside the rule, as in the gate.
	root = stage(t, "a/a_test.go", src)
	calls = nil
	if err := Hook(&config.Config{}, root, "example.com/m", "go", &sb, fake(t, &calls, "a/a_test.go\x00", "", "", "")); err != nil {
		t.Fatalf("a test file is outside the rule: %v", err)
	}
}

func TestDelegates(t *testing.T) {
	if !Delegates(Prepush, PrepushInvocation) {
		t.Error("the shipped pre-push must delegate")
	}
	if Delegates(Prepush, HookInvocation) {
		t.Error("the pre-push does not run the pre-commit checks")
	}
	own := "#!/bin/sh\nrefs=$(cat)\nprintf '%s\\n' \"$refs\" | go tool lateregate prepush || exit 1\necho \"$refs\" | ./own-check.sh\n"
	if !Delegates(own, PrepushInvocation) {
		t.Error("a pre-push with its own lines around the delegation delegates")
	}
}
