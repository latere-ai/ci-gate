// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

type call struct {
	env    []string
	stream bool
	name   string
	args   []string
}

// fake returns an Exec that records its calls and replays canned output.
func fake(t *testing.T, calls *[]call, outputs ...any) Exec {
	t.Helper()
	i := 0
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{env, stream, name, args})
		if i >= len(outputs) {
			return nil, nil
		}
		o := outputs[i]
		i++
		switch v := o.(type) {
		case string:
			return []byte(v), nil
		case error:
			return nil, v
		}
		return nil, nil
	}
}

func TestPathForKeepsTheToolchainFirst(t *testing.T) {
	sep := string(os.PathListSeparator)
	if got, want := PathFor("/usr/local/go/bin", nil), "/usr/local/go/bin"; got != want {
		t.Errorf("PathFor = %q, want %q; empty allow is the strictest setting", got, want)
	}
	want := "/usr/local/go/bin" + sep + "/usr/bin" + sep + "/bin"
	if got := PathFor("/usr/local/go/bin", []string{"/usr/bin", "/bin"}); got != want {
		t.Errorf("PathFor = %q, want %q", got, want)
	}
}

func TestHermeticRunsTheSuiteWithAStrippedPath(t *testing.T) {
	var calls []call
	var sb strings.Builder
	cfg := config.Hermetic{Allow: []string{"/usr/bin"}}
	if err := Hermetic(cfg, "/opt/go/bin/go", &sb, fake(t, &calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("want one call, got %d", len(calls))
	}
	c := calls[0]
	if c.name != "/opt/go/bin/go" || strings.Join(c.args, " ") != "test ./..." {
		t.Errorf("ran %q %v", c.name, c.args)
	}
	if !c.stream {
		t.Error("the test run's output must reach the terminal")
	}
	want := "PATH=/opt/go/bin" + string(os.PathListSeparator) + "/usr/bin"
	if !hasEnv(c.env, want) {
		t.Errorf("child PATH not stripped; env has %v, want %q", pathOf(c.env), want)
	}
	if !strings.Contains(sb.String(), want) {
		t.Errorf("the PATH being used should be printed:\n%s", sb.String())
	}
}

// The developer's PATH must not survive into the child, or the gate proves
// nothing.
func TestHermeticReplacesRatherThanAppendsToPath(t *testing.T) {
	var calls []call
	if err := Hermetic(config.Hermetic{}, "/opt/go/bin/go", &strings.Builder{}, fake(t, &calls)); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, kv := range calls[0].env {
		if strings.HasPrefix(kv, "PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("child env has %d PATH entries, want exactly 1", n)
	}
	if got := pathOf(calls[0].env); got != "/opt/go/bin" {
		t.Errorf("PATH = %q, want only the toolchain", got)
	}
}

func TestHermeticReportsAFailingSuite(t *testing.T) {
	var calls []call
	err := Hermetic(config.Hermetic{}, "/opt/go/bin/go", &strings.Builder{},
		fake(t, &calls, errors.New("exit status 1")))
	if err == nil || !strings.Contains(err.Error(), "hermetic test run failed") {
		t.Fatalf("a failing suite must fail the gate, got %v", err)
	}
}

func TestHermeticReportsAMissingToolchain(t *testing.T) {
	var calls []call
	err := Hermetic(config.Hermetic{}, "definitely-not-a-real-go-binary", &strings.Builder{}, fake(t, &calls))
	if err == nil || !strings.Contains(err.Error(), "cannot locate") {
		t.Fatalf("a missing toolchain must be named, got %v", err)
	}
}

func TestFmtCheckPassesOnAFormattedTree(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := FmtCheck(&sb, fake(t, &calls, "a.go\x00b/b.go\x00", "")); err != nil {
		t.Fatalf("an empty gofmt -l must pass: %v", err)
	}
	// The file set comes from git, so a nested checkout parked inside the
	// repository is never handed to gofmt.
	if calls[0].name != "git" || !strings.Contains(strings.Join(calls[0].args, " "), "ls-files") {
		t.Errorf("first call was %q %v, want git ls-files", calls[0].name, calls[0].args)
	}
	if calls[1].name != "gofmt" || strings.Join(calls[1].args, " ") != "-l a.go b/b.go" {
		t.Errorf("ran %q %v", calls[1].name, calls[1].args)
	}
	if !strings.Contains(sb.String(), "gofmt-formatted") {
		t.Errorf("report:\n%s", sb.String())
	}
}

func TestFmtCheckNamesEveryUnformattedFile(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := FmtCheck(&sb, fake(t, &calls, "a.go\x00b.go\x00", "a.go\nb.go\n"))
	if err == nil || !strings.Contains(err.Error(), "2 file(s)") {
		t.Fatalf("want a count of unformatted files, got %v", err)
	}
	for _, f := range []string{"a.go", "b.go"} {
		if !strings.Contains(sb.String(), f) {
			t.Errorf("report should list %s:\n%s", f, sb.String())
		}
	}
}

func TestFmtCheckReportsAMissingGofmt(t *testing.T) {
	var calls []call
	if err := FmtCheck(&strings.Builder{}, fake(t, &calls, "a.go\x00", errors.New("not found"))); err == nil {
		t.Fatal("gofmt failing to run must be an error")
	}
}

// A repository with no tracked Go files is not a formatted repository; it is a
// checkout the gate could not read, and passing there would be vacuous.
func TestFmtCheckFailsWhenGitListsNothing(t *testing.T) {
	var calls []call
	err := FmtCheck(&strings.Builder{}, fake(t, &calls, ""))
	if err == nil || !strings.Contains(err.Error(), "no tracked Go files") {
		t.Fatalf("want a clear error, got %v", err)
	}
}

// pkgList is what `go list ./...` answers in these tests. The gate resolves
// the package set and passes it to go fix rather than ./..., so every case
// that reaches the fix call answers a list call first.
const pkgList = "example.com/m\nexample.com/m/internal/a\n"

func TestModernizePassesOnAnEmptyDiff(t *testing.T) {
	var calls []call
	if err := Modernize(config.Modernize{}, "go", &strings.Builder{}, fake(t, &calls, pkgList, "")); err != nil {
		t.Fatalf("an empty diff must pass: %v", err)
	}
	want := "fix -diff example.com/m example.com/m/internal/a"
	if got := strings.Join(calls[1].args, " "); got != want {
		t.Errorf("ran go %q, want %q", got, want)
	}
}

// A JavaScript package may ship Go source, and a repository with a frontend
// grows a tree of them the moment anyone installs dependencies. That code is
// not this repository's to modernize and no patch here could be applied to
// it, so ./... would fail the gate on every workstation with the frontend set
// up.
func TestModernizeLeavesNodeModulesAlone(t *testing.T) {
	var calls []call
	listed := "example.com/m\nexample.com/m/frontend/node_modules/flatted/golang/pkg/flatted\n"
	if err := Modernize(config.Modernize{}, "go", &strings.Builder{}, fake(t, &calls, listed, "")); err != nil {
		t.Fatalf("an empty diff must pass: %v", err)
	}
	if got := strings.Join(calls[1].args, " "); strings.Contains(got, "node_modules") {
		t.Errorf("a vendored JavaScript tree is not ours to fix, ran go %q", got)
	}
}

func TestModernizeFailsWhenTheModuleHasNoPackages(t *testing.T) {
	var calls []call
	err := Modernize(config.Modernize{}, "go", &strings.Builder{}, fake(t, &calls, "", ""))
	if err == nil || !strings.Contains(err.Error(), "no packages") {
		t.Fatalf("a gate with nothing to check has proven nothing, got %v", err)
	}
}

func TestModernizeReportsAFailingPackageList(t *testing.T) {
	var calls []call
	err := Modernize(config.Modernize{}, "go", &strings.Builder{}, fake(t, &calls, errors.New("boom")))
	if err == nil || !strings.Contains(err.Error(), "listing the module packages") {
		t.Fatalf("want a clear error, got %v", err)
	}
}

func TestModernizeFailsOnANonEmptyDiff(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := Modernize(config.Modernize{}, "go", &sb, fake(t, &calls, pkgList, "--- a.go\n+++ b.go\n"))
	if err == nil {
		t.Fatal("a non-empty diff must fail")
	}
	if !strings.Contains(sb.String(), "--- a.go") {
		t.Errorf("the patch belongs in the report:\n%s", sb.String())
	}
}

func TestModernizeDisablesTheConfiguredFixers(t *testing.T) {
	var calls []call
	help := "analyzers:\n    newexpr    rewrite new(T) forms\n    errorsastype  ...\n"
	cfg := config.Modernize{Disable: []string{"newexpr", "errorsastype"}}
	if err := Modernize(cfg, "go", &strings.Builder{}, fake(t, &calls, help, pkgList, "")); err != nil {
		t.Fatalf("disabled fixers should not fail the gate: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("want a help call, a list call and a fix call, got %d", len(calls))
	}
	want := "fix -diff -newexpr=false -errorsastype=false example.com/m example.com/m/internal/a"
	if got := strings.Join(calls[2].args, " "); got != want {
		t.Errorf("ran go %q, want %q", got, want)
	}
}

// The guard that matters: if go fix drops a fixer, -name=false is rejected and
// the check would report green over an unrun gate.
func TestModernizeFailsWhenADisabledFixerNoLongerExists(t *testing.T) {
	var calls []call
	help := "analyzers:\n    someother   ...\n"
	cfg := config.Modernize{Disable: []string{"newexpr"}}
	err := Modernize(cfg, "go", &strings.Builder{}, fake(t, &calls, help, ""))
	if err == nil || !strings.Contains(err.Error(), "pass silently") {
		t.Fatalf("a vanished fixer must fail loudly, got %v", err)
	}
}

// A fixer named only in prose is not a fixer that exists.
func TestModernizeDoesNotAcceptAFixerMentionedInProse(t *testing.T) {
	var calls []call
	help := "This tool used to carry newexpr, which was removed.\n"
	cfg := config.Modernize{Disable: []string{"newexpr"}}
	if err := Modernize(cfg, "go", &strings.Builder{}, fake(t, &calls, help, "")); err == nil {
		t.Fatal("a prose mention must not count as an available fixer")
	}
}

func TestModernizeReportsAFailingHelpCall(t *testing.T) {
	var calls []call
	cfg := config.Modernize{Disable: []string{"newexpr"}}
	err := Modernize(cfg, "go", &strings.Builder{}, fake(t, &calls, errors.New("boom")))
	if err == nil || !strings.Contains(err.Error(), "cannot list") {
		t.Fatalf("want a clear error, got %v", err)
	}
}

// go fix exits non-zero when a package does not type-check. That is a build
// error, and the build is another gate's job.
func TestModernizeTreatsAnEmptyPatchWithAnErrorAsAPass(t *testing.T) {
	var calls []call
	if err := Modernize(config.Modernize{}, "go", &strings.Builder{}, fake(t, &calls, pkgList, errors.New("exit 1"))); err != nil {
		t.Fatalf("an empty patch must pass whatever the exit code: %v", err)
	}
}

func hasEnv(env []string, kv string) bool {
	return slices.Contains(env, kv)
}

func pathOf(env []string) string {
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "PATH="); ok {
			return after
		}
	}
	return ""
}
