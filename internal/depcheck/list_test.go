// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package depcheck

import (
	"slices"
	"strings"
	"testing"
)

// GoLister is the process wiring, so it is exercised against a real toolchain
// and this repository's own build rather than a fake.
func TestGoListerReportsDependenciesOutsideTheModule(t *testing.T) {
	deps, err := GoLister("go", "..")("", "", "latere.ai/x/ci-gate/internal/config")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(deps, "github.com/goccy/go-yaml") {
		t.Errorf("config's build reaches go-yaml; got %v", deps)
	}
	// The module is not a dependency of itself, and neither is the standard
	// library, which has no module at all.
	for _, d := range deps {
		if strings.HasPrefix(d, "latere.ai/x/ci-gate") {
			t.Errorf("%s is this module, not a dependency", d)
		}
		if d == "fmt" || d == "strings" {
			t.Errorf("%s is the standard library", d)
		}
	}
}

func TestGoListerCrossCompiles(t *testing.T) {
	if _, err := GoLister("go", "..")("windows", "amd64", "latere.ai/x/ci-gate/internal/config"); err != nil {
		t.Fatalf("listing for another platform should work: %v", err)
	}
}

func TestGoListerReportsAPackageThatDoesNotExist(t *testing.T) {
	_, err := GoLister("go", "..")("", "", "latere.ai/x/ci-gate/internal/nosuchpackage")
	if err == nil {
		t.Fatal("a missing package must be an error")
	}
}

func TestGoListerReportsAMissingToolchain(t *testing.T) {
	if _, err := GoLister("no-such-go-binary", "..")("", "", "x"); err == nil {
		t.Fatal("a missing toolchain must be an error")
	}
}
