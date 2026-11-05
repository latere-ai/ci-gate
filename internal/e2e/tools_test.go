// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// helmArchive is a gzipped tar holding a helm binary under a platform
// directory, as the release ships it.
func helmArchive(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{{"linux-amd64/LICENSE", "x"}, {"linux-amd64/helm", body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// release serves a kubectl at /kubectl and a helm archive at /helm.tgz, and
// counts the requests.
func release(t *testing.T, kubectl, archive []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/linux/amd64/kubectl":
			_, _ = w.Write(kubectl)
		case "/linux/amd64/helm.tgz":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func testFetcher(t *testing.T, srv *httptest.Server, kubectl, archive []byte) *Fetcher {
	t.Helper()
	cache := t.TempDir()
	return &Fetcher{
		Pins: map[string]Pin{
			"kubectl": {Version: "v1", URL: srv.URL + "/{os}/{arch}/kubectl", SHA256: map[string]string{"linux/amd64": sum(kubectl)}},
			"helm":    {Version: "v2", URL: srv.URL + "/{os}/{arch}/helm.tgz", Archive: "helm", SHA256: map[string]string{"linux/amd64": sum(archive)}},
		},
		Cache: func() (string, error) { return cache, nil }, Platform: "linux/amd64", Client: srv.Client(), Out: io.Discard,
	}
}

// A tool is downloaded, checked, made executable and installed under the
// cache; the next ask for it makes no request.
func TestAToolIsFetchedCheckedAndCached(t *testing.T) {
	kubectl, archive := []byte("#!/bin/sh\necho kubectl\n"), helmArchive(t, "helm-binary")
	srv, hits := release(t, kubectl, archive)
	f := testFetcher(t, srv, kubectl, archive)
	dir, err := f.Dir(context.Background(), "kubectl")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "kubectl"))
	if err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("installed %v %v", fi, err)
	}
	if want := filepath.Join("lateregate", "kubectl-v1"); !strings.HasSuffix(dir, want) {
		t.Errorf("dir %q", dir)
	}
	if _, err := f.Dir(context.Background(), "kubectl"); err != nil || hits.Load() != 1 {
		t.Errorf("a second ask fetched again: %v %d", err, hits.Load())
	}
	hdir, err := f.Dir(context.Background(), "helm")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(hdir, "helm"))
	if err != nil || string(got) != "helm-binary" {
		t.Fatalf("helm %q %v", got, err)
	}
	if entries, _ := os.ReadDir(hdir); len(entries) != 1 {
		t.Errorf("the download is left behind: %v", entries)
	}
}

// Every way a fetch fails says what, and installs nothing.
func TestAToolThatCannotBeFetchedIsRefused(t *testing.T) {
	kubectl, archive := []byte("kubectl"), helmArchive(t, "helm")
	srv, _ := release(t, kubectl, archive)
	for name, tc := range map[string]struct {
		tool  string
		setup func(*Fetcher)
		want  string
	}{
		"checksum": {"kubectl", func(f *Fetcher) {
			p := f.Pins["kubectl"]
			p.SHA256 = map[string]string{"linux/amd64": strings.Repeat("0", 64)}
			f.Pins["kubectl"] = p
		}, "not the pinned"},
		"unknown tool":    {"terraform", func(*Fetcher) {}, "no tool is called"},
		"unpinned system": {"kubectl", func(f *Fetcher) { f.Platform = "plan9/arm" }, "is pinned for plan9/arm"},
		"not found": {"kubectl", func(f *Fetcher) {
			f.Pins["kubectl"] = Pin{Version: "v1", URL: srv.URL + "/missing", SHA256: f.Pins["kubectl"].SHA256}
		}, "answered 404"},
		"no server": {"kubectl", func(f *Fetcher) {
			f.Pins["kubectl"] = Pin{Version: "v1", URL: "http://127.0.0.1:1/x", SHA256: f.Pins["kubectl"].SHA256}
		}, "download"},
		"bad url": {"kubectl", func(f *Fetcher) {
			f.Pins["kubectl"] = Pin{Version: "v1", URL: "http://bad host/x", SHA256: f.Pins["kubectl"].SHA256}
		}, "invalid character"},
		"no cache":         {"kubectl", func(f *Fetcher) { f.Cache = func() (string, error) { return "", errors.New("no HOME") } }, "no cache directory"},
		"archive lacks it": {"helm", func(f *Fetcher) { p := f.Pins["helm"]; p.Archive = "tiller"; f.Pins["helm"] = p }, "holds no tiller"},
		"cache is a file": {"kubectl", func(f *Fetcher) {
			file := filepath.Join(t.TempDir(), "f")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			f.Cache = func() (string, error) { return file, nil }
		}, "not a directory"},
	} {
		t.Run(name, func(t *testing.T) {
			f := testFetcher(t, srv, kubectl, archive)
			tc.setup(f)
			_, err := f.Dir(context.Background(), tc.tool)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

// A download over the limit is refused rather than filling the disk.
func TestADownloadOverTheLimitIsRefused(t *testing.T) {
	kubectl, archive := []byte("0123456789"), helmArchive(t, "helm")
	srv, _ := release(t, kubectl, archive)
	old := maxTool
	maxTool = 4
	t.Cleanup(func() { maxTool = old })
	_, err := testFetcher(t, srv, kubectl, archive).Dir(context.Background(), "kubectl")
	if err == nil || !strings.Contains(err.Error(), "over the 4 bytes") {
		t.Fatalf("err %v", err)
	}
}

// A cancelled run does not download.
func TestACancelledRunDoesNotFetch(t *testing.T) {
	kubectl, archive := []byte("kubectl"), helmArchive(t, "helm")
	srv, hits := release(t, kubectl, archive)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := testFetcher(t, srv, kubectl, archive).Dir(ctx, "kubectl"); err == nil || hits.Load() != 0 {
		t.Fatalf("err %v, %d requests", err, hits.Load())
	}
}

// The pins in the source name every tool a suite may list, on the four
// systems lateregate runs on, with a SHA-256 each.
func TestEveryToolIsPinnedOnEverySystem(t *testing.T) {
	hexSum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, name := range config.E2ETools {
		p, ok := Pins[name]
		if !ok || p.Version == "" || !strings.Contains(p.URL, p.Version) {
			t.Errorf("%s: %+v", name, p)
		}
		for _, plat := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
			if !hexSum.MatchString(p.SHA256[plat]) {
				t.Errorf("%s on %s: %q", name, plat, p.SHA256[plat])
			}
		}
	}
	if f := NewFetcher(io.Discard); f.Platform == "" || f.Cache == nil {
		t.Errorf("%+v", f)
	}
}
