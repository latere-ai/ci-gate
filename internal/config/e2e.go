// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The services lateregate starts for an end-to-end suite, the tools it
// fetches, and the events a suite runs on.
const (
	ServicePostgres = "postgres"
	ServiceMinIO    = "minio"

	ToolKubectl = "kubectl"
	ToolKind    = "kind"
	ToolHelm    = "helm"

	OnPush = "push"
	OnTag  = "tag"

	// DefaultE2ETimeout is how long a suite runs when it names no timeout.
	DefaultE2ETimeout = 20 * time.Minute
)

// E2EServices, E2ETools and E2EEvents are the names an entry may list, in the
// order the messages print them.
var (
	E2EServices = []string{ServicePostgres, ServiceMinIO}
	E2ETools    = []string{ToolKubectl, ToolKind, ToolHelm}
	E2EEvents   = []string{OnPush, OnTag}
)

// E2ERefs are the values a service hands a suite, by the name an env value
// writes between ${ and }, and the service each one needs.
var E2ERefs = map[string]string{
	"postgres.url":     ServicePostgres,
	"minio.endpoint":   ServiceMinIO,
	"minio.access_key": ServiceMinIO,
	"minio.secret_key": ServiceMinIO,
	"minio.region":     ServiceMinIO,
}

// E2ESuite is one end-to-end suite: tests or a command that need more than
// the suite gate gives them, with the services and tools they need.
type E2ESuite struct {
	// Name labels the suite in the output and selects it with -only.
	Name string `yaml:"name"`
	// Why says what the suite proves. It is required, as a reason is for a
	// waiver: a suite nobody can justify is one nobody keeps.
	Why string `yaml:"why"`
	// Test runs Go tests; Command runs a shell command. Exactly one is set.
	Test    *E2ETest `yaml:"test"`
	Command string   `yaml:"command"`
	// Services are started before the suite and removed after it.
	Services []string `yaml:"services"`
	// Buckets are created in MinIO before the suite starts.
	Buckets []string `yaml:"buckets"`
	// Env maps a variable to its value, which may name a service's address
	// as ${postgres.url}.
	Env map[string]string `yaml:"env"`
	// Tools are fetched at their pinned versions and put first on PATH.
	Tools []string `yaml:"tools"`
	// Reads are the directories a command suite reads, which decide whether
	// a change since a base reaches it.
	Reads []string `yaml:"reads"`
	// Timeout is a Go duration, DefaultE2ETimeout when unset.
	Timeout string `yaml:"timeout"`
	// AllowSkip, when set, is the reason a skipped test does not fail the
	// suite.
	AllowSkip string `yaml:"allow_skip"`
	// On lists the events the suite runs on, push when unset.
	On []string `yaml:"on"`
}

// E2ETest is a suite of Go tests behind build tags.
type E2ETest struct {
	Packages []string `yaml:"packages"`
	Tags     []string `yaml:"tags"`
	// Run is a -run regular expression.
	Run   string `yaml:"run"`
	Race  bool   `yaml:"race"`
	Count int    `yaml:"count"`
}

// e2eName is what a suite is called: a slug that is also safe in a container
// name.
var e2eName = regexp.MustCompile(`^[a-z0-9-]+$`)

// e2eBucket is the shape of an S3 bucket name.
var e2eBucket = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// e2eEnvName is a variable name a shell and a process accept.
var e2eEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// TimeoutOr is the suite's timeout, or DefaultE2ETimeout. Validated at load.
func (s E2ESuite) TimeoutOr() time.Duration {
	if s.Timeout == "" {
		return DefaultE2ETimeout
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil {
		return DefaultE2ETimeout
	}
	return d
}

// Events is the events the suite runs on: push when none is named.
func (s E2ESuite) Events() []string {
	if len(s.On) == 0 {
		return []string{OnPush}
	}
	return s.On
}

// RunsOn reports whether the suite runs on event.
func (s E2ESuite) RunsOn(event string) bool { return slices.Contains(s.Events(), event) }

// Skips reports whether a skipped test is allowed in the suite.
func (s E2ESuite) Skips() bool { return s.AllowSkip != "" }

// CountOr is how many times the tests run: once when unset.
func (t E2ETest) CountOr() int {
	if t.Count == 0 {
		return 1
	}
	return t.Count
}

// E2ERefsIn lists the ${...} references in a value, in order, and fails on
// one that is not closed.
func E2ERefsIn(v string) ([]string, error) {
	var refs []string
	for {
		_, rest, ok := strings.Cut(v, "${")
		if !ok {
			return refs, nil
		}
		ref, after, closed := strings.Cut(rest, "}")
		if !closed {
			return nil, fmt.Errorf("%q has a ${ with no closing }", v)
		}
		refs = append(refs, ref)
		v = after
	}
}

func validateE2E(suites []E2ESuite) error {
	seen := map[string]bool{}
	for i, s := range suites {
		at := fmt.Sprintf("e2e[%d]", i)
		if s.Name == "" {
			return fmt.Errorf("%s: name is required", at)
		}
		if !e2eName.MatchString(s.Name) {
			return fmt.Errorf("%s: name %q must be lowercase letters, digits and hyphens", at, s.Name)
		}
		at = "e2e " + s.Name
		if seen[s.Name] {
			return fmt.Errorf("%s is named twice", at)
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Why) == "" {
			return fmt.Errorf("%s: why is required; say what the suite proves", at)
		}
		if err := s.validateRun(at); err != nil {
			return err
		}
		if err := s.validateNeeds(at); err != nil {
			return err
		}
		if err := s.validateEnv(at); err != nil {
			return err
		}
		if err := s.validateWhen(at); err != nil {
			return err
		}
	}
	return nil
}

// validateRun checks what the suite runs.
func (s E2ESuite) validateRun(at string) error {
	switch {
	case s.Test != nil && s.Command != "":
		return fmt.Errorf("%s: set test or command, not both", at)
	case s.Test == nil && strings.TrimSpace(s.Command) == "":
		return fmt.Errorf("%s: set test, to run Go tests, or command, to run a shell command", at)
	case s.Test == nil && len(s.Reads) == 0:
		return nil
	case s.Test != nil && len(s.Reads) > 0:
		return fmt.Errorf("%s: reads is for a command suite; a test suite reads the packages it names", at)
	}
	if s.Test != nil {
		t := s.Test
		if len(t.Packages) == 0 {
			return fmt.Errorf("%s: test.packages is required", at)
		}
		for _, p := range t.Packages {
			if strings.TrimSpace(p) == "" || strings.HasPrefix(p, "-") {
				return fmt.Errorf("%s: test.packages entry %q is not a package pattern", at, p)
			}
		}
		for _, tag := range t.Tags {
			if tag == "" || strings.ContainsAny(tag, " ,") {
				return fmt.Errorf("%s: test.tags entry %q is not a build tag", at, tag)
			}
		}
		if _, err := regexp.Compile(t.Run); err != nil {
			return fmt.Errorf("%s: test.run %q is not a regular expression: %w", at, t.Run, err)
		}
		if t.Count < 0 {
			return fmt.Errorf("%s: test.count %d must be 1 or more", at, t.Count)
		}
		return nil
	}
	for _, r := range s.Reads {
		if !filepath.IsLocal(filepath.Clean(r)) {
			return fmt.Errorf("%s: reads entry %q must be a path inside the repository", at, r)
		}
	}
	return nil
}

// validateNeeds checks the services, buckets and tools.
func (s E2ESuite) validateNeeds(at string) error {
	if err := oneOf(at, "services", s.Services, E2EServices); err != nil {
		return err
	}
	if err := oneOf(at, "tools", s.Tools, E2ETools); err != nil {
		return err
	}
	if len(s.Buckets) > 0 && !slices.Contains(s.Services, ServiceMinIO) {
		return fmt.Errorf("%s: buckets are created in minio, which services does not list", at)
	}
	seen := map[string]bool{}
	for _, b := range s.Buckets {
		if !e2eBucket.MatchString(b) {
			return fmt.Errorf("%s: buckets entry %q is not a bucket name (3 to 63 lowercase letters, digits, dots and hyphens)", at, b)
		}
		if seen[b] {
			return fmt.Errorf("%s: bucket %q is named twice", at, b)
		}
		seen[b] = true
	}
	return nil
}

// validateEnv checks the variable names and the references in their values.
func (s E2ESuite) validateEnv(at string) error {
	for _, k := range sortedKeys(s.Env) {
		if !e2eEnvName.MatchString(k) {
			return fmt.Errorf("%s: env name %q is not a variable name", at, k)
		}
		refs, err := E2ERefsIn(s.Env[k])
		if err != nil {
			return fmt.Errorf("%s: env %s: %w", at, k, err)
		}
		for _, ref := range refs {
			svc, ok := E2ERefs[ref]
			if !ok {
				return fmt.Errorf("%s: env %s names ${%s}, which is not a value a service gives\nreferences: %s",
					at, k, ref, strings.Join(sortedKeys(E2ERefs), ", "))
			}
			if !slices.Contains(s.Services, svc) {
				return fmt.Errorf("%s: env %s names ${%s}, but services does not list %s", at, k, ref, svc)
			}
		}
	}
	return nil
}

// validateWhen checks the timeout, the skip reason and the events.
func (s E2ESuite) validateWhen(at string) error {
	if s.Timeout != "" {
		d, err := time.ParseDuration(s.Timeout)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s: timeout %q is not a positive duration such as 20m", at, s.Timeout)
		}
	}
	if s.AllowSkip != "" && strings.TrimSpace(s.AllowSkip) == "" {
		return fmt.Errorf("%s: allow_skip is the reason a skip is allowed; it cannot be blank", at)
	}
	if s.On != nil && len(s.On) == 0 {
		return fmt.Errorf("%s: on is empty, so the suite never runs; list %s or leave it out", at, strings.Join(E2EEvents, " or "))
	}
	return oneOf(at, "on", s.On, E2EEvents)
}

// oneOf rejects a list that names something outside the allowed set or
// names the same thing twice.
func oneOf(at, key string, got, allowed []string) error {
	seen := map[string]bool{}
	for _, g := range got {
		if !slices.Contains(allowed, g) {
			return fmt.Errorf("%s: %s names %q, which is not one of %s", at, key, g, strings.Join(allowed, ", "))
		}
		if seen[g] {
			return fmt.Errorf("%s: %s names %q twice", at, key, g)
		}
		seen[g] = true
	}
	return nil
}
