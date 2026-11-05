// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/e2e"
)

// E2E is the gate the end-to-end suites run under.
const E2E = "e2e"

// newE2ERunner is the runner the gate runs suites with. A variable so a test
// runs the gate without starting a process or a container.
var newE2ERunner = func(c Ctx) *e2e.Runner {
	return e2e.NewRunner(c.Root, c.GoBin, c.Out, c.Exec)
}

// onPush are the suites that run on a push, which is what the plan and a
// run with no flag mean.
func onPush(cfg *config.Config) []config.E2ESuite {
	var push []config.E2ESuite
	for _, s := range cfg.E2E {
		if s.RunsOn(config.OnPush) {
			push = append(push, s)
		}
	}
	return push
}

// e2eApplies is the gate's subject: a suite that runs on a push.
func e2eApplies(c Ctx) (bool, string, error) {
	if len(c.Cfg.E2E) == 0 {
		return false, "e2e names no suite", nil
	}
	if len(onPush(c.Cfg)) == 0 {
		return false, "every e2e suite runs on a tag", nil
	}
	return true, "", nil
}

// e2eNeeds is what the suites that run on a push want installed: a container
// engine when any starts a service, and each tool they name. lateregate
// fetches the tools itself, so the names are for a pipeline that wants to
// warm a cache; the engine is the one thing a runner must already have.
func e2eNeeds(cfg *config.Config) []string {
	var needs []string
	push := onPush(cfg)
	if slices.ContainsFunc(push, func(s config.E2ESuite) bool { return len(s.Services) > 0 }) {
		needs = append(needs, "docker")
	}
	for _, tool := range config.E2ETools {
		if slices.ContainsFunc(push, func(s config.E2ESuite) bool { return slices.Contains(s.Tools, tool) }) {
			needs = append(needs, "tool:"+tool)
		}
	}
	return needs
}

// e2eSkip is why a change since a base cannot move the verdict of suite s,
// or "" when it can and the suite runs.
//
// A test suite follows the rule the Go gates follow: on CI it is skipped when
// every changed file is inert to Go, and on a laptop when no Go package
// changed. A command suite reads what its reads name, so a change under one
// runs it; otherwise it follows the Go rule too, since the command may run
// Go.
func e2eSkip(c Ctx, s config.E2ESuite) string {
	sc := c.Scope
	if !sc.Narrows() {
		return ""
	}
	if s.Test == nil {
		for _, r := range s.Reads {
			if sc.Touches(filepath.ToSlash(filepath.Clean(r))) {
				return ""
			}
		}
	}
	if sc.CI {
		if sc.GoInert {
			return "every file changed since " + sc.Since() + " is inert to Go and none is under " + reads(s)
		}
		return ""
	}
	if len(sc.Changed) == 0 {
		return "no Go package changed since " + sc.Since() + " and none is under " + reads(s)
	}
	return ""
}

// reads names what a suite reads, for the reason it was skipped.
func reads(s config.E2ESuite) string {
	if s.Test != nil {
		return "the packages it names"
	}
	if len(s.Reads) == 0 {
		return "a directory it reads"
	}
	return strings.Join(s.Reads, ", ")
}

// e2eScoped is the plan's answer for the gate under a change: skipped when
// every suite that runs on a push would be, else run with a note on how many.
func e2eScoped(c Ctx) (skip, note string) {
	push := onPush(c.Cfg)
	var run int
	for _, s := range push {
		if e2eSkip(c, s) == "" {
			run++
		}
	}
	switch {
	case run == 0 && c.Scope.Narrows():
		return "no e2e suite reads anything changed since " + c.Scope.Since(), ""
	case run < len(push):
		return "", fmt.Sprintf("%d of %d suites", run, len(push))
	}
	return "", ""
}

// e2eItems picks the suites a run is asked for and decides which of them the
// change since a base reaches.
func e2eItems(c Ctx) ([]e2e.Item, error) {
	if len(c.Cfg.E2E) == 0 {
		return nil, fmt.Errorf("e2e names no suite to run")
	}
	var picked []config.E2ESuite
	switch {
	case c.Only != "":
		for _, s := range c.Cfg.E2E {
			if s.Name == c.Only {
				picked = append(picked, s)
			}
		}
		if len(picked) == 0 {
			names := make([]string, len(c.Cfg.E2E))
			for i, s := range c.Cfg.E2E {
				names[i] = s.Name
			}
			return nil, fmt.Errorf("e2e names no suite called %q\nsuites: %s", c.Only, strings.Join(names, ", "))
		}
	default:
		event := c.On
		if event == "" {
			event = config.OnPush
		}
		if !slices.Contains(config.E2EEvents, event) {
			return nil, fmt.Errorf("-on takes %s, not %q", strings.Join(config.E2EEvents, " or "), event)
		}
		for _, s := range c.Cfg.E2E {
			if s.RunsOn(event) {
				picked = append(picked, s)
			}
		}
	}
	items := make([]e2e.Item, len(picked))
	for i, s := range picked {
		items[i] = e2e.Item{Suite: s, Skip: e2eSkip(c, s)}
	}
	return items, nil
}

// runE2E is the e2e gate.
func runE2E(c Ctx) error {
	items, err := e2eItems(c)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		event := c.On
		if event == "" {
			event = config.OnPush
		}
		_, _ = fmt.Fprintf(c.Out, "no e2e suite runs on a %s\n", event)
		return nil
	}
	return newE2ERunner(c).RunSignals(items)
}
