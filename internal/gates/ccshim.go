// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// linkedTool is one program the shim links: the name the build looks up and
// the file it resolves to on the PATH before stripping.
type linkedTool struct {
	name, target string
}

// ccShim links the C toolchain the race detector needs into a directory of
// its own, and returns that directory and the names it holds.
//
// The detector needs cgo, and cgo needs the compiler the go command names in
// CC, which in turn finds the assembler and the linker through PATH. Putting
// the compiler's own directory on the stripped PATH would satisfy that and
// hand back everything else there too: on Linux that is /usr/bin, with git,
// docker and systemctl in it, and the hermetic property would hold nothing.
// The shim holds the compiler, and `as` and `ld` where the machine has them,
// and nothing else.
//
// The directory is the same path on every run, under the machine's TMPDIR
// rather than the suite's sandbox, so it is never a survivor. A test that
// reads PATH is cached on its value, and a fresh directory each run would
// rerun every such test. Its name carries the user and a digest of what it
// links: two runs with the same toolchain share it, and a run whose CC
// resolves elsewhere gets a directory of its own rather than rewriting links
// another run may be using.
func ccShim(goBin string, run Exec) (string, []string, error) {
	cc, err := compiler(goBin, run)
	if err != nil {
		return "", nil, err
	}
	tools := []linkedTool{{filepath.Base(cc), cc}}
	for _, name := range []string{"as", "ld"} {
		if p, err := exec.LookPath(name); err == nil {
			tools = append(tools, linkedTool{name, p})
		}
	}

	h := sha256.New()
	for _, t := range tools {
		_, _ = fmt.Fprintf(h, "%s=%s\n", t.name, t.target)
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("lateregate-cc-%d-%s", os.Getuid(), hex.EncodeToString(h.Sum(nil))[:12]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("making the C toolchain shim: %w", err)
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if err := placeLink(dir, t); err != nil {
			return "", nil, err
		}
		names = append(names, t.name)
	}
	return dir, names, nil
}

// placeLink makes dir/name a symlink to target. A link that already points
// there is kept, so a second run changes nothing; any other entry is replaced
// by renaming a new link over it, which two runs doing at once cannot tear.
func placeLink(dir string, t linkedTool) error {
	link := filepath.Join(dir, t.name)
	if got, err := os.Readlink(link); err == nil && got == t.target {
		return nil
	}
	tmp := filepath.Join(dir, "."+t.name+".tmp-"+strconv.Itoa(os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(t.target, tmp); err != nil {
		return fmt.Errorf("linking %s into the C toolchain shim: %w", t.name, err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("linking %s into the C toolchain shim: %w", t.name, err)
	}
	return nil
}

// shimNote is what the PATH line says about the shim.
func shimNote(dir string, names []string) string {
	return " (" + dir + " holds the race detector's C toolchain: " + strings.Join(names, ", ") + ")"
}
