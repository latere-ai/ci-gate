// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package greencut

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

type benchTransport func(*http.Request) (*http.Response, error)

func (f benchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// benchGuard answers every request with body, in memory.
func benchGuard(body []byte) *Guard {
	return &Guard{
		Token: "test-token",
		API:   "http://github.test",
		client: &http.Client{Transport: benchTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(body)),
				Request:    r,
			}, nil
		})},
	}
}

// benchRunsPage is a workflow-runs page as the GitHub REST API serves it:
// the fields the cut reads, among the many it does not.
func benchRunsPage(tb testing.TB, n int) []byte {
	tb.Helper()
	runs := make([]map[string]any, n)
	for i := range n {
		runs[i] = map[string]any{
			"id": 9000000000 + int64(i), "name": "ci", "node_id": fmt.Sprintf("WFR_kwLOA%010d", i),
			"run_number": 400 + i, "run_attempt": 1, "workflow_id": 123456,
			"head_sha": fmt.Sprintf("%040x", i+1), "head_branch": "main",
			"event": "push", "status": "completed", "conclusion": "success",
			"html_url":   fmt.Sprintf("https://github.com/latere-ai/example/actions/runs/%d", 9000000000+i),
			"created_at": "2026-11-01T10:00:00Z", "updated_at": "2026-11-01T10:07:31Z",
			"actor":      map[string]any{"login": "changkun", "id": 5000000, "type": "User", "html_url": "https://github.com/changkun"},
			"repository": map[string]any{"id": 1, "name": "example", "full_name": "latere-ai/example", "private": true},
		}
	}
	out, err := json.Marshal(map[string]any{"total_count": n, "workflow_runs": runs})
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// BenchmarkJSONWorkflowRunsPageDecode stands for the release guard reading
// the workflow runs of the commit it is about to tag.
func BenchmarkJSONWorkflowRunsPageDecode(b *testing.B) {
	for _, sz := range []struct {
		name string
		runs int
	}{{"small", 1}, {"medium", 20}, {"large", 100}} {
		b.Run(sz.name, func(b *testing.B) {
			body := benchRunsPage(b, sz.runs)
			g := benchGuard(body)
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				var page runsPage
				code, err := g.get("repos/latere-ai/example/actions/runs", nil, &page)
				if err != nil {
					b.Fatal(err)
				}
				if code != http.StatusOK || len(page.Runs) != sz.runs {
					b.Fatalf("code %d, %d runs, want %d", code, len(page.Runs), sz.runs)
				}
			}
		})
	}
}
