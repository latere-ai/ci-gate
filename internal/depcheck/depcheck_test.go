// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package depcheck

import (
	"errors"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

const pkg = "example.com/m/server"

// lister replays a canned build list, and records what it was asked.
func lister(t *testing.T, byPlatform map[string][]string) (Lister, *[]string) {
	t.Helper()
	var asked []string
	return func(goos, goarch, p string) ([]string, error) {
		key := goos + "/" + goarch
		asked = append(asked, key+" "+p)
		deps, ok := byPlatform[key]
		if !ok {
			return byPlatform["*"], nil
		}
		return deps, nil
	}, &asked
}

func gated(allow map[string]string) config.Depcheck {
	return config.Depcheck{
		Packages: map[string]config.Gated{
			pkg: {Decision: "009-D14", Allow: allow},
		},
	}
}

func TestAnAdmittedDependencyPasses(t *testing.T) {
	l, _ := lister(t, map[string][]string{"*": {"golang.org/x/text/unicode/norm"}})
	var sb strings.Builder
	err := Run(gated(map[string]string{"golang.org/x/text": "Unicode normalisation"}), &sb, l)
	if err != nil {
		t.Fatalf("an admitted dependency should pass: %v\n%s", err, sb.String())
	}
}

// The failure the gate exists for: one upstream import arrives on the next
// `go get` and nothing else notices.
func TestADependencyNobodyAdmittedFails(t *testing.T) {
	l, _ := lister(t, map[string][]string{"*": {"github.com/some/orm"}})
	var sb strings.Builder
	err := Run(gated(map[string]string{"golang.org/x/text": "why"}), &sb, l)
	if err == nil {
		t.Fatal("an unadmitted dependency must fail")
	}
	if !strings.Contains(sb.String(), "github.com/some/orm") {
		t.Errorf("the report must name the dependency:\n%s", sb.String())
	}
	// The message sends a reader to the argument, not to the config.
	if !strings.Contains(sb.String(), "009-D14") {
		t.Errorf("the report must name the decision that owns the list:\n%s", sb.String())
	}
}

// A stale allowance sits there admitting whatever later moves under it.
func TestAnAllowanceTheBuildDoesNotReachFails(t *testing.T) {
	l, _ := lister(t, map[string][]string{"*": {"golang.org/x/text/unicode/norm"}})
	var sb strings.Builder
	err := Run(gated(map[string]string{
		"golang.org/x/text": "reached",
		"golang.org/x/sync": "not reached any more",
	}), &sb, l)
	if err == nil {
		t.Fatal("a stale allowance must fail")
	}
	if !strings.Contains(sb.String(), "golang.org/x/sync") {
		t.Errorf("the report must name the stale entry:\n%s", sb.String())
	}
}

// A prefix admits its subtree and nothing else: a sibling whose path starts
// with the same bytes is a different module.
func TestAPrefixAdmitsItsSubtreeAndNothingElse(t *testing.T) {
	l, _ := lister(t, map[string][]string{"*": {"golang.org/x/textual/thing"}})
	var sb strings.Builder
	if err := Run(gated(map[string]string{"golang.org/x/text": "why"}), &sb, l); err == nil {
		t.Fatal("x/textual is not x/text")
	}
}

func TestAnExactMatchIsAdmitted(t *testing.T) {
	l, _ := lister(t, map[string][]string{"*": {"golang.org/x/text"}})
	var sb strings.Builder
	if err := Run(gated(map[string]string{"golang.org/x/text": "why"}), &sb, l); err != nil {
		t.Fatalf("the prefix itself must be admitted: %v", err)
	}
}

// A dependency reached only on darwin is invisible to a linux-only gate.
func TestEveryPlatformIsAsked(t *testing.T) {
	cfg := gated(map[string]string{"golang.org/x/text": "why"})
	cfg.Platforms = []string{"linux/amd64", "darwin/arm64"}
	l, asked := lister(t, map[string][]string{
		"linux/amd64":  {"golang.org/x/text"},
		"darwin/arm64": {"golang.org/x/text", "github.com/ebitengine/purego"},
	})
	var sb strings.Builder
	err := Run(cfg, &sb, l)
	if err == nil {
		t.Fatal("a darwin-only dependency must still fail")
	}
	if len(*asked) != 2 {
		t.Errorf("asked %v, want both platforms", *asked)
	}
	if !strings.Contains(sb.String(), "on darwin/arm64") {
		t.Errorf("the report must say which platform:\n%s", sb.String())
	}
}

func TestNoPlatformsMeansTheHost(t *testing.T) {
	l, asked := lister(t, map[string][]string{"*": {"golang.org/x/text"}})
	var sb strings.Builder
	if err := Run(gated(map[string]string{"golang.org/x/text": "why"}), &sb, l); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 || !strings.HasPrefix((*asked)[0], "/ ") {
		t.Errorf("asked %v, want one call with an unset GOOS/GOARCH", *asked)
	}
}

func TestAMalformedPlatformIsAnError(t *testing.T) {
	cfg := gated(map[string]string{"a": "why"})
	cfg.Platforms = []string{"linux"}
	l, _ := lister(t, nil)
	if err := Run(cfg, &strings.Builder{}, l); err == nil {
		t.Fatal("a platform that is not GOOS/GOARCH must be an error")
	}
}

// A package that does not build is a build error, not a violation.
func TestAnUnbuildablePackageIsAnErrorNotAViolation(t *testing.T) {
	l := Lister(func(string, string, string) ([]string, error) {
		return nil, errors.New("no Go files")
	})
	err := Run(gated(map[string]string{"a": "why"}), &strings.Builder{}, l)
	if err == nil || !strings.Contains(err.Error(), "go list -deps") {
		t.Fatalf("want a build error naming the command, got %v", err)
	}
}

func TestNoGatedPackagesIsANoop(t *testing.T) {
	var sb strings.Builder
	if err := Run(config.Depcheck{}, &sb, nil); err != nil {
		t.Fatalf("an unconfigured gate should do nothing: %v", err)
	}
	if !strings.Contains(sb.String(), "nothing to check") {
		t.Errorf("it should say so:\n%s", sb.String())
	}
}

func TestAGateWithNoDecisionStillReports(t *testing.T) {
	cfg := config.Depcheck{Packages: map[string]config.Gated{
		pkg: {Allow: map[string]string{"golang.org/x/text": "why"}},
	}}
	l, _ := lister(t, map[string][]string{"*": {"github.com/some/orm", "golang.org/x/text"}})
	var sb strings.Builder
	if err := Run(cfg, &sb, l); err == nil {
		t.Fatal("still a violation without a decision label")
	}
	if !strings.Contains(sb.String(), "its allowlist") {
		t.Errorf("report:\n%s", sb.String())
	}
}
