// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// step is what one process does when the runner starts it.
type step struct {
	stdout, stderr string
	err            error
	block          bool
}

// procs plays back a step for each process started, in order, and records
// what was started.
type procs struct {
	steps []step
	calls []Proc
}

func (p *procs) start(ctx context.Context, proc Proc) error {
	p.calls = append(p.calls, proc)
	var s step
	if len(p.steps) > 0 {
		s, p.steps = p.steps[0], p.steps[1:]
	}
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := io.WriteString(proc.Stdout, s.stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(proc.Stderr, s.stderr); err != nil {
		return err
	}
	return s.err
}

// stream is a go test -json stream, one event a line.
func stream(t *testing.T, events ...map[string]any) string {
	t.Helper()
	var b strings.Builder
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func ev(action, pkg, test, output string, elapsed float64) map[string]any {
	e := map[string]any{"Action": action, "Package": pkg}
	if test != "" {
		e["Test"] = test
	}
	if output != "" {
		e["Output"] = output
	}
	if elapsed != 0 {
		e["Elapsed"] = elapsed
	}
	return e
}

func testRunner(p *procs, out io.Writer) *Runner {
	return &Runner{
		Root: "/repo", GoBin: "go", Out: out, Env: []string{"HOME=/h", "PATH=/usr/bin"}, Start: p.start,
		LookPath: func(string) (string, error) { return "/bin/docker", nil },
		Grace:    time.Millisecond,
	}
}

func goSuite(name string) config.E2ESuite {
	return config.E2ESuite{Name: name, Why: "it proves " + name, Test: &config.E2ETest{Packages: []string{"./x"}}}
}

var passing = []map[string]any{
	ev("run", "p/x", "TestA", "", 0),
	ev("output", "p/x", "TestA", "=== RUN   TestA\n", 0),
	ev("pass", "p/x", "TestA", "", 0.25),
	ev("pass", "p/x", "", "", 0.3),
}

// A suite passes when its tests pass; the command is the one asked for, in
// the root, with the full environment and the entry's own variables.
func TestATestSuitePassesAndRunsTheCommandAsked(t *testing.T) {
	p := &procs{steps: []step{{stdout: stream(t, passing...)}}}
	var out bytes.Buffer
	s := goSuite("lux")
	s.Test = &config.E2ETest{Packages: []string{"./a", "./b/..."}, Tags: []string{"integration", "x"}, Run: "^TestA", Race: true, Count: 2}
	s.Timeout = "5m"
	s.Env = map[string]string{"MODE": "e2e"}
	err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: s}})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	c := p.calls[0]
	if c.Dir != "/repo" || c.Name != "go" {
		t.Errorf("call %+v", c)
	}
	if got, want := strings.Join(c.Args, " "), "test -json -count=2 -timeout=5m0s -tags=integration,x -run=^TestA -race ./a ./b/..."; got != want {
		t.Errorf("args %q, want %q", got, want)
	}
	if envGet(c.Env, "MODE") != "e2e" || envGet(c.Env, "HOME") != "/h" {
		t.Errorf("env %v", c.Env)
	}
	for _, want := range []string{"-- lux: it proves lux", "PASS p/x TestA (250ms)", "ok   p/x", "PASS lux ", "1 e2e suite(s) passed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// A failed test shows what it printed, the run goes on to the next suite,
// and the error names each suite that failed.
func TestAFailedSuiteDoesNotStopTheNext(t *testing.T) {
	p := &procs{steps: []step{
		{stdout: stream(t,
			ev("output", "p/x", "TestB", "    b_test.go:9: want 1, got 2\n", 0),
			ev("fail", "p/x", "TestB", "", 0.1),
			ev("output", "p/x", "", "FAIL\n", 0),
			ev("fail", "p/x", "", "", 0.2)), err: errors.New("exit status 1")},
		{stdout: stream(t, passing...)},
	}}
	var out bytes.Buffer
	err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: goSuite("one")}, {Suite: goSuite("two")}})
	if err == nil || !strings.Contains(err.Error(), "1 of 2 e2e suites failed: one") {
		t.Fatalf("err %v", err)
	}
	for _, want := range []string{"FAIL p/x TestB", "b_test.go:9: want 1, got 2", "FAIL p/x\n", "FAIL one", "1 test failed: p/x TestB", "PASS two"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(p.calls) != 2 {
		t.Errorf("%d suites ran", len(p.calls))
	}
}

// A skipped test fails the suite, named with the message it printed, unless
// the entry gives the reason skips are allowed.
func TestASkippedTestFailsTheSuiteUnlessAllowed(t *testing.T) {
	skip := stream(t,
		ev("output", "p/x", "TestDB", "=== RUN   TestDB\n", 0),
		ev("output", "p/x", "TestDB", "    db_test.go:12: DATABASE_URL is not set\n", 0),
		ev("output", "p/x", "TestDB", "--- SKIP: TestDB (0.00s)\n", 0),
		ev("skip", "p/x", "TestDB", "", 0),
		ev("skip", "p/y", "", "", 0),
		ev("pass", "p/x", "TestOK", "", 0.1),
		ev("pass", "p/x", "", "", 0.1))
	p := &procs{steps: []step{{stdout: skip}, {stdout: skip}}}
	var out bytes.Buffer
	r := testRunner(p, &out)
	err := r.Run(context.Background(), []Item{{Suite: goSuite("strict")}})
	if err == nil {
		t.Fatalf("a skip passed:\n%s", out.String())
	}
	for _, want := range []string{"SKIP p/x TestDB: DATABASE_URL is not set", "1 test skipped, and a skipped test proves nothing", "p/x TestDB: DATABASE_URL is not set"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	lenient := goSuite("lenient")
	lenient.AllowSkip = "the database tier is optional here"
	out.Reset()
	if err := r.Run(context.Background(), []Item{{Suite: lenient}}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "1 test skipped, allowed: the database tier is optional here") {
		t.Errorf("output:\n%s", out.String())
	}
}

// A skip with no message says so.
func TestASkipWithNoMessage(t *testing.T) {
	p := &procs{steps: []step{{stdout: stream(t, ev("skip", "p/x", "TestQ", "", 0), ev("pass", "p/x", "T", "", 0))}}}
	var out bytes.Buffer
	if err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: goSuite("a")}}); err == nil || !strings.Contains(out.String(), "p/x TestQ: no message") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// A run in which no test ran proves nothing, and fails.
func TestARunWithNoTestFails(t *testing.T) {
	p := &procs{steps: []step{{stdout: stream(t, ev("skip", "p/x", "", "", 0))}}}
	err := testRunner(p, io.Discard).Run(context.Background(), []Item{{Suite: goSuite("a")}})
	if err == nil {
		t.Fatal("passed")
	}
	p.steps = []step{{stdout: stream(t, ev("skip", "p/x", "", "", 0))}}
	var out bytes.Buffer
	_ = testRunner(p, &out).Run(context.Background(), []Item{{Suite: goSuite("a")}})
	if !strings.Contains(out.String(), "no test ran") {
		t.Errorf("output:\n%s", out.String())
	}
}

// A build failure is go test failing with no test failing; the lines the
// build and the package printed are shown, and go test's own lines pass
// through.
func TestABuildFailureIsShown(t *testing.T) {
	p := &procs{steps: []step{{
		stdout: "go: downloading nothing\n" + stream(t,
			map[string]any{"ImportPath": "p/x", "Action": "build-output", "Output": "x.go:3: undefined: y\n"},
			map[string]any{"ImportPath": "p/x", "Action": "build-fail"},
			ev("output", "p/x", "", "FAIL\tp/x [build failed]\n", 0),
			ev("fail", "p/x", "", "", 0)) + "{not json\n",
		stderr: "stderr line\n",
		err:    errors.New("exit status 1"),
	}}}
	var out bytes.Buffer
	err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: goSuite("a")}})
	if err == nil {
		t.Fatal("passed")
	}
	for _, want := range []string{"go: downloading nothing", "x.go:3: undefined: y", "[build failed]", "{not json", "stderr line", "go test failed: exit status 1 (p/x)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// A suite that outlives its timeout is stopped and fails with that reason.
func TestASuiteIsStoppedAtItsTimeout(t *testing.T) {
	for name, s := range map[string]config.E2ESuite{
		"test":    goSuite("slow"),
		"command": {Name: "slow", Why: "w", Command: "sleep 100"},
	} {
		t.Run(name, func(t *testing.T) {
			s.Timeout = "10ms"
			p := &procs{steps: []step{{block: true}}}
			var out bytes.Buffer
			err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: s}})
			if err == nil || !strings.Contains(out.String(), "timed out after 10ms") {
				t.Fatalf("%v\n%s", err, out.String())
			}
		})
	}
}

// A command suite runs under sh from the root; it fails on a non-zero exit,
// and on a line that reports a skipped test unless skips are allowed.
func TestACommandSuite(t *testing.T) {
	cmd := config.E2ESuite{Name: "walk", Why: "w", Command: "make walk"}
	p := &procs{steps: []step{{stdout: "ok\n", stderr: "warn\n"}}}
	var out bytes.Buffer
	r := testRunner(p, &out)
	if err := r.Run(context.Background(), []Item{{Suite: cmd}}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if c := p.calls[0]; c.Name != "sh" || strings.Join(c.Args, " ") != "-c make walk" || c.Dir != "/repo" {
		t.Errorf("call %+v", c)
	}
	if !strings.Contains(out.String(), "ok\n") || !strings.Contains(out.String(), "warn\n") {
		t.Errorf("output:\n%s", out.String())
	}

	p.steps = []step{{err: errors.New("exit status 2")}}
	out.Reset()
	if err := r.Run(context.Background(), []Item{{Suite: cmd}}); err == nil || !strings.Contains(out.String(), "the command failed: exit status 2") {
		t.Fatalf("%v\n%s", err, out.String())
	}

	skipping := "=== RUN TestA\n    --- SKIP: TestA (0.00s)\n--- SKIP: TestB (0.00s)\nnot --- SKIP in the line"
	p.steps = []step{{stdout: skipping}}
	out.Reset()
	if err := r.Run(context.Background(), []Item{{Suite: cmd}}); err == nil || !strings.Contains(out.String(), "2 tests skipped") ||
		!strings.Contains(out.String(), "--- SKIP: TestA (0.00s)") {
		t.Fatalf("%v\n%s", err, out.String())
	}

	cmd.AllowSkip = "the cluster tier skips off CI"
	p.steps = []step{{stdout: skipping}}
	out.Reset()
	if err := r.Run(context.Background(), []Item{{Suite: cmd}}); err != nil || !strings.Contains(out.String(), "2 tests skipped, allowed: the cluster tier skips off CI") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// Skipped items are named with their reasons, and a run with nothing to run
// says so.
func TestSkippedItemsAreNamed(t *testing.T) {
	p := &procs{}
	var out bytes.Buffer
	err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: goSuite("a"), Skip: "no Go package changed"}})
	if err != nil || len(p.calls) != 0 {
		t.Fatalf("%v", err)
	}
	for _, want := range []string{"SKIP a: no Go package changed", "SKIP a                no Go package changed", "no e2e suite runs here"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// A suite's services are started before it, handed to its env, and removed
// after it whether it passed or failed.
func TestServicesReachTheSuiteAndAreRemoved(t *testing.T) {
	for _, fail := range []bool{false, true} {
		e := scripted()
		p := &procs{steps: []step{{stdout: stream(t, passing...)}}}
		if fail {
			p.steps = []step{{stdout: stream(t, ev("fail", "p/x", "TestA", "", 0)), err: errors.New("exit status 1")}}
		}
		var out bytes.Buffer
		r := testRunner(p, &out)
		r.NewHost = func(engine, suite string, out io.Writer) *Host {
			h := testHost(e, out)
			h.Engine, h.Suite = engine, suite
			return h
		}
		s := goSuite("store")
		s.Services = []string{config.ServicePostgres}
		s.Env = map[string]string{"DATABASE_URL": "${postgres.url}"}
		err := r.Run(context.Background(), []Item{{Suite: s}})
		if (err != nil) != fail {
			t.Fatalf("fail=%v: %v\n%s", fail, err, out.String())
		}
		if got := envGet(p.calls[0].Env, "DATABASE_URL"); !strings.HasPrefix(got, "postgres://lateregate:") {
			t.Errorf("DATABASE_URL %q", got)
		}
		if !e.has("rm -f -v lateregate-e2e-store-postgres-") {
			t.Errorf("fail=%v: the container was not removed: %q", fail, e.ran)
		}
	}
}

// A suite whose services cannot start, or whose env names what no service
// gave, fails before it runs anything, and its services are still removed.
func TestASuiteThatCannotBeSetUpRunsNothing(t *testing.T) {
	s := goSuite("store")
	s.Services = []string{config.ServicePostgres}
	for name, tc := range map[string]struct {
		setup func(*engine, *Runner)
		env   map[string]string
		want  string
	}{
		"no engine":      {func(_ *engine, r *Runner) { r.LookPath = func(string) (string, error) { return "", os.ErrNotExist } }, nil, "neither docker nor podman"},
		"will not start": {func(e *engine, _ *Runner) { e.fail["run"] = true }, nil, "postgres: the container does not start"},
		"bad reference":  {func(*engine, *Runner) {}, map[string]string{"X": "${minio.region}"}, "env X"},
	} {
		t.Run(name, func(t *testing.T) {
			e := scripted()
			p := &procs{}
			var out bytes.Buffer
			r := testRunner(p, &out)
			r.NewHost = func(engine, suite string, out io.Writer) *Host { return testHost(e, out) }
			tc.setup(e, r)
			s := s
			s.Env = tc.env
			if err := r.Run(context.Background(), []Item{{Suite: s}}); err == nil || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("%v\n%s", err, out.String())
			}
			if len(p.calls) != 0 {
				t.Error("the suite ran")
			}
		})
	}
}

// A container that cannot be removed fails the suite that passed.
func TestAContainerLeftBehindFailsTheSuite(t *testing.T) {
	e := scripted()
	e.fail["rm"] = true
	p := &procs{steps: []step{{stdout: stream(t, passing...)}}}
	r := testRunner(p, io.Discard)
	r.NewHost = func(engine, suite string, out io.Writer) *Host { return testHost(e, out) }
	s := goSuite("store")
	s.Services = []string{config.ServicePostgres}
	if err := r.Run(context.Background(), []Item{{Suite: s}}); err == nil {
		t.Fatal("passed with a container left behind")
	}
}

// The tools are on the suite's PATH, first, whatever PATH the machine has.
func TestToolsComeFirstOnPath(t *testing.T) {
	kubectl, archive := []byte("k"), helmArchive(t, "h")
	srv, _ := release(t, kubectl, archive)
	f := testFetcher(t, srv, kubectl, archive)
	p := &procs{steps: []step{{stdout: stream(t, passing...)}, {}}}
	r := testRunner(p, io.Discard)
	r.Tools = f
	s := goSuite("walk")
	s.Tools = []string{"kubectl", "helm"}
	if err := r.Run(context.Background(), []Item{{Suite: s}}); err != nil {
		t.Fatal(err)
	}
	path := envGet(p.calls[0].Env, "PATH")
	kdir, hdir := strings.Split(path, string(os.PathListSeparator))[0], strings.Split(path, string(os.PathListSeparator))[1]
	if !strings.HasSuffix(kdir, "kubectl-v1") || !strings.HasSuffix(hdir, "helm-v2") || !strings.HasSuffix(path, "/usr/bin") {
		t.Errorf("PATH %q", path)
	}
	s.Tools = []string{"terraform"}
	var out bytes.Buffer
	r.Out = &out
	if err := r.Run(context.Background(), []Item{{Suite: s}}); err == nil || !strings.Contains(out.String(), "no tool is called") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestEnvHelpers(t *testing.T) {
	env := []string{"A=1", "AB=2", "A_=3"}
	if envGet(env, "A") != "1" || envGet(env, "Z") != "" {
		t.Error("envGet")
	}
	got := envSet(env, "A", "9")
	if envGet(got, "A") != "9" || envGet(got, "AB") != "2" || len(got) != 3 {
		t.Errorf("envSet %v", got)
	}
}

// Output is passed through whole and in pieces; a last line with no newline
// is still read.
func TestSkipScannerReadsLinesAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	s := &skipScanner{out: &out}
	for _, piece := range []string{"--- SK", "IP: TestA\n  ---", " SKIP: TestB"} {
		if _, err := s.Write([]byte(piece)); err != nil {
			t.Fatal(err)
		}
	}
	s.flush()
	s.flush()
	if len(s.skips) != 2 || out.String() != "--- SKIP: TestA\n  --- SKIP: TestB" {
		t.Errorf("%v %q", s.skips, out.String())
	}
	if _, err := (&skipScanner{out: errWriter{}}).Write([]byte("x")); err == nil {
		t.Error("a failed write was swallowed")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// The real process runner: a shell command's output and exit, its stop at
// the deadline, and the real runner wired to the machine.
func TestOSStart(t *testing.T) {
	needShell(t)
	var out bytes.Buffer
	w := &lockedWriter{w: &out}
	if err := OSStart(context.Background(), Proc{Dir: t.TempDir(), Env: os.Environ(), Name: "sh", Args: []string{"-c", "echo out; echo err >&2"}, Stdout: w, Stderr: w}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "out") || !strings.Contains(out.String(), "err") {
		t.Errorf("output %q", out.String())
	}
	if err := OSStart(context.Background(), Proc{Name: "sh", Args: []string{"-c", "exit 3"}, Stdout: w, Stderr: w}); err == nil {
		t.Error("exit 3 passed")
	}
	// The shell starts a child, and stopping the shell stops it too.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	begin := time.Now()
	err := OSStart(ctx, Proc{Name: "sh", Args: []string{"-c", "sleep 60 & wait"}, Stdout: w, Stderr: w})
	if err == nil || time.Since(begin) > 20*time.Second {
		t.Errorf("a stopped shell: %v after %s", err, time.Since(begin))
	}
}

func TestNewRunnerIsWiredToTheMachine(t *testing.T) {
	needShell(t)
	r := NewRunner(t.TempDir(), "go", io.Discard, nil)
	if r.Start == nil || r.Tools == nil || r.NewHost == nil || r.LookPath == nil || len(r.Env) == 0 || r.Grace == 0 {
		t.Errorf("%+v", r)
	}
	if h := r.NewHost("docker", "s", io.Discard); h.Engine != "docker" || h.Suite != "s" {
		t.Errorf("%+v", h)
	}
	if _, err := r.LookPath("sh"); err != nil {
		t.Error(err)
	}
	if err := r.RunSignals(nil); err != nil {
		t.Error(err)
	}
	if filepath.Base(r.Root) == "" {
		t.Error("root")
	}
}

// needShell skips a test that runs a shell where the suite's PATH holds none.
func needShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
}

// A long list of failures is cut short in the summary; each was printed where
// it failed.
func TestAHundredFailuresAreCountedNotListed(t *testing.T) {
	var events []map[string]any
	for i := range 20 {
		events = append(events, ev("fail", "p/x", "Test"+strings.Repeat("x", i), "", 0))
	}
	p := &procs{steps: []step{{stdout: stream(t, events...), err: errors.New("exit status 1")}}}
	var out bytes.Buffer
	err := testRunner(p, &out).Run(context.Background(), []Item{{Suite: goSuite("a")}})
	if err == nil || !strings.Contains(out.String(), "20 tests failed: ") || !strings.Contains(out.String(), "and 12 more") {
		t.Fatalf("%v\n%s", err, out.String())
	}
}
