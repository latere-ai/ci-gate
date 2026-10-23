// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ElanVersion pins elan, the installer that fetches the Lean toolchain a Lake
// project's lean-toolchain file names. One version, in one place, as
// VulnVersion is.
const ElanVersion = "v4.2.4"

// ElanAsset is one platform's elan release archive and the SHA-256 its
// release publishes for it.
type ElanAsset struct {
	Name   string
	SHA256 string
}

// elanAssets are ElanVersion's archives, by GOOS/GOARCH.
var elanAssets = map[string]ElanAsset{
	"darwin/arm64": {"elan-aarch64-apple-darwin.tar.gz", "7ad829861392c718dfebde3a83b5c8508df47be02af68894b094b0b3952616e5"},
	"darwin/amd64": {"elan-x86_64-apple-darwin.tar.gz", "8a340b309d8ed2e96f930761fa223b3af57a38f5d253b53ac90293c9516f8cd4"},
	"linux/arm64":  {"elan-aarch64-unknown-linux-gnu.tar.gz", "05febd124d84ebf994b2e7479922a5650b1e950c17ae3bd1ddd776b65bb72bf9"},
	"linux/amd64":  {"elan-x86_64-unknown-linux-gnu.tar.gz", "42b94d4244e8353142c456ec0e4ca6528fd898a6c604d4059f494e706e431f63"},
}

// lakefiles are the file names that make a directory a Lake project.
var lakefiles = []string{"lakefile.toml", "lakefile.lean"}

// maxElanInit bounds the elan-init read out of its archive, so a corrupt or
// hostile archive cannot fill the disk; the real binary is about 6 MB.
const maxElanInit = 64 << 20

// LeanToolchain is where the lean gate finds lake.
type LeanToolchain struct {
	// LookPath finds a lake already on PATH, which the gate prefers.
	LookPath func(string) (string, error)
	// Cache is the directory an elan the gate installs lives under.
	Cache func() (string, error)
	// Platform is GOOS/GOARCH, which picks the elan archive.
	Platform string
	// Assets are the elan archives by platform, and BaseURL the address
	// their names follow.
	Assets  map[string]ElanAsset
	BaseURL string
}

// DefaultLeanToolchain is the machine's lake, or ElanVersion installed under
// the user's cache directory.
func DefaultLeanToolchain() LeanToolchain {
	return LeanToolchain{
		LookPath: exec.LookPath,
		Cache:    os.UserCacheDir,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Assets:   elanAssets,
		BaseURL:  "https://github.com/leanprover/elan/releases/download/" + ElanVersion + "/",
	}
}

// LakeProjects lists, sorted, the directories of the Lake projects git
// tracks: each one holding a lakefile.toml or a lakefile.lean, outside the
// .lake directory where Lake keeps the packages it fetched.
func LakeProjects(run Exec) ([]string, error) {
	args := []string{"ls-files", "--"}
	for _, f := range lakefiles {
		args = append(args, f, "*/"+f)
	}
	out, err := run(nil, false, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("cannot list Lake projects with git: %w", err)
	}
	var dirs []string
	for line := range strings.SplitSeq(string(out), "\n") {
		p := strings.TrimSpace(line)
		if !slices.Contains(lakefiles, path.Base(p)) || slices.Contains(strings.Split(p, "/"), ".lake") {
			continue
		}
		if d := path.Dir(p); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	slices.Sort(dirs)
	return dirs, nil
}

// Lean builds every Lake project git tracks with `lake build --wfail`, which
// fails on any warning a module logs, a replayed one included. A proof that
// uses sorry is such a warning, so a project passes only when every theorem
// in it is proved.
//
// lake runs in the project's own directory, through in: elan picks the
// toolchain from the lean-toolchain file of the directory it runs in, so a
// build started from the repository root would run whatever toolchain is the
// machine's default, or none.
func Lean(tc LeanToolchain, out io.Writer, run Exec, in func(dir string) Exec) error {
	dirs, err := LakeProjects(run)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		return errors.New("git tracks no Lake project to build")
	}
	lake, env, unlock, err := tc.lake(out, run)
	if err != nil {
		return err
	}
	defer unlock()
	var failed []string
	for _, d := range dirs {
		_, _ = fmt.Fprintf(out, "lake build --wfail in %s\n", d)
		if _, err := in(d)(env, true, lake, "build", "--wfail"); err != nil {
			failed = append(failed, d)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("lake build --wfail failed in %d of %d Lake projects: %s; "+
			"it fails on an error and on any warning, and a proof that uses sorry is a warning, as is a deprecated name",
			len(failed), len(dirs), strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(out, "%d Lake project(s) build with no warning\n", len(dirs))
	return nil
}

// lake returns the lake to run and the environment to run it in, and a
// release for the lock it holds. A lake on PATH is used as it is. Otherwise
// the gate installs ElanVersion under the cache directory and holds a lock on
// it for the whole build, since elan installs the toolchains a project names
// into that directory as lake first runs, and two runs on one machine would
// write the same files.
func (tc LeanToolchain) lake(out io.Writer, run Exec) (string, []string, func(), error) {
	if p, err := tc.LookPath("lake"); err == nil {
		_, _ = fmt.Fprintf(out, "building with %s\n", p)
		return p, nil, func() {}, nil
	}
	asset, ok := tc.Assets[tc.Platform]
	if !ok {
		return "", nil, nil, fmt.Errorf("no lake on PATH, and no elan %s archive is pinned for %s; "+
			"install elan from https://github.com/leanprover/elan and put lake on PATH", ElanVersion, tc.Platform)
	}
	cache, err := tc.Cache()
	if err != nil {
		return "", nil, nil, fmt.Errorf("no lake on PATH, and no cache directory to install elan into: %w", err)
	}
	home := filepath.Join(cache, "lateregate", "elan-"+ElanVersion)
	if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
		return "", nil, nil, err
	}
	unlock, err := lockFile(home+".lock", "Lean build on this machine", out)
	if err != nil {
		return "", nil, nil, err
	}
	env := append(withoutKey(os.Environ(), "ELAN_HOME"), "ELAN_HOME="+home)
	lake := filepath.Join(home, "bin", "lake")
	if _, err := os.Stat(lake); err != nil {
		if err := tc.install(home, asset, env, out, run); err != nil {
			unlock()
			return "", nil, nil, err
		}
	}
	return lake, env, unlock, nil
}

// install downloads the elan archive, checks it against its pinned digest,
// and runs the elan-init in it with ELAN_HOME set to home. It installs no
// toolchain: lake fetches the one each project names.
func (tc LeanToolchain) install(home string, asset ElanAsset, env []string, out io.Writer, run Exec) error {
	tmp, err := os.MkdirTemp(filepath.Dir(home), "elan-download-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	url := tc.BaseURL + asset.Name
	archive := filepath.Join(tmp, asset.Name)
	_, _ = fmt.Fprintf(out, "no lake on PATH; installing elan %s into %s\n", ElanVersion, home)
	if _, err := run(nil, false, "curl", "-fsSL", "--retry", "3", "-o", archive, url); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := checkSHA256(archive, asset.SHA256); err != nil {
		return err
	}
	init := filepath.Join(tmp, "elan-init")
	if err := extractFile(archive, "elan-init", init); err != nil {
		return err
	}
	if _, err := run(env, true, init, "-y", "--no-modify-path", "--default-toolchain", "none"); err != nil {
		return fmt.Errorf("elan-init: %w", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bin", "lake")); err != nil {
		return fmt.Errorf("elan-init installed no lake under %s: %w", home, err)
	}
	return nil
}

// checkSHA256 refuses a file whose SHA-256 is not want.
func checkSHA256(file, want string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s has SHA-256 %s, not the pinned %s", filepath.Base(file), got, want)
	}
	return nil
}

// extractFile writes the regular file named name in a gzipped tar archive to
// dst, executable.
func extractFile(archive, name, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(archive), err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s holds no %s", filepath.Base(archive), name)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(archive), err)
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != name {
			continue
		}
		if h.Size > maxElanInit {
			return fmt.Errorf("%s in %s is %d bytes, over the %d the gate reads", name, filepath.Base(archive), h.Size, maxElanInit)
		}
		w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, io.LimitReader(tr, maxElanInit)); err != nil {
			_ = w.Close()
			return err
		}
		return w.Close()
	}
}
