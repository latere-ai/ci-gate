// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The projects git lists: one per directory, whichever lakefile it holds,
// and none from the packages Lake fetched into .lake or a file that only
// ends like a lakefile.
func TestLakeProjectsAreTheTrackedLakefiles(t *testing.T) {
	var calls []call
	listed := strings.Join([]string{
		"lakefile.toml",
		"formal/lakefile.toml",
		"formal/lakefile.lean",
		"proofs/.lake/packages/batteries/lakefile.toml",
		"docs/notlakefile.toml",
		"specs/001-a.md",
		"",
	}, "\n")
	dirs, err := LakeProjects(fake(t, &calls, listed))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirs, []string{".", "formal"}) {
		t.Fatalf("projects %v", dirs)
	}
	if len(calls) != 1 || calls[0].name != "git" || !slices.Contains(calls[0].args, "*/lakefile.lean") {
		t.Fatalf("asked %v", calls)
	}
	if _, err := LakeProjects(fake(t, &calls, errors.New("not a git repository"))); err == nil ||
		!strings.Contains(err.Error(), "cannot list Lake projects") {
		t.Fatalf("a git that cannot answer: %v", err)
	}
}

func onPath(lake string) LeanToolchain {
	return LeanToolchain{LookPath: func(string) (string, error) { return lake, nil }}
}

// inDirs runs each build in a directory it records, failing the ones failing
// names.
func inDirs(dirs *[]string, calls *[]call, failing ...string) func(string) Exec {
	return func(dir string) Exec {
		return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
			*dirs = append(*dirs, dir)
			*calls = append(*calls, call{env, stream, name, args})
			if slices.Contains(failing, dir) {
				return nil, errors.New("exit 1")
			}
			return nil, nil
		}
	}
}

// A lake on PATH builds every project with --wfail, streamed, in the
// project's own directory, so elan reads that project's lean-toolchain, and
// in the environment the gate itself runs in.
func TestLeanBuildsEveryProjectWithTheLakeOnPath(t *testing.T) {
	var listed, builds []call
	var dirs []string
	var sb strings.Builder
	if err := Lean(onPath("/opt/lake"), &sb, fake(t, &listed, "a/lakefile.toml\nb/lakefile.lean\n"), inDirs(&dirs, &builds)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirs, []string{"a", "b"}) || len(listed) != 1 {
		t.Fatalf("built in %v after %v", dirs, listed)
	}
	for i, c := range builds {
		if joined(c) != "/opt/lake build --wfail" || !c.stream || c.env != nil {
			t.Errorf("build %d: %+v", i, c)
		}
	}
	if !strings.Contains(sb.String(), "building with /opt/lake") || !strings.Contains(sb.String(), "2 Lake project(s) build with no warning") {
		t.Errorf("output %q", sb.String())
	}
}

// Every project is built, and the failure names the ones that logged a
// warning and says that sorry is one.
func TestLeanReportsEveryProjectWithAWarning(t *testing.T) {
	var listed, builds []call
	var dirs []string
	err := Lean(onPath("lake"), &strings.Builder{},
		fake(t, &listed, "a/lakefile.toml\nb/lakefile.toml\nc/lakefile.toml\n"), inDirs(&dirs, &builds, "a", "c"))
	if err == nil || !strings.Contains(err.Error(), "2 of 3 Lake projects") ||
		!strings.Contains(err.Error(), "a, c") || !strings.Contains(err.Error(), "sorry") {
		t.Fatalf("got %v", err)
	}
	if len(builds) != 3 {
		t.Fatalf("a failure stopped the other builds: %v", builds)
	}
}

func TestLeanWithNoProjectFails(t *testing.T) {
	var calls []call
	var dirs []string
	if err := Lean(onPath("lake"), &strings.Builder{}, fake(t, &calls, ""), inDirs(&dirs, &calls)); err == nil ||
		!strings.Contains(err.Error(), "no Lake project") {
		t.Fatalf("got %v", err)
	}
	if err := Lean(onPath("lake"), &strings.Builder{}, fake(t, &calls, errors.New("no git")), inDirs(&dirs, &calls)); err == nil {
		t.Fatal("a git that cannot answer passed")
	}
}

// elanArchive is a gzipped tar holding one executable named name.
func elanArchive(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\n")
	if err := tw.WriteHeader(&tar.Header{Name: "./dir/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// machine is a computer with no lake: git lists one project, curl writes
// archive, elan-init installs a lake unless told not to, and lake answers
// build.
type machine struct {
	t       *testing.T
	archive []byte
	calls   []call
	noLake  bool
	fail    string
	dirs    []string
}

func (m *machine) exec(env []string, stream bool, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, call{env, stream, name, args})
	base := filepath.Base(name)
	if base == m.fail {
		return nil, errors.New(base + " failed")
	}
	switch base {
	case "git":
		return []byte("formal/lakefile.toml\n"), nil
	case "curl":
		return nil, os.WriteFile(args[slices.Index(args, "-o")+1], m.archive, 0o644)
	case "elan-init":
		home := ""
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "ELAN_HOME="); ok {
				home = v
			}
		}
		if home == "" {
			m.t.Fatal("elan-init ran without ELAN_HOME")
		}
		if m.noLake {
			return nil, nil
		}
		if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(home, "bin", "lake"), []byte("#!/bin/sh\n"), 0o755)
	}
	return nil, nil
}

// in runs a build the same way, recording the directory.
func (m *machine) in(dir string) Exec {
	return func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		m.dirs = append(m.dirs, dir)
		return m.exec(env, stream, name, args...)
	}
}

func (m *machine) ran(name string) bool {
	return slices.ContainsFunc(m.calls, func(c call) bool { return filepath.Base(c.name) == name })
}

func provisioned(t *testing.T, sha string) (LeanToolchain, string) {
	t.Helper()
	cache := t.TempDir()
	return LeanToolchain{
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Cache:    func() (string, error) { return cache, nil },
		Platform: "plan9/mips",
		Assets:   map[string]ElanAsset{"plan9/mips": {"elan-mips.tar.gz", sha}},
		BaseURL:  "https://example.invalid/elan/",
	}, filepath.Join(cache, "lateregate", "elan-"+ElanVersion)
}

// With no lake on PATH the gate downloads the pinned archive, checks its
// digest, runs its elan-init into the cache, and builds with that lake under
// the same ELAN_HOME. The next run finds the lake and downloads nothing.
func TestLeanInstallsElanWhenNoLakeIsOnPath(t *testing.T) {
	archive, sha := elanArchive(t, "./elan-init")
	tc, home := provisioned(t, sha)
	m := &machine{t: t, archive: archive}
	var sb strings.Builder
	if err := Lean(tc, &sb, m.exec, m.in); err != nil {
		t.Fatal(err)
	}
	last := m.calls[len(m.calls)-1]
	if last.name != filepath.Join(home, "bin", "lake") || !slices.Contains(last.env, "ELAN_HOME="+home) || !slices.Equal(m.dirs, []string{"formal"}) {
		t.Fatalf("built with %+v in %v", last, m.dirs)
	}
	if got := joined(m.calls[1]); !strings.Contains(got, "https://example.invalid/elan/elan-mips.tar.gz") {
		t.Fatalf("downloaded %s", got)
	}
	if !strings.Contains(sb.String(), "installing elan "+ElanVersion) {
		t.Errorf("output %q", sb.String())
	}
	left, err := filepath.Glob(filepath.Join(filepath.Dir(home), "elan-download-*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("the download was left behind: %v %v", left, err)
	}

	again := &machine{t: t, archive: archive}
	if err := Lean(tc, &strings.Builder{}, again.exec, again.in); err != nil {
		t.Fatal(err)
	}
	if again.ran("curl") || again.ran("elan-init") {
		t.Fatalf("an installed elan was installed again: %v", again.calls)
	}
}

// Every way the install can go wrong stops the gate with what went wrong,
// and nothing is built.
func TestLeanRefusesAnInstallThatFails(t *testing.T) {
	archive, sha := elanArchive(t, "elan-init")
	noInit, noInitSHA := elanArchive(t, "README")
	cases := []struct {
		name    string
		sha     string
		archive []byte
		m       machine
		want    string
	}{
		{"a digest that differs", strings.Repeat("0", 64), archive, machine{}, "not the pinned"},
		{"a download that fails", sha, archive, machine{fail: "curl"}, "download https://example.invalid/elan/elan-mips.tar.gz"},
		{"an archive with no elan-init", noInitSHA, noInit, machine{}, "holds no elan-init"},
		{"an elan-init that fails", sha, archive, machine{fail: "elan-init"}, "elan-init: elan-init failed"},
		{"an elan-init that installs no lake", sha, archive, machine{noLake: true}, "installed no lake"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tc, _ := provisioned(t, tt.sha)
			m := tt.m
			m.t, m.archive = t, tt.archive
			err := Lean(tc, &strings.Builder{}, m.exec, m.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			if m.ran("lake") {
				t.Fatalf("built after a failed install: %v", m.calls)
			}
		})
	}
}

func TestLeanNeedsAPinnedArchiveAndACache(t *testing.T) {
	_, sha := elanArchive(t, "elan-init")
	tc, _ := provisioned(t, sha)
	tc.Platform = "windows/amd64"
	m := &machine{t: t}
	if err := Lean(tc, &strings.Builder{}, m.exec, m.in); err == nil || !strings.Contains(err.Error(), "no elan "+ElanVersion+" archive is pinned for windows/amd64") {
		t.Fatalf("an unpinned platform: %v", err)
	}
	tc, _ = provisioned(t, sha)
	tc.Cache = func() (string, error) { return "", errors.New("$HOME is not defined") }
	if err := Lean(tc, &strings.Builder{}, m.exec, m.in); err == nil || !strings.Contains(err.Error(), "no cache directory") {
		t.Fatalf("no cache: %v", err)
	}
}

func TestExtractFileRefusesWhatIsNotAnArchive(t *testing.T) {
	dir := t.TempDir()
	notGzip := filepath.Join(dir, "elan.tar.gz")
	if err := os.WriteFile(notGzip, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractFile(notGzip, "elan-init", filepath.Join(dir, "out")); err == nil || !strings.Contains(err.Error(), "read elan.tar.gz") {
		t.Fatalf("got %v", err)
	}
	if err := extractFile(filepath.Join(dir, "missing"), "elan-init", filepath.Join(dir, "out")); err == nil {
		t.Fatal("a missing archive was read")
	}
	if err := checkSHA256(filepath.Join(dir, "missing"), "00"); err == nil {
		t.Fatal("a missing file passed its digest")
	}
}

// The pins are complete: every platform the gate installs on names an
// archive of ElanVersion and a SHA-256.
func TestTheElanPinsAreWhole(t *testing.T) {
	tc := DefaultLeanToolchain()
	if !strings.HasSuffix(tc.BaseURL, "/download/"+ElanVersion+"/") || tc.LookPath == nil || tc.Cache == nil || !strings.Contains(tc.Platform, "/") {
		t.Fatalf("default toolchain %+v", tc)
	}
	for _, p := range []string{"darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64"} {
		a, ok := tc.Assets[p]
		if !ok || !strings.HasPrefix(a.Name, "elan-") || !strings.HasSuffix(a.Name, ".tar.gz") || len(a.SHA256) != 64 {
			t.Errorf("%s: %+v", p, a)
		}
	}
}
