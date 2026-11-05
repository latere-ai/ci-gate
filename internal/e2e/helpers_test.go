// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newServer answers every request with status.
func newServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return srv
}
