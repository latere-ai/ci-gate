// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// engineOrSkip is the container engine on this machine, or skips the test
// where there is none or it does not answer; the suite gate's PATH holds
// neither.
func engineOrSkip(t *testing.T) string {
	t.Helper()
	engine, err := FindEngine(exec.LookPath)
	if err != nil {
		t.Skip(err)
	}
	if out, err := exec.Command(engine, "info").CombinedOutput(); err != nil {
		t.Skipf("%s does not answer: %v\n%s", engine, err, out)
	}
	return engine
}

// The real thing: a suite's Go test reaches a Postgres that lateregate
// started from the pinned image, and the container is gone afterward whether
// the suite passed or not.
func TestARealPostgresSuiteRunsAndLeavesNoContainer(t *testing.T) {
	engine := engineOrSkip(t)
	mod, err := filepath.Abs(filepath.Join("testdata", "dbmod"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := NewRunner(mod, "go", &out, gates.OSExec(mod, &out))
	r.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	store := config.E2ESuite{
		Name: "store", Why: "the Postgres answers a startup message",
		Test:     &config.E2ETest{Packages: []string{"./..."}, Tags: []string{"postgres"}, Count: 1},
		Services: []string{config.ServicePostgres},
		Env:      map[string]string{"DATABASE_URL": "${postgres.url}"},
		Timeout:  "10m",
	}
	// The same tests with nothing to reach: the connection is refused, and the
	// service is removed all the same.
	broken := store
	broken.Name = "broken"
	broken.Env = map[string]string{"DATABASE_URL": "postgres://u:p@127.0.0.1:1/d"}
	broken.Timeout = "5m"

	err = r.Run(t.Context(), []Item{{Suite: store}, {Suite: broken}})
	t.Logf("%s", out.String())
	if err == nil || !strings.Contains(err.Error(), "1 of 2 e2e suites failed: broken") {
		t.Fatalf("err %v", err)
	}
	for _, want := range []string{"PASS example.com/dbmod TestPostgresAnswers", "PASS store", "FAIL broken", "postgres answers on 127.0.0.1:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q", want)
		}
	}
	for _, suite := range []string{"store", "broken"} {
		name := fmt.Sprintf("lateregate-e2e-%s-postgres-%d", suite, os.Getpid())
		left, err := exec.Command(engine, "ps", "-aq", "--filter", "name="+name).Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(left)) != "" {
			t.Errorf("container %s is left behind: %s", name, left)
		}
	}
}

// A real go test run whose test skips fails the suite and names the test.
func TestARealSkippedTestFailsTheSuite(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip(err)
	}
	mod, err := filepath.Abs(filepath.Join("testdata", "dbmod"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r := NewRunner(mod, "go", &out, gates.OSExec(mod, &out))
	s := config.E2ESuite{Name: "skips", Why: "w", Test: &config.E2ETest{Packages: []string{"./..."}, Tags: []string{"skipper"}}}
	if err := r.Run(t.Context(), []Item{{Suite: s}}); err == nil {
		t.Fatalf("a skipped test passed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "example.com/dbmod TestNeedsACluster: no cluster here") {
		t.Errorf("output:\n%s", out.String())
	}
	s.AllowSkip = "the cluster tier is optional"
	out.Reset()
	if err := r.Run(t.Context(), []Item{{Suite: s}}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// MinIO starts from its pinned image, answers its endpoint and holds the
// buckets asked for, and is removed.
func TestARealMinIOStartsWithItsBuckets(t *testing.T) {
	engine := engineOrSkip(t)
	var out bytes.Buffer
	h := NewHost(engine, "repos", &out, gates.OSExec(".", &out))
	h.Wait = 2 * time.Minute
	vals, err := h.Start(t.Context(), config.E2ESuite{Services: []string{config.ServiceMinIO}, Buckets: []string{"origo", "blobs"}})
	t.Cleanup(func() {
		if err := h.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if err := ready(t.Context(), vals["minio.endpoint"]+"/minio/health/ready"); err != nil {
		t.Error(err)
	}
	if len(h.started) != 1 {
		t.Fatalf("started %v, want the one MinIO container", h.started)
	}
	listed, err := exec.Command(engine, "exec", h.started[0], "mc", "ls", "local").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, listed)
	}
	for _, b := range []string{"origo/", "blobs/"} {
		if !strings.Contains(string(listed), b) {
			t.Errorf("bucket %s is missing:\n%s", b, listed)
		}
	}
}
