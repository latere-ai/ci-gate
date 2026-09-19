// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// PostgresRole is a repository's relationship to the family's shared
// database.
//
// The vocabulary is closed because the gate picks its checks from it. A
// value outside the list would select no check, which is a gate that
// reports green over a repository nobody is checking.
type PostgresRole string

const (
	// PostgresUnset is the zero value: a block that exists and names no role,
	// or no block at all. The gate tells the two apart through Present.
	PostgresUnset PostgresRole = ""
	// PostgresNone holds no Postgres client: a library, a tool, a service
	// with another store.
	PostgresNone PostgresRole = "none"
	// PostgresDirect connects to the database on its direct endpoint. It
	// passes by declaration in this release; the family's build order makes
	// it a waived role once the cutovers land.
	PostgresDirect PostgresRole = "direct"
	// PostgresPooled reads the pooled DSN for serving traffic, falls back to
	// the direct DSN, and hands the direct DSN to its migrator.
	PostgresPooled PostgresRole = "pooled"
)

// PostgresRoles is the vocabulary, in the order a message lists it.
var PostgresRoles = []PostgresRole{PostgresNone, PostgresDirect, PostgresPooled}

// PostgresRoleList renders the vocabulary for a message.
func PostgresRoleList() string {
	names := make([]string, len(PostgresRoles))
	for i, r := range PostgresRoles {
		names[i] = string(r)
	}
	return strings.Join(names, ", ")
}

// The two environment names the family's Secrets carry, without a prefix.
// DATABASE_URL is the direct endpoint and carries migrations;
// DATABASE_POOL_URL is the transaction-mode pool the serving path reads
// first.
const (
	DirectURLName = "DATABASE_URL"
	PoolURLName   = "DATABASE_POOL_URL"
)

// prefixShape is what a prefix looks like: the part of EVAL_DATABASE_URL
// before the underscore, in the spelling identity.config_prefix uses.
var prefixShape = regexp.MustCompile(`^[A-Z][A-Z0-9_]*[A-Z0-9]$|^[A-Z]$`)

// Postgres declares a repository's relationship to the shared database,
// and holds the one value the pooled checks need.
//
// The block is optional, unlike identity's: a repository with no Postgres
// client has nothing to declare, and the gate decides an absent block from
// the imports. A client import under an absent block is the finding.
type Postgres struct {
	// Role selects the checks.
	Role PostgresRole `yaml:"role"`
	// Prefix is the part before DATABASE_URL in a repository whose Secret
	// carries a prefixed name, such as EVAL for EVAL_DATABASE_URL. Without
	// the trailing underscore. Pooled only.
	Prefix string `yaml:"prefix"`
	// Present reports whether the file carried the block at all. Computed by
	// Load; not part of the file.
	Present bool `yaml:"-"`
}

// PoolURL is the environment name the serving path reads first.
func (p Postgres) PoolURL() string { return p.prefixed(PoolURLName) }

// DirectURL is the environment name the serving path falls back to and the
// migrator receives.
func (p Postgres) DirectURL() string { return p.prefixed(DirectURLName) }

func (p Postgres) prefixed(name string) string {
	if prefix := strings.TrimSpace(p.Prefix); prefix != "" {
		return prefix + "_" + name
	}
	return name
}

// validate rejects a block whose role would run checks with nothing to
// decide from, or that carries a value nothing reads.
func (p Postgres) validate(path string) error {
	if !p.Present {
		return nil
	}
	if p.Role == PostgresUnset {
		return fmt.Errorf("%s: postgres.role is empty\n"+
			"name this repository's relationship to the shared database, one of %s", path, PostgresRoleList())
	}
	if !slices.Contains(PostgresRoles, p.Role) {
		return fmt.Errorf("%s: postgres.role %q is not a relationship to the shared database\n"+
			"one of %s", path, string(p.Role), PostgresRoleList())
	}
	if prefix := strings.TrimSpace(p.Prefix); prefix != "" {
		if p.Role != PostgresPooled {
			return fmt.Errorf("%s: postgres.prefix is set and postgres.role is %q\n"+
				"only a pooled repository is checked for the names it reads, so a prefix "+
				"under any other role is a decision with no effect", path, string(p.Role))
		}
		if !prefixShape.MatchString(prefix) {
			return fmt.Errorf("%s: postgres.prefix %q is not the part of a variable name before %s\n"+
				"write it as EVAL for EVAL_%s: uppercase letters, digits and underscores, "+
				"without the trailing underscore", path, prefix, DirectURLName, DirectURLName)
		}
	}
	return nil
}
