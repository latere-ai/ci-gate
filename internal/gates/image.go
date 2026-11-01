// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// Engines are the container engines the image gate drives, in the order it
// looks for them: Docker on a hosted runner, podman on the self-hosted one.
// Both take the same build, run, inspect, port and rm arguments.
var Engines = []string{"docker", "podman"}

// probeClient asks a container on 127.0.0.1: no proxy, and no connection
// kept alive past the container it was made to.
var probeClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

// ImageProbe asks url for a 2xx answer until the deadline. It is a variable
// so the tests answer without a container.
var ImageProbe = func(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	return nil
}

// Image checks each configured image the way a release ships it: it builds,
// reports its version, stays under its size cap, holds no shell, and starts
// with a read-only root file system and no capabilities and answers its
// probe. Each image is checked whether another failed, and every one built
// is removed afterward.
func Image(images []config.ImageSpec, out io.Writer, lookPath func(string) (string, error), run Exec) error {
	if len(images) == 0 {
		return errors.New("image.images names no image to check")
	}
	engine := ""
	for _, e := range Engines {
		if _, err := lookPath(e); err == nil {
			engine = e
			break
		}
	}
	if engine == "" {
		return fmt.Errorf("neither %s is on PATH; the image gate builds and starts each image with one", strings.Join(Engines, " nor "))
	}
	var failed []string
	for _, im := range images {
		if err := checkImage(engine, im, out, run); err != nil {
			_, _ = fmt.Fprintf(out, "%s: %v\n", im.Name, err)
			failed = append(failed, im.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d image(s) failed: %s", len(failed), len(images), strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(out, "%d image(s) build, report their version, stay under their cap and start read-only (%s)\n", len(images), engine)
	return nil
}

// checkImage runs one image's checks, in the order a failure is cheapest
// to read: a build that fails says nothing about the size.
func checkImage(engine string, im config.ImageSpec, out io.Writer, run Exec) (err error) {
	// The process id keeps two runner slots on one machine from sharing a
	// tag or a container name.
	id := fmt.Sprintf("lateregate-%s-%d", im.Name, os.Getpid())
	tag := "localhost/" + id + ":candidate"
	args := []string{"build", "-f", im.DockerfileOr(), "-t", tag}
	for _, k := range im.BuildArgKeys() {
		args = append(args, "--build-arg", k+"="+im.BuildArgs[k])
	}
	args = append(args, im.ContextOr())
	_, _ = fmt.Fprintf(out, "%s build -f %s %s\n", engine, im.DockerfileOr(), im.ContextOr())
	if _, err := run(nil, true, engine, args...); err != nil {
		return fmt.Errorf("the image does not build: %w", err)
	}
	defer func() {
		if _, rmErr := run(nil, false, engine, "rmi", "-f", tag); rmErr != nil && err == nil {
			err = fmt.Errorf("remove the candidate %s: %w", tag, rmErr)
		}
	}()

	got, err := run(nil, false, engine, append([]string{"run", "--rm", tag}, im.VersionArgs...)...)
	if err != nil {
		return fmt.Errorf("`%s` failed in the image: %w", strings.Join(im.VersionArgs, " "), err)
	}
	version := strings.TrimSpace(string(got))
	if version == "" || !strings.HasPrefix(version, im.VersionExpect) {
		return fmt.Errorf("`%s` printed %q, which does not start with %q", strings.Join(im.VersionArgs, " "), version, im.VersionExpect)
	}
	_, _ = fmt.Fprintf(out, "%s reports %s\n", im.Name, firstLine(version))

	if im.MaxMiB > 0 {
		raw, err := run(nil, false, engine, "image", "inspect", tag, "--format", "{{.Size}}")
		if err != nil {
			return fmt.Errorf("cannot read the image's size: %w", err)
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			return fmt.Errorf("the image's size %q is not a number: %w", strings.TrimSpace(string(raw)), err)
		}
		if size > int64(im.MaxMiB)<<20 {
			return fmt.Errorf("the image is %.1f MiB, over its cap of %d MiB", float64(size)/(1<<20), im.MaxMiB)
		}
		_, _ = fmt.Fprintf(out, "%s is %.1f MiB, under %d MiB\n", im.Name, float64(size)/(1<<20), im.MaxMiB)
	}

	if im.AllowShell == "" {
		if _, err := run(nil, false, engine, "run", "--rm", "--entrypoint", "/bin/sh", tag, "-c", "true"); err == nil {
			return errors.New("the image holds a shell at /bin/sh; remove it, or set allow_shell with the reason it ships one")
		}
	}

	if im.SkipStart != "" {
		_, _ = fmt.Fprintf(out, "%s is not started: %s\n", im.Name, im.SkipStart)
		return nil
	}
	return startImage(engine, id, tag, im, out, run)
}

// startImage starts the candidate the way production runs it, read-only and
// with no capabilities, and asks its probe until it answers or the wait
// runs out. The container is removed either way.
func startImage(engine, name, tag string, im config.ImageSpec, out io.Writer, run Exec) (err error) {
	port := strconv.Itoa(im.Port)
	args := []string{"run", "-d", "--name", name, "--read-only", "--cap-drop", "ALL", "-p", "127.0.0.1::" + port}
	if engine == "podman" {
		// podman mounts a writable tmpfs on /tmp, /run and /dev under
		// --read-only where Docker and a Kubernetes read-only root mount
		// none, so an image that writes /tmp would pass here alone.
		args = append(args, "--read-only-tmpfs=false")
	}
	for _, k := range im.EnvKeys() {
		args = append(args, "-e", k+"="+im.Env[k])
	}
	args = append(append(args, tag), im.StartArgs...)
	if _, err := run(nil, false, engine, args...); err != nil {
		return fmt.Errorf("the image does not start read-only: %w", err)
	}
	defer func() {
		if _, rmErr := run(nil, false, engine, "rm", "-f", name); rmErr != nil && err == nil {
			err = fmt.Errorf("remove the container %s: %w", name, rmErr)
		}
	}()
	mapped, err := run(nil, false, engine, "port", name, port)
	if err != nil {
		return fmt.Errorf("cannot read the host port for %s: %w", port, err)
	}
	host := hostPort(string(mapped))
	if host == "" {
		return fmt.Errorf("%s port printed %q, which names no host port", engine, strings.TrimSpace(string(mapped)))
	}
	url := "http://127.0.0.1:" + host + im.Probe
	wait := time.Duration(im.WaitSecondsOr()) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var last error
	for {
		try, stop := context.WithTimeout(ctx, 2*time.Second)
		last = ImageProbe(try, url)
		stop()
		if last == nil {
			_, _ = fmt.Fprintf(out, "%s answers %s, read-only with no capabilities\n", im.Name, im.Probe)
			return nil
		}
		select {
		case <-ctx.Done():
			logs, _ := run(nil, false, engine, "logs", name)
			_, _ = fmt.Fprintf(out, "%s logs:\n%s\n", im.Name, logs)
			return fmt.Errorf("%s did not answer within %s: %w", im.Probe, wait, last)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// hostPort reads the host port from `port` output such as
// "127.0.0.1:49153", one mapping a line.
func hostPort(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	_, p, ok := strings.CutLast(line, ":")
	if !ok {
		return ""
	}
	p = strings.TrimSpace(p)
	if _, err := strconv.Atoi(p); err != nil {
		return ""
	}
	return p
}

// firstLine is s up to its first newline.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
