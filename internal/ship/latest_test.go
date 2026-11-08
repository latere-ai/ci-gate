// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushedTags lists the image names the build was asked to carry.
func pushedTags(t *testing.T, r *rig) string {
	t.Helper()
	b, ok := r.find("podman build")
	if !ok {
		t.Fatalf("no build: %v", r.calls)
	}
	return strings.Join(b.args, " ")
}

func TestPushLeavesLatestWhereItIsForAnOlderTag(t *testing.T) {
	r := newRig(t, "")
	r.tags = []string{"v1.2.0"}
	r.remote = "aaa\trefs/tags/v1.2.0\nbbb\trefs/tags/v1.4.0\n"
	if err := Push(r.d); err != nil {
		t.Fatal(err)
	}
	line := pushedTags(t, r)
	if strings.Contains(line, ":latest") {
		t.Errorf("an older tag moved latest: %s", line)
	}
	if !strings.Contains(line, "-t ghcr.io/example/auth:v1.2.0") {
		t.Errorf("the version tag is not published: %s", line)
	}
	for _, want := range []string{"latest stays where it is", "v1.4.0"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
}

func TestPushMovesLatestForTheNewestTagOnOrigin(t *testing.T) {
	r := newRig(t, "")
	r.tags = []string{"v1.4.0"}
	r.remote = "aaa\trefs/tags/v1.2.0\nbbb\trefs/tags/v1.4.0\n"
	if err := Push(r.d); err != nil {
		t.Fatal(err)
	}
	if line := pushedTags(t, r); !strings.Contains(line, "-t ghcr.io/example/auth:latest") {
		t.Errorf("the newest tag did not move latest: %s", line)
	}
}

func TestPushStopsWhenOriginsTagsCannotBeRead(t *testing.T) {
	r := newRig(t, "")
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "git" && c.args[0] == "ls-remote" {
			return nil, errors.New("exit 128"), true
		}
		return nil, nil, false
	}
	err := Push(r.d)
	if err == nil || !strings.Contains(err.Error(), "listing origin's tags") {
		t.Fatalf("error: %v", err)
	}
	if _, ok := r.find("podman build"); ok {
		t.Error("an image was built without knowing whether latest may move")
	}
}

func TestPushLeavesLatestAloneForATagOriginDoesNotHave(t *testing.T) {
	r := newRig(t, "")
	r.tags = []string{"v1.5.0"}
	r.remote = "bbb\trefs/tags/v1.4.0\n"
	if err := Push(r.d); err != nil {
		t.Fatal(err)
	}
	if line := pushedTags(t, r); strings.Contains(line, ":latest") {
		t.Errorf("a tag newer than origin's moved latest: %s", line)
	}
}

func TestExtractRefusesAnEntryUnderASymlinkItCreated(t *testing.T) {
	outside := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range []tar.Header{
		{Name: "deploy", Typeflag: tar.TypeSymlink, Linkname: outside, Mode: 0o777},
		{Name: "deploy/pwned.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2},
	} {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tw.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "tree")
	err := extract(buf.Bytes(), dest)
	if _, statErr := os.Stat(filepath.Join(outside, "pwned.txt")); statErr == nil {
		t.Fatalf("extract (err=%v) wrote %s/pwned.txt, outside the tree %s", err, outside, dest)
	}
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the archive was not refused for writing through a symlink: %v", err)
	}
}
