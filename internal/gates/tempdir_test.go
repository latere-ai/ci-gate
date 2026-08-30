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

// suite returns an Exec standing in for a test run. do receives the sandbox
// the gate pointed the run at, so a test writes exactly what it wants to
// survive. err is what the run itself reports.
func suite(t *testing.T, calls *[]call, do func(sandbox string), err error) Exec {
	t.Helper()
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, call{env, stream, name, args})
		if do != nil {
			do(tempDirOf(t, env))
		}
		return nil, err
	}
}

// tempDirOf reads the sandbox back out of the environment the gate built,
// which is the only channel a real subprocess has for it.
func tempDirOf(t *testing.T, env []string) string {
	t.Helper()
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "TMPDIR="); ok {
			return v
		}
	}
	t.Fatal("the gate ran the suite without TMPDIR")
	return ""
}

// leak writes a directory holding n bytes, the way a test that forgot to
// remove its os.MkdirTemp leaves one.
func leak(t *testing.T, dir, name string, n int) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "cache"), make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

// touch creates an entry and removes it, which is what a suite that cleans up
// after itself does and what separates it from one that ignored TMPDIR.
func touch(t *testing.T, dir string) {
	t.Helper()
	leak(t, dir, "work", 1)
	if err := os.RemoveAll(filepath.Join(dir, "work")); err != nil {
		t.Fatal(err)
	}
}

func TestTempDirPassesWhenTheSuiteCleansUp(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, func(d string) { touch(t, d) }, nil))
	if err != nil {
		t.Fatalf("a suite that removed its own directory must pass: %v", err)
	}
	if !strings.Contains(sb.String(), "nothing survived") {
		t.Errorf("report does not say the sandbox was clean:\n%s", sb.String())
	}
	if len(calls) != 1 {
		t.Fatalf("want one call, got %d", len(calls))
	}
	if calls[0].name != "go" || strings.Join(calls[0].args, " ") != "test ./..." {
		t.Errorf("default command is %q %v, want go test ./...", calls[0].name, calls[0].args)
	}
	if !calls[0].stream {
		t.Error("the test run's output must reach the terminal")
	}
}

func TestTempDirFailsOnWhatSurvives(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, func(d string) {
		leak(t, d, "nanogo-corpus1234", 2048)
		leak(t, d, "nanogo-audit99", 1024)
	}, nil))
	if err == nil {
		t.Fatal("two leaked directories must fail the gate")
	}
	if !strings.Contains(err.Error(), "2 entries survived") {
		t.Errorf("error does not count the leaks: %v", err)
	}
	// Sorted, so a report does not change between runs over the same leak.
	audit, corpus := strings.Index(sb.String(), "nanogo-audit99"), strings.Index(sb.String(), "nanogo-corpus1234")
	if audit < 0 || corpus < 0 || audit > corpus {
		t.Errorf("report does not name both leaks in order:\n%s", sb.String())
	}
	// The size is what makes a leak worth acting on.
	if !strings.Contains(sb.String(), "2.0KB") {
		t.Errorf("report does not size the leak:\n%s", sb.String())
	}
}

func TestTempDirAdmitsAnAllowedPrefix(t *testing.T) {
	var calls []call
	var sb strings.Builder
	cfg := config.TempDir{Allow: map[string]string{"go-build": "the toolchain's own cache, which outlives any one run by design"}}
	err := TempDir(cfg, nil, &sb, suite(t, &calls, func(d string) { leak(t, d, "go-build3321", 16) }, nil))
	if err != nil {
		t.Fatalf("an allowed prefix must pass: %v", err)
	}
	if !strings.Contains(sb.String(), "allowed go-build3321: the toolchain's own cache") {
		t.Errorf("report does not carry the reason the entry was admitted:\n%s", sb.String())
	}
}

// A sandbox nothing wrote to is the failure mode that would make this gate
// report a perfect score having measured nothing: a suite launched through a
// wrapper that resets the environment lands somewhere else entirely.
func TestTempDirRefusesToPassOnAnUnusedSandbox(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, nil, nil))
	if err == nil {
		t.Fatal("an empty sandbox nothing ever wrote to must not pass")
	}
	if !strings.Contains(err.Error(), "did not use it") {
		t.Errorf("error does not explain the vacuous pass: %v", err)
	}
}

func TestTempDirReportsASuiteThatFailed(t *testing.T) {
	var calls []call
	var sb strings.Builder
	boom := errors.New("exit status 1")
	err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, func(d string) { touch(t, d) }, boom))
	if !errors.Is(err, boom) {
		t.Fatalf("a failing suite must be reported: %v", err)
	}
}

// A red suite gets re-run. A leak that only surfaces on a green one is a leak
// nobody ever sees, so the leak is the verdict when both happened.
func TestTempDirReportsTheLeakBeforeTheSuiteFailure(t *testing.T) {
	var calls []call
	var sb strings.Builder
	err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, func(d string) {
		leak(t, d, "left-behind", 4)
	}, errors.New("exit status 1")))
	if err == nil || !strings.Contains(err.Error(), "survived") {
		t.Fatalf("the leak must win over the suite failure: %v", err)
	}
}

func TestTempDirOverridesTheConfiguredCommand(t *testing.T) {
	var calls []call
	var sb strings.Builder
	cfg := config.TempDir{Command: []string{"make", "test"}}
	argv := []string{"pytest", "-q"}
	if err := TempDir(cfg, argv, &sb, suite(t, &calls, func(d string) { touch(t, d) }, nil)); err != nil {
		t.Fatal(err)
	}
	if calls[0].name != "pytest" || strings.Join(calls[0].args, " ") != "-q" {
		t.Errorf("ran %q %v, want the argv override", calls[0].name, calls[0].args)
	}
}

func TestTempDirRunsTheConfiguredCommand(t *testing.T) {
	var calls []call
	var sb strings.Builder
	cfg := config.TempDir{Command: []string{"cargo", "test", "--all"}}
	if err := TempDir(cfg, nil, &sb, suite(t, &calls, func(d string) { touch(t, d) }, nil)); err != nil {
		t.Fatal(err)
	}
	if calls[0].name != "cargo" || strings.Join(calls[0].args, " ") != "test --all" {
		t.Errorf("ran %q %v, want the configured command", calls[0].name, calls[0].args)
	}
}

// The gate makes a directory of its own. One that leaked while checking for
// leaks would be worse than no gate at all.
func TestTempDirRemovesItsOwnSandbox(t *testing.T) {
	var calls []call
	var sb strings.Builder
	if err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, func(d string) {
		leak(t, d, "whatever", 8)
	}, nil)); err == nil {
		t.Fatal("expected the leak to fail the gate")
	}
	sandbox := tempDirOf(t, calls[0].env)
	if _, err := os.Stat(sandbox); !os.IsNotExist(err) {
		t.Errorf("%s outlived the gate: %v", sandbox, err)
	}
}

// Windows tools read TMP and TEMP rather than TMPDIR, and a suite that shells
// out to one would land outside the sandbox if only TMPDIR were set.
func TestTempDirPointsEveryTemporaryVariableAtTheSandbox(t *testing.T) {
	// A real directory, because the gate makes its sandbox inside it, and a
	// stale value in each of the other two.
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("TMP", "/somewhere/else")
	t.Setenv("TEMP", "/somewhere/else")
	var calls []call
	var sb strings.Builder
	if err := TempDir(config.TempDir{}, nil, &sb, suite(t, &calls, func(d string) { touch(t, d) }, nil)); err != nil {
		t.Fatal(err)
	}
	sandbox := tempDirOf(t, calls[0].env)
	for _, v := range tempVars {
		want := v + "=" + sandbox
		if n := count(calls[0].env, want); n != 1 {
			t.Errorf("env carries %q %d times, want once; the caller's value must be replaced, not added to", want, n)
		}
	}
}

func count(env []string, want string) int {
	n := 0
	for _, kv := range env {
		if kv == want {
			n++
		}
	}
	return n
}

func TestHumanSizeReadsLikeDf(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
		{160 * 1024 * 1024 * 1024, "160.0GB"},
	} {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestTempDirArgvDefaultsToTheGoSuite(t *testing.T) {
	if got := strings.Join(config.TempDir{}.Argv(), " "); got != "go test ./..." {
		t.Errorf("default Argv is %q", got)
	}
}
