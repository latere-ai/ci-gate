// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// engine answers the container engine's commands from a script: the output
// for a command's first argument, an error for the ones in fail, and for the
// ones in failAfter an error from that call on.
type engine struct {
	ran   []string
	out   map[string]string
	fail  map[string]bool
	calls map[string]int
}

func (e *engine) exec(_ []string, _ bool, name string, args ...string) ([]byte, error) {
	e.ran = append(e.ran, name+" "+strings.Join(args, " "))
	key := args[0]
	if key == "container" {
		key = "inspect"
	}
	if e.calls == nil {
		e.calls = map[string]int{}
	}
	e.calls[key]++
	if e.fail[key] {
		return []byte(e.out[key]), errors.New("exit 1")
	}
	return []byte(e.out[key]), nil
}

func (e *engine) has(sub string) bool {
	for _, r := range e.ran {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func testHost(e *engine, out io.Writer) *Host {
	h := NewHost("podman", "store", out, e.exec)
	h.Wait, h.Poll = 200*time.Millisecond, time.Millisecond
	h.Dial = func(context.Context, string) error { return nil }
	h.Ready = func(context.Context, string) error { return nil }
	return h
}

func scripted() *engine {
	return &engine{out: map[string]string{"port": "127.0.0.1:49153\n"}, fail: map[string]bool{}}
}

// Postgres starts from the pinned image on a port the engine picks, with a
// random password, and the suite gets a URL that reaches it.
func TestPostgresStartsAndGivesItsURL(t *testing.T) {
	e := scripted()
	var out bytes.Buffer
	h := testHost(e, &out)
	vals, err := h.Start(context.Background(), config.E2ESuite{Services: []string{config.ServicePostgres}})
	if err != nil {
		t.Fatal(err)
	}
	url := vals["postgres.url"]
	if !strings.HasPrefix(url, "postgres://lateregate:") || !strings.HasSuffix(url, "@127.0.0.1:49153/lateregate?sslmode=disable") {
		t.Fatalf("url %q", url)
	}
	run := e.ran[0]
	for _, want := range []string{
		"podman run -d --name lateregate-e2e-store-postgres-", "-p 127.0.0.1::5432", "--label ai.latere.ci-gate.owner=",
		"-e POSTGRES_USER=lateregate", "-e POSTGRES_DB=lateregate", PostgresImage,
	} {
		if !strings.Contains(run, want) {
			t.Errorf("run %q lacks %q", run, want)
		}
	}
	if !e.has("pg_isready -h 127.0.0.1 -U lateregate -d lateregate") {
		t.Errorf("ran %q", e.ran)
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if last := e.ran[len(e.ran)-1]; !strings.HasPrefix(last, "podman rm -f -v lateregate-e2e-store-postgres-") {
		t.Errorf("last command %q", last)
	}
	if err := h.Stop(); err != nil || e.calls["rm"] != 1 {
		t.Errorf("a second Stop removed again: %v %d", err, e.calls["rm"])
	}
}

// MinIO starts on the pinned image, waits for /minio/health/ready, creates
// the buckets through mc with the keys read inside the container, and hands
// out its endpoint and keys.
func TestMinIOStartsCreatesBucketsAndGivesItsKeys(t *testing.T) {
	e := scripted()
	h := testHost(e, io.Discard)
	var asked []string
	h.Ready = func(_ context.Context, url string) error { asked = append(asked, url); return nil }
	vals, err := h.Start(context.Background(), config.E2ESuite{Services: []string{config.ServiceMinIO}, Buckets: []string{"origo", "blobs"}})
	if err != nil {
		t.Fatal(err)
	}
	if vals["minio.endpoint"] != "http://127.0.0.1:49153" || vals["minio.region"] != "us-east-1" ||
		len(vals["minio.access_key"]) != 20 || len(vals["minio.secret_key"]) != 40 {
		t.Fatalf("vals %v", vals)
	}
	if len(asked) != 1 || asked[0] != "http://127.0.0.1:49153/minio/health/ready" {
		t.Errorf("health asked %v", asked)
	}
	if !e.has("run -d") || !e.has(MinIOImage+" server /data") || !e.has("local/origo local/blobs") {
		t.Errorf("ran %q", e.ran)
	}
	if exec := e.ran[len(e.ran)-1]; strings.Contains(exec, vals["minio.secret_key"]) {
		t.Errorf("the bucket command carries the secret: %q", exec)
	}
}

// Both services start for one suite, in the order the entry lists them.
func TestBothServicesStartInOrder(t *testing.T) {
	e := scripted()
	h := testHost(e, io.Discard)
	vals, err := h.Start(context.Background(), config.E2ESuite{Services: []string{config.ServiceMinIO, config.ServicePostgres}})
	if err != nil || len(vals) != 5 {
		t.Fatalf("%v %v", vals, err)
	}
	if !strings.Contains(e.ran[0], "minio") {
		t.Errorf("first %q", e.ran[0])
	}
}

// Every way a service fails to start says which service and why, and a
// container that was made is still removed.
func TestAServiceThatFailsToStartIsRemoved(t *testing.T) {
	pg := config.E2ESuite{Services: []string{config.ServicePostgres}}
	mn := config.E2ESuite{Services: []string{config.ServiceMinIO}, Buckets: []string{"b"}}
	for name, tc := range map[string]struct {
		suite  config.E2ESuite
		setup  func(*engine, *Host)
		want   string
		remove bool
	}{
		"run fails, nothing made":   {pg, func(e *engine, _ *Host) { e.fail["run"], e.fail["inspect"] = true, true }, "does not start", false},
		"run fails, container made": {pg, func(e *engine, _ *Host) { e.fail["run"] = true }, "does not start", true},
		"port unreadable":           {pg, func(e *engine, _ *Host) { e.fail["port"] = true }, "cannot read the host port", true},
		"port names none":           {pg, func(e *engine, _ *Host) { e.out["port"] = "garbage" }, "names no host port", true},
		"never ready":               {pg, func(e *engine, _ *Host) { e.fail["exec"] = true; e.fail["logs"] = true }, "did not answer", true},
		"port not open": {pg, func(_ *engine, h *Host) {
			h.Dial = func(context.Context, string) error { return errors.New("refused") }
		}, "refused", true},
		"minio never ready": {mn, func(_ *engine, h *Host) {
			h.Ready = func(context.Context, string) error { return errors.New("503") }
		}, "503", true},
		"buckets fail":  {mn, func(e *engine, _ *Host) { e.fail["exec"] = true }, "cannot create the buckets b", true},
		"no randomness": {pg, func(_ *engine, h *Host) { h.Rand = strings.NewReader("") }, "make a password", false},
		"unknown":       {config.E2ESuite{Services: []string{"mysql"}}, func(*engine, *Host) {}, "no service is called", false},
	} {
		t.Run(name, func(t *testing.T) {
			e := scripted()
			var out bytes.Buffer
			h := testHost(e, &out)
			tc.setup(e, h)
			_, err := h.Start(context.Background(), tc.suite)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if err := h.Stop(); err != nil {
				t.Fatal(err)
			}
			if got := e.has("rm -f -v"); got != tc.remove {
				t.Errorf("removed %v, want %v: %q", got, tc.remove, e.ran)
			}
		})
	}
}

// The logs of a service that never answers are printed, and so is the
// reason when they cannot be read.
func TestLogsAreShownWhenAServiceDoesNotAnswer(t *testing.T) {
	e := scripted()
	e.fail["exec"] = true
	e.out["logs"] = "FATAL: bad config\n"
	var out bytes.Buffer
	h := testHost(e, &out)
	if _, err := h.Start(context.Background(), config.E2ESuite{Services: []string{config.ServicePostgres}}); err == nil {
		t.Fatal("started")
	}
	if !strings.Contains(out.String(), "FATAL: bad config") {
		t.Errorf("out %q", out.String())
	}
	e.fail["logs"] = true
	out.Reset()
	if _, err := h.Start(context.Background(), config.E2ESuite{Services: []string{config.ServicePostgres}}); err == nil {
		t.Fatal("started")
	}
	if !strings.Contains(out.String(), "logs could not be read") {
		t.Errorf("out %q", out.String())
	}
}

// A container that cannot be removed is an error Stop returns, and the rest
// are still tried.
func TestStopReturnsWhatItCouldNotRemove(t *testing.T) {
	e := scripted()
	h := testHost(e, io.Discard)
	h.started = []string{"a", "b"}
	e.fail["rm"] = true
	err := h.Stop()
	if err == nil || !strings.Contains(err.Error(), "remove the container a") || !strings.Contains(err.Error(), "remove the container b") {
		t.Fatalf("err %v", err)
	}
}

// A cancelled run stops waiting.
func TestACancelledRunStopsWaiting(t *testing.T) {
	e := scripted()
	e.fail["exec"] = true
	h := testHost(e, io.Discard)
	h.Wait = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Start(ctx, config.E2ESuite{Services: []string{config.ServicePostgres}}); err == nil {
		t.Fatal("started")
	}
}

func TestFindEngineTakesDockerThenPodman(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			if slices.Contains(names, n) {
				return "/bin/" + n, nil
			}
			return "", exec.ErrNotFound
		}
	}
	if got, err := FindEngine(has("podman", "docker")); err != nil || got != "docker" {
		t.Errorf("%q %v", got, err)
	}
	if got, err := FindEngine(has("podman")); err != nil || got != "podman" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := FindEngine(has()); err == nil || !strings.Contains(err.Error(), "neither docker nor podman") {
		t.Errorf("%v", err)
	}
}

func TestExpand(t *testing.T) {
	vals := map[string]string{"postgres.url": "u", "minio.region": "r"}
	if got, err := Expand("a=${postgres.url};${minio.region}${postgres.url}", vals); err != nil || got != "a=u;ru" {
		t.Errorf("%q %v", got, err)
	}
	if got, err := Expand("plain $HOME", vals); err != nil || got != "plain $HOME" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := Expand("${minio.endpoint}", vals); err == nil {
		t.Error("an ungiven value expanded")
	}
	if _, err := Expand("${x", vals); err == nil {
		t.Error("an unclosed reference expanded")
	}
}

// The real probes: a 2xx from a server, a refusal, a connection.
func TestRealProbes(t *testing.T) {
	srv := newServer(t, 200)
	if err := ready(context.Background(), srv.URL); err != nil {
		t.Error(err)
	}
	if err := dial(context.Background(), strings.TrimPrefix(srv.URL, "http://")); err != nil {
		t.Error(err)
	}
	bad := newServer(t, 503)
	if err := ready(context.Background(), bad.URL); err == nil {
		t.Error("a 503 was ready")
	}
	if err := ready(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Error("a closed port was ready")
	}
	if err := ready(context.Background(), "http://bad host"); err == nil {
		t.Error("a bad URL was ready")
	}
	if err := dial(context.Background(), "127.0.0.1:1"); err == nil {
		t.Error("a closed port accepted")
	}
	if hostname() == "" {
		t.Error("no hostname")
	}
}
