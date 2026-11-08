// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package identity

import (
	"strings"
	"testing"
	"time"

	"latere.ai/x/ci-gate/internal/config"
)

// The closing line counts the rules that held and names the skipped ones
// separately, so a rule that found nothing to read is not reported as held.
func TestTheSummaryCountsOnlyTheRulesThatRan(t *testing.T) {
	root := repo(t, map[string]string{"README.md": "# nothing here\n"})
	out, err := run(t, config.Identity{Role: config.RoleService, Audience: "svc"}, root, noHistory())
	if err != nil {
		t.Fatal(err)
	}
	want := "role service holds 2 rule(s) of the shape; 5 skipped, not checked"
	if !strings.Contains(out, want) {
		t.Errorf("the closing line should say %q:\n%s", want, out)
	}
}

// A waived rule that still has findings is named, not counted as held.
func TestTheSummaryNamesAWaivedRuleWithFindings(t *testing.T) {
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai",
		Waive: map[string]config.Waiver{"no-latere-value": {Until: "2999-01-01", Reason: "later"}}}
	root := repo(t, map[string]string{
		"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.latere.ai\n"),
	})
	out, err := run(t, core, root, noHistory())
	if err != nil {
		t.Fatal(err)
	}
	want := "role core holds 1 rule(s) of the shape; 9 skipped, not checked; 1 waived with findings"
	if !strings.Contains(out, want) {
		t.Errorf("the closing line should say %q:\n%s", want, out)
	}
}

// When every rule of the role skipped, the line says nothing was checked
// rather than claiming the role holds its rules. The gate still passes.
func TestTheSummarySaysWhenEveryRuleWasSkipped(t *testing.T) {
	root := repo(t, map[string]string{"notes.txt": "nothing here\n"})
	out, err := run(t, config.Identity{Role: config.RoleClient}, root, noHistory())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "PASS ") {
		t.Skipf("the fixture no longer skips every client rule:\n%s", out)
	}
	if !strings.Contains(out, "all 5 rule(s) were skipped, so nothing of the shape was checked") {
		t.Errorf("an all-skipped run must say so:\n%s", out)
	}
}

// A waiver's last day is judged by the config package's Live, the one
// definition every gate shares: until today holds today and not tomorrow.
func TestAWaiverDateIsJudgedByTheSharedDefinition(t *testing.T) {
	waiver := config.Waiver{Until: "2026-09-13", Reason: "today"}
	core := config.Identity{Role: config.RoleCore, Audience: "cella", ConfigPrefix: "CELLA", APIGroup: "cella.latere.ai",
		Waive: map[string]config.Waiver{"no-latere-value": waiver}, Present: true}
	root := repo(t, map[string]string{
		"deploy/base/app.yaml": deployment("        - name: CELLA_OIDC_ISSUERS\n          value: https://auth.latere.ai\n"),
	})
	for _, now := range []time.Time{
		time.Date(2026, 9, 13, 23, 59, 0, 0, time.UTC),
		time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC),
	} {
		var sb strings.Builder
		_ = Run(core, root, &sb, noHistory(), now)
		if got := strings.Contains(sb.String(), "WAIV no-latere-value"); got != waiver.Live(now) {
			t.Errorf("at %s the gate says waived=%v, config says live=%v", now, got, waiver.Live(now))
		}
	}
}
