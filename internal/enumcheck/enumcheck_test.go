// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package enumcheck

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const enumSource = `package state
type Status string
const (
 Empty Status = ""
 Running Status = "running"
 Stopped Status = "stopped"
 RunningAgain Status = Running
)
type Alias = Status
type Count int
const (Zero Count = iota; One)
type Record struct { Status Status; Optional *Alias; Count Count; Name string }
type Wrong struct { Status string }
type EmptyDomain string
type Float float64
const Half Float = 0.5
type Boolean bool
type Composite struct{}
type Primitive = string
func Parse(raw string) Status { return Status(raw) }
func (Record) Method(s Status) {}
`

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test/enums\n\ngo 1.27.0\n")
	for name, source := range files {
		writeFile(t, root, name, source)
	}
	return root
}

func writeFile(t *testing.T, root, name, source string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func basePolicy() Policy {
	return Policy{
		Types: []string{"state.Status", "state.Count"},
		Fields: map[string]string{
			"state.Record.Status": "state.Status", "state.Record.Optional": "state.Status", "state.Record.Count": "state.Count",
		},
		Parsers: map[string]string{"state.Parse": "Validates external status values."},
	}
}

func run(t *testing.T, policy Policy, root string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := Run(policy, root, &out)
	return out.String(), err
}

func TestRunNamedMembers(t *testing.T) {
	root := fixture(t, map[string]string{
		"state/state.go": enumSource,
		"app.go": `package app
import s "example.test/enums/state"
type Alias = s.Status
func identity(value s.Status) s.Status { return value }
func accepts(value s.Status, rest ...s.Status) {}
func tuple() (s.Status, int) { return s.Running, 1 }
func consumePair(value s.Status, count int) {}
func main() {
 var status Alias = (s.Running)
 var copied = status
 var count s.Count = s.Zero
 const local = s.Stopped
 status, count = copied, s.One
 values := []s.Status{s.Running, local}
 values = append(values, s.Stopped)
 array := [...]s.Status{0: s.Running}
 records := []s.Record{{Status: s.Running}, {s.Stopped, &status, s.Zero, "free"}}
 records[0].Method(s.Running)
 records[0].Status = identity(status)
 mapping := map[s.Status]s.Status{s.Running: s.Stopped}
 mapping[s.Running] = status
 accepts(s.Running, s.Stopped)
 accepts(status, values...)
 consumePair(tuple())
 _, _ = tuple()
 status = func() s.Status { return s.Stopped }()
 _ = status == s.Running
 _ = s.Running != status
 _ = count < s.One
 _ = s.Status(status)
 _ = s.Status(s.Running)
 _ = string(status)
 _ = string(status) == string(s.Running)
 _ = int(count) == int(s.One)
 switch status { case s.Empty, s.RunningAgain, s.Stopped: }
 switch count { case s.Zero: case s.One: default: }
 switch string(status) { case string(s.Empty), string(s.Running), string(s.Stopped): }
 switch { case true: }
 switch "open" { case "open": }
 channel := make(chan s.Status, 1)
 channel <- s.Running
 _ = (<-channel)
 _, _, _ = array, records, mapping
}
`,
		// Tests may deliberately create invalid enum values and are never loaded.
		"app_test.go": `package app; import s "example.test/enums/state"; var bad s.Status = "typo"`,
		"excluded.go": "//go:build ignored\n\npackage app\nthis is not valid Go\n",
	})
	if output, err := run(t, basePolicy(), root); err != nil {
		t.Fatalf("Run() = %v\n%s", err, output)
	}
}

func TestRunRejectsLiteralUseSites(t *testing.T) {
	source := `package app
import s "example.test/enums/state"
func accepts(value s.Status, rest ...s.Status) {}
var global s.Status = "running" // want named
var globalCount s.Count = 0 // want named
func badReturn() s.Status { return "running" } // want named
func main() {
 var status s.Status = "running" // want named
 var count s.Count = 0 // want named
 status = "stopped" // want named
 count = 1 // want named
 status, count = "running", 0 // want named
 const local s.Status = "typo" // want named
 accepts("running") // want named
 accepts(s.Running, "stopped") // want named
 _ = status == "running" // want named
 _ = "stopped" != status // want named
 _ = count < 1 // want named
 _ = []s.Status{"running"} // want named
 _ = [...]s.Status{0: "running"} // want named
 _ = s.Record{Status: "running"} // want named
 _ = s.Record{"running", nil, s.Zero, "free"} // want named
 _ = map[s.Status]string{"running": "free"} // want named
 _ = map[string]s.Status{"free": "running"} // want named
 m := map[s.Status]s.Status{}
 _ = m["running"] // want named
 m[s.Running] = "running" // want named
 _ = append([]s.Status{}, "running") // want named
 _ = func() s.Status { return "running" } // want named
 _ = s.Status("running") // want conversion
 raw := "running"
 _ = s.Status(raw) // want conversion
 _ = s.Count(int(count)) // want conversion
 _ = s.Status(string(status)) // want conversion
 _ = status == s.Status("running") // want conversion
 _ = string(status) == "running" // want named
 _ = "running" == string(status) // want named
 _ = int(count) == 1 // want named
 _ = 1 == int(count) // want named
 switch status { case "", "running", "stopped": } // want named
 switch status { case s.Running: default: } // want missing
 switch count { case s.One: } // want missing
 switch string(status) { case string(s.Running): default: } // want missing
 switch int(count) { case 1: default: } // want missing
 channel := make(chan s.Status, 1)
 channel <- "running" // want named
 _ = local
}
`
	root := fixture(t, map[string]string{"state/state.go": enumSource, "app.go": source})
	output, err := run(t, basePolicy(), root)
	if err == nil || !strings.Contains(err.Error(), "violation") {
		t.Fatalf("expected violations, got %v\n%s", err, output)
	}
	for i, line := range strings.Split(source, "\n") {
		_, wanted, ok := strings.Cut(line, "// want ")
		if !ok {
			continue
		}
		prefix := fmt.Sprintf("app.go:%d:", i+1)
		found := false
		for diagnostic := range strings.SplitSeq(output, "\n") {
			if strings.HasPrefix(diagnostic, prefix) && strings.Contains(diagnostic, wanted) {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q on line %d: %s\n%s", wanted, i+1, line, output)
		}
	}
}

func TestRunFailThenPass(t *testing.T) {
	root := fixture(t, map[string]string{"state.go": `package state
type Status string
const Running Status = "running"
var current Status = "running"
`})
	policy := Policy{Types: []string{".Status"}}
	if _, err := run(t, policy, root); err == nil {
		t.Fatal("literal implementation passed")
	}
	writeFile(t, root, "state.go", `package state
type Status string
const Running Status = "running"
var current Status = Running
`)
	if output, err := run(t, policy, root); err != nil {
		t.Fatalf("named implementation failed: %v\n%s", err, output)
	}
}

func TestEnumDeclarationsMayUseTypedConstantConversions(t *testing.T) {
	root := fixture(t, map[string]string{"state.go": `package state
type Status string
const Running, Counter = Status("running"), 1
const Stopped = Status("stopped")
var current Status = Running
`})
	if output, err := run(t, Policy{Types: []string{".Status"}}, root); err != nil {
		t.Fatalf("typed constant declaration rejected: %v\n%s", err, output)
	}
}

func TestRunConfigurationFailures(t *testing.T) {
	root := fixture(t, map[string]string{"state/state.go": enumSource})
	tests := []struct {
		name string
		edit func(*Policy)
		want string
	}{
		{"empty", func(p *Policy) { p.Types = nil }, "at least one"},
		{"unknown type", func(p *Policy) { p.Types = []string{"state.Missing"} }, "unknown type"},
		{"unknown package", func(p *Policy) { p.Types = []string{"missing.Status"} }, "unknown type"},
		{"unqualified type", func(p *Policy) { p.Types = []string{"Status"} }, "unknown type"},
		{"primitive alias", func(p *Policy) { p.Types = []string{"state.Primitive"} }, "defined string or integer"},
		{"float", func(p *Policy) { p.Types = []string{"state.Float"} }, "defined string or integer"},
		{"bool", func(p *Policy) { p.Types = []string{"state.Boolean"} }, "defined string or integer"},
		{"struct", func(p *Policy) { p.Types = []string{"state.Composite"} }, "defined string or integer"},
		{"empty domain", func(p *Policy) { p.Types = []string{"state.EmptyDomain"} }, "no named enum constants"},
		{"unknown field", func(p *Policy) { p.Fields = map[string]string{"state.Record.Missing": "state.Status"} }, "unknown field"},
		{"unknown struct", func(p *Policy) { p.Fields = map[string]string{"state.Missing.Status": "state.Status"} }, "unknown field"},
		{"unqualified field", func(p *Policy) { p.Fields = map[string]string{"Status": "state.Status"} }, "unknown field"},
		{"method is not field", func(p *Policy) { p.Fields = map[string]string{"state.Record.Method": "state.Status"} }, "unknown field"},
		{"field primitive", func(p *Policy) { p.Fields = map[string]string{"state.Wrong.Status": "state.Status"} }, "must use state.Status"},
		{"field other enum", func(p *Policy) { p.Fields = map[string]string{"state.Record.Count": "state.Status"} }, "must use state.Status"},
		{"unconfigured field enum", func(p *Policy) { p.Fields = map[string]string{"state.Record.Status": "state.Other"} }, "unconfigured enum"},
		{"unknown parser", func(p *Policy) { p.Parsers = map[string]string{"state.Missing": "reason"} }, "unknown parser"},
		{"parser not function", func(p *Policy) { p.Parsers = map[string]string{"state.Running": "reason"} }, "unknown parser"},
		{"parser no reason", func(p *Policy) { p.Parsers = map[string]string{"state.Parse": " "} }, "requires a reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := basePolicy()
			tt.edit(&policy)
			_, err := run(t, policy, root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunAliasesAndFullPaths(t *testing.T) {
	root := fixture(t, map[string]string{"state/state.go": enumSource})
	policy := basePolicy()
	policy.Types = []string{"example.test/enums/state.Alias", "./state.Count"}
	policy.Fields = map[string]string{"example.test/enums/state.Record.Optional": "example.test/enums/state.Alias"}
	if output, err := run(t, policy, root); err != nil {
		t.Fatalf("Run() = %v\n%s", err, output)
	}
}

func TestRunLoadErrors(t *testing.T) {
	for _, source := range []string{"package state\nfunc broken(\n", "package state\nimport _ \"not.available/dependency\"\n"} {
		root := fixture(t, map[string]string{"state.go": source})
		if _, err := run(t, Policy{Types: []string{".Status"}}, root); err == nil || !strings.Contains(err.Error(), "load packages") {
			t.Fatalf("Run() = %v, want package load error", err)
		}
	}
	root := fixture(t, nil)
	if _, err := run(t, Policy{Types: []string{".Status"}}, root); err == nil || !strings.Contains(err.Error(), "no production packages") {
		t.Fatalf("Run() = %v, want no packages", err)
	}
	if _, err := run(t, Policy{Types: []string{".Status"}}, filepath.Join(root, "absent")); err == nil || !strings.Contains(err.Error(), "load packages") {
		t.Fatalf("Run() = %v, want load error", err)
	}
}

func TestParserExceptionsStayBounded(t *testing.T) {
	root := fixture(t, map[string]string{"state.go": `package state
type Status string
const (Running Status = "running"; Stopped Status = "stopped")
func Parse(raw string) Status {
 value := Status(raw)
 switch value { case Running: default: }
 _ = func() Status { return Status(raw) }
 return value
}
`})
	output, err := run(t, Policy{Types: []string{".Status"}, Parsers: map[string]string{".Parse": "Input boundary"}}, root)
	if err == nil || !strings.Contains(output, "missing: Stopped") || !strings.Contains(output, "conversion") {
		t.Fatalf("parser exception escaped scope: %v\n%s", err, output)
	}
}
