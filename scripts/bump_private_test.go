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

// stubs write gh, git, go, jq and sleep scripts into a directory and return
// it. Each stub appends what it was asked to $STUB_LOG. When $FAIL_ON names a
// step, that step fails, or for branch, dirty and ahead the checkout is in
// that state, for the repository whose path holds "repoA".
func stubs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"gh": "#!/bin/bash\necho \"gh $*\" >> \"$STUB_LOG\"\necho repoA\necho repoB\n",
		"git": `#!/bin/bash
echo "git $*" >> "$STUB_LOG"
a=0; [[ $2 == *repoA* ]] && a=1
is() { [ "$a" = 1 ] && [ "$FAIL_ON" = "$1" ]; }
[ "$1" = -C ] || exit 0
shift 2
case "$1" in
symbolic-ref)
  if [ "${@: -1}" = HEAD ]; then
    is branch && { echo feature; exit 0; }
    echo main
  else
    is symbolic-ref && { echo "fatal: no origin/HEAD" >&2; exit 1; }
    echo origin/main
  fi ;;
status) is dirty && echo " M main.go" ;;
rev-list) if is ahead; then echo 1; else echo 0; fi ;;
ls-files) echo go.mod ;;
add|commit|push) is "$1" && { echo "$1 failed" >&2; exit 1; } ;;
rev-parse) echo abc1234 ;;
esac
exit 0
`,
		"go": `#!/bin/bash
echo "go $*" >> "$STUB_LOG"
a=0; [[ $* == *repoA* ]] && a=1
case "$*" in
# The proxy's cache lags a new release; only the repository has it.
*"list -m -f"*"@latest"*) if [ "$GOPROXY" = direct ]; then echo v0.2.0; else echo v0.1.0; fi ;;
*"list -m -f"*) echo v0.1.0 ;;
*"mod edit -json"*) [ "$FAIL_ON" = mod-edit ] && [ "$a" = 1 ] && { echo "go: no go.mod" >&2; exit 1; }
  echo '{}' ;;
*"tool lateregate check"*) [ "$FAIL_ON" = bar ] && [ "$a" = 1 ] && { echo "gate failed" >&2; exit 1; } ;;
esac
exit 0
`,
		"jq":    "#!/bin/bash\ncat > /dev/null\necho 'go 1.22, toolchain none'\n",
		"sleep": "#!/bin/bash\nexit 0\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// checkouts makes a folder holding a checkout for each name, each pinning the
// tool, and returns it.
func checkouts(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		d := filepath.Join(root, n)
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		mod := "module x\n\ngo 1.22\n\ntool latere.ai/x/ci-gate/cmd/lateregate\n"
		if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
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

// calls returns the stub log lines that touch repoA and those that touch repoB.
func calls(t *testing.T, log string) (a, b []string) {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for l := range strings.Lines(string(raw)) {
		switch {
		case strings.Contains(l, "repoA"):
			a = append(a, strings.TrimSpace(l))
		case strings.Contains(l, "repoB"):
			b = append(b, strings.TrimSpace(l))
		}
	}
	return a, b
}

func has(lines []string, part string) bool {
	for _, l := range lines {
		if strings.Contains(l, part) {
			return true
		}
	}
	return false
}

func TestBumpPrivateMovesThePinInTheCheckoutItself(t *testing.T) {
	root := checkouts(t, "repoA", "repoB")
	out, status, _, log := runBump(t, "", "-d", root, "repoA", "repoB")
	if status != 0 {
		t.Fatalf("exit status %d:\n%s", status, out)
	}
	a, _ := calls(t, log)
	for _, want := range []string{
		"-C " + filepath.Join(root, "repoA") + " merge -q --ff-only origin/main",
		"tool lateregate check -all -C " + filepath.Join(root, "repoA"),
		"-C " + filepath.Join(root, "repoA") + " commit -q -m gate: ci-gate v0.2.0",
		"-C " + filepath.Join(root, "repoA") + " push -q origin HEAD:refs/heads/main",
	} {
		if !has(a, want) {
			t.Errorf("repoA never ran %q:\n%s", want, strings.Join(a, "\n"))
		}
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), "git clone") {
		t.Errorf("a repository was cloned:\n%s", raw)
	}
	if !strings.Contains(out, "moved v0.1.0 -> v0.2.0, pushed abc1234 to main") {
		t.Errorf("no move reported:\n%s", out)
	}
}

func TestBumpPrivateGoesOnToTheNextRepositoryAfterAStepFails(t *testing.T) {
	for _, failOn := range []string{"symbolic-ref", "mod-edit", "bar", "add", "commit", "push"} {
		t.Run(failOn, func(t *testing.T) {
			root := checkouts(t, "repoA", "repoB")
			out, status, tmp, log := runBump(t, failOn, "-d", root, "repoA", "repoB")
			if status != 1 {
				t.Errorf("exit status %d, want 1 after a failed repository:\n%s", status, out)
			}
			for _, want := range []string{"== repoB", "repoA: FAILED", "== summary", "repoB: moved v0.1.0 -> v0.2.0, pushed abc1234 to main"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			a, b := calls(t, log)
			if !has(b, "push") {
				t.Errorf("repoB was never pushed:\n%s", strings.Join(b, "\n"))
			}
			// A failure after the pin moved puts go.mod and go.sum back, and
			// a refused push also undoes its commit.
			switch failOn {
			case "bar", "add", "commit", "push":
				if !has(a, "checkout -q -- ./go.mod ./go.sum") {
					t.Errorf("repoA's go.mod was not put back:\n%s", strings.Join(a, "\n"))
				}
			}
			if failOn == "push" && !has(a, "reset -q --soft HEAD~1") {
				t.Errorf("repoA's commit was not undone:\n%s", strings.Join(a, "\n"))
			}
			kept, err := filepath.Glob(filepath.Join(tmp, "bump-private.*", "repoA.log"))
			if err != nil || len(kept) != 1 {
				t.Errorf("the failed repository's log is not kept: %v %v", kept, err)
			}
		})
	}
}

func TestBumpPrivateLeavesACheckoutWithWorkInProgressAlone(t *testing.T) {
	for _, state := range []string{"branch", "dirty", "ahead"} {
		t.Run(state, func(t *testing.T) {
			root := checkouts(t, "repoA", "repoB")
			out, status, _, log := runBump(t, state, "-d", root, "repoA", "repoB")
			if status != 0 {
				t.Errorf("exit status %d, want 0: a skipped checkout is not a failure:\n%s", status, out)
			}
			if !strings.Contains(out, "repoA: ") || !strings.Contains(out, "skipped, left as it is") {
				t.Errorf("the skip is not named:\n%s", out)
			}
			a, b := calls(t, log)
			for _, never := range []string{"merge", "get -tool", "mod tidy", "add", "commit", "push", "checkout"} {
				if has(a, never) {
					t.Errorf("repoA ran %q although it holds work in progress:\n%s", never, strings.Join(a, "\n"))
				}
			}
			if !has(b, "push") {
				t.Errorf("repoB was never pushed:\n%s", strings.Join(b, "\n"))
			}
		})
	}
}

func TestBumpPrivateNamesARepositoryWithNoCheckout(t *testing.T) {
	root := checkouts(t, "repoB")
	out, status, _, log := runBump(t, "", "-d", root, "repoA", "repoB")
	if status != 0 {
		t.Errorf("exit status %d:\n%s", status, out)
	}
	if !strings.Contains(out, "repoA: no checkout at "+filepath.Join(root, "repoA")) {
		t.Errorf("the missing checkout is not named:\n%s", out)
	}
	if a, _ := calls(t, log); len(a) != 0 {
		t.Errorf("a command ran for repoA:\n%s", strings.Join(a, "\n"))
	}
}

func TestBumpPrivateRefusesARepositoryNameThatIsNotAName(t *testing.T) {
	root := checkouts(t, "repoB")
	for _, arg := range []string{".", "..", "a/b", "../x", "a b", ""} {
		t.Run(arg, func(t *testing.T) {
			out, status, tmp, log := runBump(t, "", "-d", root, "repoB", arg)
			if status != 2 {
				t.Errorf("exit status %d, want 2:\n%s", status, out)
			}
			if !strings.Contains(out, "not a repository name") || !strings.Contains(out, "Usage:") {
				t.Errorf("no usage message:\n%s", out)
			}
			if _, err := os.Stat(log); err == nil {
				b, _ := os.ReadFile(log)
				t.Errorf("a command ran before the argument was refused:\n%s", b)
			}
			if left, _ := filepath.Glob(filepath.Join(tmp, "bump-private.*")); len(left) != 0 {
				t.Errorf("a working folder was made: %v", left)
			}
		})
	}
}
