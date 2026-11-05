// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// Item is one suite the gate was asked to consider, and why it is not run.
type Item struct {
	Suite config.E2ESuite
	// Skip, when set, is why the suite is not run here.
	Skip string
}

// Runner runs suites.
type Runner struct {
	// Root is the repository root, where every suite runs.
	Root  string
	GoBin string
	Out   io.Writer
	// Env is the environment every suite starts from.
	Env   []string
	Start Start
	// Exec drives the container engine.
	Exec     gates.Exec
	LookPath func(string) (string, error)
	Tools    *Fetcher
	// NewHost makes the host of a suite's services.
	NewHost func(engine, suite string, out io.Writer) *Host
	// Grace is how long past its timeout a go test run has to report its
	// own timeout before it is stopped.
	Grace time.Duration
}

// NewRunner is a runner for the repository at root, on this machine, with
// its full environment.
func NewRunner(root, goBin string, out io.Writer, run gates.Exec) *Runner {
	return &Runner{
		Root: root, GoBin: goBin, Out: out, Env: os.Environ(), Start: OSStart, Exec: run,
		LookPath: lookPath, Tools: NewFetcher(out),
		NewHost: func(engine, suite string, out io.Writer) *Host { return NewHost(engine, suite, out, run) },
		Grace:   30 * time.Second,
	}
}

// RunSignals runs the items, stopping the suite in flight and removing what
// it started when the process is interrupted or terminated.
func (r *Runner) RunSignals(items []Item) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return r.Run(ctx, items)
}

// result is how one suite ended.
type result struct {
	name    string
	skip    string
	err     error
	elapsed time.Duration
}

// Run runs each item that is not skipped, in order, and reports each by
// name. A suite that fails does not stop the ones after it, and the run fails
// if any did.
func (r *Runner) Run(ctx context.Context, items []Item) error {
	out := &lockedWriter{w: r.Out}
	var results []result
	for _, it := range items {
		s := it.Suite
		if it.Skip != "" {
			_, _ = fmt.Fprintf(out, "SKIP %s: %s\n", s.Name, it.Skip)
			results = append(results, result{name: s.Name, skip: it.Skip})
			continue
		}
		_, _ = fmt.Fprintf(out, "-- %s: %s\n", s.Name, s.Why)
		begin := time.Now()
		err := r.suite(ctx, s, out)
		results = append(results, result{name: s.Name, err: err, elapsed: time.Since(begin)})
		if err != nil {
			_, _ = fmt.Fprintf(out, "FAIL %s: %v\n", s.Name, err)
		}
	}

	var failed []string
	ran := 0
	_, _ = fmt.Fprintln(out)
	for _, res := range results {
		switch {
		case res.skip != "":
			_, _ = fmt.Fprintf(out, "SKIP %-16s %s\n", res.name, res.skip)
		case res.err != nil:
			ran++
			failed = append(failed, res.name)
			_, _ = fmt.Fprintf(out, "FAIL %-16s %s (%s)\n", res.name, firstLine(res.err.Error()), res.elapsed.Round(time.Millisecond))
		default:
			ran++
			_, _ = fmt.Fprintf(out, "PASS %-16s %s\n", res.name, res.elapsed.Round(time.Millisecond))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d e2e suites failed: %s", len(failed), ran, strings.Join(failed, ", "))
	}
	if ran == 0 {
		_, _ = fmt.Fprintln(out, "no e2e suite runs here")
		return nil
	}
	_, _ = fmt.Fprintf(out, "%d e2e suite(s) passed\n", ran)
	return nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// suite runs one suite: its tools first, since a download is cheaper than a
// container, then its services, then the tests or the command. The services
// are removed whatever happens.
func (r *Runner) suite(ctx context.Context, s config.E2ESuite, out io.Writer) (err error) {
	env := slices.Clone(r.Env)
	var dirs []string
	for _, tool := range s.Tools {
		dir, terr := r.Tools.Dir(ctx, tool)
		if terr != nil {
			return terr
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) > 0 {
		env = envSet(env, "PATH", strings.Join(append(dirs, envGet(env, "PATH")), string(filepath.ListSeparator)))
	}

	vals := map[string]string{}
	if len(s.Services) > 0 {
		var engine string
		if engine, err = FindEngine(r.LookPath); err != nil {
			return err
		}
		host := r.NewHost(engine, s.Name, out)
		// err is the result of suite, so a container that will not go fails
		// a suite that passed.
		defer func() { err = errors.Join(err, host.Stop()) }()
		if vals, err = host.Start(ctx, s); err != nil {
			return err
		}
	}
	for _, k := range sortedKeys(s.Env) {
		v, xerr := Expand(s.Env[k], vals)
		if xerr != nil {
			return fmt.Errorf("env %s: %w", k, xerr)
		}
		env = envSet(env, k, v)
	}

	if s.Test != nil {
		return r.goTest(ctx, s, env, out)
	}
	return r.command(ctx, s, env, out)
}

// goTest runs the suite's packages with go test -json and judges the stream.
func (r *Runner) goTest(ctx context.Context, s config.E2ESuite, env []string, out io.Writer) error {
	t := s.Test
	timeout := s.TimeoutOr()
	args := []string{"test", "-json", "-count=" + strconv.Itoa(t.CountOr()), "-timeout=" + timeout.String()}
	if len(t.Tags) > 0 {
		args = append(args, "-tags="+strings.Join(t.Tags, ","))
	}
	if t.Run != "" {
		args = append(args, "-run="+t.Run)
	}
	if t.Race {
		args = append(args, "-race")
	}
	args = append(args, t.Packages...)
	_, _ = fmt.Fprintf(out, "%s %s\n", r.GoBin, strings.Join(args, " "))

	// go test reports its own timeout with the stacks of what hung, so it is
	// given the first chance; the outer deadline is for a build that hangs.
	ctx, cancel := context.WithTimeout(ctx, timeout+r.Grace)
	defer cancel()
	sink := newTestSink(out)
	runErr := r.Start(ctx, Proc{Dir: r.Root, Env: env, Name: r.GoBin, Args: args, Stdout: sink, Stderr: out})
	sink.flush()
	return sink.verdict(ctx, s, runErr, timeout)
}

// command runs the suite's shell command from the root and fails it on a
// non-zero exit or a skipped test.
func (r *Runner) command(ctx context.Context, s config.E2ESuite, env []string, out io.Writer) error {
	timeout := s.TimeoutOr()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, _ = fmt.Fprintf(out, "sh -c %q\n", s.Command)
	scan := &skipScanner{out: out}
	runErr := r.Start(ctx, Proc{Dir: r.Root, Env: env, Name: "sh", Args: []string{"-c", s.Command}, Stdout: scan, Stderr: scan})
	scan.flush()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s", timeout)
	}
	var problems []string
	if runErr != nil {
		problems = append(problems, fmt.Sprintf("the command failed: %v", runErr))
	}
	if len(scan.skips) > 0 {
		if s.Skips() {
			_, _ = fmt.Fprintf(out, "%s skipped, allowed: %s\n", count(len(scan.skips), "test"), s.AllowSkip)
		} else {
			problems = append(problems, fmt.Sprintf("%s skipped, and a skipped test proves nothing; set allow_skip with the reason one may:\n  %s",
				count(len(scan.skips), "test"), strings.Join(scan.skips, "\n  ")))
		}
	}
	return joinProblems(problems)
}

func joinProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func lookPath(name string) (string, error) { return exec.LookPath(name) }

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// envGet is the value of key in an environment, or "".
func envGet(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// envSet is env with key set to value, replacing any earlier setting.
func envSet(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}
