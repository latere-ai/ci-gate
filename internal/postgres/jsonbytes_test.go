// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package postgres

import (
	"strings"
	"testing"

	"latere.ai/x/ci-gate/internal/config"
)

// dbPackage is the statement surface a fixture calls, written locally so a
// fixture module type-checks with nothing downloaded. The signatures are
// pgx's: the SQL as a string, the parameters as the variadic empty interface
// after it, which is what the check reads.
const dbPackage = `package db

import "context"

// Tag is what a statement reports.
type Tag struct{ Rows int64 }

// Row is a one-row result.
type Row interface{ Scan(dest ...any) error }

// Pool is the serving pool.
type Pool struct{}

// Exec runs a statement.
func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (Tag, error) { return Tag{}, nil }

// Query runs a statement and returns its rows.
func (p *Pool) Query(ctx context.Context, sql string, args ...any) (Row, error) { return nil, nil }

// QueryRow runs a statement and returns its one row.
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) Row { return nil }
`

// store wraps a fixture's store body in the package the check reads.
func store(body string) map[string]string {
	return map[string]string{
		"internal/db/db.go":       dbPackage,
		"internal/store/store.go": body,
	}
}

// storeFile is the head every fixture body shares.
const storeFile = `package store

import (
	"context"
	"encoding/json"

	"example.com/app/internal/db"
)

// jsonUse holds the encoder in the import set, so a fixture body that never
// calls it still compiles.
var jsonUse = json.Marshal

`

// binds runs the gate over a store body and returns the report.
//
// The role is none, so the two rows that run are client-free and json-bytes.
// A fixture that declared pooled would have to import the pgx tree to satisfy
// the client row, and this check type-checks what it reads, so the fixture
// module would need that dependency resolved to say anything at all. The
// statement surface is written locally instead, and
// TestJSONBytesRunsUnderEveryRole is what holds that the row is in every
// role.
func binds(t *testing.T, body string) (string, error) {
	t.Helper()
	return run(t, declared(config.PostgresNone), repo(t, store(storeFile+body)))
}

// marshalBody binds a json encoding as bytes, which is the shape auth
// deployed. It is a named fixture because the register test reads the
// sentence this check writes, and that test needs a tree that produces one.
const marshalBody = `
// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, v any) error {
	doc, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = p.Exec(ctx, "UPDATE t SET doc = $2 WHERE id = $1", id, doc)
	return err
}
`

// A byte slice bound to a parameter the statement casts to jsonb is the bug,
// and the finding names the parameter.
func TestAByteSliceIntoAJsonbCastIsCaught(t *testing.T) {
	out, err := binds(t, `
// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, doc []byte) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, doc)
	return err
}
`)
	if err == nil {
		t.Fatalf("a byte slice into a jsonb cast passed:\n%s", out)
	}
	if !strings.Contains(out, "internal/store/store.go:17: this binds a byte slice into parameter $2") {
		t.Errorf("the finding names the file, the line and the parameter:\n%s", out)
	}
	for _, want := range []string{"SQLSTATE 22P02", "bind a string", "pointer to string", "bytea column"} {
		if !strings.Contains(out, want) {
			t.Errorf("the finding says %q:\n%s", want, out)
		}
	}
}

// The repair passes: a string, and a pointer to one where SQL NULL has to
// stay distinguishable from the empty document.
func TestAStringIntoAJsonbCastPasses(t *testing.T) {
	out, err := binds(t, `
// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, doc string, maybe *string) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb, alt = $3::jsonb WHERE id = $1", id, doc, maybe)
	return err
}
`)
	if err != nil {
		t.Fatalf("the repair fails the check: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PASS json-bytes") {
		t.Errorf("the report:\n%s", out)
	}
}

// A byte slice is the right type for a bytea column, and the check says so by
// staying quiet: a rule that flagged these would be waived, and a waived rule
// checks nothing.
func TestAByteSliceIntoByteaPasses(t *testing.T) {
	out, err := binds(t, `
// Seal writes an encrypted blob.
func Seal(ctx context.Context, p *db.Pool, id string, ciphertext []byte) error {
	_, err := p.Exec(ctx, "UPDATE secrets SET blob = $2 WHERE id = $1", id, ciphertext)
	return err
}

// SealCast writes an encrypted blob into a column the statement names.
func SealCast(ctx context.Context, p *db.Pool, id string, ciphertext []byte) error {
	_, err := p.Exec(ctx, "UPDATE secrets SET blob = $2::bytea WHERE id = $1", id, ciphertext)
	return err
}
`)
	if err != nil {
		t.Fatalf("a bytea bind is a finding: %v\n%s", err, out)
	}
}

// A json encoding bound straight into a statement is the bug whatever the
// statement says, because the column that takes a json encoding is a json
// column. This is auth's registry and agents' payload.
func TestAnInlineMarshalIsCaught(t *testing.T) {
	out, err := binds(t, `
// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, v any) error {
	doc, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = p.Exec(ctx, "UPDATE t SET doc = $2 WHERE id = $1", id, doc)
	return err
}
`)
	if err == nil {
		t.Fatalf("a json encoding bound as bytes passed:\n%s", out)
	}
	if !strings.Contains(out, "the value is a json encoding") {
		t.Errorf("the finding says what made it json:\n%s", out)
	}
}

// A statement that mentions no json, binding a value that is no json
// encoding, is nothing to report.
func TestNoJsonIsIgnored(t *testing.T) {
	out, err := binds(t, `
// Save writes a row.
func Save(ctx context.Context, p *db.Pool, id, name string, n int) error {
	_, err := p.Exec(ctx, "UPDATE t SET name = $2, n = $3 WHERE id = $1", id, name, n)
	return err
}
`)
	if err != nil {
		t.Fatalf("a statement with no json is a finding: %v\n%s", err, out)
	}
}

// agents wrapped the encoder in a helper and bound the helper's result nine
// times. The helper's result type is where the bug is, so the check reads the
// helper rather than the call.
func TestAHelperThatReturnsAnEncodingIsFollowed(t *testing.T) {
	out, err := binds(t, `
// marshalJSON renders a value for a json column.
func marshalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}

// Save writes two documents.
func Save(ctx context.Context, p *db.Pool, id string, a, b any) error {
	ra, err := marshalJSON(a)
	if err != nil {
		return err
	}
	rb, err := marshalJSON(b)
	if err != nil {
		return err
	}
	_, err = p.Exec(ctx, "UPDATE t SET x = $2, y = $3 WHERE id = $1", id, ra, rb)
	return err
}
`)
	if err == nil {
		t.Fatalf("a helper's encoding passed:\n%s", out)
	}
	if !strings.Contains(out, "parameter $2") || !strings.Contains(out, "parameter $3") {
		t.Errorf("both binds are named:\n%s", out)
	}
}

// auth's columns() assigns the encoder's result to a named result and returns
// it, which is the same helper written the other way round.
func TestANamedResultCarriesTheEncoding(t *testing.T) {
	out, err := binds(t, `
// columns renders the document columns.
func columns(v any) (doc []byte, err error) {
	if v != nil {
		if doc, err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, v any) error {
	doc, err := columns(v)
	if err != nil {
		return err
	}
	_, err = p.Exec(ctx, "UPDATE t SET doc = $2 WHERE id = $1", id, doc)
	return err
}
`)
	if err == nil {
		t.Fatalf("a named result carrying an encoding passed:\n%s", out)
	}
}

// A value whose type is the encoder's own raw message is a json document
// wherever it came from, and converting it to a byte slice keeps it bytes.
// agents bound three of these.
func TestARawMessageConvertedToBytesIsCaught(t *testing.T) {
	out, err := binds(t, `
// PutReceipt writes a receipt.
func PutReceipt(ctx context.Context, p *db.Pool, id string, receipt json.RawMessage) error {
	_, err := p.Exec(ctx, "UPDATE sessions SET receipt = $2 WHERE id = $1", id, []byte(receipt))
	return err
}
`)
	if err == nil {
		t.Fatalf("a raw message bound as bytes passed:\n%s", out)
	}
	if !strings.Contains(out, "parameter $2") {
		t.Errorf("the finding names the parameter:\n%s", out)
	}
}

// The same value put through a variable of interface type first, which is
// agents' AppendEvent: the declared type says nothing, so the assignment is
// what has to be read.
func TestAnEncodingReachingTheStatementThroughAnInterfaceIsCaught(t *testing.T) {
	out, err := binds(t, `
// AppendEvent writes an event.
func AppendEvent(ctx context.Context, p *db.Pool, id string, raw json.RawMessage) error {
	var payload any
	if len(raw) > 0 {
		payload = []byte(raw)
	}
	_, err := p.Exec(ctx, "INSERT INTO events (id, payload) VALUES ($1, $2)", id, payload)
	return err
}
`)
	if err == nil {
		t.Fatalf("an encoding reaching the statement through an interface passed:\n%s", out)
	}
	if !strings.Contains(out, "parameter $2") {
		t.Errorf("the finding names the parameter:\n%s", out)
	}
}

// The repair of that shape is the string conversion, and it passes.
func TestTheStringConversionRepairPasses(t *testing.T) {
	out, err := binds(t, `
// PutReceipt writes a receipt.
func PutReceipt(ctx context.Context, p *db.Pool, id string, receipt json.RawMessage) error {
	_, err := p.Exec(ctx, "UPDATE sessions SET receipt = $2 WHERE id = $1", id, string(receipt))
	return err
}

// AppendEvent writes an event.
func AppendEvent(ctx context.Context, p *db.Pool, id string, raw json.RawMessage) error {
	var payload any
	if len(raw) > 0 {
		payload = string(raw)
	}
	_, err := p.Exec(ctx, "INSERT INTO events (id, payload) VALUES ($1, $2)", id, payload)
	return err
}
`)
	if err != nil {
		t.Fatalf("the repair fails the check: %v\n%s", err, out)
	}
}

// The name narrows and the signature decides. Something called Query that
// does not take a statement and its parameters is not a statement, and a
// parameter list handed over as a slice cannot be numbered, so neither is
// read.
func TestWhatIsNotAStatement(t *testing.T) {
	out, err := binds(t, `
// Query is a search, not a statement.
func Query(name string, doc []byte) error { return nil }

// Search calls it.
func Search(v any) error {
	doc, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return Query("jsonb", doc)
}

// Spread hands the parameters over as a slice.
func Spread(ctx context.Context, p *db.Pool, v any) error {
	doc, err := json.Marshal(v)
	if err != nil {
		return err
	}
	args := []any{"id", doc}
	_, err = p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", args...)
	return err
}
`)
	if err != nil {
		t.Fatalf("something that is not a statement was read as one: %v\n%s", err, out)
	}
}

// The check is in every role's row, because the failure is latent under
// every role: a repository on the direct endpoint carries it silently until
// the day it is pooled.
func TestJSONBytesRunsUnderEveryRole(t *testing.T) {
	body := storeFile + marshalBody
	for _, tc := range []struct {
		name   string
		cfg    config.Postgres
		waiver *config.Waiver
	}{
		{"absent", absent, nil},
		{"none", declared(config.PostgresNone), nil},
		{"direct", declared(config.PostgresDirect), waiver("the cutover is scheduled", "2026-12-31")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runWaived(t, tc.cfg, tc.waiver, repo(t, store(body)))
			if err == nil {
				t.Fatalf("role %s let a json encoding through:\n%s", tc.name, out)
			}
			if !strings.Contains(out, "FAIL json-bytes") {
				t.Errorf("role %s reports the check:\n%s", tc.name, out)
			}
		})
	}
}

// A tree with statements that does not type-check is a tree this check did
// not read, and it says so rather than passing.
func TestATreeThatDoesNotTypeCheckIsAnError(t *testing.T) {
	files := store(storeFile + `
// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, missing)
	return err
}
`)
	_, err := run(t, declared(config.PostgresNone), repo(t, files))
	if err == nil {
		t.Fatal("a tree that does not type-check passed")
	}
	if !strings.Contains(err.Error(), "does not type-check") {
		t.Fatalf("the error says what it could not read: %v", err)
	}
}

// A repository with no statement at all pays for no type-checking, and the
// report says what it decided from.
func TestATreeWithNoStatementIsNotTypeChecked(t *testing.T) {
	out, err := run(t, declared(config.PostgresNone), repo(t, noneClean))
	if err != nil {
		t.Fatalf("a tree with no statement failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no statement across") {
		t.Errorf("the report says why it read nothing:\n%s", out)
	}
}

// A parameter cast is read in both spellings Postgres accepts.
func TestParameterCasts(t *testing.T) {
	got := parameterCasts("INSERT INTO t VALUES (COALESCE($6::jsonb, '[]'::jsonb), CAST($7 AS TEXT), $8::JSON, $6::text)")
	want := map[int]string{6: "jsonb", 7: "text", 8: "json"}
	if len(got) != len(want) {
		t.Fatalf("parameterCasts() = %v, want %v", got, want)
	}
	for n, typ := range want {
		if got[n] != typ {
			t.Errorf("parameter $%d cast to %q, want %q", n, got[n], typ)
		}
	}
}
