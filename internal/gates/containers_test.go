// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"latere.ai/x/ci-gate/internal/containers"
)

// engine stands in for a container engine: it lists what it holds, filtered
// by label the way an engine filters, and removes on request.
type engine struct {
	mu      sync.Mutex
	held    []containers.Container
	removed []string
	// down makes every request fail the way an engine that is not running
	// does behind a socket that still exists.
	down bool
}

func (e *engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.down {
		http.Error(w, `{"message":"no machine"}`, http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodDelete {
		id := strings.TrimPrefix(r.URL.Path, "/containers/")
		e.removed = append(e.removed, id)
		e.held = slices.DeleteFunc(e.held, func(c containers.Container) bool { return c.ID == id })
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var q map[string][]string
	_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &q)
	var out []containers.Container
	for _, c := range e.held {
		k, v, hasValue := strings.Cut(q["label"][0], "=")
		if got, ok := c.Labels[k]; ok && (!hasValue || got == v) {
			out = append(out, c)
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (e *engine) hold(c containers.Container) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.held = append(e.held, c)
}

// withEngine makes e the engine the gates find, behind a unix socket at a
// short relative path, since a socket path is limited to about a hundred
// bytes and a test's temporary directory can exceed that alone.
func withEngine(t *testing.T, e *engine) {
	t.Helper()
	t.Chdir(t.TempDir())
	l, err := net.Listen("unix", "e.sock")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: e}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	setFindEngine(t, func() (*containers.Engine, bool) { return containers.Socket("e.sock"), true })
}

func setFindEngine(t *testing.T, f func() (*containers.Engine, bool)) {
	t.Helper()
	prev := findEngine
	findEngine = f
	t.Cleanup(func() { findEngine = prev })
}

// startsContainers stands in for a suite whose tests start n containers
// under the run label and stop the first stopped of them.
func startsContainers(e *engine, n, stopped int) func([]string) {
	return func(env []string) {
		run, _ := envValue(env, containers.RunEnv)
		for i := range n {
			id := "c" + string(rune('0'+i))
			if i < stopped {
				continue
			}
			e.hold(containers.Container{ID: id, Image: "postgres:17-alpine", State: "running",
				Labels: containers.Labels(1, "box", run)})
		}
	}
}

func exitedPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

var runIDShape = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestSuiteNamesItsRunAndPassesWhenNothingIsLeft(t *testing.T) {
	root := isolated(t)
	e := &engine{}
	withEngine(t, e)
	s := &suiteRun{do: startsContainers(e, 2, 2)}
	var sb strings.Builder
	if err := Suite(SuiteRun{Containers: true}, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatalf("a suite that stopped its containers must pass: %v\n%s", err, sb.String())
	}
	if run, _ := envValue(s.testCall(t).env, containers.RunEnv); !runIDShape.MatchString(run) {
		t.Fatalf("%s=%q, want a run id", containers.RunEnv, run)
	}
	if !strings.Contains(sb.String(), "the suite passes: no container left behind") {
		t.Fatalf("the verdict must say the containers were watched:\n%s", sb.String())
	}
}

func TestSuiteRemovesAndNamesWhatTheRunLeft(t *testing.T) {
	root := isolated(t)
	e := &engine{}
	withEngine(t, e)
	other := containers.Container{ID: "other-run", Labels: containers.Labels(os.Getpid(), "box", "another")}
	e.hold(other)
	s := &suiteRun{do: startsContainers(e, 3, 1)}
	var sb strings.Builder
	err := Suite(SuiteRun{Containers: true}, root, suiteGo, &sb, s.exec)
	if err == nil || !strings.Contains(err.Error(), "the suite left 2 containers behind") {
		t.Fatalf("err = %v, want the two left containers counted", err)
	}
	if !slices.Equal(e.removed, []string{"c1", "c2"}) {
		t.Fatalf("removed %v, want the run's two and nothing of another run's", e.removed)
	}
	if !strings.Contains(sb.String(), "  c1 postgres:17-alpine (running)") {
		t.Fatalf("each left container must be named:\n%s", sb.String())
	}
}

func TestSuiteReportsTheLeakBesideAFailure(t *testing.T) {
	root := isolated(t)
	e := &engine{}
	withEngine(t, e)
	s := &suiteRun{do: startsContainers(e, 1, 0), err: errors.New("exit status 1")}
	err := Suite(SuiteRun{Containers: true}, root, suiteGo, &strings.Builder{}, s.exec)
	if err == nil || !strings.Contains(err.Error(), "the test suite failed") ||
		!strings.Contains(err.Error(), "the suite left 1 container behind") {
		t.Fatalf("err = %v, want both the failure and the leak", err)
	}
}

func TestSuiteSweepsWhatAnExitedProcessLeftBeforeItRuns(t *testing.T) {
	root := isolated(t)
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	e := &engine{held: []containers.Container{
		{ID: "orphan", Image: "postgres:17-alpine", Labels: containers.Labels(exitedPid(t), host, "killed-run")},
		{ID: "in-use", Labels: containers.Labels(os.Getpid(), host, "live-run")},
	}}
	withEngine(t, e)
	var removedBeforeTests []string
	s := &suiteRun{do: func([]string) { removedBeforeTests = slices.Clone(e.removed) }}
	var sb strings.Builder
	if err := Suite(SuiteRun{Containers: true}, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatalf("what another run left must not fail this one: %v", err)
	}
	if !slices.Equal(removedBeforeTests, []string{"orphan"}) {
		t.Fatalf("removed %v before the tests, want only the exited process's container", removedBeforeTests)
	}
	if !strings.Contains(sb.String(), "removed orphan postgres:17-alpine, which an exited process left") {
		t.Fatalf("the sweep must say what it removed:\n%s", sb.String())
	}
}

func TestSuiteWithoutAnEngineSaysSoAndClaimsNothing(t *testing.T) {
	root := isolated(t)
	setFindEngine(t, func() (*containers.Engine, bool) { return nil, false })
	s := &suiteRun{}
	var sb strings.Builder
	if err := Suite(SuiteRun{Containers: true}, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatal(err)
	}
	if _, ok := envValue(s.testCall(t).env, containers.RunEnv); ok {
		t.Fatal("a run with nothing watching it was named")
	}
	out := sb.String()
	if !strings.Contains(out, "no container engine socket") || strings.Contains(out, "no container left behind") {
		t.Fatalf("the output must say nothing was watched, and not claim otherwise:\n%s", out)
	}
}

func TestSuiteWithAnEngineThatDoesNotAnswerWatchesNothing(t *testing.T) {
	root := isolated(t)
	withEngine(t, &engine{down: true})
	s := &suiteRun{}
	var sb strings.Builder
	if err := Suite(SuiteRun{Containers: true}, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "the suite's containers are not watched: ") {
		t.Fatalf("the output must say why nothing was watched:\n%s", sb.String())
	}
}

func TestSuiteReportsAnEngineThatStopsAnsweringDuringTheRun(t *testing.T) {
	root := isolated(t)
	e := &engine{}
	withEngine(t, e)
	s := &suiteRun{do: func([]string) { e.mu.Lock(); e.down = true; e.mu.Unlock() }}
	err := Suite(SuiteRun{Containers: true}, root, suiteGo, &strings.Builder{}, s.exec)
	if err == nil || !strings.Contains(err.Error(), "removing the containers the suite left") {
		t.Fatalf("err = %v, want a check that could not run to fail", err)
	}
}

func TestSuiteLeavesTheRunUnnamedWhenTheWatchIsOff(t *testing.T) {
	root := isolated(t)
	setFindEngine(t, func() (*containers.Engine, bool) {
		t.Fatal("a run with the watch off looked for an engine")
		return nil, false
	})
	t.Setenv(containers.RunEnv, "inherited")
	s := &suiteRun{}
	if err := Suite(SuiteRun{}, root, suiteGo, &strings.Builder{}, s.exec); err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(s.testCall(t).env, containers.RunEnv); v != "inherited" {
		t.Fatalf("%s=%q, want the environment untouched", containers.RunEnv, v)
	}
}

func TestContainersGate(t *testing.T) {
	e := &engine{}
	withEngine(t, e)

	var sb strings.Builder
	s := &suiteRun{do: startsContainers(e, 1, 1)}
	if err := Containers(suiteGo, &sb, s.exec); err != nil {
		t.Fatalf("a clean suite must pass: %v", err)
	}
	if got := strings.Join(s.testCall(t).args, " "); got != "test ./..." {
		t.Fatalf("go %s, want the plain suite", got)
	}
	if !strings.Contains(sb.String(), "the suite leaves no container behind") {
		t.Fatalf("the verdict:\n%s", sb.String())
	}

	s = &suiteRun{do: startsContainers(e, 1, 0), err: errors.New("exit status 1")}
	err := Containers(suiteGo, &strings.Builder{}, s.exec)
	if err == nil || !strings.Contains(err.Error(), "the suite left 1 container behind") {
		t.Fatalf("err = %v, want the leak before the suite's own failure", err)
	}

	s = &suiteRun{err: errors.New("exit status 1")}
	if err := Containers(suiteGo, &strings.Builder{}, s.exec); err == nil || !strings.Contains(err.Error(), "the test suite failed") {
		t.Fatalf("err = %v, want the suite's failure", err)
	}
}

func TestContainersGateWithoutAnEngineRunsTheSuite(t *testing.T) {
	setFindEngine(t, func() (*containers.Engine, bool) { return nil, false })
	s := &suiteRun{}
	var sb strings.Builder
	if err := Containers(suiteGo, &sb, s.exec); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb.String(), "no container behind") {
		t.Fatalf("nothing was watched, so nothing may be claimed:\n%s", sb.String())
	}
	s = &suiteRun{err: errors.New("exit status 1")}
	if err := Containers(suiteGo, &strings.Builder{}, s.exec); err == nil {
		t.Fatal("a failed suite passed")
	}
}
