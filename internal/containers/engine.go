// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

// Package containers finds the containers a test run started and removes the
// ones nothing will stop.
//
// It speaks the Docker Engine API over a unix socket, which Docker and Podman
// both serve, with the standard library alone: the gate and every test helper
// that imports it would otherwise carry a container client's dependency graph
// for two requests.
package containers

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Engine is a container engine reached over its API socket.
type Engine struct {
	base   string
	client *http.Client
}

// Container is the part of an engine's container listing this package reads.
type Container struct {
	ID     string            `json:"Id"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// Network is the part of an engine's network listing this package reads.
type Network struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
}

// Find returns the engine a test helper on this machine would reach, or false
// when no socket answers to the name.
func Find() (*Engine, bool) {
	return find(os.Getenv, isSocket)
}

func find(getenv func(string) string, socket func(string) bool) (*Engine, bool) {
	for _, p := range lookup(getenv).sockets {
		if socket(p) {
			return Socket(p), true
		}
	}
	return nil, false
}

// Missing says why Find found no engine, for a gate that then watches
// nothing: the engine a client is configured for and this package does not
// reach, or else the places it looked.
func Missing() string {
	return missing(os.Getenv)
}

func missing(getenv func(string) string) string {
	l := lookup(getenv)
	msg := "no container engine found (looked at " + strings.Join(l.sockets, ", ") + ")"
	if l.unreached != "" {
		msg = "the engine at " + l.unreached + " is not one this gate reaches"
	}
	if l.err != nil {
		msg += "; " + l.err.Error()
	}
	return msg + ", so the suite's containers are not watched"
}

// propertiesFile is testcontainers' own configuration, in the home directory.
const propertiesFile = ".testcontainers.properties"

// search is where a client looks for an engine.
type search struct {
	// sockets are the unix sockets to try, in order.
	sockets []string
	// unreached names the setting, as KEY=value, that points at an engine
	// this package cannot reach (tcp, ssh), when one does.
	unreached string
	// err is a configuration file that exists and could not be read.
	err error
}

// lookup lists the sockets a client tries, in the order the Docker client
// and testcontainers do: DOCKER_HOST when it is set, and only it, then
// docker.host in ~/.testcontainers.properties, and only it, then the system
// socket, Docker Desktop's per-user socket and rootless Podman's.
//
// A setting over TCP or SSH names an engine this package does not reach, so
// it yields no socket rather than a default that may be another engine.
func lookup(getenv func(string) string) search {
	var l search
	setting, source := getenv("DOCKER_HOST"), "DOCKER_HOST="
	home := getenv("HOME")
	if setting == "" && home != "" {
		var err error
		setting, err = propertiesHost(filepath.Join(home, propertiesFile))
		if err != nil {
			l.err = err
		}
		source = "docker.host=" // in ~/.testcontainers.properties
	}
	if setting != "" {
		if p, ok := strings.CutPrefix(setting, "unix://"); ok {
			l.sockets = []string{p}
			return l
		}
		l.unreached = source + setting
		if source != "DOCKER_HOST=" {
			l.unreached += " in ~/" + propertiesFile
		}
		return l
	}
	l.sockets = []string{"/var/run/docker.sock"}
	if home != "" {
		l.sockets = append(l.sockets, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	if run := getenv("XDG_RUNTIME_DIR"); run != "" {
		l.sockets = append(l.sockets, filepath.Join(run, "podman", "podman.sock"))
	}
	return l
}

// propertiesHost is docker.host in the properties file at path, or "" when
// the file is missing or does not set it.
func propertiesHost(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	for line := range strings.Lines(string(b)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) == "docker.host" {
			return strings.TrimSpace(v), nil
		}
	}
	return "", nil
}

// isSocket follows a link, because a Podman machine on macOS is reached
// through /var/run/docker.sock linked to the machine's forwarded socket.
func isSocket(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

// Socket is the engine behind the unix socket at path.
func Socket(path string) *Engine {
	d := &net.Dialer{}
	return &Engine{
		// The host is never resolved: every request dials path.
		base: "http://engine",
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return d.DialContext(ctx, "unix", path)
				},
			},
			Timeout: 30 * time.Second,
		},
	}
}

// List returns every container, running or not, that carries label. label is
// a key, or key=value for one value of it.
func (e *Engine) List(ctx context.Context, label string) ([]Container, error) {
	var cs []Container
	return cs, e.list(ctx, "/containers/json?all=1&", label, "container", &cs)
}

// Networks returns every network that carries label, a key or key=value.
func (e *Engine) Networks(ctx context.Context, label string) ([]Network, error) {
	var ns []Network
	return ns, e.list(ctx, "/networks?", label, "network", &ns)
}

// foreignJSON reads JSON this package does not produce. Duplicate keys,
// invalid UTF-8 and any letter case are accepted, as the tools whose output
// is read here may produce them.
var foreignJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)

// outboundJSON writes the filters the container engine reads: stable key
// order, and null where a slice or map is nil, as the engine's API expects.
var outboundJSON = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
)

func (e *Engine) list(ctx context.Context, path, label, kind string, into any) error {
	filters, err := json.Marshal(map[string][]string{"label": {label}}, outboundJSON)
	if err != nil {
		return err
	}
	resp, err := e.do(ctx, http.MethodGet, path+url.Values{"filters": {string(filters)}}.Encode())
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return refused(resp, "listing "+kind+"s")
	}
	if err := json.UnmarshalRead(resp.Body, into, foreignJSON); err != nil {
		return fmt.Errorf("reading the %s list: %w", kind, err)
	}
	return nil
}

// Remove stops and deletes the container id with its anonymous volumes. A
// container already gone, or already being removed by someone else, is not an
// error: two sweeps that race for one container both got what they wanted.
func (e *Engine) Remove(ctx context.Context, id string) error {
	resp, err := e.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?force=1&v=1")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusConflict:
		return nil
	}
	return refused(resp, "removing container "+Short(id))
}

// RemoveNetwork deletes the network id. One already gone is not an error. One
// that still has a container attached is, and says so: the containers go
// first, so a network left in use holds a container nothing labeled.
func (e *Engine) RemoveNetwork(ctx context.Context, id string) error {
	resp, err := e.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(id))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	}
	return refused(resp, "removing network "+Short(id))
}

func (e *Engine) do(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.base+path, nil)
	if err != nil {
		return nil, err
	}
	return e.client.Do(req)
}

// refused is the engine's own message for a status this package does not
// expect, which says more than the status alone.
func refused(resp *http.Response, doing string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	var msg struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &msg, foreignJSON) == nil && msg.Message != "" {
		return fmt.Errorf("%s: the engine answered %d: %s", doing, resp.StatusCode, msg.Message)
	}
	return fmt.Errorf("%s: the engine answered %d", doing, resp.StatusCode)
}

// Short is the twelve-character form of a container id that the engines'
// own listings print.
func Short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
