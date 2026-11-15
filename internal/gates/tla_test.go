// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A model is a cfg with the spec it checks beside it: X.cfg and X.label.cfg
// both check X.tla, a cfg with no tla is not a model, and nothing under .lake
// is.
func TestTLAModelsPairACfgWithItsSpec(t *testing.T) {
	var calls []call
	listed := strings.Join([]string{
		"Device.tla", "Device.cfg", "Device.stale.cfg",
		"proto/Wire.tla", "proto/Wire.cfg",
		"conf/app.cfg", "other/Lone.cfg", "other/Else.tla",
		"x/.lake/packages/p/M.tla", "x/.lake/packages/p/M.cfg",
		"mod/A.b.tla", "mod/A.b.cfg", "mod/A.b.c.cfg",
		"ü dir/Ü.tla", "ü dir/Ü.cfg", "",
	}, "\x00")
	models, err := TLAModels(fake(t, &calls, listed))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range models {
		got = append(got, m.Path()+"="+m.Spec)
	}
	want := []string{
		"Device.cfg=Device.tla", "Device.stale.cfg=Device.tla",
		"mod/A.b.c.cfg=A.b.tla", "mod/A.b.cfg=A.b.tla",
		"proto/Wire.cfg=Wire.tla", "ü dir/Ü.cfg=Ü.tla",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("models %v, want %v", got, want)
	}
	if len(calls) != 1 || !slices.Contains(calls[0].args, "-z") {
		t.Fatalf("asked %v", calls)
	}
	if _, err := TLAModels(fake(t, &calls, errors.New("not a git repository"))); err == nil ||
		!strings.Contains(err.Error(), "cannot list TLA+ models") {
		t.Fatalf("a git that cannot answer: %v", err)
	}
}

type javaMachine struct {
	env     map[string]string
	onPath  string
	files   map[string]bool
	stubbed bool
	ran     []string
}

func (j *javaMachine) toolchain() TLAToolchain {
	return TLAToolchain{
		Getenv: func(k string) string { return j.env[k] },
		LookPath: func(string) (string, error) {
			if j.onPath == "" {
				return "", errors.New("not found")
			}
			return j.onPath, nil
		},
		Exists:    func(p string) bool { return j.files[p] },
		Fallbacks: []string{"/opt/homebrew/opt/openjdk/bin/java", "/usr/local/opt/openjdk/bin/java"},
	}
}

func (j *javaMachine) run(_ []string, _ bool, name string, args ...string) ([]byte, error) {
	j.ran = append(j.ran, name+" "+strings.Join(args, " "))
	if j.stubbed {
		return nil, errors.New("exit 1")
	}
	return nil, nil
}

// JAVA_HOME wins, then a java on PATH that runs, then Homebrew's openjdk.
func TestTLAJavaLookupOrder(t *testing.T) {
	brew := "/opt/homebrew/opt/openjdk/bin/java"
	intel := "/usr/local/opt/openjdk/bin/java"
	cases := []struct {
		name string
		m    javaMachine
		want string
		err  string
	}{
		{"JAVA_HOME first", javaMachine{env: map[string]string{"JAVA_HOME": "/jdk"}, onPath: "/usr/bin/java",
			files: map[string]bool{"/jdk/bin/java": true, brew: true}}, "/jdk/bin/java", ""},
		{"a JAVA_HOME with no java falls to PATH", javaMachine{env: map[string]string{"JAVA_HOME": "/gone"}, onPath: "/p/java",
			files: map[string]bool{brew: true}}, "/p/java", ""},
		{"PATH before Homebrew", javaMachine{onPath: "/p/java", files: map[string]bool{brew: true}}, "/p/java", ""},
		{"the macOS stub is skipped", javaMachine{onPath: "/usr/bin/java", stubbed: true, files: map[string]bool{brew: true}}, brew, ""},
		{"no java on PATH", javaMachine{files: map[string]bool{brew: true}}, brew, ""},
		{"Intel Homebrew last", javaMachine{files: map[string]bool{intel: true}}, intel, ""},
		{"none", javaMachine{onPath: "/usr/bin/java", stubbed: true}, "", "TLC needs a Java runtime"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.m.toolchain().java(tt.m.run)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) ||
					!strings.Contains(err.Error(), "brew install openjdk") || !strings.Contains(err.Error(), "JAVA_HOME") {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v, want %q", got, err, tt.want)
			}
		})
	}
	m := javaMachine{onPath: "/p/java"}
	if _, err := m.toolchain().java(m.run); err != nil || !slices.Equal(m.ran, []string{"/p/java -version"}) {
		t.Fatalf("PATH java was probed with %v: %v", m.ran, err)
	}
}

// tlaMachine answers git with models, writes the jar for curl, and answers
// each TLC run from tlc by cfg.
type tlaMachine struct {
	t     *testing.T
	jar   []byte
	list  string
	tlc   map[string]tlcResult
	calls []call
	dirs  []string
	metas []string
}

type tlcResult struct {
	out string
	err error
}

func (m *tlaMachine) exec(env []string, stream bool, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, call{env, stream, name, args})
	switch filepath.Base(name) {
	case "git":
		return []byte(m.list), nil
	case "curl":
		return nil, os.WriteFile(args[slices.Index(args, "-o")+1], m.jar, 0o644)
	}
	return nil, nil
}

func (m *tlaMachine) in(dir string) Exec {
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		m.dirs = append(m.dirs, dir)
		m.calls = append(m.calls, call{env, stream, name, args})
		meta := args[slices.Index(args, "-metadir")+1]
		if st, err := os.Stat(meta); err != nil || !st.IsDir() {
			m.t.Errorf("metadir %s is not there for TLC", meta)
		}
		m.metas = append(m.metas, meta)
		r := m.tlc[args[slices.Index(args, "-config")+1]]
		return []byte(r.out), r.err
	}
}

func (m *tlaMachine) toolchain(sha string) TLAToolchain {
	cache := m.t.TempDir()
	return TLAToolchain{
		Getenv:   func(string) string { return "/jdk" },
		LookPath: func(string) (string, error) { return "", errors.New("no") },
		Exists:   func(p string) bool { return p == "/jdk/bin/java" },
		Cache:    func() (string, error) { return cache, nil },
		URL:      "https://example.invalid/tla2tools.jar",
		SHA256:   sha,
	}
}

// repoWith writes the cfg files into a root and returns it.
func repoWith(t *testing.T, cfgs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range cfgs {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

const (
	stale     = "SPECIFICATION Spec\n\\* expect violation: Follows\n"
	actionBad = "State 2\nError: Action property Follows is violated.\n"
	invBad    = "Error: Invariant Safe is violated.\n"
)

// The jar is downloaded once, checked, and used with the documented command
// line in each model's directory, in path order, with a fresh metadir that is
// gone afterwards. A model with an expected violation passes when TLC finds it.
func TestTLACheckModelsWithTheDownloadedJar(t *testing.T) {
	jar := []byte("jar bytes")
	m := &tlaMachine{t: t, jar: jar,
		list: "z/Z.tla\x00z/Z.cfg\x00Device.tla\x00Device.cfg\x00Device.stale.cfg\x00",
		tlc: map[string]tlcResult{
			"Device.stale.cfg": {actionBad, errors.New("exit status 13")},
		}}
	root := repoWith(t, map[string]string{"Device.cfg": "SPECIFICATION Spec\n", "Device.stale.cfg": stale, "z/Z.cfg": "INIT Init\n"})
	tc := m.toolchain(sum(jar))
	var sb strings.Builder
	if err := TLA(tc, root, &sb, m.exec, m.in); err != nil {
		t.Fatalf("%v\n%s", err, sb.String())
	}
	if !slices.Equal(m.dirs, []string{".", ".", "z"}) {
		t.Fatalf("ran in %v", m.dirs)
	}
	var tlc []call
	for _, c := range m.calls {
		if c.name == "/jdk/bin/java" {
			tlc = append(tlc, c)
		}
	}
	cache, _ := tc.Cache()
	jarPath := filepath.Join(cache, "lateregate", "tla2tools-"+TLAVersion+".jar")
	a := tlc[0].args
	want := []string{"-XX:+UseParallelGC", "-cp", jarPath, "tlc2.TLC", "-workers", "auto", "-cleanup", "-metadir", a[8], "-config", "Device.cfg", "Device.tla"}
	if !slices.Equal(a, want) || tlc[0].stream {
		t.Fatalf("ran %v, want %v", a, want)
	}
	if tlc[1].args[10] != "Device.stale.cfg" || tlc[1].args[11] != "Device.tla" || tlc[2].args[11] != "Z.tla" {
		t.Fatalf("pairs %v %v", tlc[1].args, tlc[2].args)
	}
	for _, meta := range m.metas {
		if _, err := os.Stat(meta); err == nil {
			t.Errorf("metadir %s was left behind", meta)
		}
	}
	if len(slices.Compact(slices.Clone(m.metas))) != 3 {
		t.Errorf("metadirs are not fresh: %v", m.metas)
	}
	for _, w := range []string{"Device.cfg: ok", "Device.stale.cfg: ok, finds the expected violation of Follows", "3 TLA+ model(s) check"} {
		if !strings.Contains(sb.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, sb.String())
		}
	}

	again := &tlaMachine{t: t, jar: jar, list: m.list, tlc: m.tlc}
	if err := TLA(tc, root, &strings.Builder{}, again.exec, again.in); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(again.calls, func(c call) bool { return c.name == "curl" }) {
		t.Fatal("a downloaded jar was downloaded again")
	}
}

func TestTLARefusesAJarWithTheWrongDigest(t *testing.T) {
	m := &tlaMachine{t: t, jar: []byte("tampered"), list: "A.tla\x00A.cfg\x00"}
	tc := m.toolchain(sum([]byte("the real jar")))
	err := TLA(tc, repoWith(t, map[string]string{"A.cfg": ""}), &strings.Builder{}, m.exec, m.in)
	if err == nil || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("got %v", err)
	}
	if len(m.dirs) != 0 {
		t.Fatal("TLC ran with an unverified jar")
	}
	cache, _ := tc.Cache()
	if _, err := os.Stat(filepath.Join(cache, "lateregate", "tla2tools-"+TLAVersion+".jar")); err == nil {
		t.Fatal("an unverified jar was installed")
	}
	left, _ := filepath.Glob(filepath.Join(cache, "lateregate", "tla-download-*"))
	if len(left) != 0 {
		t.Fatalf("download left behind: %v", left)
	}
}

func TestTLADownloadFailureAndMissingCache(t *testing.T) {
	m := &tlaMachine{t: t, list: "A.tla\x00A.cfg\x00"}
	tc := m.toolchain("00")
	failing := func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		if name == "curl" {
			return nil, errors.New("offline")
		}
		return m.exec(env, stream, name, args...)
	}
	root := repoWith(t, map[string]string{"A.cfg": ""})
	if err := TLA(tc, root, &strings.Builder{}, failing, m.in); err == nil || !strings.Contains(err.Error(), "download https://example.invalid/tla2tools.jar") {
		t.Fatalf("got %v", err)
	}
	tc.Cache = func() (string, error) { return "", errors.New("$HOME is not defined") }
	if err := TLA(tc, root, &strings.Builder{}, m.exec, m.in); err == nil || !strings.Contains(err.Error(), "no cache directory") {
		t.Fatalf("got %v", err)
	}
}

// Without the expectation a model passes only on exit 0; with it, only when
// TLC reports that violation. Every model runs and the failures are named,
// each with the tail of TLC's output.
func TestTLAExpectedViolationsAndFailures(t *testing.T) {
	jar := []byte("jar")
	var long strings.Builder
	for i := 1; i <= 40; i++ {
		long.WriteString("line " + string(rune('A'+i%26)) + "\n")
	}
	long.WriteString("Error: Invariant Safe is violated.\n")
	cases := []struct {
		name string
		cfg  string
		res  tlcResult
		want string // "" passes
	}{
		{"plain pass", "INIT Init\n", tlcResult{}, ""},
		{"plain fail", "INIT Init\n", tlcResult{invBad, errors.New("exit status 12")}, "TLC failed: exit status 12"},
		{"invariant expected", "\\* expect violation: Safe\n", tlcResult{invBad, errors.New("exit status 12")}, ""},
		{"action property expected", stale, tlcResult{actionBad, errors.New("exit status 13")}, ""},
		{"expected, TLC passes", stale, tlcResult{"Model checking completed. No error has been found.\n", nil},
			"no longer finds the violation of Follows it exists to show: TLC passed it"},
		{"expected, another violation", stale, tlcResult{invBad, errors.New("exit status 12")},
			"no longer finds the violation of Follows it exists to show: TLC failed with exit status 12"},
		{"expected, TLC crashes", stale, tlcResult{"Error: Action property Follows is violated.", errors.New("signal: killed")}, ""},
		{"the marker must be exact", "  \\* expect violation: Safe\n", tlcResult{invBad, errors.New("exit status 12")}, "TLC failed"},
		{"a longer name is another name", "\\* expect violation: Safe\n", tlcResult{"Error: Invariant SafeToo is violated.\n", errors.New("exit status 12")}, "no longer finds"},
		{"a CRLF cfg", "\\* expect violation: Safe\r\n", tlcResult{invBad, errors.New("exit status 12")}, ""},
		{"a long output is cut to its tail", "INIT Init\n", tlcResult{long.String(), errors.New("exit status 12")}, "Error: Invariant Safe"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m := &tlaMachine{t: t, jar: jar, list: "A.tla\x00A.cfg\x00B.tla\x00B.cfg\x00",
				tlc: map[string]tlcResult{"A.cfg": tt.res}}
			root := repoWith(t, map[string]string{"A.cfg": tt.cfg, "B.cfg": "INIT Init\n"})
			var sb strings.Builder
			err := TLA(m.toolchain(sum(jar)), root, &sb, m.exec, m.in)
			if len(m.dirs) != 2 {
				t.Fatalf("a failing model stopped the others: %v", m.dirs)
			}
			if tt.want == "" {
				if err != nil {
					t.Fatalf("%v\n%s", err, sb.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "1 of 2 TLA+ models: A.cfg") || !strings.Contains(sb.String(), tt.want) {
				t.Fatalf("got %v\n%s", err, sb.String())
			}
			if strings.Contains(sb.String(), "B.cfg: ok") == false {
				t.Errorf("the passing model was not reported:\n%s", sb.String())
			}
			if tt.name == "a long output is cut to its tail" && strings.Count(sb.String(), "line ") > tlaTail {
				t.Errorf("more than %d lines of output:\n%s", tlaTail, sb.String())
			}
		})
	}
}

func TestTLAWithNoModelOrNoJavaFails(t *testing.T) {
	m := &tlaMachine{t: t, jar: []byte("j"), list: "conf/app.cfg\x00"}
	tc := m.toolchain(sum(m.jar))
	if err := TLA(tc, t.TempDir(), &strings.Builder{}, m.exec, m.in); err == nil || !strings.Contains(err.Error(), "no TLA+ model") {
		t.Fatalf("got %v", err)
	}
	m.list = "A.tla\x00A.cfg\x00"
	tc.Exists = func(string) bool { return false }
	if err := TLA(tc, t.TempDir(), &strings.Builder{}, m.exec, m.in); err == nil || !strings.Contains(err.Error(), "needs a Java runtime") {
		t.Fatalf("got %v", err)
	}
	if err := TLA(tc, t.TempDir(), &strings.Builder{}, fake(t, &m.calls, errors.New("no git")), m.in); err == nil {
		t.Fatal("a git that cannot answer passed")
	}
	// A cfg git lists but the tree lacks is an error, not a pass.
	tc = m.toolchain(sum(m.jar))
	if err := TLA(tc, t.TempDir(), &strings.Builder{}, m.exec, m.in); err == nil {
		t.Fatal("an unreadable cfg passed")
	}
}

func TestTheTLAPinsAreWhole(t *testing.T) {
	tc := DefaultTLAToolchain()
	if tc.URL != "https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar" || TLAVersion != "v1.7.4" ||
		tc.SHA256 != "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88" ||
		tc.Getenv == nil || tc.LookPath == nil || tc.Cache == nil || len(tc.Fallbacks) != 2 {
		t.Fatalf("default toolchain %+v", tc)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "java")
	if err := os.WriteFile(f, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if !tc.Exists(f) || tc.Exists(dir) || tc.Exists(filepath.Join(dir, "none")) {
		t.Fatal("Exists must be true for files only")
	}
}
