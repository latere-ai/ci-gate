// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package ship

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DeployTokenFile holds a 1Password service account token, relative to the
// user's configuration directory ($XDG_CONFIG_HOME, or ~/.config). When it
// exists, `op read` of the rollout kubeconfig authenticates with that token
// instead of asking the 1Password app, so a deploy started while nobody is at
// the workstation does not stop on an expired approval. Whoever can read the
// file can read every item the service account can, so the account should
// read only the vault that holds rollout kubeconfigs.
const DeployTokenFile = "lateregate/op-service-account-token"

// opEnv is the environment `op read` runs with, and the token file it came
// from. Both are empty when there is no token file: `op` then inherits the
// environment and asks the 1Password app. Only `op read` gets the token; no
// deploy step, hook or kubectl call sees it.
func (d Deps) opEnv() (env []string, path string, err error) {
	if d.Environ == nil {
		return nil, "", nil
	}
	base := d.Environ()
	path = deployTokenPath(base)
	if path == "" {
		return nil, "", nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading the deploy token %s: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, "", fmt.Errorf("the deploy token %s can be read by others (mode %v): run chmod 600 on it", path, perm)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading the deploy token %s: %w", path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, "", fmt.Errorf("the deploy token %s is empty: write the service account's token into it, or remove it to approve each read in the 1Password app", path)
	}
	return withEnv(base, "OP_SERVICE_ACCOUNT_TOKEN="+token), path, nil
}

// deployTokenPath is where DeployTokenFile sits for env, or "" when env names
// neither a configuration directory nor a home.
func deployTokenPath(env []string) string {
	dir := lookupEnv(env, "XDG_CONFIG_HOME")
	if dir == "" {
		home := lookupEnv(env, "HOME")
		if home == "" {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, DeployTokenFile)
}

// lookupEnv is the value of key in env; the last setting wins, as in exec.
func lookupEnv(env []string, key string) string {
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}
