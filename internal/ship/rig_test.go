// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/gates"
)

const (
	testCommit = "0123456789abcdef0123456789abcdef01234567"
	testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testRef    = "op://LatereAI/auth rollout kubeconfig/kubeconfig"
	secretText = "SECRET-KUBECONFIG-CONTENT"
)

// call is one command the code under test ran.
type call struct {
	where string // root, or tree for a command run in the tag's tree
	env   []string
	name  string
	args  []string
	// mode is the kubeconfig file's mode when the call ran.
	mode os.FileMode
}

func (c call) String() string { return c.name + " " + strings.Join(c.args, " ") }

func (c call) has(arg string) bool { return slices.Contains(c.args, arg) }

// rig runs the commands of a ship command against answers, not machines.
type rig struct {
	t      *testing.T
	d      Deps
	out    *strings.Builder
	calls  []call
	have   map[string]bool // programs on PATH
	tags   []string        // what HEAD sits on
	dirty  string
	remote string // ls-remote output
	// answer, when it returns handled, replaces the default for a command.
	answer func(c call) (out []byte, err error, handled bool)
	tree   map[string]string
	kube   string // the kubeconfig named in the environment of the last call
	tmp    string
}

func newRig(t *testing.T, shipYAML string) *rig {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if shipYAML == "" {
		shipYAML = goodShip
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.Name), []byte(shipYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, out: &strings.Builder{}, have: map[string]bool{"podman": true, "op": true}, tags: []string{"v1.2.3"}, tmp: tmp}
	r.tree = map[string]string{
		"deploy/prod/deployment.yaml": "kind: Deployment\n",
		"tools/smoke/release.sh":      "#!/bin/sh\n",
	}
	r.d = Deps{
		Root: dir, Cfg: cfg, Out: r.out,
		Exec: func(env []string, _ bool, name string, args ...string) ([]byte, error) {
			return r.run("root", env, name, args)
		},
		In: func(dir string) gates.Exec {
			return func(env []string, _ bool, name string, args ...string) ([]byte, error) {
				return r.run("tree", env, name, args)
			}
		},
		LookPath: func(p string) (string, error) {
			if r.have[p] {
				return "/bin/" + p, nil
			}
			return "", errors.New("not found")
		},
		Environ: func() []string {
			return []string{"PATH=/bin", "SERVICE_TOKEN=inherited", "KUBECONFIG=/operator/default"}
		},
		Exit: func(int) {},
	}
	return r
}

const goodShip = `ship:
  image:
    name: ghcr.io/latere-ai/auth
    version_arg: VERSION
    build_args: {B: c}
  deploy:
    kubeconfig: ` + testRef + `
    namespace: latere
    deployment: auth
    container: authd
    url: https://auth.latere.ai
`

// run records a command and answers it.
func (r *rig) run(where string, env []string, name string, args []string) ([]byte, error) {
	r.t.Helper()
	// A command run through combined is recorded as itself.
	if name == "sh" && len(args) > 2 && args[0] == "-c" && strings.HasPrefix(args[1], `exec "$0"`) {
		name, args = args[2], args[3:]
	}
	c := call{where: where, env: env, name: name, args: args}
	if k := envOf(env, "KUBECONFIG"); k != "" && k != "/operator/default" {
		r.kube = k
	}
	for i, a := range args {
		if a == "--kubeconfig" && i+1 < len(args) {
			if info, err := os.Stat(args[i+1]); err == nil {
				c.mode = info.Mode().Perm()
			}
		}
	}
	r.calls = append(r.calls, c)
	if r.answer != nil {
		if out, err, ok := r.answer(c); ok {
			return out, err
		}
	}
	switch name {
	case "git":
		return r.git(args)
	case "op":
		if len(args) > 0 && args[0] == "read" {
			path := args[slices.Index(args, "--out-file")+1]
			return nil, os.WriteFile(path, []byte(secretText), 0o644)
		}
	case "podman":
		switch args[0] {
		case "push":
			if i := slices.Index(args, "--digestfile"); i >= 0 {
				return nil, os.WriteFile(args[i+1], []byte(testDigest), 0o644)
			}
		case "image":
			// Another name's digest comes first, as podman lists it for an
			// image it also knows under a second name.
			return []byte("localhost/auth@sha256:" + strings.Repeat("b", 64) + "\nghcr.io/latere-ai/auth@" + testDigest + "\n"), nil
		}
	case "docker":
		if args[0] == "buildx" {
			return []byte("Name:      ghcr.io/latere-ai/auth:v1.2.3\nMediaType: x\nDigest:    " + testDigest + "\n"), nil
		}
		if args[0] == "push" {
			return []byte("v1.2.3: digest: " + testDigest + " size: 1\n"), nil
		}
	case "gh":
		if len(args) > 1 && args[1] == "view" {
			return []byte("release not found"), errors.New("exit 1")
		}
	}
	return nil, nil
}

func (r *rig) git(args []string) ([]byte, error) {
	switch args[0] {
	case "tag":
		return []byte(strings.Join(r.tags, "\n")), nil
	case "status":
		return []byte(r.dirty), nil
	case "describe":
		return []byte("v1.2.3-4-gabc1234-dirty\n"), nil
	case "ls-remote":
		return []byte(r.remote), nil
	case "rev-parse":
		switch {
		case slices.Contains(args, "--short"):
			return []byte("abc1234\n"), nil
		case slices.Contains(args, "--verify"):
			return []byte(testCommit + "\n"), nil
		}
		return []byte(testCommit + "\n"), nil
	case "show":
		tag, _, _ := strings.Cut(args[1], ":")
		return []byte("## " + tag + " - 2026-11-06\n\n- the note\n"), nil
	case "archive":
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header"})
		for name, body := range r.tree {
			mode := int64(0o644)
			if strings.HasSuffix(name, ".sh") {
				mode = 0o755
			}
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
				r.t.Fatal(err)
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				r.t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			r.t.Fatal(err)
		}
		return buf.Bytes(), nil
	}
	return nil, nil
}

func envOf(env []string, key string) string {
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// find returns the first call whose text starts with prefix.
func (r *rig) find(prefix string) (call, bool) {
	for _, c := range r.calls {
		if strings.HasPrefix(c.String(), prefix) {
			return c, true
		}
	}
	return call{}, false
}

// steps lists the commands that matter to the order of a deploy.
func (r *rig) steps() []string {
	var out []string
	for _, c := range r.calls {
		switch {
		case c.name == "kubectl":
			i := slices.Index(c.args, "--kubeconfig") + 2
			rest := c.args[i:]
			if rest[0] == "-n" {
				rest = rest[2:]
			}
			out = append(out, "kubectl "+rest[0]+" "+rest[1])
		case c.name == "bash":
			out = append(out, "bash "+firstLine(strings.TrimPrefix(c.args[1], "set -euo pipefail\n")))
		case strings.HasPrefix(c.name, "./"):
			out = append(out, c.name)
		}
	}
	return out
}

// leftovers lists what a command left under TMPDIR.
func (r *rig) leftovers() []string {
	entries, err := os.ReadDir(r.tmp)
	if err != nil {
		r.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
