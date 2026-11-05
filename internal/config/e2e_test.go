// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// A well-formed file loads, and the unset keys read as their defaults.
func TestE2ESuitesLoadWithTheirDefaults(t *testing.T) {
	c, err := Load(write(t, `e2e:
  - name: lux-policy
    why: the policy holds against the pinned core
    test: {tags: [integration], packages: [./test/luxe2e]}
  - name: store
    why: the Postgres store keeps the memory store's contract
    test: {tags: [postgres], packages: ["./internal/store/..."], race: true, count: 3, run: "^TestStore"}
    services: [postgres]
    env: {DATABASE_URL: "${postgres.url}"}
    timeout: 5m
  - name: install
    why: the install document brings up a cluster
    command: make install-walk
    tools: [kind, helm, kubectl]
    reads: [docs/install.md, deploy]
    allow_skip: the cluster tier skips without a GPU
    on: [push, tag]
  - name: repos
    why: the Repos section reads and writes objects
    command: go test ./x
    services: [minio]
    buckets: [origo]
    env: {S3: "${minio.endpoint}/${minio.region}", K: "${minio.access_key}${minio.secret_key}", PLAIN: "$HOME"}
    on: [tag]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.E2E) != 4 {
		t.Fatalf("suites %+v", c.E2E)
	}
	lux, store, install, repos := c.E2E[0], c.E2E[1], c.E2E[2], c.E2E[3]
	if lux.TimeoutOr() != DefaultE2ETimeout || lux.Test.CountOr() != 1 || !slices.Equal(lux.Events(), []string{OnPush}) || lux.Skips() {
		t.Errorf("defaults %+v", lux)
	}
	if store.TimeoutOr() != 5*time.Minute || store.Test.CountOr() != 3 || !store.Test.Race {
		t.Errorf("store %+v", store)
	}
	if !install.Skips() || !install.RunsOn(OnTag) || !install.RunsOn(OnPush) || repos.RunsOn(OnPush) {
		t.Errorf("events %+v %+v", install, repos)
	}
	if got := (E2ESuite{Timeout: "bogus"}).TimeoutOr(); got != DefaultE2ETimeout {
		t.Errorf("a timeout that does not parse reads as %s", got)
	}
}

// Everything wrong is refused at load, in words that say what to change.
func TestE2ESuitesAreRefusedAtLoad(t *testing.T) {
	const head = "e2e:\n  - name: a\n    why: because\n"
	const test = "    test: {packages: [./x]}\n"
	for body, want := range map[string]string{
		"e2e:\n  - why: x\n":                             "name is required",
		"e2e:\n  - name: Bad_Name\n":                     "lowercase letters, digits and hyphens",
		head + test + "  - name: a\n    why: y\n" + test: "named twice",
		"e2e:\n  - name: a\n" + test:                     "why is required",
		"e2e:\n  - name: a\n    why: '  '\n" + test:      "why is required",
		head:                                                             "set test",
		head + test + "    command: make x\n":                            "not both",
		head + "    test: {}\n":                                          "test.packages is required",
		head + "    test: {packages: ['']}\n":                            "not a package pattern",
		head + "    test: {packages: [-race]}\n":                         "not a package pattern",
		head + "    test: {packages: [./x], tags: ['a b']}\n":            "not a build tag",
		head + "    test: {packages: [./x], run: '('}\n":                 "not a regular expression",
		head + "    test: {packages: [./x], count: -1}\n":                "must be 1 or more",
		head + test + "    reads: [docs]\n":                              "reads is for a command suite",
		head + "    command: make x\n    reads: [../up]\n":               "inside the repository",
		head + test + "    services: [mysql]\n":                          "not one of postgres, minio",
		head + test + "    services: [postgres, postgres]\n":             "twice",
		head + test + "    tools: [terraform]\n":                         "not one of kubectl, kind, helm",
		head + test + "    buckets: [data]\n":                            "which services does not list",
		head + test + "    services: [minio]\n    buckets: [A]\n":        "not a bucket name",
		head + test + "    services: [minio]\n    buckets: [abc, abc]\n": "named twice",
		head + test + "    env: {1BAD: x}\n":                             "not a variable name",
		head + test + "    env: {A: '${postgres.url'}\n":                 "no closing",
		head + test + "    env: {A: '${postgres.host}', B: x}\n":         "not a value a service gives",
		head + test + "    env: {A: '${postgres.url}'}\n":                "does not list postgres",
		head + test + "    env: {A: '${minio.region}'}\n":                "does not list minio",
		head + test + "    timeout: soon\n":                              "not a positive duration",
		head + test + "    timeout: -5m\n":                               "not a positive duration",
		head + test + "    allow_skip: '  '\n":                           "cannot be blank",
		head + test + "    on: []\n":                                     "never runs",
		head + test + "    on: [pull]\n":                                 "not one of push, tag",
		head + test + "    on: [tag, tag]\n":                             "twice",
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", body, err, want)
		}
	}
}

// A reference list reads each ${...} and refuses an unclosed one.
func TestE2ERefsIn(t *testing.T) {
	got, err := E2ERefsIn("a${x}b${y}")
	if err != nil || !slices.Equal(got, []string{"x", "y"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := E2ERefsIn("none"); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := E2ERefsIn("${x"); err == nil {
		t.Fatal("an unclosed reference was accepted")
	}
}
