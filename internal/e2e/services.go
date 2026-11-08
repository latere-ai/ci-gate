// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package e2e runs the end-to-end suites a repository declares: Go tests or
// a command that need Postgres, MinIO or a pinned tool, started and removed
// by lateregate so a laptop and a runner run the same thing.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/containers"
	"latere.ai/x/ci-gate/internal/gates"
)

// The images the services run, pinned by the digest of their multi-platform
// index so a laptop and a runner start the same bytes whatever the tag moves
// to.
const (
	// PostgresImage is docker.io/library/postgres:18-alpine, linux/amd64 and
	// linux/arm64 among others.
	PostgresImage = "docker.io/library/postgres@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5"
	// MinIOImage is ghcr.io/latere-ai/minio:RELEASE.2026-09-25T12-30-31Z,
	// linux/amd64 and linux/arm64.
	MinIOImage = "ghcr.io/latere-ai/minio@sha256:025fb85cf53bad7b734dc05b012c65ce617105248ddd044d8974fb636f2a8824"
)

// What the services are called inside, which no suite sees: the database and
// its user, and the region MinIO answers for.
const (
	pgName      = "lateregate"
	minioRegion = "us-east-1"
)

// FindEngine is the first container engine on PATH, in the order the image
// gate looks for them.
func FindEngine(lookPath func(string) (string, error)) (string, error) {
	for _, e := range gates.Engines {
		if _, err := lookPath(e); err == nil {
			return e, nil
		}
	}
	return "", fmt.Errorf("neither %s is on PATH; a suite that names services starts them with one", strings.Join(gates.Engines, " nor "))
}

// Host starts the services of one suite and removes them afterward.
type Host struct {
	// Engine is docker or podman; Run drives it.
	Engine string
	Run    gates.Exec
	Out    io.Writer
	// Labels go on every container, so the sweep of a killed run finds them.
	Labels map[string]string
	// Suite and PID name the containers, with a random part besides, so two
	// suites, and two runner slots on one machine, never share a name and a
	// name another container holds is never taken for one of ours.
	Suite string
	PID   int
	// Wait is how long a service has to answer; Poll is the pause between
	// asks.
	Wait time.Duration
	Poll time.Duration
	// Rand makes the passwords; the system's source when nil.
	Rand io.Reader
	// Dial asks whether the published port accepts a connection, and Ready
	// asks a URL for a 2xx. They are variables so a test answers without a
	// container.
	Dial  func(ctx context.Context, addr string) error
	Ready func(ctx context.Context, url string) error

	// started holds the id of each container a successful run printed, and
	// nothing else: Stop removes exactly these.
	started []string
}

// NewHost is a host for suite on engine, labeled the way a test helper
// labels its containers.
func NewHost(engine, suite string, out io.Writer, run gates.Exec) *Host {
	labels := containers.Labels(os.Getpid(), containers.Hostname(), os.Getenv(containers.RunEnv))
	return &Host{
		Engine: engine, Run: run, Out: out, Labels: labels, Suite: suite, PID: os.Getpid(),
		Wait: time.Minute, Poll: 250 * time.Millisecond,
		Dial: dial, Ready: ready,
	}
}

func dial(ctx context.Context, addr string) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

var readyClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func ready(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := readyClient.Do(req)
	if err != nil {
		return err
	}
	if err := resp.Body.Close(); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	return nil
}

// Start starts each of the suite's services and returns the values they
// hand it, by the names an env reference uses. A service that does not
// start leaves the others for Stop.
func (h *Host) Start(ctx context.Context, s config.E2ESuite) (map[string]string, error) {
	vals := map[string]string{}
	for _, svc := range s.Services {
		var err error
		switch svc {
		case config.ServicePostgres:
			err = h.startPostgres(ctx, vals)
		case config.ServiceMinIO:
			err = h.startMinIO(ctx, vals, s.Buckets)
		default:
			err = fmt.Errorf("no service is called %q", svc)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", svc, err)
		}
	}
	return vals, nil
}

// Stop removes every container Start made, with its volumes. Each is tried
// whether another failed.
func (h *Host) Stop() error {
	var errs []error
	for _, name := range h.started {
		if _, err := h.Run(nil, false, h.Engine, "rm", "-f", "-v", name); err != nil {
			errs = append(errs, fmt.Errorf("remove the container %s: %w", name, err))
		}
	}
	h.started = nil
	return errors.Join(errs...)
}

// lastLine is the last non-empty line of b, trimmed: the container id, after
// any warning an engine printed before it.
func lastLine(b []byte) string {
	lines := strings.Fields(string(b))
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// secret is n random bytes as hex.
func (h *Host) secret(n int) (string, error) {
	src := h.Rand
	if src == nil {
		src = rand.Reader
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(src, b); err != nil {
		return "", fmt.Errorf("make a password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// launch starts a container from image publishing port on 127.0.0.1 at a
// port the engine picks, and returns its name and that host port.
func (h *Host) launch(svc, image, port string, env []string, args ...string) (name, host string, err error) {
	suffix, err := h.secret(4)
	if err != nil {
		return "", "", err
	}
	name = fmt.Sprintf("lateregate-e2e-%s-%s-%d-%s", h.Suite, svc, h.PID, suffix)
	run := []string{"run", "-d", "--name", name, "-p", "127.0.0.1::" + port}
	for _, k := range sortedLabels(h.Labels) {
		run = append(run, "--label", k+"="+h.Labels[k])
	}
	for _, e := range env {
		run = append(run, "-e", e)
	}
	run = append(append(run, image), args...)
	_, _ = fmt.Fprintf(h.Out, "starting %s from %s\n", svc, image)
	printed, err := h.Run(nil, false, h.Engine, run...)
	if err != nil {
		// Only the id a successful run printed is ours to remove. A start
		// that failed after the container was made carries the run's label,
		// so the run's check names and removes it.
		return "", "", fmt.Errorf("the container does not start: %w", err)
	}
	id := lastLine(printed)
	if id == "" {
		return "", "", fmt.Errorf("the container does not start: %s run printed no container id", h.Engine)
	}
	h.started = append(h.started, id)
	mapped, err := h.Run(nil, false, h.Engine, "port", name, port)
	if err != nil {
		h.logs(svc, name)
		return "", "", fmt.Errorf("cannot read the host port for %s; the container may have exited: %w", port, err)
	}
	host = gates.HostPort(string(mapped))
	if host == "" {
		return "", "", fmt.Errorf("%s port printed %q, which names no host port", h.Engine, strings.TrimSpace(string(mapped)))
	}
	return name, host, nil
}

func sortedLabels(m map[string]string) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	return keys
}

// logs prints a container's logs, or says why they could not be read.
func (h *Host) logs(svc, name string) {
	logs, err := h.Run(nil, false, h.Engine, "logs", name)
	if err != nil {
		_, _ = fmt.Fprintf(h.Out, "%s logs could not be read: %v\n", svc, err)
		return
	}
	_, _ = fmt.Fprintf(h.Out, "%s logs:\n%s\n", svc, logs)
}

// until asks probe until it answers, the wait runs out, or ctx ends.
func (h *Host) until(ctx context.Context, svc, name string, probe func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, h.Wait)
	defer cancel()
	for {
		last := probe(ctx)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			h.logs(svc, name)
			return fmt.Errorf("did not answer within %s: %w", h.Wait, last)
		case <-time.After(h.Poll):
		}
	}
}

func (h *Host) startPostgres(ctx context.Context, vals map[string]string) error {
	pw, err := h.secret(16)
	if err != nil {
		return err
	}
	name, port, err := h.launch(config.ServicePostgres, PostgresImage, "5432", []string{
		"POSTGRES_USER=" + pgName, "POSTGRES_DB=" + pgName, "POSTGRES_PASSWORD=" + pw,
	})
	if err != nil {
		return err
	}
	// -h 127.0.0.1: the image runs a server on its socket alone while it
	// initializes, then restarts it on the network, and a check on the
	// socket passes in between.
	err = h.until(ctx, config.ServicePostgres, name, func(ctx context.Context) error {
		if _, err := h.Run(nil, false, h.Engine, "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", pgName, "-d", pgName); err != nil {
			return err
		}
		return h.Dial(ctx, "127.0.0.1:"+port)
	})
	if err != nil {
		return err
	}
	vals["postgres.url"] = fmt.Sprintf("postgres://%s:%s@127.0.0.1:%s/%s?sslmode=disable", pgName, pw, port, pgName)
	_, _ = fmt.Fprintf(h.Out, "postgres answers on 127.0.0.1:%s\n", port)
	return nil
}

func (h *Host) startMinIO(ctx context.Context, vals map[string]string, buckets []string) error {
	access, err := h.secret(10)
	if err != nil {
		return err
	}
	secret, err := h.secret(20)
	if err != nil {
		return err
	}
	name, port, err := h.launch(config.ServiceMinIO, MinIOImage, "9000", []string{
		"MINIO_ROOT_USER=" + access, "MINIO_ROOT_PASSWORD=" + secret, "MINIO_SITE_REGION=" + minioRegion,
	}, "server", "/data")
	if err != nil {
		return err
	}
	endpoint := "http://127.0.0.1:" + port
	err = h.until(ctx, config.ServiceMinIO, name, func(ctx context.Context) error {
		return h.Ready(ctx, endpoint+"/minio/health/ready")
	})
	if err != nil {
		return err
	}
	if len(buckets) > 0 {
		// mc ships in the image; the keys are read from the container's own
		// environment, so the bucket command does not repeat them.
		script := `mc alias set local http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null && mc mb --ignore-existing "$@"`
		args := []string{"exec", name, "sh", "-c", script, "sh"}
		for _, b := range buckets {
			args = append(args, "local/"+b)
		}
		if _, err := h.Run(nil, false, h.Engine, args...); err != nil {
			h.logs(config.ServiceMinIO, name)
			return fmt.Errorf("cannot create the buckets %s: %w", strings.Join(buckets, ", "), err)
		}
	}
	vals["minio.endpoint"] = endpoint
	vals["minio.access_key"] = access
	vals["minio.secret_key"] = secret
	vals["minio.region"] = minioRegion
	_, _ = fmt.Fprintf(h.Out, "minio answers on %s\n", endpoint)
	return nil
}

// Expand replaces each ${name} in v with the value a service gave.
func Expand(v string, vals map[string]string) (string, error) {
	refs, err := config.E2ERefsIn(v)
	if err != nil {
		return "", err
	}
	for _, ref := range refs {
		val, ok := vals[ref]
		if !ok {
			return "", fmt.Errorf("${%s} names a value no service of this suite gave", ref)
		}
		v = strings.ReplaceAll(v, "${"+ref+"}", val)
	}
	return v, nil
}
