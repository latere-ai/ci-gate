// Copyright 2026 Latere AI.
// Licensed under the Apache License, Version 2.0.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A repo adopting only the required targets writes no config at all, so a
// missing file has to be a success with defaults rather than an error.
func TestAMissingFileIsTheDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("no config should not be an error: %v", err)
	}
	if c.Cover.Threshold != DefaultThreshold {
		t.Errorf("threshold = %v, want %v", c.Cover.Threshold, DefaultThreshold)
	}
	if c.Spec.Dir != "" {
		t.Errorf("spec.dir = %q, want empty so spec-lint stays off", c.Spec.Dir)
	}
}

func TestLoadReadsEverySection(t *testing.T) {
	dir := write(t, `
cover:
  threshold: 85.5
  trim_prefix: github.com/latere-ai/llmops/
  exempt:
    internal/harness: shells out to a real binary
spec:
  dir: specs
  status: [draft, partial, complete]
  require: [title, status]
  index: specs/README.md
  wikilinks: true
  exclude: [README.md]
hermetic:
  allow: [/usr/bin, /bin]
modernize:
  disable: [newexpr]
`)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Cover.Threshold != 85.5 {
		t.Errorf("threshold = %v", c.Cover.Threshold)
	}
	if why, ok := c.Cover.ExemptFor("github.com/latere-ai/llmops/internal/harness"); !ok || why == "" {
		t.Errorf("ExemptFor = %q, %v; want the reason", why, ok)
	}
	if !c.Spec.Wikilinks || c.Spec.Index != "specs/README.md" {
		t.Errorf("spec section not read: %+v", c.Spec)
	}
	if !c.Spec.IsExcluded("README.md") {
		t.Error("README.md should be excluded")
	}
	if len(c.Hermetic.Allow) != 2 || len(c.Modernize.Disable) != 1 {
		t.Errorf("hermetic/modernize not read: %+v %+v", c.Hermetic, c.Modernize)
	}
}

// The invariant both original implementations stated in comments and got for
// free from a Go map literal: an exemption cannot exist without a reason.
// Moving the data to YAML turns that into a validation rule, so it has to
// fail rather than warn.
func TestAnExemptionWithoutAReasonIsRejected(t *testing.T) {
	dir := write(t, "cover:\n  exempt:\n    internal/thing: \"\"\n    internal/other: \"   \"\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("an exemption with no reason must fail the load")
	}
	for _, want := range []string{"internal/other", "internal/thing", "decision"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestAThresholdOutsideAPercentageIsRejected(t *testing.T) {
	for _, v := range []string{"-1", "101"} {
		if _, err := Load(write(t, "cover:\n  threshold: "+v+"\n")); err == nil {
			t.Errorf("threshold %s should be rejected", v)
		}
	}
}

// A typo in a key silently disabling a gate is the failure mode this guards:
// `exmept:` would parse as an empty config and report green.
func TestAnUnknownKeyIsRejected(t *testing.T) {
	if _, err := Load(write(t, "cover:\n  exmept:\n    a: b\n")); err == nil {
		t.Fatal("an unknown key must fail rather than silently disable a gate")
	}
}

func TestMalformedYAMLIsAnError(t *testing.T) {
	if _, err := Load(write(t, "cover:\n\tthreshold: [\n")); err == nil {
		t.Fatal("malformed YAML must be an error")
	}
}

func TestAnUnreadableFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a directory in place of the config must be an error")
	}
}

func TestAnEmptyVocabularyAllowsAnyStatus(t *testing.T) {
	if !(Spec{}).AllowsStatus("anything") {
		t.Error("no configured vocabulary should allow any status")
	}
	s := Spec{Status: []string{"draft"}}
	if !s.AllowsStatus("draft") || s.AllowsStatus("shipped") {
		t.Error("a configured vocabulary should be closed")
	}
}

func TestExemptForMissesAnUnlistedPackage(t *testing.T) {
	c := Cover{Exempt: map[string]string{"internal/a": "why"}}
	if _, ok := c.ExemptFor("mod/internal/b"); ok {
		t.Error("internal/b is not exempt")
	}
}
