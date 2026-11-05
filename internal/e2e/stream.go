// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// lines splits what is written to it into lines and hands each whole line to
// a function, keeping the partial last one until more arrives or it is
// flushed.
type lines struct {
	buf  []byte
	each func(line string)
}

func (l *lines) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.each(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
}

func (l *lines) flush() {
	if len(l.buf) > 0 {
		l.each(string(l.buf))
		l.buf = nil
	}
}

// skipLine is what `go test -v` and a test script print for a skipped test.
var skipLine = regexp.MustCompile(`^\s*--- SKIP`)

// skipScanner passes a command's output through and notes each line that
// reports a skipped test.
type skipScanner struct {
	out   io.Writer
	split lines
	skips []string
}

func (s *skipScanner) Write(p []byte) (int, error) {
	if s.split.each == nil {
		s.split.each = func(line string) {
			if skipLine.MatchString(line) {
				s.skips = append(s.skips, strings.TrimSpace(line))
			}
		}
	}
	if _, err := s.out.Write(p); err != nil {
		return 0, err
	}
	return s.split.Write(p)
}

func (s *skipScanner) flush() { s.split.flush() }

// event is a line of `go test -json`.
type event struct {
	Action  string
	Package string
	Test    string
	Output  string
	Elapsed float64
	// ImportPath and Action build-output are the build's own lines.
	ImportPath string
}

// skipped is a test that skipped, and the message it gave.
type skipped struct{ Package, Test, Message string }

func (s skipped) String() string {
	msg := s.Message
	if msg == "" {
		msg = "no message"
	}
	return fmt.Sprintf("%s %s: %s", s.Package, s.Test, msg)
}

// testSink reads a `go test -json` stream, prints a line for each test as it
// ends, and keeps what the verdict needs.
type testSink struct {
	out     io.Writer
	split   lines
	output  map[string][]string
	passed  int
	failed  []string
	skipped []skipped
	pkgFail []string
}

func newTestSink(out io.Writer) *testSink {
	k := &testSink{out: out, output: map[string][]string{}}
	k.split.each = k.line
	return k
}

func (k *testSink) Write(p []byte) (int, error) { return k.split.Write(p) }

func (k *testSink) flush() { k.split.flush() }

// say writes one line of the report.
func (k *testSink) say(format string, args ...any) {
	_, _ = fmt.Fprintf(k.out, format+"\n", args...)
}

func (k *testSink) line(raw string) {
	var ev event
	if !strings.HasPrefix(raw, "{") || json.Unmarshal([]byte(raw), &ev) != nil {
		// Not an event: a line go test itself printed before it began.
		k.say("%s", raw)
		return
	}
	key := ev.Package + "\x00" + ev.Test
	switch ev.Action {
	case "output":
		k.output[key] = append(k.output[key], ev.Output)
	case "build-output":
		_, _ = io.WriteString(k.out, ev.Output)
	case "pass", "fail", "skip":
		if ev.Test == "" {
			k.end(ev, key)
		} else {
			k.endTest(ev, key)
		}
	}
}

// endTest reports a test that ended and forgets what it printed.
func (k *testSink) endTest(ev event, key string) {
	out := k.output[key]
	delete(k.output, key)
	name := ev.Package + " " + ev.Test
	took := time.Duration(ev.Elapsed * float64(time.Second)).Round(time.Millisecond)
	switch ev.Action {
	case "pass":
		k.passed++
		k.say("PASS %s (%s)", name, took)
	case "fail":
		k.failed = append(k.failed, name)
		k.say("FAIL %s (%s)", name, took)
		for _, l := range out {
			if l = strings.TrimRight(l, "\n"); strings.TrimSpace(l) != "" {
				k.say("    %s", l)
			}
		}
	case "skip":
		s := skipped{Package: ev.Package, Test: ev.Test, Message: skipMessage(out)}
		k.skipped = append(k.skipped, s)
		k.say("SKIP %s", s)
	}
}

// end reports a package that ended. A package that failed without a test
// failing, a build error or a panic, shows what it printed.
func (k *testSink) end(ev event, key string) {
	out := k.output[key]
	delete(k.output, key)
	switch ev.Action {
	case "pass":
		k.say("ok   %s", ev.Package)
	case "fail":
		k.pkgFail = append(k.pkgFail, ev.Package)
		k.say("FAIL %s", ev.Package)
		for _, l := range out {
			if l = strings.TrimRight(l, "\n"); strings.TrimSpace(l) != "" {
				k.say("    %s", l)
			}
		}
	}
}

// sourcePrefix is the file and line t.Log puts before a message.
var sourcePrefix = regexp.MustCompile(`^\S+\.go:\d+: `)

// listed joins the first few names of a long list and counts the rest, so
// one failure in a hundred subtests does not bury the summary.
func listed(names []string, sep string) string {
	const most = 8
	if len(names) <= most {
		return strings.Join(names, sep)
	}
	return fmt.Sprintf("%s%sand %d more", strings.Join(names[:most], sep), sep, len(names)-most)
}

// skipMessage is what a test printed before it skipped, less the lines the
// test runner prints around every test.
func skipMessage(out []string) string {
	var msg []string
	for _, l := range out {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "=== ") || strings.HasPrefix(l, "--- ") {
			continue
		}
		msg = append(msg, sourcePrefix.ReplaceAllString(l, ""))
	}
	return strings.Join(msg, " ")
}

// verdict is whether the run proved what the suite says it proves: no test
// failed, none skipped without leave, and some test ran.
func (k *testSink) verdict(ctx context.Context, s config.E2ESuite, runErr error, timeout time.Duration) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s", timeout)
	}
	var problems []string
	if len(k.failed) > 0 {
		problems = append(problems, fmt.Sprintf("%s failed: %s", count(len(k.failed), "test"), listed(k.failed, ", ")))
	}
	if runErr != nil && len(k.failed) == 0 {
		why := ""
		if len(k.pkgFail) > 0 {
			why = " (" + strings.Join(k.pkgFail, ", ") + ")"
		}
		problems = append(problems, fmt.Sprintf("go test failed: %v%s", runErr, why))
	}
	if len(k.skipped) > 0 {
		if s.Skips() {
			k.say("%s skipped, allowed: %s", count(len(k.skipped), "test"), s.AllowSkip)
		} else {
			list := make([]string, len(k.skipped))
			for i, sk := range k.skipped {
				list[i] = sk.String()
			}
			problems = append(problems, fmt.Sprintf("%s skipped, and a skipped test proves nothing; set allow_skip with the reason one may:\n  %s",
				count(len(k.skipped), "test"), listed(list, "\n  ")))
		}
	}
	if k.passed+len(k.failed)+len(k.skipped) == 0 && len(problems) == 0 {
		problems = append(problems, "no test ran; check the packages and the build tags")
	}
	return joinProblems(problems)
}
