// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const ruleFile = `apiVersion: v1
kind: ConfigMap
metadata:
  name: not-rules
---
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: svc
spec:
  groups:
    - name: svc
      rules:
        - alert: Down
          expr: up == 0
          for: 5m
`

// Every PrometheusRule document is found, in a file with others, by its
// position, and its spec is the rules file; a file that names the kind and
// does not parse, or a rule with no spec, is an error rather than a skip.
func TestPrometheusRulesLiftsEverySpec(t *testing.T) {
	files := map[string]string{
		"deploy/base/rules.yaml": ruleFile,
		"deploy/other.yml":       "kind: Deployment\n",
		"docs/list.yaml":         "- PrometheusRule\n- ServiceMonitor\n",
	}
	read := func(f string) ([]byte, error) { return []byte(files[f]), nil }
	var calls []call
	got, err := PrometheusRules(fake(t, &calls, "deploy/base/rules.yaml\ndeploy/other.yml\ndocs/list.yaml\nx/testdata/r.yaml\n"), read)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].File != "deploy/base/rules.yaml" || got[0].Index != 2 {
		t.Fatalf("rules %+v", got)
	}
	if spec := string(got[0].Spec); !strings.Contains(spec, "groups:") || !strings.Contains(spec, "expr: up == 0") || strings.Contains(spec, "kind") {
		t.Fatalf("spec %q", spec)
	}
	files["bad.yaml"] = "kind: PrometheusRule\nspec: [\n"
	if _, err := PrometheusRules(fake(t, &calls, "bad.yaml\n"), read); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("a broken rule file: %v", err)
	}
	files["empty.yaml"] = "kind: PrometheusRule\nmetadata: {name: x}\n"
	if _, err := PrometheusRules(fake(t, &calls, "empty.yaml\n"), read); err == nil || !strings.Contains(err.Error(), "no spec") {
		t.Fatalf("a rule with no spec: %v", err)
	}
}

// promMachine is a computer with no promtool: git lists one rule file,
// curl writes archive, and promtool answers check rules, or fails.
type promMachine struct {
	archive []byte
	calls   []call
	fail    string
	checked []string
}

func (m *promMachine) exec(env []string, stream bool, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, call{env, stream, name, args})
	base := filepath.Base(name)
	if base == m.fail {
		return nil, errors.New(base + " failed")
	}
	switch base {
	case "git":
		return []byte("deploy/rules.yaml\n"), nil
	case "curl":
		return nil, os.WriteFile(args[slices.Index(args, "-o")+1], m.archive, 0o644)
	case "promtool":
		for _, a := range args[2:] {
			b, err := os.ReadFile(a)
			if err != nil {
				return nil, err
			}
			m.checked = append(m.checked, string(b))
		}
	}
	return nil, nil
}

func pinnedPromtool(t *testing.T, sha string) (Promtool, string) {
	t.Helper()
	cache := t.TempDir()
	return Promtool{
		Cache:    func() (string, error) { return cache, nil },
		Platform: "plan9/mips",
		Assets:   map[string]ElanAsset{"plan9/mips": {"prometheus-mips.tar.gz", sha}},
		BaseURL:  "https://example.invalid/prometheus/",
	}, filepath.Join(cache, "lateregate", "promtool-"+PromtoolVersion)
}

// The gate downloads the pinned archive, checks its digest, installs its
// promtool, and checks each rule's spec with it; the next run downloads
// nothing.
func TestRulesInstallsThePinnedPromtoolAndChecksEachSpec(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "deploy/rules.yaml", ruleFile)
	archive, sha := elanArchive(t, "prometheus-3/promtool")
	tc, dir := pinnedPromtool(t, sha)
	m := &promMachine{archive: archive}
	var out strings.Builder
	if err := Rules(tc, root, &out, m.exec); err != nil {
		t.Fatalf("Rules = %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "promtool")); err != nil {
		t.Fatalf("promtool not installed: %v", err)
	}
	if len(m.checked) != 1 || !strings.Contains(m.checked[0], "alert: Down") {
		t.Fatalf("checked %q", m.checked)
	}
	last := m.calls[len(m.calls)-1]
	if last.name != filepath.Join(dir, "promtool") || last.args[0] != "check" || last.args[1] != "rules" || !last.stream {
		t.Fatalf("promtool call %+v", last)
	}
	m.calls, m.checked = nil, nil
	if err := Rules(tc, root, &out, m.exec); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(m.calls, func(c call) bool { return c.name == "curl" }) {
		t.Fatal("an installed promtool was downloaded again")
	}
}

// A digest that does not match installs nothing; a rule promtool rejects
// fails the gate; a platform with no pin is named.
func TestRulesRefusesWhatItCannotTrust(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "deploy/rules.yaml", ruleFile)
	archive, sha := elanArchive(t, "promtool")
	tc, dir := pinnedPromtool(t, strings.Repeat("0", 64))
	m := &promMachine{archive: archive}
	if err := Rules(tc, root, &strings.Builder{}, m.exec); err == nil || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("a wrong digest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "promtool")); err == nil {
		t.Fatal("an archive with the wrong digest was installed")
	}
	tc, _ = pinnedPromtool(t, sha)
	m = &promMachine{archive: archive, fail: "promtool"}
	if err := Rules(tc, root, &strings.Builder{}, m.exec); err == nil || !strings.Contains(err.Error(), "rejects the rules") {
		t.Fatalf("a rejected rule: %v", err)
	}
	tc.Platform = "windows/amd64"
	if err := Rules(tc, root, &strings.Builder{}, (&promMachine{}).exec); err == nil || !strings.Contains(err.Error(), "pinned for windows/amd64") {
		t.Fatalf("an unpinned platform: %v", err)
	}
	if err := Rules(DefaultPromtool(), root, &strings.Builder{}, fake(t, &[]call{}, "")); err == nil || !strings.Contains(err.Error(), "no PrometheusRule") {
		t.Fatalf("nothing to check passed vacuously: %v", err)
	}
}

// The pins are complete: every platform names an archive of PromtoolVersion
// and a SHA-256.
func TestThePromtoolPinsAreWhole(t *testing.T) {
	tc := DefaultPromtool()
	if !strings.HasSuffix(tc.BaseURL, "/download/"+PromtoolVersion+"/") || tc.Cache == nil {
		t.Fatalf("default promtool %+v", tc)
	}
	v := strings.TrimPrefix(PromtoolVersion, "v")
	for _, p := range []string{"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64"} {
		a, ok := tc.Assets[p]
		want := "prometheus-" + v + "." + strings.Replace(p, "/", "-", 1) + ".tar.gz"
		if !ok || a.Name != want || len(a.SHA256) != 64 {
			t.Errorf("%s: %+v, want %s", p, a, want)
		}
	}
}
