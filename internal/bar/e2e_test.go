// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package bar

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/e2e"
	"latere.ai/x/ci-gate/internal/scope"
)

func goSuite(name string, on ...string) config.E2ESuite {
	return config.E2ESuite{Name: name, Why: "proves " + name, Test: &config.E2ETest{Packages: []string{"./x"}}, On: on}
}

func cmdSuite(name string, reads ...string) config.E2ESuite {
	return config.E2ESuite{Name: name, Why: "proves " + name, Command: "make " + name, Reads: reads}
}

func e2eCtx(t *testing.T, suites ...config.E2ESuite) Ctx {
	t.Helper()
	c, _ := ctx(t, nil, lake)
	c.Cfg.E2E = suites
	return c
}

// The gate has a subject when some suite runs on a push, and says why not
// otherwise.
func TestE2EAppliesWhenASuiteRunsOnPush(t *testing.T) {
	for name, tc := range map[string]struct {
		suites []config.E2ESuite
		status Status
		reason string
	}{
		"none":     {nil, Skip, "e2e names no suite"},
		"all tags": {[]config.E2ESuite{goSuite("a", "tag")}, Skip, "every e2e suite runs on a tag"},
		"a push":   {[]config.E2ESuite{goSuite("a", "tag"), goSuite("b")}, Run, ""},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := Plan(e2eCtx(t, tc.suites...))
			if err != nil {
				t.Fatal(err)
			}
			if e := entry(plan, E2E); e.Status != tc.status || e.Reason != tc.reason {
				t.Errorf("%s %q", e.Status, e.Reason)
			}
		})
	}
	names := Names()
	if i := slices.Index(names, Suite); names[i+1] != E2E {
		t.Errorf("e2e does not follow the suite: %v", names)
	}
}

// The plan asks for a container engine when a suite that runs on a push
// starts a service, and for each tool such a suite names.
func TestE2ENeedsADockerAndTheToolsOfPushSuites(t *testing.T) {
	db := goSuite("db")
	db.Services = []string{config.ServicePostgres}
	walk := cmdSuite("walk")
	walk.Tools = []string{config.ToolHelm, config.ToolKubectl}
	tagged := cmdSuite("tagged")
	tagged.Tools = []string{config.ToolKind}
	tagged.Services = []string{config.ServiceMinIO}
	tagged.On = []string{config.OnTag}
	for suites, want := range map[string][]string{
		"db":   {"docker"},
		"walk": {"tool:kubectl", "tool:helm"},
		"both": {"docker", "tool:kubectl", "tool:helm"},
		"tag":  nil,
	} {
		var set []config.E2ESuite
		switch suites {
		case "db":
			set = []config.E2ESuite{db}
		case "walk":
			set = []config.E2ESuite{walk}
		case "both":
			set = []config.E2ESuite{db, walk, tagged}
		case "tag":
			set = []config.E2ESuite{goSuite("lone"), tagged}
		}
		plan, err := Plan(e2eCtx(t, set...))
		if err != nil {
			t.Fatal(err)
		}
		if got := entry(plan, E2E).Needs; !slices.Equal(got, want) {
			t.Errorf("%s: needs %v, want %v", suites, got, want)
		}
	}
}

// node and bun are listed by their bare names, after the fetched tools, and
// only for a suite that runs on a push.
func TestE2ENeedsNodeAndBunByBareName(t *testing.T) {
	kindBun := cmdSuite("kind-bun")
	kindBun.Tools = []string{config.ToolKind, config.ToolBun}
	full := cmdSuite("full")
	full.Services = []string{config.ServicePostgres}
	full.Tools = []string{config.ToolBun, config.ToolNode}
	tagged := cmdSuite("tagged")
	tagged.Tools = []string{config.ToolBun}
	tagged.On = []string{config.OnTag}
	for name, tc := range map[string]struct {
		suites []config.E2ESuite
		want   []string
	}{
		"kind and bun": {[]config.E2ESuite{kindBun}, []string{"tool:kind", "bun"}},
		"services":     {[]config.E2ESuite{full}, []string{"docker", "node", "bun"}},
		"tag-only bun": {[]config.E2ESuite{goSuite("lone"), tagged}, nil},
	} {
		plan, err := Plan(e2eCtx(t, tc.suites...))
		if err != nil {
			t.Fatal(err)
		}
		if got := entry(plan, E2E).Needs; !slices.Equal(got, tc.want) {
			t.Errorf("%s: needs %v, want %v", name, got, tc.want)
		}
	}
}

// Under a change, a test suite follows the Go rule, a command suite runs when
// a change is under its reads, and the gate is skipped only when every suite
// would be.
func TestE2EFollowsTheChange(t *testing.T) {
	lux, walk := goSuite("lux"), cmdSuite("walk", "docs/install.md", "deploy")
	laptopGo := goChange()
	laptopDocs := change("docs/other.md")
	laptopDeploy := change("deploy/values.yaml")
	ciInert := ciChange(true, "specs/a.md")
	ciInertDeploy := ciChange(true, "deploy/values.yaml")
	ciGo := ciChange(false, "store/store.go")
	for name, tc := range map[string]struct {
		scope   *scope.Scope
		suites  []config.E2ESuite
		status  Status
		reason  string
		skipped []string
	}{
		"laptop go change":       {laptopGo, []config.E2ESuite{lux, walk}, Run, "", nil},
		"laptop docs only":       {laptopDocs, []config.E2ESuite{lux, walk}, Skip, "no e2e suite reads anything changed since origin/main (222222222222)", []string{"lux", "walk"}},
		"laptop under reads":     {laptopDeploy, []config.E2ESuite{lux, walk}, Run, "1 of 2 suites", []string{"lux"}},
		"laptop only the test":   {laptopDeploy, []config.E2ESuite{lux}, Skip, "no e2e suite reads anything changed since origin/main (222222222222)", []string{"lux"}},
		"ci inert":               {ciInert, []config.E2ESuite{lux, walk}, Skip, "no e2e suite reads anything changed since 222222222222", []string{"lux", "walk"}},
		"ci inert under reads":   {ciInertDeploy, []config.E2ESuite{lux, walk}, Run, "1 of 2 suites", []string{"lux"}},
		"ci go":                  {ciGo, []config.E2ESuite{lux, walk}, Run, "", nil},
		"whole tree":             {scope.WholeTree("go.mod changed"), []config.E2ESuite{lux, walk}, Run, "", nil},
		"a tag suite is no vote": {laptopDocs, []config.E2ESuite{goSuite("t", "tag"), walk}, Skip, "no e2e suite reads anything changed since origin/main (222222222222)", nil},
	} {
		t.Run(name, func(t *testing.T) {
			c := e2eCtx(t, tc.suites...)
			c.Scope = tc.scope
			plan, err := Plan(c)
			if err != nil {
				t.Fatal(err)
			}
			if e := entry(plan, E2E); e.Status != tc.status || e.Reason != tc.reason {
				t.Errorf("%s %q, want %s %q", e.Status, e.Reason, tc.status, tc.reason)
			}
			items, err := e2eItems(c)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, it := range items {
				if it.Skip != "" {
					got = append(got, it.Suite.Name)
				}
			}
			if tc.skipped != nil && !slices.Equal(got, tc.skipped) {
				t.Errorf("skipped %v, want %v", got, tc.skipped)
			}
		})
	}
}

// The reason a suite is skipped says what it reads.
func TestE2ESkipReasonsNameWhatTheSuiteReads(t *testing.T) {
	c := e2eCtx(t)
	c.Scope = change("docs/other.md")
	for s, want := range map[string]string{
		"test":    "no Go package changed since origin/main (222222222222) and none is under the packages it names",
		"reads":   "none is under docs/install.md, deploy",
		"noreads": "none is under a directory it reads",
	} {
		var suite config.E2ESuite
		switch s {
		case "test":
			suite = goSuite("a")
		case "reads":
			suite = cmdSuite("a", "docs/install.md", "deploy")
		case "noreads":
			suite = cmdSuite("a")
		}
		if got := e2eSkip(c, suite); !strings.Contains(got, want) {
			t.Errorf("%s: %q lacks %q", s, got, want)
		}
	}
	c.Scope = ciChange(true, "docs/other.md")
	if got := e2eSkip(c, goSuite("a")); !strings.Contains(got, "is inert to Go") {
		t.Errorf("ci: %q", got)
	}
}

// startsNothing is a runner whose processes record their command and pass.
func startsNothing(c Ctx, ran *[]string) *e2e.Runner {
	return &e2e.Runner{
		Root: c.Root, GoBin: "go", Out: c.Out, Env: nil,
		Start: func(_ context.Context, p e2e.Proc) error {
			*ran = append(*ran, p.Name+" "+strings.Join(p.Args, " "))
			if p.Name == "go" {
				_, err := io.WriteString(p.Stdout, `{"Action":"pass","Package":"p","Test":"T","Elapsed":0.1}`+"\n")
				return err
			}
			return nil
		},
		LookPath: func(string) (string, error) { return "", io.EOF },
	}
}

// lateregate e2e runs the push suites, -only runs the one named whatever it
// runs on, and -on tag the tag suites.
func TestE2ERunsTheSuitesAskedFor(t *testing.T) {
	c := e2eCtx(t, goSuite("lux"), cmdSuite("walk"), goSuite("release", "tag"))
	var ran []string
	old := newE2ERunner
	newE2ERunner = func(c Ctx) *e2e.Runner { return startsNothing(c, &ran) }
	t.Cleanup(func() { newE2ERunner = old })
	g, _ := Find(E2E)

	for _, tc := range []struct {
		only, on string
		want     int
	}{{"", "", 2}, {"release", "", 1}, {"walk", "", 1}, {"", "tag", 1}, {"", "push", 2}} {
		ran = nil
		c.Only, c.On = tc.only, tc.on
		if err := g.Run(c); err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		if len(ran) != tc.want {
			t.Errorf("%+v ran %v", tc, ran)
		}
	}

	c.Only, c.On = "nope", ""
	if err := g.Run(c); err == nil || !strings.Contains(err.Error(), `no suite called "nope"`) || !strings.Contains(err.Error(), "lux, walk, release") {
		t.Errorf("unknown suite: %v", err)
	}
	c.Only, c.On = "", "pull"
	if err := g.Run(c); err == nil || !strings.Contains(err.Error(), "-on takes push or tag") {
		t.Errorf("unknown event: %v", err)
	}
	c.Cfg.E2E = nil
	c.Only, c.On = "", ""
	if err := g.Run(c); err == nil || !strings.Contains(err.Error(), "names no suite") {
		t.Errorf("no suites: %v", err)
	}
}

// A run for an event no suite runs on says so and passes.
func TestE2ESaysWhenNoSuiteRunsOnAnEvent(t *testing.T) {
	c := e2eCtx(t, goSuite("lux"))
	c.On = "tag"
	var ran []string
	old := newE2ERunner
	newE2ERunner = func(c Ctx) *e2e.Runner { return startsNothing(c, &ran) }
	t.Cleanup(func() { newE2ERunner = old })
	g, _ := Find(E2E)
	if err := g.Run(c); err != nil || len(ran) != 0 {
		t.Fatalf("%v %v", err, ran)
	}
	if out := c.Out.(*strings.Builder).String(); !strings.Contains(out, "no e2e suite runs on a tag") {
		t.Errorf("output %q", out)
	}
}

// The real runner is the machine's.
func TestE2EDefaultRunnerIsTheMachines(t *testing.T) {
	c := e2eCtx(t)
	if r := newE2ERunner(c); r.Root != c.Root || r.Start == nil {
		t.Errorf("%+v", r)
	}
}

// Check runs the gate in its place and names the suites it left out.
func TestCheckPrintsTheSuitesItLeavesOut(t *testing.T) {
	c := e2eCtx(t, goSuite("lux"), cmdSuite("walk", "deploy"))
	c.Scope = change("deploy/values.yaml")
	var ran []string
	old := newE2ERunner
	newE2ERunner = func(c Ctx) *e2e.Runner { return startsNothing(c, &ran) }
	t.Cleanup(func() { newE2ERunner = old })
	g, _ := Find(E2E)
	if err := g.Run(c); err != nil {
		t.Fatal(err)
	}
	out := c.Out.(*strings.Builder).String()
	if !strings.Contains(out, "SKIP lux: no Go package changed since origin/main (222222222222)") || len(ran) != 1 || !strings.HasPrefix(ran[0], "sh -c make walk") {
		t.Errorf("ran %v\n%s", ran, out)
	}
}
