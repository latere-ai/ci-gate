// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package cover

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// module writes a throwaway module and returns its root.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.com/m\n\ngo 1.27.0\n"
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGoListerNamesThePackagesThatCanBeMeasured(t *testing.T) {
	root := module(t, map[string]string{
		"logic/logic.go": "package logic\n\nfunc Add(a, b int) int { return a + b }\n",
		// Constants and types hold no statements, so the coverage tool
		// instruments nothing here however the package is tested. Listing it
		// would be a finding no test can clear.
		"kinds/kinds.go": "package kinds\n\ntype Kind string\n\nconst Draft Kind = \"draft\"\n",
		// A function whose body is empty is the same case reached a different
		// way: the tool instruments statements, and there are none to mark.
		"empty/empty.go": "package empty\n\nfunc Nothing() {}\n",
	})

	got, err := GoLister("go", root)()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com/m/logic"}
	if !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v; a package with no statements must be left out", got, want)
	}
}

// go list ./... reaches the Go source a JavaScript package ships under
// node_modules. It is a dependency's, so it is not a package the floor could
// find unmeasured.
func TestGoListerLeavesOutPackagesUnderNodeModules(t *testing.T) {
	root := module(t, map[string]string{
		"logic/logic.go": "package logic\n\nfunc Add(a, b int) int { return a + b }\n",
		"frontend/node_modules/flatted/golang/pkg/flatted/flatted.go": "package flatted\n\nfunc Parse() int { return 1 }\n",
	})

	got, err := GoLister("go", root)()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com/m/logic"}
	if !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v; a package under node_modules is not the module's own", got, want)
	}
}

func TestGoListerFailsOnATreeItCannotList(t *testing.T) {
	root := t.TempDir() // no go.mod, so there is no module to list
	if _, err := GoLister("go", root)(); err == nil {
		t.Fatal("a tree that cannot be listed must fail rather than report no packages")
	}
}

func TestGoListerFailsOnUnparseableSource(t *testing.T) {
	root := module(t, map[string]string{"broken/broken.go": "package broken\n\nfunc ("})
	if _, err := GoLister("go", root)(); err == nil {
		t.Fatal("source the gate cannot parse must fail rather than count as no statements")
	}
}

// Narrowed, the list keeps the named packages it holds and drops the rest,
// and a list that fails still fails.
func TestOnlyKeepsTheNamedPackages(t *testing.T) {
	all := func() ([]string, error) {
		return []string{"example.com/m/a", "example.com/m/b", "example.com/m/c"}, nil
	}
	got, err := Only(all, []string{"example.com/m/c", "example.com/m/a", "example.com/m/gone"})()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"example.com/m/a", "example.com/m/c"}) {
		t.Errorf("kept %v", got)
	}
	broken := func() ([]string, error) { return nil, os.ErrPermission }
	if _, err := Only(broken, []string{"example.com/m/a"})(); err == nil {
		t.Error("a list that fails must fail narrowed")
	}
}
