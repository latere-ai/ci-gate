// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package scope

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

const benchModule = "latere.ai/x/ci-gate"

var benchStdlib = []string{
	"bytes", "context", "errors", "fmt", "io", "io/fs", "net/http", "os", "os/exec",
	"path", "path/filepath", "slices", "sort", "strconv", "strings", "sync", "time",
	"unicode/utf8", "internal/abi", "internal/bytealg", "internal/cpu", "runtime",
}

// benchListStream builds the output of `go list -json` as the toolchain
// prints it: one indented object per package, back to back.
func benchListStream(tb testing.TB, pkgs, deps int) []byte {
	tb.Helper()
	var sb strings.Builder
	for i := range pkgs {
		p := listed{
			ImportPath:   fmt.Sprintf("%s/internal/pkg%03d", benchModule, i),
			TestImports:  []string{"testing", "os", "path/filepath", "strings"},
			XTestImports: []string{"testing"},
		}
		for j := range deps {
			switch j % 3 {
			case 0:
				p.Deps = append(p.Deps, benchStdlib[j%len(benchStdlib)])
			case 1:
				p.Deps = append(p.Deps, fmt.Sprintf("%s/internal/pkg%03d", benchModule, (i+j)%max(pkgs, 1)))
			default:
				p.Deps = append(p.Deps, fmt.Sprintf("github.com/example/dep%02d/sub%02d", j%17, j%11))
			}
		}
		if i%4 == 0 {
			p.EmbedFiles = []string{"testdata/schema.json", "templates/report.tmpl"}
			p.TestEmbedFiles = []string{"testdata/golden.txt"}
		}
		b, err := json.Marshal(p, jsontext.WithIndent("\t"))
		if err != nil {
			tb.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

var benchListSizes = []struct {
	name       string
	pkgs, deps int
}{
	{"small", 2, 4},
	{"medium", 12, 45},
	{"large", 60, 140},
}

// BenchmarkJSONGoListPackagesDecode stands for the scope gate reading
// `go list -e -json=...` for the whole module on every gate run.
func BenchmarkJSONGoListPackagesDecode(b *testing.B) {
	for _, sz := range benchListSizes {
		b.Run(sz.name, func(b *testing.B) {
			stream := benchListStream(b, sz.pkgs, sz.deps)
			run := func(_ []string, _ bool, _ string, args ...string) ([]byte, error) {
				if len(args) > 1 && args[1] == "-m" {
					return []byte(benchModule + "\n"), nil
				}
				return stream, nil
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(stream)))
			for b.Loop() {
				_, pkgs, err := list("/repo", "go", run)
				if err != nil {
					b.Fatal(err)
				}
				if len(pkgs) != sz.pkgs {
					b.Fatalf("listed %d packages, want %d", len(pkgs), sz.pkgs)
				}
			}
		})
	}
}

// BenchmarkJSONGoListBaseTestsDecode stands for reading `go list -test
// -json` for the packages a change reaches, as they were at the base.
func BenchmarkJSONGoListBaseTestsDecode(b *testing.B) {
	for _, sz := range benchListSizes {
		b.Run(sz.name, func(b *testing.B) {
			var sb strings.Builder
			var reached []Package
			for i := range sz.pkgs {
				path := fmt.Sprintf("%s/internal/pkg%03d", benchModule, i)
				reached = append(reached, Package{ImportPath: path, Dir: fmt.Sprintf("internal/pkg%03d", i)})
				for _, suffix := range []string{"", ".test"} {
					p := struct {
						ImportPath                                  string
						Deps                                        []string
						EmbedFiles, TestEmbedFiles, XTestEmbedFiles []string `json:",omitempty"`
					}{ImportPath: path + suffix}
					for j := range sz.deps {
						p.Deps = append(p.Deps, benchStdlib[j%len(benchStdlib)]+fmt.Sprintf("/v%d", j))
					}
					if i%4 == 0 && suffix == "" {
						p.EmbedFiles = []string{"testdata/schema.json"}
					}
					enc, err := json.Marshal(p, jsontext.WithIndent("\t"))
					if err != nil {
						b.Fatal(err)
					}
					sb.Write(enc)
					sb.WriteByte('\n')
				}
			}
			stream := []byte(sb.String())
			s := &Scope{module: benchModule, Reached: reached}
			run := func(_ []string, _ bool, _ string, _ ...string) ([]byte, error) { return stream, nil }
			b.ReportAllocs()
			b.SetBytes(int64(len(stream)))
			for b.Loop() {
				if _, err := s.baseTests("go", run, "/tmp/overlay.json"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
