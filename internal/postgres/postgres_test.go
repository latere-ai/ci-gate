// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package postgres

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
	"latere.ai/x/ci-gate/internal/registers"
)

// repo writes a tree at a fresh root and returns the root. Every tree has a
// go.mod, because a finding about the whole repository names it.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"go.mod": "module example.com/app\n\ngo 1.27\n"}
	maps.Copy(all, files)
	for rel, body := range all {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, cfg config.Postgres, root string) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := Run(cfg, root, &sb)
	return sb.String(), err
}

// declared is a block naming the role, as Load would carry it.
func declared(role config.PostgresRole) config.Postgres {
	return config.Postgres{Role: role, Present: true}
}

// absent is no block at all.
var absent = config.Postgres{}

// The fixture repositories. Each is a tree in the shape the family's
// repositories have, reduced to the files the checks read.

// pgFile connects with pgx and reads the names it is given, in the shape
// auth, lectio and replichai use.
func pgFile(reads ...string) string {
	var b strings.Builder
	b.WriteString("package store\n\nimport (\n\t\"context\"\n\t\"os\"\n\n\t\"github.com/jackc/pgx/v5/pgxpool\"\n)\n\n")
	b.WriteString("// Open connects to the database named by the environment.\nfunc Open(ctx context.Context) (*pgxpool.Pool, error) {\n")
	b.WriteString("\tdsn := \"\"\n")
	for _, name := range reads {
		b.WriteString("\tif dsn == \"\" {\n\t\tdsn = os.Getenv(\"" + name + "\")\n\t}\n")
	}
	b.WriteString("\treturn pgxpool.New(ctx, dsn)\n}\n")
	return b.String()
}

var (
	// noneClean holds no client: a tool with an in-memory store.
	noneClean = map[string]string{
		"internal/store/mem.go": "package store\n\n// Mem is the in-memory store.\ntype Mem struct{ rows map[string]string }\n",
	}
	// nonePgx says none and imports the pool.
	nonePgx = map[string]string{
		"internal/store/pg.go": pgFile("DATABASE_URL"),
	}
	// direct connects on the direct endpoint and reads only that name.
	direct = nonePgx
	// pooledComplete reads the pooled name and falls back to the direct one.
	pooledComplete = map[string]string{
		"internal/store/pg.go": pgFile("DATABASE_POOL_URL", "DATABASE_URL"),
	}
	// pooledMissingPool reads the direct name alone.
	pooledMissingPool = map[string]string{
		"internal/store/pg.go": pgFile("DATABASE_URL"),
	}
	// pooledMissingPgx reads both names and imports no client.
	pooledMissingPgx = map[string]string{
		"internal/config/config.go": "package config\n\nimport \"os\"\n\n" +
			"// DSN is the pooled name, then the direct one.\nfunc DSN() string {\n" +
			"\tif v := os.Getenv(\"DATABASE_POOL_URL\"); v != \"\" {\n\t\treturn v\n\t}\n\treturn os.Getenv(\"DATABASE_URL\")\n}\n",
	}
)

// A tree with no block passes when nothing imports a client, and the report
// says the decision came from the imports; the same tree with a client fails
// naming the file, the line and the three roles, once per file.
func TestAbsentRoleDecidesFromTheImports(t *testing.T) {
	out, err := run(t, absent, repo(t, noneClean))
	if err != nil {
		t.Fatalf("no block and no client passes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "deciding from the imports") || !strings.Contains(out, "PASS declared") {
		t.Errorf("the report says how it decided:\n%s", out)
	}

	out, err = run(t, absent, repo(t, nonePgx))
	if err == nil {
		t.Fatalf("no block and a client is the undeclared consumer:\n%s", out)
	}
	if !strings.Contains(out, "FAIL declared") || !strings.Contains(out, "internal/store/pg.go:7:") {
		t.Errorf("the finding names the file and the import line:\n%s", out)
	}
	for _, role := range config.PostgresRoles {
		if !strings.Contains(out, string(role)) {
			t.Errorf("the finding names the role %q:\n%s", string(role), out)
		}
	}
	if !strings.Contains(err.Error(), "role absent") {
		t.Errorf("the failure names the absent role: %v", err)
	}

	// The fix is one line in the tool's file whatever the count, so a file
	// with three client imports is one finding, at the first of them.
	three := map[string]string{"internal/store/pg.go": "package store\n\nimport (\n\t_ \"github.com/jackc/pgx/v5\"\n" +
		"\t_ \"github.com/jackc/pgx/v5/pgxpool\"\n\t_ \"github.com/lib/pq\"\n)\n"}
	out, _ = run(t, absent, repo(t, three))
	if !strings.Contains(out, "1 finding(s)") || !strings.Contains(out, "internal/store/pg.go:4:") {
		t.Errorf("one finding per file, at its first client import:\n%s", out)
	}
}

// none passes a clean tree and fails one with a client, and the finding says
// which roles to declare instead.
func TestRoleNone(t *testing.T) {
	out, err := run(t, declared(config.PostgresNone), repo(t, noneClean))
	if err != nil {
		t.Fatalf("none over a clean tree passes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS client-free") || !strings.Contains(out, "role none holds 1 check(s)") {
		t.Errorf("the report:\n%s", out)
	}

	out, err = run(t, declared(config.PostgresNone), repo(t, nonePgx))
	if err == nil || !strings.Contains(err.Error(), "client-free") {
		t.Fatalf("none over a client fails the client-free check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "internal/store/pg.go:7: this imports pgx, and postgres.role says none") {
		t.Errorf("the finding names the file, the line and the client:\n%s", out)
	}
	if !strings.Contains(out, "declares direct") || !strings.Contains(out, "or pooled") {
		t.Errorf("the finding names the roles to declare:\n%s", out)
	}
}

// Every client in the table is one, and database/sql alone is not.
func TestWhatAClientIs(t *testing.T) {
	for _, c := range clients {
		t.Run(c.path, func(t *testing.T) {
			tree := map[string]string{"internal/db/db.go": "package db\n\nimport _ \"" + c.path + "\"\n"}
			out, err := run(t, declared(config.PostgresNone), repo(t, tree))
			if err == nil {
				t.Fatalf("%s is a client:\n%s", c.path, out)
			}
			if !strings.Contains(out, "this imports "+c.name) {
				t.Errorf("the finding calls it %q:\n%s", c.name, out)
			}
		})
	}
	sub := map[string]string{"internal/db/db.go": "package db\n\nimport _ \"github.com/jackc/pgx/v5/stdlib\"\n"}
	if out, err := run(t, declared(config.PostgresNone), repo(t, sub)); err == nil {
		t.Errorf("a subpackage of the pgx tree is a client:\n%s", out)
	}
	generic := map[string]string{"internal/db/db.go": "package db\n\nimport \"database/sql\"\n\nvar _ *sql.DB\n"}
	if out, err := run(t, declared(config.PostgresNone), repo(t, generic)); err != nil {
		t.Errorf("database/sql alone is generic, not a Postgres client: %v\n%s", err, out)
	}
	lookalike := map[string]string{"internal/db/db.go": "package db\n\nimport _ \"github.com/jackc/pgxutil\"\n"}
	if out, err := run(t, declared(config.PostgresNone), repo(t, lookalike)); err != nil {
		t.Errorf("a path that only starts like the pgx tree is not it: %v\n%s", err, out)
	}
}

// What is not read: a test, a fixture, a file another tool owns, and a file
// that does not parse, which the build gate reports.
func TestWhatIsNotRead(t *testing.T) {
	tree := map[string]string{
		"internal/store/mem.go":         noneClean["internal/store/mem.go"],
		"internal/store/pg_test.go":     pgFile("DATABASE_URL"),
		"internal/store/testdata/pg.go": pgFile("DATABASE_URL"),
		"node_modules/x/pg.go":          pgFile("DATABASE_URL"),
		".claude/worktrees/w/pg.go":     pgFile("DATABASE_URL"),
		"internal/broken/b.go":          "package broken\n\nimport \"github.com/jackc/pgx/v5\"\n\nfunc (",
	}
	out, err := run(t, declared(config.PostgresNone), repo(t, tree))
	if err != nil {
		t.Fatalf("none of those is a file this repository decides with: %v\n%s", err, out)
	}
	if !strings.Contains(out, "across 1 Go file(s)") {
		t.Errorf("one file was read:\n%s", out)
	}
}

// direct passes, and the row says it passed by declaration rather than by
// anything read, so the seam the family's step 4 opens is visible.
func TestRoleDirectPassesByDeclaration(t *testing.T) {
	out, err := run(t, declared(config.PostgresDirect), repo(t, direct))
	if err != nil {
		t.Fatalf("direct passes in this release: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS direct") || !strings.Contains(out, "passes by declaration") {
		t.Errorf("the row says how it passed:\n%s", out)
	}
	if !strings.Contains(out, "later release requires a dated reason") {
		t.Errorf("the row says what is coming:\n%s", out)
	}
}

// pooled passes the complete fixture and every shape the family reads a DSN
// in: the standard library's two calls, a local helper, a struct tag, and a
// constant bound in another file of the package.
func TestRolePooledReadsInEveryShape(t *testing.T) {
	out, err := run(t, declared(config.PostgresPooled), repo(t, pooledComplete))
	if err != nil {
		t.Fatalf("the complete fixture passes: %v\n%s", err, out)
	}
	for _, want := range []string{"PASS client", "PASS pool-url", "PASS direct-url", "role pooled holds 3 check(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report carries %q:\n%s", want, out)
		}
	}

	shapes := map[string]map[string]string{
		"LookupEnv": {"internal/store/pg.go": "package store\n\nimport (\n\t\"os\"\n\n\t_ \"github.com/jackc/pgx/v5\"\n)\n\n" +
			"func dsn() string {\n\tif v, ok := os.LookupEnv(\"DATABASE_POOL_URL\"); ok {\n\t\treturn v\n\t}\n" +
			"\tv, _ := os.LookupEnv(\"DATABASE_URL\")\n\treturn v\n}\n"},
		"a local helper": {"cmd/appd/main.go": "package main\n\nimport (\n\t\"os\"\n\n\t_ \"github.com/jackc/pgx/v5\"\n)\n\n" +
			"func getenv(k string) string { return os.Getenv(k) }\n\n" +
			"func main() {\n\t_ = getenv(\"DATABASE_POOL_URL\")\n\t_ = getenv(\"DATABASE_URL\")\n}\n"},
		"a helper with a default": {"cmd/appd/main.go": "package main\n\nimport _ \"github.com/lib/pq\"\n\n" +
			"func envOr(k, d string) string { return d }\n\n" +
			"func main() {\n\t_ = envOr(\"DATABASE_POOL_URL\", envOr(\"DATABASE_URL\", \"\"))\n}\n"},
		"a struct tag": {"internal/config/config.go": "package config\n\nimport _ \"github.com/jackc/pgx/v5\"\n\n" +
			"// Config is what the environment sets.\ntype Config struct {\n" +
			"\tPoolURL     string `env:\"DATABASE_POOL_URL\"`\n" +
			"\tDatabaseURL string `env:\"DATABASE_URL,required\"`\n}\n"},
		"a constant in another file of the package": {
			"internal/store/names.go": "package store\n\nconst (\n\tpoolEnv   = \"DATABASE_POOL_URL\"\n\tdirectEnv = \"DATABASE_URL\"\n)\n",
			"internal/store/pg.go": "package store\n\nimport (\n\t\"os\"\n\n\t_ \"github.com/jackc/pgx/v5\"\n)\n\n" +
				"func dsn() string {\n\tif v := os.Getenv(poolEnv); v != \"\" {\n\t\treturn v\n\t}\n\treturn os.Getenv(directEnv)\n}\n",
		},
	}
	for name, tree := range shapes {
		t.Run(name, func(t *testing.T) {
			out, err := run(t, declared(config.PostgresPooled), repo(t, tree))
			if err != nil {
				t.Fatalf("%s is a read: %v\n%s", name, err, out)
			}
		})
	}
}

// Each pooled fixture missing one thing fails that check and names it.
func TestRolePooledFails(t *testing.T) {
	cases := []struct {
		name  string
		tree  map[string]string
		check string
		want  string
	}{
		{"missing the pooled name", pooledMissingPool, "pool-url", "nothing here reads DATABASE_POOL_URL"},
		{"missing the client", pooledMissingPgx, "client", "nothing here imports a Postgres client, and postgres.role says pooled"},
		{"missing the direct name", map[string]string{"internal/store/pg.go": pgFile("DATABASE_POOL_URL")},
			"direct-url", "nothing here reads DATABASE_URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := run(t, declared(config.PostgresPooled), repo(t, c.tree))
			if err == nil {
				t.Fatalf("the fixture fails:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.check) || !strings.Contains(out, "FAIL "+c.check) {
				t.Errorf("the %s check is the one that failed: %v\n%s", c.check, err, out)
			}
			if !strings.Contains(out, "go.mod:1: "+c.want) {
				t.Errorf("the finding says %q:\n%s", c.want, out)
			}
		})
	}
}

// A prefixed repository reads the prefixed names and not the bare ones.
func TestPrefixNamesTheVariables(t *testing.T) {
	prefixed := map[string]string{"internal/store/pg.go": pgFile("EVAL_DATABASE_POOL_URL", "EVAL_DATABASE_URL")}
	cfg := declared(config.PostgresPooled)
	cfg.Prefix = "EVAL"
	out, err := run(t, cfg, repo(t, prefixed))
	if err != nil {
		t.Fatalf("the prefixed names are the ones checked: %v\n%s", err, out)
	}
	if !strings.Contains(out, "EVAL_DATABASE_POOL_URL read in internal/store/pg.go") {
		t.Errorf("the note names what was read and where:\n%s", out)
	}
	out, err = run(t, cfg, repo(t, pooledComplete))
	if err == nil || !strings.Contains(out, "nothing here reads EVAL_DATABASE_POOL_URL") {
		t.Errorf("the bare names do not satisfy a prefix: %v\n%s", err, out)
	}
	out, err = run(t, declared(config.PostgresPooled), repo(t, prefixed))
	if err == nil || !strings.Contains(out, "nothing here reads DATABASE_POOL_URL") {
		t.Errorf("the prefixed names do not satisfy the bare check: %v\n%s", err, out)
	}
}

// A repository that writes its two names out is checked at those names,
// and the checks that read a derivation read nothing of it.
func TestWrittenOutNamesAreTheOnesChecked(t *testing.T) {
	written := map[string]string{"internal/store/pg.go": pgFile("LUX_DB_POOL_URL", "LUX_DB_URL")}
	cfg := declared(config.PostgresPooled)
	cfg.DirectEnv, cfg.PoolEnv = "LUX_DB_URL", "LUX_DB_POOL_URL"
	out, err := run(t, cfg, repo(t, written))
	if err != nil {
		t.Fatalf("the written-out names are the ones checked: %v\n%s", err, out)
	}
	for _, want := range []string{"LUX_DB_POOL_URL read in internal/store/pg.go", "LUX_DB_URL read in internal/store/pg.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("the note names what was read and where, %q:\n%s", want, out)
		}
	}
	out, err = run(t, cfg, repo(t, pooledComplete))
	if err == nil || !strings.Contains(out, "nothing here reads LUX_DB_POOL_URL") {
		t.Errorf("the bare names do not satisfy written-out ones: %v\n%s", err, out)
	}
	out, err = run(t, declared(config.PostgresPooled), repo(t, written))
	if err == nil || !strings.Contains(out, "nothing here reads DATABASE_POOL_URL") {
		t.Errorf("the written-out names do not satisfy the bare check: %v\n%s", err, out)
	}
	// A tree that reads the pool and not the direct endpoint fails at the
	// written-out direct name, and the sentence names the other endpoint.
	half := map[string]string{"internal/store/pg.go": pgFile("LUX_DB_POOL_URL")}
	out, err = run(t, cfg, repo(t, half))
	if err == nil || !strings.Contains(out, "nothing here reads LUX_DB_URL") || !strings.Contains(out, "LUX_DB_POOL_URL is the pool") {
		t.Errorf("the missing direct read names both written-out names: %v\n%s", err, out)
	}
}

// A pooled tree with no Go file has shown nothing, so every check skips and
// the gate fails rather than passing over an empty tree.
func TestNothingPassesVacuously(t *testing.T) {
	out, err := run(t, declared(config.PostgresPooled), repo(t, map[string]string{"README.md": "# app\n"}))
	if err == nil {
		t.Fatalf("a pooled tree with nothing to read fails:\n%s", out)
	}
	if !strings.Contains(err.Error(), "read anything") || !strings.Contains(err.Error(), "client, pool-url, direct-url") {
		t.Errorf("the failure names every skipped check: %v", err)
	}
	if strings.Count(out, "SKIP") != 3 {
		t.Errorf("every check reports SKIP:\n%s", out)
	}
	// none over an empty tree is a pass with its reason: the role claims
	// nothing a file would have to show.
	out, err = run(t, declared(config.PostgresNone), repo(t, nil))
	if err != nil || !strings.Contains(out, "no non-test Go file") {
		t.Errorf("none over an empty tree passes and says why: %v\n%s", err, out)
	}
}

// A name in a comment, a log line or an error message is a mention, not a
// read; only the two shapes the family reads with count.
func TestAMentionIsNotARead(t *testing.T) {
	tree := map[string]string{"internal/store/pg.go": "package store\n\nimport (\n\t\"errors\"\n\t\"log/slog\"\n\t\"os\"\n\n" +
		"\t_ \"github.com/jackc/pgx/v5\"\n)\n\n" +
		"// Open reads DATABASE_POOL_URL when it is set.\nfunc Open() (string, error) {\n" +
		"\tslog.Info(\"DATABASE_POOL_URL unset; using the direct endpoint\")\n" +
		"\tdsn := os.Getenv(\"DATABASE_URL\")\n" +
		"\tif dsn == \"\" {\n\t\treturn \"\", errors.New(\"DATABASE_POOL_URL or DATABASE_URL is required\")\n\t}\n" +
		"\tvar name = \"DATABASE_POOL_URL\"\n\t_ = name\n" +
		"\treturn dsn, nil\n}\n"}
	out, err := run(t, declared(config.PostgresPooled), repo(t, tree))
	if err == nil || !strings.Contains(out, "FAIL pool-url") {
		t.Errorf("four mentions and no read fail the pool-url check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS direct-url") {
		t.Errorf("the one read is still a read:\n%s", out)
	}
}

// A root that cannot be read is an error, not an empty tree.
func TestAnUnreadableTreeIsAnError(t *testing.T) {
	_, err := run(t, absent, filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "reading the tree") {
		t.Errorf("got %v", err)
	}
}

// The sentence of every finding, after its location, is in the register of
// somebody who runs the tool: no import path, no qualified identifier, no
// file path. The tells are the registers gate's own.
func TestPostgresFindingsAreUserRegister(t *testing.T) {
	runs := []struct {
		cfg  config.Postgres
		tree map[string]string
	}{
		{absent, nonePgx},
		{declared(config.PostgresNone), nonePgx},
		{declared(config.PostgresPooled), pooledMissingPool},
		{declared(config.PostgresPooled), pooledMissingPgx},
		{declared(config.PostgresPooled), map[string]string{"internal/store/pg.go": pgFile("DATABASE_POOL_URL")}},
	}
	sentences := 0
	for _, r := range runs {
		out, _ := run(t, r.cfg, repo(t, r.tree))
		for line := range strings.SplitSeq(out, "\n") {
			line = strings.TrimSpace(line)
			_, sentence, ok := cutLocation(line)
			if !ok {
				continue
			}
			sentences++
			if tells := registers.Tells(sentence); len(tells) > 0 {
				t.Errorf("the sentence of a finding is in the user's register: %s\n%s", strings.Join(tells, "; "), sentence)
			}
		}
	}
	if sentences == 0 {
		t.Fatal("no finding was read, so nothing was checked")
	}
}

// cutLocation splits "rel:line: sentence" into its two halves.
func cutLocation(line string) (string, string, bool) {
	rel, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	num, sentence, ok := strings.Cut(rest, ": ")
	if !ok || num == "" {
		return "", "", false
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return rel, sentence, true
}
