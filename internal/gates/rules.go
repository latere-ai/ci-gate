// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/goccy/go-yaml"
)

// promtoolAssets are PromtoolVersion's release archives, by GOOS/GOARCH, with
// the SHA-256 its sha256sums.txt publishes for each.
var promtoolAssets = map[string]ElanAsset{
	"darwin/arm64": {"prometheus-3.15.1.darwin-arm64.tar.gz", "0d4d3d9837d27e4fcd0bd16d115b1b8d6732a4bf3e69107f1190d9ef653ae471"},
	"darwin/amd64": {"prometheus-3.15.1.darwin-amd64.tar.gz", "c89fcb30045d8c394c9d93e96cbbee3aaf2448a48326b417e811eb4e528edf88"},
	"linux/arm64":  {"prometheus-3.15.1.linux-arm64.tar.gz", "ceb6d21493c37ca0b4513b147642797c68cf39a0f50a444d50e1e80c74f21b26"},
	"linux/amd64":  {"prometheus-3.15.1.linux-amd64.tar.gz", "6a726885328ad9efebbf36e876ce2fb6225468a71fd1e7ce3ba666d5f4d97a84"},
}

// maxPromtool bounds the promtool read out of its archive; the real binary
// is about 150 MB.
const maxPromtool = 512 << 20

// Promtool is where the rules gate gets promtool: always the pinned release,
// never a promtool on PATH, so a laptop and a runner check the same rules
// with the same parser.
type Promtool struct {
	// Cache is the directory the pinned promtool is installed under.
	Cache func() (string, error)
	// Platform is GOOS/GOARCH, which picks the archive.
	Platform string
	// Assets are the archives by platform, and BaseURL the address their
	// names follow.
	Assets  map[string]ElanAsset
	BaseURL string
}

// DefaultPromtool is PromtoolVersion under the user's cache directory.
func DefaultPromtool() Promtool {
	return Promtool{
		Cache:    os.UserCacheDir,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Assets:   promtoolAssets,
		BaseURL:  "https://github.com/prometheus/prometheus/releases/download/" + PromtoolVersion + "/",
	}
}

// RuleGroups is one PrometheusRule document: the file it is in, its
// position among that file's documents, and its spec, which is the rules
// file promtool reads.
type RuleGroups struct {
	File  string
	Index int
	Spec  []byte
}

// PrometheusRules finds every PrometheusRule document in the YAML files git
// tracks, outside testdata. read gives a tracked file's contents. A file
// that names PrometheusRule and does not parse is an error, not a skip: the
// rules in it would otherwise go unchecked.
func PrometheusRules(run Exec, read func(string) ([]byte, error)) ([]RuleGroups, error) {
	files, err := trackedFiles(run, "YAML files", []string{"testdata"}, "*.yaml", "*.yml")
	if err != nil {
		return nil, err
	}
	var found []RuleGroups
	for _, f := range files {
		if ext := path.Ext(f); ext != ".yaml" && ext != ".yml" {
			continue
		}
		body, err := read(f)
		if errors.Is(err, fs.ErrNotExist) {
			// Tracked and deleted in the working tree: no rules to check.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		if !bytes.Contains(body, []byte("PrometheusRule")) {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(body))
		for i := 0; ; i++ {
			var doc any
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("%s names PrometheusRule and does not parse as YAML: %w", f, err)
			}
			obj, ok := doc.(map[string]any)
			if !ok || obj["kind"] != "PrometheusRule" {
				continue
			}
			if _, ok := obj["spec"].(map[string]any); !ok {
				return nil, fmt.Errorf("%s document %d is a PrometheusRule with no spec to check", f, i+1)
			}
			spec, err := yaml.Marshal(obj["spec"])
			if err != nil {
				return nil, fmt.Errorf("%s document %d: %w", f, i+1, err)
			}
			found = append(found, RuleGroups{File: f, Index: i + 1, Spec: spec})
		}
	}
	return found, nil
}

// Rules checks every PrometheusRule's groups with `promtool check rules` at
// PromtoolVersion: the expressions parse, the durations and labels are well
// formed, and no group repeats a name. A rule promtool rejects is a rule
// Prometheus refuses to load, which nobody notices until the alert it was
// meant to fire does not.
func Rules(tc Promtool, root string, out io.Writer, run Exec) error {
	groups, err := PrometheusRules(run, func(f string) ([]byte, error) { return os.ReadFile(filepath.Join(root, f)) })
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return errors.New("git tracks no PrometheusRule to check")
	}
	promtool, unlock, err := tc.binary(out, run)
	if err != nil {
		return err
	}
	defer unlock()
	tmp, err := os.MkdirTemp("", "lateregate-rules-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	args := []string{"check", "rules"}
	for _, g := range groups {
		name := fmt.Sprintf("%s.%d.yaml", strings.NewReplacer("/", "_", ".", "_").Replace(g.File), g.Index)
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, g.Spec, 0o600); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s: PrometheusRule %d is %s\n", g.File, g.Index, name)
		args = append(args, p)
	}
	if _, err := run(nil, true, promtool, args...); err != nil {
		return fmt.Errorf("promtool %s rejects the rules above: %w", PromtoolVersion, err)
	}
	_, _ = fmt.Fprintf(out, "%d PrometheusRule(s) pass promtool\n", len(groups))
	return nil
}

// binary returns the pinned promtool, installing it under the cache
// directory first when it is not there, and a release for the lock held
// while it installs.
func (tc Promtool) binary(out io.Writer, run Exec) (string, func(), error) {
	asset, ok := tc.Assets[tc.Platform]
	if !ok {
		return "", nil, fmt.Errorf("no promtool %s archive is pinned for %s", PromtoolVersion, tc.Platform)
	}
	cache, err := tc.Cache()
	if err != nil {
		return "", nil, fmt.Errorf("no cache directory to install promtool into: %w", err)
	}
	dir := filepath.Join(cache, "lateregate", "promtool-"+PromtoolVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	unlock, err := lockFile(dir+".lock", "promtool install on this machine", out)
	if err != nil {
		return "", nil, err
	}
	bin := filepath.Join(dir, "promtool")
	if _, err := os.Stat(bin); err == nil {
		return bin, unlock, nil
	}
	tmp, err := os.MkdirTemp(dir, "download-")
	if err != nil {
		unlock()
		return "", nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	url := tc.BaseURL + asset.Name
	archive := filepath.Join(tmp, asset.Name)
	_, _ = fmt.Fprintf(out, "installing promtool %s into %s\n", PromtoolVersion, dir)
	if _, err := run(nil, false, "curl", "-fsSL", "--retry", "3", "-o", archive, url); err != nil {
		unlock()
		return "", nil, fmt.Errorf("download %s: %w", url, err)
	}
	if err := checkSHA256(archive, asset.SHA256); err != nil {
		unlock()
		return "", nil, err
	}
	// Into the download directory first, then renamed, so an interrupted
	// extraction never leaves a partial promtool where the next run would
	// take it as installed.
	part := filepath.Join(tmp, "promtool")
	if err := extractFile(archive, "promtool", part, maxPromtool); err != nil {
		unlock()
		return "", nil, err
	}
	if err := os.Rename(part, bin); err != nil {
		unlock()
		return "", nil, err
	}
	return bin, unlock, nil
}
