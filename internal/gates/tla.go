// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// TLAVersion pins TLC, the model checker in tla2tools.jar. One version, in
// one place, as ElanVersion is.
const TLAVersion = "v1.7.4"

// TLASHA256 is the SHA-256 of the tla2tools.jar that TLAVersion's release
// publishes (2274532 bytes).
const TLASHA256 = "936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88"

// tlaTail is how many lines of TLC's output a failure prints, enough to show
// the counterexample's last states.
const tlaTail = 30

// TLAToolchain is where the tla gate finds a Java runtime and tla2tools.jar.
type TLAToolchain struct {
	// Getenv reads JAVA_HOME.
	Getenv func(string) string
	// LookPath finds a java on PATH.
	LookPath func(string) (string, error)
	// Exists reports whether a file is there.
	Exists func(string) bool
	// Fallbacks are the java binaries tried last, where Homebrew puts a
	// keg-only openjdk.
	Fallbacks []string
	// Cache is the directory the jar the gate downloads lives under.
	Cache func() (string, error)
	// URL and SHA256 name the jar and its digest.
	URL, SHA256 string
}

// DefaultTLAToolchain is the machine's Java, and TLAVersion's jar under the
// user's cache directory.
func DefaultTLAToolchain() TLAToolchain {
	return TLAToolchain{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Exists: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && !st.IsDir()
		},
		Fallbacks: []string{"/opt/homebrew/opt/openjdk/bin/java", "/usr/local/opt/openjdk/bin/java"},
		Cache:     os.UserCacheDir,
		URL:       "https://github.com/tlaplus/tlaplus/releases/download/" + TLAVersion + "/tla2tools.jar",
		SHA256:    TLASHA256,
	}
}

// TLAModel is one model TLC checks: a .cfg file and the specification it
// configures, in the same directory.
type TLAModel struct {
	Dir  string
	Cfg  string
	Spec string
}

// Path is the cfg's path from the repository root.
func (m TLAModel) Path() string { return path.Join(m.Dir, m.Cfg) }

// TLAModels lists, sorted by path, the models git tracks. A model is a .cfg
// file whose directory also holds the specification it configures: X.cfg
// checks X.tla, and so does X.label.cfg. A .cfg with no tracked .tla beside
// it is some other tool's file, and is not a model. Anything under a .lake
// directory is a package a Lake project fetched.
func TLAModels(run Exec) ([]TLAModel, error) {
	out, err := run(nil, false, "git", "ls-files", "-z", "--", "*.cfg", "*.tla")
	if err != nil {
		return nil, fmt.Errorf("cannot list TLA+ models with git: %w", err)
	}
	files := nulSeparated(string(out))
	specs := map[string]bool{}
	for _, p := range files {
		if strings.HasSuffix(p, ".tla") && !slices.Contains(strings.Split(p, "/"), ".lake") {
			specs[p] = true
		}
	}
	var models []TLAModel
	for _, p := range files {
		stem, ok := strings.CutSuffix(p, ".cfg")
		if !ok || slices.Contains(strings.Split(p, "/"), ".lake") {
			continue
		}
		// X.label.cfg falls back from X.label.tla to X.tla.
		for s := stem; ; {
			if specs[s+".tla"] {
				models = append(models, TLAModel{path.Dir(p), path.Base(p), path.Base(s) + ".tla"})
				break
			}
			i := strings.LastIndex(s, ".")
			if i < 0 || i < strings.LastIndex(s, "/") {
				break
			}
			s = s[:i]
		}
	}
	slices.SortFunc(models, func(a, b TLAModel) int { return strings.Compare(a.Path(), b.Path()) })
	return models, nil
}

var expectViolation = regexp.MustCompile(`^\\\* expect violation: ([A-Za-z_][A-Za-z0-9_]*)$`)

// expectedViolation is the name a cfg's `\* expect violation: Name` line
// gives, or "" when it has none.
func expectedViolation(cfg string) string {
	for l := range strings.SplitSeq(cfg, "\n") {
		if m := expectViolation.FindStringSubmatch(strings.TrimSuffix(l, "\r")); m != nil {
			return m[1]
		}
	}
	return ""
}

// reportsViolation reports whether TLC's output says name is violated, as an
// invariant or as an action property.
func reportsViolation(output, name string) bool {
	for l := range strings.SplitSeq(output, "\n") {
		l = strings.TrimSpace(l)
		if l == "Error: Invariant "+name+" is violated." || l == "Error: Action property "+name+" is violated." {
			return true
		}
	}
	return false
}

// tail is the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// TLA model-checks every TLA+ model git tracks with TLC, one after another,
// each in its own directory with a fresh metadir that is removed afterwards.
// A model passes when TLC exits 0. One whose cfg carries the line
// `\* expect violation: Name` exists to show a violation, and passes only when
// TLC exits non-zero and reports that invariant or action property violated:
// a model that no longer finds it has stopped showing what it was written to
// show.
func TLA(tc TLAToolchain, root string, out io.Writer, run Exec, in func(dir string) Exec) error {
	models, err := TLAModels(run)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return errors.New("git tracks no TLA+ model to check")
	}
	java, err := tc.java(run)
	if err != nil {
		return err
	}
	jar, unlock, err := tc.jar(out, run)
	if err != nil {
		return err
	}
	defer unlock()
	_, _ = fmt.Fprintf(out, "checking with %s\n", java)
	var failed []string
	for _, m := range models {
		if err := checkModel(java, jar, root, m, out, in); err != nil {
			_, _ = fmt.Fprintf(out, "%s: %v\n", m.Path(), err)
			failed = append(failed, m.Path())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("TLC failed on %d of %d TLA+ models: %s", len(failed), len(models), strings.Join(failed, ", "))
	}
	_, _ = fmt.Fprintf(out, "%d TLA+ model(s) check\n", len(models))
	return nil
}

// checkModel runs TLC on one model and prints its one-line result. The error
// it returns says what failed and carries the tail of TLC's output.
func checkModel(java, jar, root string, m TLAModel, out io.Writer, in func(dir string) Exec) error {
	cfg, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(m.Path())))
	if err != nil {
		return err
	}
	meta, err := os.MkdirTemp("", "tlc-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(meta) }()
	_, _ = fmt.Fprintf(out, "tlc %s\n", m.Path())
	output, runErr := in(m.Dir)(nil, false, java, "-XX:+UseParallelGC", "-cp", jar, "tlc2.TLC",
		"-workers", "auto", "-cleanup", "-metadir", meta, "-config", m.Cfg, m.Spec)
	want := expectedViolation(string(cfg))
	switch {
	case want == "" && runErr == nil:
		_, _ = fmt.Fprintf(out, "%s: ok\n", m.Path())
		return nil
	case want != "" && runErr != nil && reportsViolation(string(output), want):
		_, _ = fmt.Fprintf(out, "%s: ok, finds the expected violation of %s\n", m.Path(), want)
		return nil
	case want != "" && runErr == nil:
		return fmt.Errorf("the model no longer finds the violation of %s it exists to show: TLC passed it\n%s", want, tail(string(output), tlaTail))
	case want != "":
		return fmt.Errorf("the model no longer finds the violation of %s it exists to show: TLC failed with %w and no such violation\n%s", want, runErr, tail(string(output), tlaTail))
	}
	return fmt.Errorf("TLC failed: %w\n%s", runErr, tail(string(output), tlaTail))
}

// java finds a Java runtime: JAVA_HOME's, then a java on PATH that runs (the
// /usr/bin/java a Mac ships without a JDK is a stub that exits non-zero), then
// the Homebrew openjdk. The gate downloads none.
func (tc TLAToolchain) java(run Exec) (string, error) {
	if home := tc.Getenv("JAVA_HOME"); home != "" {
		if p := filepath.Join(home, "bin", "java"); tc.Exists(p) {
			return p, nil
		}
	}
	if p, err := tc.LookPath("java"); err == nil {
		if _, err := run(nil, false, p, "-version"); err == nil {
			return p, nil
		}
	}
	for _, p := range tc.Fallbacks {
		if tc.Exists(p) {
			return p, nil
		}
	}
	return "", errors.New("TLC needs a Java runtime and none was found: set JAVA_HOME, " +
		"or on macOS run `brew install openjdk`; the gate does not download one")
}

// jar returns the tla2tools.jar to run and a release for the lock it holds.
// The gate downloads TLAVersion's jar into the cache directory, beside the
// elan the lean gate installs, checks it against its pinned digest before it
// is used, and reuses it on later runs. The lock keeps two runs on one machine
// from writing the file at once.
func (tc TLAToolchain) jar(out io.Writer, run Exec) (string, func(), error) {
	cache, err := tc.Cache()
	if err != nil {
		return "", nil, fmt.Errorf("no cache directory to download tla2tools.jar into: %w", err)
	}
	dir := filepath.Join(cache, "lateregate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	jar := filepath.Join(dir, "tla2tools-"+TLAVersion+".jar")
	unlock, err := lockFile(jar+".lock", "TLC download on this machine", out)
	if err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(jar); err == nil {
		return jar, unlock, nil
	}
	if err := tc.download(dir, jar, out, run); err != nil {
		unlock()
		return "", nil, err
	}
	return jar, unlock, nil
}

// download fetches the jar to a temporary file beside jar, checks its digest,
// and only then moves it into place.
func (tc TLAToolchain) download(dir, jar string, out io.Writer, run Exec) error {
	tmp, err := os.MkdirTemp(dir, "tla-download-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	file := filepath.Join(tmp, "tla2tools.jar")
	_, _ = fmt.Fprintf(out, "downloading tla2tools.jar %s into %s\n", TLAVersion, dir)
	if _, err := run(nil, false, "curl", "-fsSL", "--retry", "3", "-o", file, tc.URL); err != nil {
		return fmt.Errorf("download %s: %w", tc.URL, err)
	}
	if err := checkSHA256(file, tc.SHA256); err != nil {
		return err
	}
	return os.Rename(file, jar)
}
