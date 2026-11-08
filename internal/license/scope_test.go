// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package license

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

func scopeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scopeGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

const (
	scopeMIT  = "MIT License\n\nPermission is hereby granted, free of charge\n"
	scopeGood = "// SPDX-FileCopyrightText: 2026 Latere AI\n// SPDX-License-Identifier: MIT\n\npackage a\n"
)

func scopeCfg() config.License { return config.License{SPDX: "MIT", Holder: "Latere AI"} }

// A git-ignored file is not the repository's, so ignored build output on a
// laptop does not fail a gate that passes on a clean checkout.
func TestAGitIgnoredFileIsNotChecked(t *testing.T) {
	root := t.TempDir()
	scopeGit(t, root, "init", "-q")
	scopeFile(t, root, "LICENSE", scopeMIT)
	scopeFile(t, root, "a.go", scopeGood)
	scopeFile(t, root, ".gitignore", "gen/\n")
	scopeFile(t, root, "gen/out.go", "package gen\n")
	scopeGit(t, root, "add", ".")
	if err := Run(scopeCfg(), root, io.Discard); err != nil {
		t.Fatalf("an ignored file failed the gate: %v", err)
	}
}

// A vendored tree is other people's code and carries no notice of ours.
func TestAVendorTreeIsNotChecked(t *testing.T) {
	root := t.TempDir()
	scopeGit(t, root, "init", "-q")
	scopeFile(t, root, "LICENSE", scopeMIT)
	scopeFile(t, root, "a.go", scopeGood)
	scopeFile(t, root, "vendor/dep/dep.go", "package dep\n")
	scopeGit(t, root, "add", ".")
	if err := Run(scopeCfg(), root, io.Discard); err != nil {
		t.Fatalf("vendor/ failed the gate: %v", err)
	}
	bad, err := Files(scopeCfg(), root, []string{"vendor/dep/dep.go"})
	if err != nil || len(bad) != 0 {
		t.Fatalf("the hook checked vendor/: %v %v", bad, err)
	}
}

// With license.holder unset a notice that names nobody must not pass.
func TestAnUnsetHolderFailsTheGate(t *testing.T) {
	root := t.TempDir()
	scopeFile(t, root, "LICENSE", scopeMIT)
	scopeFile(t, root, "a.go", "// SPDX-FileCopyrightText: 2026\n// SPDX-License-Identifier: MIT\n\npackage a\n")
	err := Run(config.License{SPDX: "MIT"}, root, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "license.holder is not set") {
		t.Fatalf("want a license.holder failure, got %v", err)
	}
	if _, err := Files(config.License{SPDX: "MIT"}, root, []string{"a.go"}); err == nil {
		t.Fatal("the hook passed with no holder")
	}
}
