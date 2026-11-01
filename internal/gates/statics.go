// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// The tools the static gates run, each pinned once. actionlint and kustomize
// run through `go run module@version`, so the Go checksum database verifies
// what is built; promtool's module cannot be run that way (its go.mod holds
// replace directives), so its release archive is fetched and checked against
// the digest pinned in promtoolAssets.
const (
	ActionlintVersion = "v1.7.12"
	KustomizeVersion  = "v5.7.1"
	PromtoolVersion   = "v3.15.1"
)

// trackedFiles lists the files git tracks that match pathspecs, skipping any
// under a directory named in skip.
func trackedFiles(run Exec, what string, skip []string, pathspecs ...string) ([]string, error) {
	out, err := run(nil, false, "git", append([]string{"ls-files", "--"}, pathspecs...)...)
	if err != nil {
		return nil, fmt.Errorf("cannot list %s with git: %w", what, err)
	}
	var files []string
	for line := range strings.SplitSeq(string(out), "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		parts := strings.Split(path.Dir(p), "/")
		if slices.ContainsFunc(skip, func(s string) bool { return slices.Contains(parts, s) }) {
			continue
		}
		files = append(files, p)
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// GoModules lists the directories of the Go modules git tracks, outside
// testdata (fixtures, not modules anyone builds) and vendor.
func GoModules(run Exec) ([]string, error) {
	files, err := trackedFiles(run, "Go modules", []string{"testdata", "vendor"}, "go.mod", "*/go.mod")
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(files))
	for _, f := range files {
		if path.Base(f) == "go.mod" {
			dirs = append(dirs, path.Dir(f))
		}
	}
	return dirs, nil
}

// Tidy fails when `go mod tidy` would change any tracked module's go.mod or
// go.sum. A module that is not tidy builds today and stops building the day
// a requirement it leans on implicitly moves.
func Tidy(goBin string, out io.Writer, run Exec) error {
	dirs, err := GoModules(run)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		return errors.New("git tracks no go.mod to check")
	}
	var untidy []string
	for _, d := range dirs {
		if _, err := run(nil, true, goBin, "-C", d, "mod", "tidy", "-diff"); err != nil {
			untidy = append(untidy, d)
		}
	}
	if len(untidy) > 0 {
		return fmt.Errorf("go mod tidy would change %d of %d modules: %s; "+
			"run go mod tidy in each and commit the result",
			len(untidy), len(dirs), strings.Join(untidy, ", "))
	}
	_, _ = fmt.Fprintf(out, "%d module(s) are tidy\n", len(dirs))
	return nil
}

// Workflows lists the GitHub Actions workflow files git tracks.
func Workflows(run Exec) ([]string, error) {
	files, err := trackedFiles(run, "workflows", nil, ".github/workflows/*.yml", ".github/workflows/*.yaml")
	if err != nil {
		return nil, err
	}
	var flows []string
	for _, f := range files {
		if path.Dir(f) == ".github/workflows" {
			flows = append(flows, f)
		}
	}
	return flows, nil
}

// Actionlint lints every tracked workflow with actionlint at
// ActionlintVersion, which reads .github/actionlint.yaml when the repository
// has one. Its shellcheck and pyflakes passes are off: they run whatever
// shellcheck the machine happens to have, so a laptop and a runner would
// disagree, which is the drift this binary exists to remove.
func Actionlint(goBin string, out io.Writer, run Exec) error {
	flows, err := Workflows(run)
	if err != nil {
		return err
	}
	if len(flows) == 0 {
		return errors.New("git tracks no workflow under .github/workflows to lint")
	}
	args := append([]string{"run", "github.com/rhysd/actionlint/cmd/actionlint@" + ActionlintVersion,
		"-shellcheck=", "-pyflakes="}, flows...)
	if _, err := run(nil, true, goBin, args...); err != nil {
		return fmt.Errorf("actionlint %s found a problem in the workflows: %w", ActionlintVersion, err)
	}
	_, _ = fmt.Fprintf(out, "%d workflow(s) pass actionlint\n", len(flows))
	return nil
}

// kustomizationNames are the file names that make a directory a kustomization.
var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// Kustomizations lists the directories of the kustomizations git tracks that
// render on their own. A Component is left out: it is rendered only through
// the kustomization that includes it, and `kustomize build` refuses one by
// itself. read gives a tracked file's contents.
func Kustomizations(run Exec, read func(string) ([]byte, error)) ([]string, error) {
	var specs []string
	for _, n := range kustomizationNames {
		specs = append(specs, n, "*/"+n)
	}
	files, err := trackedFiles(run, "kustomizations", []string{"testdata"}, specs...)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, f := range files {
		if !slices.Contains(kustomizationNames, path.Base(f)) {
			continue
		}
		body, err := read(f)
		if errors.Is(err, fs.ErrNotExist) {
			// Tracked and deleted in the working tree: nothing to render.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
		if isComponent(body) {
			continue
		}
		if d := path.Dir(f); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs, nil
}

// isComponent reports whether a kustomization file declares kind: Component.
func isComponent(body []byte) bool {
	for line := range strings.SplitSeq(string(body), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && k == "kind" && strings.Trim(strings.TrimSpace(v), `"'`) == "Component" {
			return true
		}
	}
	return false
}

// Manifests renders every tracked kustomization with kustomize at
// KustomizeVersion and fails on any that does not render: a patch whose
// target is gone, a resource that moved, a base an overlay can no longer
// reach. A release applies the rendered tree, so one that cannot render is
// found on the tag otherwise.
func Manifests(goBin, root string, out io.Writer, run Exec) error {
	dirs, err := Kustomizations(run, func(f string) ([]byte, error) { return os.ReadFile(filepath.Join(root, f)) })
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		return errors.New("git tracks no kustomization to render")
	}
	var broken []string
	for _, d := range dirs {
		_, _ = fmt.Fprintf(out, "kustomize build %s\n", d)
		if _, err := run(nil, true, goBin, "run", "sigs.k8s.io/kustomize/kustomize/v5@"+KustomizeVersion,
			"build", d, "--output", os.DevNull); err != nil {
			broken = append(broken, d)
		}
	}
	if len(broken) > 0 {
		return fmt.Errorf("kustomize %s cannot render %d of %d kustomizations: %s",
			KustomizeVersion, len(broken), len(dirs), strings.Join(broken, ", "))
	}
	_, _ = fmt.Fprintf(out, "%d kustomization(s) render\n", len(dirs))
	return nil
}
