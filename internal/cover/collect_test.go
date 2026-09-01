// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The one instrumented command every repository ran four ways.
func TestCollectRunsTheInstrumentedSuite(t *testing.T) {
	var ran string
	var streamed bool
	exec := func(_ []string, stream bool, name string, args ...string) ([]byte, error) {
		ran = name + " " + strings.Join(args, " ")
		streamed = stream
		return nil, nil
	}
	p, err := Collect("go", "/repo", &strings.Builder{}, exec)
	if err != nil {
		t.Fatal(err)
	}
	want := "go test ./... -covermode=atomic -coverpkg=./... -coverprofile=" + Profile
	if ran != want {
		t.Errorf("ran %q, want %q", ran, want)
	}
	if !streamed {
		t.Error("the suite's output must reach the terminal")
	}
	if p != filepath.Join("/repo", Profile) {
		t.Errorf("profile path = %q", p)
	}
}

func TestCollectReportsAFailingSuite(t *testing.T) {
	exec := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("exit 1")
	}
	if _, err := Collect("go", ".", &strings.Builder{}, exec); err == nil || !strings.Contains(err.Error(), "instrumented test run failed") {
		t.Fatalf("got %v", err)
	}
}
