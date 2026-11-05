// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"latere.ai/x/ci-gate/internal/cover"
	"latere.ai/x/ci-gate/internal/gates"
	"latere.ai/x/ci-gate/internal/golangci"
)

// execIn is the Exec for commands run in dir, a slash path below the root.
func (c Ctx) execIn(dir string) gates.Exec {
	if c.ExecIn != nil {
		return c.ExecIn(dir)
	}
	return gates.OSExec(filepath.Join(c.Root, filepath.FromSlash(dir)), c.Out)
}

// moduleSkip is a listed module a check leaves out, and why.
type moduleSkip struct{ Dir, Why string }

// selectModules splits the modules the configuration lists into those a
// check runs in and those it skips. Measured against a change on a laptop,
// a module runs when a changed file lies inside its directory, its go.mod
// and go.sum included, since nothing in the root's import graph reaches it.
// Every other run, CI with a base among them, runs them all: the gates that
// read Go there already decide for the whole repository whether a change
// can move them, and a file inside a module is never inert to Go.
func selectModules(c Ctx) (run []string, skipped []moduleSkip) {
	for _, dir := range c.Cfg.Modules {
		if c.Scope.Packages() && !c.Scope.Touches(dir) {
			skipped = append(skipped, moduleSkip{dir, "no file under it changed since " + c.Scope.Since()})
			continue
		}
		run = append(run, dir)
	}
	return run, skipped
}

// rootSkipped is the reason a check leaves the root module alone while it
// runs in modules, or "". A change that touches only a module leaves the
// root's package list empty, and an empty list would read the whole root.
func rootSkipped(c Ctx, modules []string) string {
	if len(modules) > 0 && c.Scope.Packages() && len(c.Scope.Changed) == 0 {
		return "no Go package of the root module changed since " + c.Scope.Since()
	}
	return ""
}

// requireModules fails when a listed module has no tracked go.mod. A name
// that points at nothing is a module nobody tests, which is the gap this
// key exists to close, so it is an error and never a skip.
func requireModules(c Ctx) error {
	if len(c.Cfg.Modules) == 0 {
		return nil
	}
	args := []string{"ls-files", "-z", "--"}
	for _, dir := range c.Cfg.Modules {
		args = append(args, dir+"/go.mod")
	}
	out, err := c.Exec(nil, false, "git", args...)
	if err != nil {
		return fmt.Errorf("cannot list the modules' go.mod files with git: %w", err)
	}
	tracked := map[string]bool{}
	for f := range strings.SplitSeq(string(out), "\x00") {
		tracked[f] = true
	}
	for _, dir := range c.Cfg.Modules {
		if !tracked[dir+"/go.mod"] {
			return fmt.Errorf("modules names %s, and git tracks no %s/go.mod there", dir, dir)
		}
	}
	return nil
}

// inModules runs a gate in the root module and then in each listed module
// a check reaches, and fails if any did. One failing module does not stop
// the next: a run that fixes one module per push is a run that learns about
// the others a push late.
//
// With no module listed the root's own error is returned as it is.
func inModules(c Ctx, root func() error, module func(dir string) error) error {
	if err := requireModules(c); err != nil {
		return err
	}
	if len(c.Cfg.Modules) == 0 {
		return root()
	}
	run, skipped := selectModules(c)
	var failures []string
	if why := rootSkipped(c, run); why != "" {
		_, _ = fmt.Fprintln(c.Out, "root module skipped: "+why)
	} else {
		_, _ = fmt.Fprintln(c.Out, "root module")
		if err := root(); err != nil {
			failures = append(failures, "the root module: "+err.Error())
		}
	}
	for _, s := range skipped {
		_, _ = fmt.Fprintf(c.Out, "module %s skipped: %s\n", s.Dir, s.Why)
	}
	for _, dir := range run {
		_, _ = fmt.Fprintln(c.Out, "module "+dir)
		if err := module(dir); err != nil {
			failures = append(failures, "module "+dir+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\nand "))
	}
	return nil
}

// moduleRun is the suite run for the module at dir: the root's run with its
// every property, over the whole module, and with the floor read from the
// module's own profile and packages.
func moduleRun(c Ctx, base gates.SuiteRun, dir string, exec gates.Exec) (gates.SuiteRun, error) {
	run := base
	run.Packages = nil
	run.Home = c.Root
	if base.Floor == nil {
		return run, nil
	}
	out, err := exec(nil, false, c.GoBin, "list", "-m")
	if err != nil {
		return run, fmt.Errorf("go list cannot name the module at %s: %w", dir, err)
	}
	cfg := c.Cfg.Cover
	cfg.Module, cfg.Dir = strings.TrimSpace(string(out)), dir
	list := cover.GoLister(c.GoBin, filepath.Join(c.Root, filepath.FromSlash(dir)))
	run.Floor = func(profile string, _ gates.Tester) error {
		return cover.Run(cfg, []string{profile}, c.Out, list)
	}
	return run, nil
}

// suiteInModule is the suite in the module at dir.
func suiteInModule(c Ctx, base gates.SuiteRun, dir string) error {
	exec := c.execIn(dir)
	run, err := moduleRun(c, base, dir, exec)
	if err != nil {
		return err
	}
	return gates.Suite(run, filepath.Join(c.Root, filepath.FromSlash(dir)), c.GoBin, c.Out, exec)
}

// lintModule is the linter in the module at dir, with the shared
// configuration rendered for the module's own path.
//
// A repository that keeps its own configuration keeps one file at the
// root, which the linter does not read from below it, so its modules are
// left to the root's run and the output says so.
func lintModule(c Ctx, dir string) error {
	if why := strings.TrimSpace(c.Cfg.Golangci.Own); why != "" {
		_, _ = fmt.Fprintf(c.Out, "lint reads the root module only: %s is this repository's own (%s)\n", golangci.Name, why)
		return nil
	}
	abs := filepath.Join(c.Root, filepath.FromSlash(dir))
	return golangci.Lint(abs, c.Cfg, c.GoBin, c.Out, c.execIn(dir))
}

// moduleNote is what the plan says of the modules a scoped check reaches:
// "" when none is listed.
func moduleNote(c Ctx) string {
	run, _ := selectModules(c)
	if len(run) == 0 {
		return ""
	}
	return count(len(run), "module") + " it touches: " + strings.Join(run, ", ")
}
