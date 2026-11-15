// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withToken gives the rig a configuration directory holding a deploy token
// with the given content and mode, and returns the token file's path.
func withToken(t *testing.T, r *rig, content string, mode os.FileMode) string {
	t.Helper()
	cfg := t.TempDir()
	path := filepath.Join(cfg, DeployTokenFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	base := r.d.Environ()
	r.d.Environ = func() []string { return append(base, "XDG_CONFIG_HOME="+cfg) }
	return path
}

func TestDeployReadsTheKubeconfigWithTheTokenAndNoStepSeesIt(t *testing.T) {
	r := newRig(t, goodShip+"    preflight: [./tools/check.sh]\n")
	r.tree["tools/check.sh"] = "x"
	withToken(t, r, "ops_TOKEN\n", 0o600)
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	read, ok := r.find("op read")
	if !ok {
		t.Fatal("op read never ran")
	}
	if got := envOf(read.env, "OP_SERVICE_ACCOUNT_TOKEN"); got != "ops_TOKEN" {
		t.Errorf("op read token = %q, want the file's content without its newline", got)
	}
	if envOf(read.env, "PATH") != "/bin" {
		t.Error("op read lost the inherited environment")
	}
	for _, c := range r.calls {
		if c.name != "op" && envOf(c.env, "OP_SERVICE_ACCOUNT_TOKEN") != "" {
			t.Errorf("%s ran with the token", c)
		}
	}
	if strings.Contains(r.out.String(), "ops_TOKEN") {
		t.Error("the token was printed")
	}
}

func TestDeployWithoutATokenAsksThe1PasswordApp(t *testing.T) {
	r := newRig(t, "")
	base := r.d.Environ()
	r.d.Environ = func() []string { return append(base, "HOME="+t.TempDir()) }
	if err := Deploy(r.d, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	read, ok := r.find("op read")
	if !ok {
		t.Fatal("op read never ran")
	}
	if read.env != nil {
		t.Errorf("op read env = %v, want the inherited environment (nil)", read.env)
	}
}

func TestDeployRefusesABadTokenFileBeforeReading(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		mode                os.FileMode
	}{
		{"readable by others", "ops_TOKEN", "chmod 600", 0o644},
		{"readable by the group", "ops_TOKEN", "chmod 600", 0o640},
		{"empty", " \n", "is empty", 0o600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, "")
			path := withToken(t, r, tc.content, tc.mode)
			err := Deploy(r.d, "v1.2.3")
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
				t.Fatalf("error: %v", err)
			}
			if strings.Contains(err.Error(), "ops_TOKEN") {
				t.Error("the error holds the token")
			}
			if _, ok := r.find("op"); ok {
				t.Error("op ran")
			}
			if _, ok := r.find("kubectl"); ok {
				t.Error("the cluster was touched")
			}
		})
	}
}

func TestDeployNamesTheTokenWhenTheServiceAccountCannotRead(t *testing.T) {
	r := newRig(t, "")
	path := withToken(t, r, "ops_TOKEN", 0o600)
	r.answer = func(c call) ([]byte, error, bool) {
		if c.name == "op" {
			return []byte("[ERROR] could not read secret: vault not found\n"), errors.New("exit 1"), true
		}
		return nil, nil, false
	}
	err := Deploy(r.d, "v1.2.3")
	if err == nil {
		t.Fatal("deploy passed")
	}
	for _, want := range []string{path, testRef, "vault not found", "remove the file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, ok := r.find("kubectl"); ok {
		t.Error("the cluster was touched")
	}
}

func TestDeployTokenPathFollowsTheConfigurationDirectory(t *testing.T) {
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{[]string{"HOME=/h"}, "/h/.config/" + DeployTokenFile},
		{[]string{"HOME=/h", "XDG_CONFIG_HOME=/x"}, "/x/" + DeployTokenFile},
		{[]string{"XDG_CONFIG_HOME=/x", "XDG_CONFIG_HOME=/y"}, "/y/" + DeployTokenFile},
		{[]string{"PATH=/bin"}, ""},
	} {
		if got := deployTokenPath(tc.env); got != tc.want {
			t.Errorf("deployTokenPath(%v) = %q, want %q", tc.env, got, tc.want)
		}
	}
}
