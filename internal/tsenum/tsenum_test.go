// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package tsenum

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "consumer's repo")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	project := Project{Project: "frontend/tsconfig.json", Types: []string{"status.ts#Status"},
		Fields:  map[string]string{"status.ts#Session.status": "status.ts#Status"},
		Parsers: map[string]string{"status.ts#parse": "validated network input"}}
	var output bytes.Buffer
	calls := 0
	err := Run([]Project{project}, root, &output, func(env []string, stream bool, name string, args ...string) ([]byte, error) {
		calls++
		if env != nil || !stream || name != "node" || len(args) != 6 || args[0] != "--input-type=module" || args[1] != "--eval" || args[2] != analyzer || args[3] != "--" || args[4] != "--enum-input" {
			t.Fatalf("unexpected command: env=%v stream=%v name=%s args=%d", env, stream, name, len(args))
		}
		var input struct {
			Root     string    `json:"root"`
			Projects []Project `json:"projects"`
		}
		if err := json.Unmarshal([]byte(args[5]), &input); err != nil {
			t.Fatal(err)
		}
		if input.Root != root || len(input.Projects) != 1 || input.Projects[0].Fields["status.ts#Session.status"] != "status.ts#Status" || input.Projects[0].Parsers["status.ts#parse"] == "" {
			t.Fatalf("input did not round trip: %+v", input)
		}
		if !strings.Contains(analyzer, "export function analyze") {
			t.Fatal("analyzer was not embedded")
		}
		return nil, nil
	})
	if err != nil || calls != 1 || !strings.Contains(output.String(), "named types and members") {
		t.Fatalf("Run = %v, calls=%d, output=%q", err, calls, &output)
	}
}

func TestRunFailure(t *testing.T) {
	t.Parallel()
	cause := errors.New("node unavailable or analyzer rejected source")
	var output bytes.Buffer
	err := Run([]Project{{Project: "tsconfig.json"}}, ".", &output, func([]string, bool, string, ...string) ([]byte, error) {
		return nil, cause
	})
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "requires Node") || output.Len() != 0 {
		t.Fatalf("Run = %v; output=%q", err, &output)
	}
}

func TestRunRejectsEmptyProjects(t *testing.T) {
	t.Parallel()
	if err := Run(nil, ".", &bytes.Buffer{}, nil); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("Run = %v", err)
	}
}

func TestRelativeRootResolvesBeforeChangingCommandDirectory(t *testing.T) {
	t.Parallel()
	want, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	err = Run([]Project{{Project: "tsconfig.json"}}, ".", &bytes.Buffer{}, func(_ []string, _ bool, _ string, args ...string) ([]byte, error) {
		var input struct {
			Root string `json:"root"`
		}
		if err := json.Unmarshal([]byte(args[len(args)-1]), &input); err != nil {
			t.Fatal(err)
		}
		if input.Root != want {
			t.Fatalf("relative root would be resolved twice by command cwd: got %q want %q", input.Root, want)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingRepositoryFailsBeforeLaunchingNode(t *testing.T) {
	t.Parallel()
	called := false
	err := Run([]Project{{Project: "tsconfig.json"}}, filepath.Join(t.TempDir(), "missing"), &bytes.Buffer{}, func([]string, bool, string, ...string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err == nil || called {
		t.Fatalf("missing repository reached Node: called=%v err=%v", called, err)
	}
}
