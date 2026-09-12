// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

func TestEnumPoliciesLoad(t *testing.T) {
	c, err := Load(write(t, `enums:
  go:
    types: [internal/state.Status, .Mode]
    fields: {internal/job.Job.Status: internal/state.Status}
    parsers: {internal/state.Parse: validates a wire status before conversion}
  typescript:
    - project: frontend/tsconfig.json
      types: [src/state.ts#Status]
      fields: {src/job.ts#Job.status: src/state.ts#Status}
      parsers: {src/state.ts#parseStatus: validates the response before conversion}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Enums.Go.Types) != 2 || len(c.Enums.TypeScript) != 1 || c.Enums.TypeScript[0].Fields["src/job.ts#Job.status"] != "src/state.ts#Status" {
		t.Fatalf("policy not loaded: %+v", c.Enums)
	}
}

func TestEnumPoliciesRejectSilentHoles(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"go: {types: [string]}", "invalid enum selector"},
		{"go: {types: [a.Status, a.Status]}", "repeats"},
		{"go: {types: [a.Status], fields: {Job.status: a.Status}}", "invalid field"},
		{"go: {types: [a.Status], fields: {a.Job.Status: a.Other}}", "undeclared enum"},
		{"go: {types: [a.Status], parsers: {Parse: boundary}}", "invalid function"},
		{"go: {types: [a.Status], parsers: {a.Parse: ' '}}", "needs a reason"},
		{"go: {parsers: {a.Parse: boundary}}", "no enum domain"},
		{"typescript: [{project: ../tsconfig.json, types: [a.ts#Status]}]", "repository-relative"},
		{"typescript: [{project: tsconfig.js, types: [a.ts#Status]}]", "tsconfig JSON"},
		{"typescript: [{project: tsconfig.json}]", "no enum types"},
		{"typescript: [{project: tsconfig.json, types: [a.ts#Status]}, {project: ./tsconfig.json, types: [a.ts#Status]}]", "repeats project"},
		{"typescript: [{project: tsconfig.json, types: [a.ts#Status, a.ts#Status]}]", "repeats"},
		{"typescript: [{project: tsconfig.json, types: [a.ts#Status], fields: {a.ts#Job.status: a.ts#Other}}]", "undeclared enum"},
		{"typescript: [{project: tsconfig.json, types: [a.ts#Status], parsers: {a.ts#parse: ''}}]", "needs a reason"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			_, err := Load(write(t, "enums: {"+tc.body+"}\n"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %s", err, tc.want)
			}
		})
	}
}

func TestEnumSelectorGrammar(t *testing.T) {
	for _, s := range []string{".Status", "internal/state.Status", "example.com/m/state.Status"} {
		if !goEnumSelector(s, false) {
			t.Errorf("rejects %s", s)
		}
	}
	for _, s := range []string{" a.Status", "a._", "a.for", "a.2Status", "../a.Status", "/a.Status", "a\\b.Status", "Status"} {
		if goEnumSelector(s, false) {
			t.Errorf("accepts %s", s)
		}
	}
	for _, s := range []string{"src/s.ts#Status", "src/s.tsx#$Status", "src/S.vue#Status"} {
		if !tsEnumSelector(s, false) {
			t.Errorf("rejects %s", s)
		}
	}
	for _, s := range []string{"s.ts", "../s.ts#Status", "s.js#Status", "s.ts#Status.field", "s.ts#", "s.ts#2Status", "s.ts#Status#Other"} {
		if tsEnumSelector(s, false) {
			t.Errorf("accepts %s", s)
		}
	}
	if !tsEnumSelector("src/job.ts#Job.status", true) || tsEnumSelector("src/job.ts#Job", true) || !goEnumSelector(".Job.Status", true) {
		t.Fatal("wrong field selector grammar")
	}
}
