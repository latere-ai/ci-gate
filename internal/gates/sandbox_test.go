// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// A package that leaks passes its test, so the go command caches its result
// and the next run replays it without making the directory. The leak has to
// be reported on every run over the same tree, not once.
func TestTempDirReportsALeakOnEveryRunOverTheSameTree(t *testing.T) {
	if !lockable {
		t.Skip("without a file lock every run makes a directory of its own")
	}
	root := isolated(t)
	t.Setenv("GOCACHE", t.TempDir())
	writeFile(t, root, "go.mod", "module example.com/leak\n\ngo 1.21\n")
	writeFile(t, root, "leak_test.go", "package leak\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestLeak(t *testing.T) {\n\tif _, err := os.MkdirTemp(\"\", \"leaked-\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n")
	run := OSExec(root, io.Discard)
	var first, second strings.Builder
	if err := TempDir(config.TempDir{}, root, nil, &first, run); err == nil {
		t.Fatal("the first run did not report the leak")
	}
	if strings.Contains(first.String(), "-count=1") {
		t.Errorf("a run with no earlier leak caches as usual:\n%s", first.String())
	}
	if err := TempDir(config.TempDir{}, root, nil, &second, run); err == nil {
		t.Fatal("the second run over the same leaking tree passed: the cached result replayed without making the directory")
	}
	if !strings.Contains(second.String(), "-count=1") {
		t.Errorf("the run must say why the cache is off:\n%s", second.String())
	}
}

// markerRoot is a repository root whose marker path is known.
func markerRoot(t *testing.T) (root, marker string) {
	t.Helper()
	root = isolated(t)
	marker, err := leakMarker(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, marker
}

func TestTempDirTurnsTheCacheOffAfterALeakAndBackOnAfterACleanRun(t *testing.T) {
	if !lockable {
		t.Skip("without a file lock every run makes a directory of its own")
	}
	root, marker := markerRoot(t)
	var sb strings.Builder
	argvOf := func(do func(string)) string {
		var calls []call
		sb.Reset()
		_ = TempDir(config.TempDir{}, root, nil, &sb, suite(t, &calls, do, nil))
		return strings.Join(calls[0].args, " ")
	}
	if got := argvOf(func(d string) { leak(t, d, "left", 8) }); got != "test ./..." {
		t.Errorf("first run: go %s", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a leak must leave a marker: %v", err)
	}
	if got := argvOf(func(d string) { touch(t, d) }); got != "test -count=1 ./..." {
		t.Errorf("run after a leak: go %s", got)
	}
	if !strings.Contains(sb.String(), "-count=1") {
		t.Errorf("the run must say why the cache is off:\n%s", sb.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("a run that leaves nothing removes the marker: %v", err)
	}
	if got := argvOf(func(d string) { touch(t, d) }); got != "test ./..." {
		t.Errorf("run after a clean one: go %s", got)
	}
}

func TestTempDirSaysWhenItCannotTurnTheCacheOffForACustomCommand(t *testing.T) {
	if !lockable {
		t.Skip("without a file lock every run makes a directory of its own")
	}
	root, marker := markerRoot(t)
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []call
	var sb strings.Builder
	argv := []string{"make", "test"}
	if err := TempDir(config.TempDir{}, root, argv, &sb, suite(t, &calls, func(d string) { touch(t, d) }, nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls[0].args, " ") != "test" || !strings.Contains(sb.String(), "cannot bypass its cache") {
		t.Errorf("args %v\n%s", calls[0].args, sb.String())
	}
}

func TestSuiteTurnsTheCacheOffAfterASandboxLeak(t *testing.T) {
	if !lockable {
		t.Skip("without a file lock every run makes a directory of its own")
	}
	root, marker := markerRoot(t)
	opt := SuiteRun{Sandbox: true, Profile: "c.out"}
	s := &suiteRun{do: func(env []string) {
		dir, _ := envValue(env, "TMPDIR")
		leak(t, dir, "left", 8)
	}}
	if err := Suite(opt, root, suiteGo, io.Discard, s.exec); err == nil {
		t.Fatal("the leak must fail the suite")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a leak must leave a marker: %v", err)
	}
	var sb strings.Builder
	s = &suiteRun{do: cleanSuite(t)}
	if err := Suite(opt, root, suiteGo, &sb, s.exec); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.testCall(t).args, " "); got != "test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=c.out ./..." {
		t.Errorf("go %s", got)
	}
	if !strings.Contains(sb.String(), "-count=1") {
		t.Errorf("the run must say why the cache is off:\n%s", sb.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("a clean run removes the marker: %v", err)
	}
	s = &suiteRun{do: cleanSuite(t)}
	if err := Suite(opt, root, suiteGo, io.Discard, s.exec); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(s.testCall(t).args, " "), "-count=1") {
		t.Error("a run after a clean one caches as usual")
	}
}

func TestSuiteTurnsTheCacheOffAfterALeftContainer(t *testing.T) {
	if !lockable {
		t.Skip("without a file lock every run makes a directory of its own")
	}
	root, marker := markerRoot(t)
	e := &engine{}
	withEngine(t, e)
	opt := SuiteRun{Containers: true}
	s := &suiteRun{do: startsContainers(e, 1, 0)}
	if err := Suite(opt, root, suiteGo, io.Discard, s.exec); err == nil {
		t.Fatal("the left container must fail the suite")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a left container must leave a marker: %v", err)
	}
	s = &suiteRun{}
	if err := Suite(opt, root, suiteGo, io.Discard, s.exec); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.testCall(t).args, " "); got != "test -count=1 ./..." {
		t.Errorf("go %s", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("a clean run removes the marker: %v", err)
	}
}

func TestSettleMarkerReportsAMarkerItCannotWrite(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "dir", "m")
	if f := settleMarker(missing, true, io.Discard); !strings.Contains(f, missing) {
		t.Errorf("failure %q does not name the marker", f)
	}
	var sb strings.Builder
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "m"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Getuid() != 0 {
		settleMarker(filepath.Join(dir, "m"), false, &sb)
		if !strings.Contains(sb.String(), "removing") {
			t.Errorf("a marker that cannot be removed is said: %q", sb.String())
		}
	}
	if f := settleMarker("", true, io.Discard); f != "" {
		t.Errorf("no marker path, nothing to settle: %q", f)
	}
}

// The gate removes its own sandbox on every path. A leaked tree that holds a
// read-only directory is made writable and removed, so the next run of the
// repository does not fail on a stale sandbox.
func TestTempDirRemovesAReadOnlyTreeALeakLeft(t *testing.T) {
	if !lockable {
		t.Skip("no shared sandbox here")
	}
	root := isolated(t)
	var calls []call
	do := func(sandbox string) {
		ro := filepath.Join(sandbox, "mod")
		if err := os.MkdirAll(ro, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ro, "f.go"), []byte("x"), 0o400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ro, 0o500); err != nil { // a module cache without -modcacherw
			t.Fatal(err)
		}
	}
	err := TempDir(config.TempDir{}, root, nil, io.Discard, suite(t, &calls, do, nil))
	if err == nil {
		t.Fatal("the leak must fail the gate")
	}
	sandbox := tempDirOf(t, calls[0].env)
	t.Cleanup(func() {
		_ = filepath.WalkDir(sandbox, func(p string, d os.DirEntry, _ error) error {
			if d != nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	if _, serr := os.Stat(sandbox); serr == nil {
		t.Fatalf("%s outlived the gate", sandbox)
	}
	// and the next run of the repository
	var again []call
	if err := TempDir(config.TempDir{}, root, nil, io.Discard, suite(t, &again, func(d string) { touch(t, d) }, nil)); err != nil {
		t.Fatalf("the next run: %v", err)
	}
}

// Pins that the sandbox's removal never follows a link. A link at
// the sandbox path, and a link left inside the sandbox, only lose the link.
func TestTempDirNeverFollowsALink(t *testing.T) {
	if !lockable {
		t.Skip("no shared sandbox here")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	root := filepath.Join(t.TempDir(), "repo")
	victim := t.TempDir()
	keep := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := sandboxName(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(tmp, name)); err != nil {
		t.Fatal(err)
	}
	var calls []call
	do := func(sandbox string) {
		touch(t, sandbox)
		if err := os.Symlink(victim, filepath.Join(sandbox, "link")); err != nil {
			t.Fatal(err)
		}
	}
	_ = TempDir(config.TempDir{}, root, nil, io.Discard, suite(t, &calls, do, nil))
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the gate removed a file outside its sandbox: %v", err)
	}
}

// A tree that cannot be made removable is reported with its path and what to
// do, not dropped.
func TestRemoveTreeNamesWhatItCannotRemove(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root removes anything")
	}
	parent := filepath.Join(t.TempDir(), "locked")
	dir := filepath.Join(parent, "sandbox")
	leak(t, dir, "x", 1)
	if err := os.Chmod(parent, 0o500); err != nil { // dir itself cannot be unlinked
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	err := removeTree(dir)
	if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "chmod -R u+rwx") {
		t.Fatalf("want an error naming %s and what to do, got %v", dir, err)
	}
	var sb strings.Builder
	removeSandbox(dir, &sb)
	if !strings.Contains(sb.String(), dir) {
		t.Errorf("a release that cannot remove the sandbox says so:\n%s", sb.String())
	}
}
