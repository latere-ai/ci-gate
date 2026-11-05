// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// benchTestStream builds a `go test -json` stream: for each test a run
// event, a few output events, and a pass; then the package's own pass.
func benchTestStream(tb testing.TB, tests int) []byte {
	tb.Helper()
	var sb strings.Builder
	emit := func(ev event) {
		b, err := json.Marshal(ev)
		if err != nil {
			tb.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	const pkg = "latere.ai/x/example/internal/orders"
	for i := range tests {
		name := fmt.Sprintf("TestOrders%03d", i)
		emit(event{Action: "run", Package: pkg, Test: name})
		emit(event{Action: "output", Package: pkg, Test: name, Output: "=== RUN   " + name + "\n"})
		emit(event{Action: "output", Package: pkg, Test: name, Output: "    orders_test.go:42: created order 7f3c1e0a with 3 lines\n"})
		emit(event{Action: "output", Package: pkg, Test: name, Output: "--- PASS: " + name + " (0.01s)\n"})
		emit(event{Action: "pass", Package: pkg, Test: name, Elapsed: 0.01})
	}
	emit(event{Action: "output", Package: pkg, Output: "PASS\n"})
	emit(event{Action: "pass", Package: pkg, Elapsed: 1.25})
	return []byte(sb.String())
}

// BenchmarkJSONTestEventStreamDecode stands for the e2e runner reading the
// `go test -json` stream line by line while the suite runs.
func BenchmarkJSONTestEventStreamDecode(b *testing.B) {
	for _, sz := range []struct {
		name  string
		tests int
	}{{"small", 1}, {"medium", 15}, {"large", 300}} {
		b.Run(sz.name, func(b *testing.B) {
			stream := benchTestStream(b, sz.tests)
			b.ReportAllocs()
			b.SetBytes(int64(len(stream)))
			for b.Loop() {
				k := newTestSink(io.Discard)
				if _, err := k.Write(stream); err != nil {
					b.Fatal(err)
				}
				k.flush()
				if k.passed != sz.tests {
					b.Fatalf("passed %d tests, want %d", k.passed, sz.tests)
				}
			}
		})
	}
}
