// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scripts

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubs write gh, git, go and jq scripts into a directory and return it. Each
// stub appends what it was asked to $STUB_LOG. When $FAIL_ON names a command,
// that command fails for the repository whose path holds "repoA".
func stubs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"gh": "#!/bin/bash\necho \"gh $*\" >> \"$STUB_LOG\"\necho repoA\necho repoB\n",
		"git": `#!/bin/bash
echo "git $*" >> "$STUB_LOG"
case "$1" in
clone)
  d="${@: -1}"
  mkdir -p "$d"
  printf 'module x\n\ngo 1.22\n\ntool latere.ai/x/ci-gate/cmd/lateregate\n' > "$d/go.mod"
  exit 0 ;;
-C)
  dir=$2
  shift 2
  case "$1" in
  ls-files) echo go.mod ;;
  symbolic-ref) [ "$FAIL_ON" = symbolic-ref ] && [[ $dir == *repoA* ]] && { echo "fatal: no origin/HEAD" >&2; exit 1; }
    echo origin/main ;;
  add|commit) [ "$FAIL_ON" = "$1" ] && [[ $dir == *repoA* ]] && { echo "$1 failed" >&2; exit 1; } ;;
  rev-parse) echo abc1234 ;;
  esac ;;
esac
exit 0
`,
		"go": `#!/bin/bash
echo "go $*" >> "$STUB_LOG"
case "$*" in
*"list -m -f"*"@latest"*) echo v0.2.0 ;;
*"list -m -f"*) echo v0.1.0 ;;
*"mod edit -json"*) [ "$FAIL_ON" = mod-edit ] && [[ $* == *repoA* ]] && { echo "go: no go.mod" >&2; exit 1; }
  echo '{}' ;;
esac
exit 0
`,
		"jq": "#!/bin/bash\ncat > /dev/null\necho 'go 1.22, toolchain none'\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runBump runs the script under /bin/bash with nothing from the real
// environment: a stub directory first on PATH, then only the system's own.
func runBump(t *testing.T, failOn string, args ...string) (out string, status int, tmp, log string) {
	t.Helper()
	tmp = t.TempDir()
	log = filepath.Join(t.TempDir(), "stub.log")
	cmd := exec.Command("/bin/bash", append([]string{"bump-private.sh"}, args...)...)
	cmd.Env = []string{
		"PATH=" + stubs(t) + ":/usr/bin:/bin",
		"HOME=" + tmp,
		"TMPDIR=" + tmp,
		"STUB_LOG=" + log,
		"FAIL_ON=" + failOn,
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		status = exit.ExitCode()
	default:
		t.Fatal(err)
	}
	return buf.String(), status, tmp, log
}

func TestBumpPrivateGoesOnToTheNextRepositoryAfterACommandFails(t *testing.T) {
	for _, failOn := range []string{"mod-edit", "add", "commit", "symbolic-ref"} {
		t.Run(failOn, func(t *testing.T) {
			out, status, tmp, log := runBump(t, failOn, "repoA", "repoB")
			if status != 1 {
				t.Errorf("exit status %d, want 1 after a failed repository:\n%s", status, out)
			}
			for _, want := range []string{"== repoB", "repoA", "FAILED", "== summary", "moved v0.1.0 -> v0.2.0, pushed abc1234 to main"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "repoB") || !strings.Contains(string(b), "push") {
				t.Errorf("repoB was never pushed:\n%s", b)
			}
			kept, err := filepath.Glob(filepath.Join(tmp, "bump-private.*", "repoA.log"))
			if err != nil || len(kept) != 1 {
				t.Errorf("the failed repository's log is not kept: %v %v", kept, err)
			}
		})
	}
}

func TestBumpPrivateRefusesARepositoryNameThatIsNotAName(t *testing.T) {
	for _, arg := range []string{".", "..", "a/b", "../x", "a b", ""} {
		t.Run(arg, func(t *testing.T) {
			out, status, tmp, log := runBump(t, "", "repoB", arg)
			if status != 2 {
				t.Errorf("exit status %d, want 2:\n%s", status, out)
			}
			if !strings.Contains(out, "not a repository name") || !strings.Contains(out, "Usage:") {
				t.Errorf("no usage message:\n%s", out)
			}
			if b, err := os.ReadFile(log); err == nil && strings.Contains(string(b), "clone") {
				t.Errorf("a repository was cloned before the argument was refused:\n%s", b)
			}
			if left, _ := filepath.Glob(filepath.Join(tmp, "bump-private.*")); len(left) != 0 {
				t.Errorf("a working folder was made: %v", left)
			}
		})
	}
}
