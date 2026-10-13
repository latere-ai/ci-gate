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
	"encoding/json"
	"fmt"
	"io"
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
	for _, p := range candidates(getenv) {
		if socket(p) {
			return Socket(p), true
		}
	}
	return nil, false
}

// candidates are the sockets a client tries, in the order the Docker client
// and testcontainers do: DOCKER_HOST when it is set, and only it, then the
// system socket, Docker Desktop's per-user socket and rootless Podman's.
//
// A DOCKER_HOST over TCP names an engine this package does not reach, so it
// yields nothing rather than a default socket that may be another engine.
func candidates(getenv func(string) string) []string {
	if h := getenv("DOCKER_HOST"); h != "" {
		if p, ok := strings.CutPrefix(h, "unix://"); ok {
			return []string{p}
		}
		return nil
	}
	c := []string{"/var/run/docker.sock"}
	if home := getenv("HOME"); home != "" {
		c = append(c, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	if run := getenv("XDG_RUNTIME_DIR"); run != "" {
		c = append(c, filepath.Join(run, "podman", "podman.sock"))
	}
	return c
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

func (e *Engine) list(ctx context.Context, path, label, kind string, into any) error {
	filters, err := json.Marshal(map[string][]string{"label": {label}})
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
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
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
// first, so a network left in use holds a container nothing labelled.
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
	if json.Unmarshal(body, &msg) == nil && msg.Message != "" {
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
