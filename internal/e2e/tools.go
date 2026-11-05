// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

// The versions a suite's tools are fetched at. kubectl is the version faber
// pins; kind is the release helm/kind-action installs by default.
const (
	KubectlVersion = "v1.37.2"
	KindVersion    = "v0.33.0"
	HelmVersion    = "v4.3.1"
)

// maxTool bounds what is read from a download and out of an archive; the
// largest of the three is about 100 MB. A variable so a test can lower it.
var maxTool int64 = 256 << 20

// Pin is one tool at one version: where each platform's release lives, and
// the SHA-256 that release must have.
type Pin struct {
	Version string
	// URL is the release's address with {os} and {arch} for the platform.
	URL string
	// Archive is the member of a gzipped tar to take, or empty when the
	// download is the binary itself.
	Archive string
	// SHA256 is the checksum of the download, by GOOS/GOARCH.
	SHA256 map[string]string
}

// Pins are the tools a suite may name, from each project's own release
// checksums.
var Pins = map[string]Pin{
	config.ToolKubectl: {
		Version: KubectlVersion,
		URL:     "https://dl.k8s.io/release/" + KubectlVersion + "/bin/{os}/{arch}/kubectl",
		SHA256: map[string]string{
			"darwin/amd64": "88379aff5c8111de6be0a394adffba2819e44cde269f3ab23cef76de1a48eaa8",
			"darwin/arm64": "55701af0d51815ee335f240e48acf0959c3c23017ed3fa816e880c4a92bae581",
			"linux/amd64":  "c04487523e7aa7d1493f7808174c899db93d7a9cf57531420cd76b9bd415f8f4",
			"linux/arm64":  "fcc16f63186659d9722014b7e02d5fdd2d4e669cb8c0ab66365d7d3b8ba03509",
		},
	},
	config.ToolKind: {
		Version: KindVersion,
		URL:     "https://github.com/kubernetes-sigs/kind/releases/download/" + KindVersion + "/kind-{os}-{arch}",
		SHA256: map[string]string{
			"darwin/amd64": "5a99f26f57246dc9319dd294803313197a0f34d33c525b3ea8b655db5916ece0",
			"darwin/arm64": "0c8c7dbe5e23594a198b786c4bc13dacc101fa6196b0cb0b23a1ca44e61f4b4f",
			"linux/amd64":  "aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d",
			"linux/arm64":  "20022bee6cfcd5086cb7234d218e3454e6090022f2a8f55d1fa7fcf42c3867a2",
		},
	},
	config.ToolHelm: {
		Version: HelmVersion,
		URL:     "https://get.helm.sh/helm-" + HelmVersion + "-{os}-{arch}.tar.gz",
		Archive: "helm",
		SHA256: map[string]string{
			"darwin/amd64": "34bec4eba761ba5c16d1965ee6b8f5b0abe5993974a802274e2a4f0418893d89",
			"darwin/arm64": "6b4e61c8cd27d99e5a801e63ff25dd5c70b36875c2cd659d3e9ce6b76fc23198",
			"linux/amd64":  "0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b",
			"linux/arm64":  "8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88",
		},
	},
}

// Fetcher installs the pinned tools under the user's cache directory, where
// the other pinned tools of lateregate live.
type Fetcher struct {
	Pins map[string]Pin
	// Cache is the directory the tools are installed under.
	Cache func() (string, error)
	// Platform is GOOS/GOARCH, which picks the download.
	Platform string
	Client   *http.Client
	Out      io.Writer
}

// NewFetcher is a fetcher of the pinned tools for this machine.
func NewFetcher(out io.Writer) *Fetcher {
	return &Fetcher{
		Pins: Pins, Cache: os.UserCacheDir, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}, Out: out,
	}
}

// Dir returns the directory holding the named tool at its pinned version,
// downloading and checking it first when it is not there. The download is
// verified against the checksum in this source before it is installed, and
// installed by a rename, so an interrupted run never leaves a partial tool
// where the next run would take it as installed.
func (f *Fetcher) Dir(ctx context.Context, name string) (string, error) {
	pin, ok := f.Pins[name]
	if !ok {
		return "", fmt.Errorf("no tool is called %q", name)
	}
	want, ok := pin.SHA256[f.Platform]
	if !ok {
		return "", fmt.Errorf("no %s %s is pinned for %s", name, pin.Version, f.Platform)
	}
	cache, err := f.Cache()
	if err != nil {
		return "", fmt.Errorf("no cache directory to install %s into: %w", name, err)
	}
	dir := filepath.Join(cache, "lateregate", name+"-"+pin.Version)
	bin := filepath.Join(dir, name)
	if _, err := os.Stat(bin); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, "download-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			_, _ = fmt.Fprintf(f.Out, "the download directory %s is left behind: %v\n", tmp, err)
		}
	}()
	osName, arch, _ := strings.Cut(f.Platform, "/")
	url := strings.NewReplacer("{os}", osName, "{arch}", arch).Replace(pin.URL)
	got := filepath.Join(tmp, "download")
	_, _ = fmt.Fprintf(f.Out, "installing %s %s into %s\n", name, pin.Version, dir)
	if err := f.download(ctx, url, got); err != nil {
		return "", err
	}
	if err := gates.VerifySHA256(got, want); err != nil {
		return "", fmt.Errorf("%s: %w", url, err)
	}
	part := got
	if pin.Archive != "" {
		part = filepath.Join(tmp, name)
		if err := gates.ExtractFile(got, pin.Archive, part, maxTool); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(part, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(part, bin); err != nil {
		return "", err
	}
	return dir, nil
}

// download writes the body at url to file, refusing one over maxTool bytes
// and any answer but 200.
func (f *Fetcher) download(ctx context.Context, url, file string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: the server answered %d", url, resp.StatusCode)
	}
	w, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxTool+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if n > maxTool {
		return fmt.Errorf("download %s: over the %d bytes the gate reads", url, maxTool)
	}
	return nil
}
