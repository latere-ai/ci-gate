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

// The toolchain and compiler a suite test runs against. Both are absolute,
// so neither is looked up on the test machine's PATH.
const (
	suiteGo = "/toolchain/bin/go"
	suiteCC = "/compilers/bin/cc"
)

// suiteRun stands in for the toolchain: vet passes, `go env CC` names
// suiteCC, and the test run calls do with its environment and returns what
// it printed and err.
type suiteRun struct {
	calls  []call
	vetErr error
	do     func(env []string)
	output string
	err    error
}

func (s *suiteRun) exec(env []string, stream bool, name string, args ...string) ([]byte, error) {
	s.calls = append(s.calls, call{env, stream, name, args})
	switch {
	case len(args) > 0 && args[0] == "vet":
		return nil, s.vetErr
	case len(args) > 1 && args[0] == "env" && args[1] == "CC":
		return []byte(suiteCC + "\n"), nil
	case len(args) > 0 && args[0] == "test":
		if s.do != nil {
			s.do(env)
		}
		return []byte(s.output), s.err
	}
	return nil, errors.New("unexpected command " + name + " " + strings.Join(args, " "))
}

// testCall is the go test the suite ran.
func (s *suiteRun) testCall(t *testing.T) call {
	t.Helper()
	for _, c := range s.calls {
		if len(c.args) > 0 && c.args[0] == "test" {
			return c
		}
	}
	t.Fatal("the suite ran no go test")
	return call{}
}

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// every is a run with each property on and a floor that passes.
func every(floor *[]string) SuiteRun {
	return SuiteRun{
		Race: true, Hermetic: true, Sandbox: true, Profile: "coverage.out",
		Floor: func(p string) error {
			*floor = append(*floor, p)
			return nil
		},
	}
}

// cleanSuite touches the sandbox the way a go test run does and leaves it
// empty.
func cleanSuite(t *testing.T) func([]string) {
	return func(env []string) {
		dir, _ := envValue(env, "TMPDIR")
		touch(t, dir)
	}
}

func TestSuitePassesWithEveryPropertyInOneRun(t *testing.T) {
	root := isolated(t)
	var floor []string
	s := &suiteRun{do: cleanSuite(t)}
	var sb strings.Builder
	if err := Suite(every(&floor), root, suiteGo, &sb, s.exec); err != nil {
		t.Fatalf("a clean suite must pass: %v\n%s", err, sb.String())
	}
	tests := 0
	for _, c := range s.calls {
		if len(c.args) > 0 && c.args[0] == "test" {
			tests++
		}
	}
	if tests != 1 {
		t.Fatalf("the suite ran go test %d times, want once", tests)
	}
	c := s.testCall(t)
	if got := strings.Join(c.args, " "); got != "test -race -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./..." {
		t.Errorf("go %s", got)
	}
	if !c.stream {
		t.Error("the suite's output must reach the terminal")
	}
	if v, _ := envValue(c.env, "CGO_ENABLED"); v != "1" {
		t.Errorf("CGO_ENABLED=%q, the race detector needs cgo", v)
	}
	if v, _ := envValue(c.env, "PATH"); v != "/toolchain/bin"+string(os.PathListSeparator)+"/compilers/bin" {
		t.Errorf("PATH=%q, want the toolchain and the compiler only", v)
	}
	if v, _ := envValue(c.env, "TMPDIR"); !strings.Contains(v, "lateregate-tempdir-") {
		t.Errorf("TMPDIR=%q, want the repository's sandbox", v)
	}
	if len(floor) != 1 || floor[0] != filepath.Join(root, "coverage.out") {
		t.Errorf("the floor read %v, want the profile the run wrote", floor)
	}
	if !strings.Contains(sb.String(), "the suite passes: race-clean, over the coverage floor, nothing left under TMPDIR, on the stripped PATH") {
		t.Errorf("the verdict must name what was checked:\n%s", sb.String())
	}
}

// Each failure's first line names the property, since one job now carries
// what five job names used to say.
func TestSuiteNamesEachBrokenProperty(t *testing.T) {
	exit := errors.New("exit status 1")
	for _, tc := range []struct {
		name   string
		output string
		err    error
		do     func(t *testing.T) func([]string)
		floor  error
		want   string
	}{
		{name: "race", output: "==================\nWARNING: DATA RACE\nWrite at 0x00c000\n", err: exit,
			want: "the suite is not race-clean: exit status 1"},
		{name: "tool off the stripped PATH", output: `    probe_test.go:12: exec: "docker": executable file not found in $PATH` + "\n", err: exit,
			want: "the suite reached for docker, which is not on the stripped PATH: exit status 1"},
		{name: "a test that fails", output: "--- FAIL: TestX\n", err: exit,
			want: "the test suite failed: exit status 1"},
		{name: "survivor", do: func(t *testing.T) func([]string) {
			return func(env []string) {
				dir, _ := envValue(env, "TMPDIR")
				leak(t, dir, "fixture-cache", 2048)
			}
		}, want: "the suite left 1 entry under TMPDIR, 2.0KB in all"},
		{name: "floor", floor: errors.New("below 90%: internal/x 50.0%"),
			want: "the suite's coverage is under the floor: below 90%: internal/x 50.0%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := isolated(t)
			s := &suiteRun{output: tc.output, err: tc.err, do: cleanSuite(t)}
			if tc.do != nil {
				s.do = tc.do(t)
			}
			opt := every(&[]string{})
			if tc.floor != nil {
				opt.Floor = func(string) error { return tc.floor }
			}
			var sb strings.Builder
			err := Suite(opt, root, suiteGo, &sb, s.exec)
			if err == nil {
				t.Fatalf("want %q, the suite passed:\n%s", tc.want, sb.String())
			}
			if first, _, _ := strings.Cut(err.Error(), "\n"); !strings.HasPrefix(first, tc.want) {
				t.Errorf("first line %q, want %q", first, tc.want)
			}
		})
	}
}

// A leak on a red run is still reported: a red suite gets re-run, and a leak
// that only shows on a green one is a leak nobody sees. The floor is not read
// over a run that failed.
func TestSuiteReportsALeakBesideAFailureAndSkipsTheFloor(t *testing.T) {
	root := isolated(t)
	var floor []string
	s := &suiteRun{output: "--- FAIL: TestX\n", err: errors.New("exit status 1"), do: func(env []string) {
		dir, _ := envValue(env, "TMPDIR")
		leak(t, dir, "left-behind", 8)
	}}
	err := Suite(every(&floor), root, suiteGo, &strings.Builder{}, s.exec)
	if err == nil || !strings.Contains(err.Error(), "the test suite failed") || !strings.Contains(err.Error(), "\nand the suite left 1 entry") {
		t.Fatalf("want both the failure and the leak, got %v", err)
	}
	if len(floor) != 0 {
		t.Error("the floor must not be read over a failed run")
	}
}

func TestSuiteStopsAtVet(t *testing.T) {
	root := isolated(t)
	s := &suiteRun{vetErr: errors.New("exit status 1")}
	err := Suite(every(&[]string{}), root, suiteGo, &strings.Builder{}, s.exec)
	if err == nil || !strings.HasPrefix(err.Error(), "go vet failed") {
		t.Fatalf("want the vet failure, got %v", err)
	}
	for _, c := range s.calls {
		if len(c.args) > 0 && c.args[0] == "test" {
			t.Fatal("a package vet rejects is not worth running")
		}
	}
}

// A waiver turns one property off and leaves the run: the rest still holds.
func TestSuiteNarrowsToWhatIsOn(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	root := isolated(t)
	s := &suiteRun{}
	var sb strings.Builder
	opt := SuiteRun{Timeout: "45m"}
	if err := Suite(opt, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatal(err)
	}
	c := s.testCall(t)
	if got := strings.Join(c.args, " "); got != "test -timeout 45m ./..." {
		t.Errorf("go %s, want no -race and no profile", got)
	}
	if v, _ := envValue(c.env, "CGO_ENABLED"); v != "0" {
		t.Errorf("CGO_ENABLED=%q, without -race the caller's value stands", v)
	}
	if v, _ := envValue(c.env, "PATH"); v != os.Getenv("PATH") {
		t.Errorf("PATH=%q, without hermetic the full PATH stands", v)
	}
	if v, _ := envValue(c.env, "TMPDIR"); v != os.Getenv("TMPDIR") {
		t.Errorf("TMPDIR=%q, without tempdir the run is not sandboxed", v)
	}
	if strings.Contains(sb.String(), "PATH=") || strings.Contains(sb.String(), "TMPDIR=") {
		t.Errorf("a property that is off prints nothing:\n%s", sb.String())
	}
	for _, c := range s.calls {
		if len(c.args) > 1 && c.args[0] == "env" {
			t.Error("without -race the C compiler is not looked up")
		}
	}
}

// The race detector needs cgo and cgo needs the compiler, so the stripped
// PATH keeps its directory and says why; with race waived it is the strict
// PATH the hermetic gate has always used.
func TestSuitePathKeepsTheCompilerOnlyUnderRace(t *testing.T) {
	for _, race := range []bool{true, false} {
		root := isolated(t)
		s := &suiteRun{do: cleanSuite(t)}
		var sb strings.Builder
		opt := SuiteRun{Race: race, Hermetic: true, Allow: []string{"/opt/tools"}}
		if err := Suite(opt, root, suiteGo, &sb, s.exec); err != nil {
			t.Fatal(err)
		}
		sep := string(os.PathListSeparator)
		want := "PATH=/toolchain/bin" + sep + "/opt/tools\n"
		if race {
			want = "PATH=/toolchain/bin" + sep + "/compilers/bin" + sep + "/opt/tools (kept for the race detector's C compiler /compilers/bin/cc)\n"
		}
		if !strings.Contains(sb.String(), want) {
			t.Errorf("race=%v: want %q in\n%s", race, want, sb.String())
		}
	}
}

// A compiler that sits in an allowed directory, or in the toolchain's, is on
// the PATH once.
func TestSuitePathNamesEachDirectoryOnce(t *testing.T) {
	for _, allow := range [][]string{{"/compilers/bin", "/opt/tools"}, {"/opt/tools", "/compilers/bin"}} {
		root := isolated(t)
		s := &suiteRun{do: cleanSuite(t)}
		var sb strings.Builder
		if err := Suite(SuiteRun{Race: true, Hermetic: true, Allow: allow}, root, suiteGo, &sb, s.exec); err != nil {
			t.Fatal(err)
		}
		v, _ := envValue(s.testCall(t).env, "PATH")
		if strings.Count(v, "/compilers/bin") != 1 || !strings.Contains(v, "/opt/tools") {
			t.Errorf("allow %v: PATH=%q", allow, v)
		}
	}
	root := isolated(t)
	s := &suiteRun{do: cleanSuite(t)}
	if err := Suite(SuiteRun{Race: true, Hermetic: true}, root, "/compilers/bin/go", &strings.Builder{}, s.exec); err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(s.testCall(t).env, "PATH"); v != "/compilers/bin" {
		t.Errorf("a compiler beside the toolchain: PATH=%q", v)
	}
}

// A compiler named rather than given as a path is found on the PATH as it
// was before stripping; one that is not there fails before the suite runs.
func TestSuiteCompilerIsResolvedBeforeStripping(t *testing.T) {
	bin := t.TempDir()
	cc := filepath.Join(bin, "fakecc")
	if err := os.WriteFile(cc, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	named := func(name string) Exec {
		return func(_ []string, _ bool, _ string, args ...string) ([]byte, error) {
			if len(args) > 1 && args[0] == "env" {
				return []byte(name + " -m64\n"), nil
			}
			return nil, nil
		}
	}
	got, err := compiler(suiteGo, named("fakecc"))
	if err != nil || got != cc {
		t.Fatalf("compiler() = %q, %v; want %s", got, err, cc)
	}
	if _, err := compiler(suiteGo, named("nosuchcc")); err == nil || !strings.Contains(err.Error(), "the race detector needs cgo") {
		t.Fatalf("a missing compiler must say what needs it, got %v", err)
	}
	failing := func([]string, bool, string, ...string) ([]byte, error) { return nil, errors.New("no toolchain") }
	if _, err := compiler(suiteGo, failing); err == nil {
		t.Fatal("a toolchain that cannot answer must fail")
	}
}

// The sandbox the suite runs in is tempdir's: the same path every run, so the
// test cache replays, and it admits what tempdir.allow admits.
func TestSuiteSandboxAdmitsTheAllowList(t *testing.T) {
	root := isolated(t)
	s := &suiteRun{do: func(env []string) {
		dir, _ := envValue(env, "TMPDIR")
		leak(t, dir, "go-build123", 8)
	}}
	opt := SuiteRun{Sandbox: true, TempDir: config.TempDir{Allow: map[string]string{"go-build": "a go build -work under test"}}}
	var sb strings.Builder
	if err := Suite(opt, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatalf("an admitted survivor must pass: %v", err)
	}
	if !strings.Contains(sb.String(), "allowed go-build123: a go build -work under test") {
		t.Errorf("the admission must be printed:\n%s", sb.String())
	}
	name, _ := sandboxName(root)
	if again := filepath.Join(os.TempDir(), name); !strings.Contains(sb.String(), "TMPDIR="+again) {
		t.Errorf("the suite must run in tempdir's sandbox %s:\n%s", again, sb.String())
	}
}

// A run that never touched the sandbox proves nothing about it.
func TestSuiteRefusesAnUnusedSandbox(t *testing.T) {
	root := isolated(t)
	s := &suiteRun{}
	err := Suite(SuiteRun{Sandbox: true}, root, suiteGo, &strings.Builder{}, s.exec)
	if err == nil || !strings.Contains(err.Error(), "nothing ever wrote to") {
		t.Fatalf("want the unused-sandbox refusal, got %v", err)
	}
}
