// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

// An absent block and an empty one are different decisions: the gate
// decides the first from the imports and the load refuses the second.
func TestPostgresPresenceIsRecorded(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Postgres.Present {
		t.Error("a missing file carries no block")
	}
	c, err = Load(write(t, "identity:\n  role: none\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Postgres.Present {
		t.Error("a file without the key carries no block")
	}
	c, err = Load(write(t, "postgres:\n  role: direct\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Postgres.Present {
		t.Error("a file with the key carries the block")
	}
	if c.Postgres.Role != PostgresDirect {
		t.Errorf("role = %q, want direct", string(c.Postgres.Role))
	}
}

// Every role in the vocabulary loads, and the names the pooled checks read
// follow the prefix in the spelling identity.config_prefix uses.
func TestPostgresAcceptsEveryRoleAndPrefixesTheNames(t *testing.T) {
	for _, role := range PostgresRoles {
		c, err := Load(write(t, "postgres:\n  role: "+string(role)+"\n"))
		if err != nil {
			t.Errorf("role %s: %v", string(role), err)
			continue
		}
		if c.Postgres.PoolURL() != PoolURLName || c.Postgres.DirectURL() != DirectURLName {
			t.Errorf("role %s without a prefix reads %s and %s", string(role), c.Postgres.PoolURL(), c.Postgres.DirectURL())
		}
	}
	c, err := Load(write(t, "postgres:\n  role: pooled\n  prefix: EVAL\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Postgres.PoolURL() != "EVAL_DATABASE_POOL_URL" || c.Postgres.DirectURL() != "EVAL_DATABASE_URL" {
		t.Errorf("prefixed names = %s, %s", c.Postgres.PoolURL(), c.Postgres.DirectURL())
	}
	if !strings.Contains(PostgresRoleList(), "none, direct, pooled") {
		t.Errorf("the role list reads in order: %s", PostgresRoleList())
	}
}

// Each rejection names the key and what to write instead: a block the gate
// cannot act on is one that reports green over a repository nobody checks.
func TestPostgresValidationRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no role", "postgres:\n  prefix: EVAL\n", "postgres.role is empty"},
		{"empty block", "postgres: {}\n", "postgres.role is empty"},
		{"unknown role", "postgres:\n  role: pooler\n", "is not a relationship to the shared database"},
		{"prefix under none", "postgres:\n  role: none\n  prefix: EVAL\n", "postgres.prefix is set and postgres.role is \"none\""},
		{"prefix under direct", "postgres:\n  role: direct\n  prefix: EVAL\n", "postgres.prefix is set and postgres.role is \"direct\""},
		{"lowercase prefix", "postgres:\n  role: pooled\n  prefix: eval\n", "is not the part of a variable name before DATABASE_URL"},
		{"trailing underscore", "postgres:\n  role: pooled\n  prefix: EVAL_\n", "without the trailing underscore"},
		{"unknown key", "postgres:\n  role: pooled\n  pool: yes\n", "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, c.body))
			if err == nil {
				t.Fatalf("%q must be refused", c.body)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal says %q:\n%v", c.want, err)
			}
			for _, role := range PostgresRoles {
				if strings.Contains(c.want, "role is empty") && !strings.Contains(err.Error(), string(role)) {
					t.Errorf("the refusal names the role %q:\n%v", string(role), err)
				}
			}
		})
	}
}
