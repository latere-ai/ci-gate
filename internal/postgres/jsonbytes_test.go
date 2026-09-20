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

// carrierFile is the head a carrier fixture shares. It declares one of each
// shape the measurement at the head of jsonbytes.go bound, so a body below is
// one statement and nothing else.
const carrierFile = `package store

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"time"

	"example.com/app/internal/db"
)

// jsonUse and clockUse hold the encoder and the clock in the import set, so a
// body that never calls them still compiles.
var jsonUse = json.Marshal
var clockUse = time.Now

// Name is a string under another name.
type Name string

// Blob is a byte slice that holds no json document.
type Blob []byte

// Stamp hands the driver a text of its own.
type Stamp struct{ At time.Time }

// String is that text.
func (s Stamp) String() string { return s.At.UTC().String() }

// Money hands the driver a database value.
type Money struct{ Cents int64 }

// Value is that database value.
func (m Money) Value() (driver.Value, error) { return m.Cents, nil }

// Row is a plain Go struct, which is the shape platform bound to a jsonb
// column three times.
type Row struct {
	KeyID string
	State string
}

`

// carriers runs the gate over a body that shares carrierFile's declarations.
func carriers(t *testing.T, body string) (string, error) {
	t.Helper()
	return run(t, declared(config.PostgresNone), repo(t, store(carrierFile+body)))
}

// What a json parameter takes, measured against Postgres 17 with parameters
// sent in the text format, which is what exec mode does. Each row here is a
// row of that measurement: the ones the server accepted have to pass, and the
// ones it refused have to be reported.
func TestWhatAJSONParameterTakes(t *testing.T) {
	for _, tc := range []struct {
		name string
		decl string
		arg  string
		// want is what the finding says the value is, or empty where the
		// parameter took the value and the check has to stay quiet.
		want string
	}{
		{"a byte slice", "v []byte", "v", "a byte slice"},
		{"a byte slice under another name", "v Blob", "v", "a byte slice"},
		{"the encoder's raw message", "v json.RawMessage", "v", ""},
		{"a pointer to one", "v *json.RawMessage", "v", ""},
		{"a string", "v string", "v", ""},
		{"a pointer to string", "v *string", "v", ""},
		{"a nil pointer to string", "v int", "(*string)(nil)", ""},
		{"the empty string", "v int", `""`, ""},
		{"a string under another name", "v Name", "v", ""},
		{"a value with a text of its own", "v Stamp", "v", ""},
		{"a value with a database value", "v Money", "v", ""},
		{"a number", "v int", "v", ""},
		{"an untyped nil", "v int", "nil", ""},
		{"a boolean", "v bool", "v", "a boolean"},
		{"a list of strings", "v []string", "v", "a Go slice"},
		{"a clock reading", "v time.Time", "v", "a value the driver sends as its own column type"},
		{"a Go struct", "v Row", "v", "a Go struct"},
		{"a Go map", "v map[string]any", "v", "a Go map"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := carriers(t, `
// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, `+tc.decl+`) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, `+tc.arg+`)
	return err
}
`)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("a json parameter took this and the check reported it: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("a json parameter refused this and the check passed it:\n%s", out)
			}
			if !strings.Contains(out, "this binds "+tc.want+" into parameter $2") {
				t.Errorf("the finding says what the value is:\n%s", out)
			}
		})
	}
}

// The empty string and a nil into a NOT NULL column are both refused by the
// server, and neither is visible here: one is a value and the other needs the
// schema. The two rows above record that the check stays quiet on them, and
// this one records why that is the right answer rather than a miss.
func TestWhatTheSchemaDecidesIsNotThisCheck(t *testing.T) {
	out, err := carriers(t, `
// Save writes what may be nothing.
func Save(ctx context.Context, p *db.Pool, id string, doc *string) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, doc)
	return err
}
`)
	if err != nil {
		t.Fatalf("a pointer to string is the repair and the check reported it: %v\n%s", err, out)
	}
}

// A Go struct bound to a parameter is the worse bug, and it is not about the
// column: the driver's plan lookup reads the Go type and an undescribed
// parameter, so the call fails before the statement is sent. This is
// platform's event writer, which said nothing about json anywhere.
func TestAGoStructIsNotEncodable(t *testing.T) {
	out, err := carriers(t, `
// Apply writes the observed row.
func Apply(ctx context.Context, p *db.Pool, ev string, row Row) error {
	payload := row
	_, err := p.Exec(ctx, "INSERT INTO observed (id, payload) VALUES ($1, $2)", ev, payload)
	return err
}
`)
	if err == nil {
		t.Fatalf("a Go struct bound to a parameter passed:\n%s", out)
	}
	if !strings.Contains(out, "this binds a Go struct into parameter $2") {
		t.Errorf("the finding names the file, the line and the parameter:\n%s", out)
	}
	for _, want := range []string{"cannot find encode plan", "bind the encoding as a string"} {
		if !strings.Contains(out, want) {
			t.Errorf("the finding says %q:\n%s", want, out)
		}
	}
}

// A Go map is the same failure, and cella and lux bind one to a labels column
// five times between them.
func TestAGoMapIsNotEncodable(t *testing.T) {
	out, err := carriers(t, `
// Put writes the labels.
func Put(ctx context.Context, p *db.Pool, id string, labels map[string]string) error {
	_, err := p.Exec(ctx, "INSERT INTO objects (id, labels) VALUES ($1, $2)", id, labels)
	return err
}
`)
	if err == nil {
		t.Fatalf("a Go map bound to a parameter passed:\n%s", out)
	}
	if !strings.Contains(out, "this binds a Go map into parameter $2") {
		t.Errorf("the finding names what the value is:\n%s", out)
	}
}

// A list of a type the driver does not hold is the same failure again: the
// driver registers a list beside each type of its own and nothing beside a
// repository's, so the list has no encoding although each element would have
// had one.
func TestAListOfANamedTypeIsNotEncodable(t *testing.T) {
	out, err := carriers(t, `
// Pick narrows a set of names.
func Pick(ctx context.Context, p *db.Pool, names []Name) error {
	_, err := p.Query(ctx, "SELECT n FROM unnest($1::text[]) AS n", names)
	return err
}
`)
	if err == nil {
		t.Fatalf("a list of a named type passed:\n%s", out)
	}
	if !strings.Contains(out, "this binds a Go slice into parameter $1") {
		t.Errorf("the finding names what the value is:\n%s", out)
	}
}

// What the driver does hold an encoding for stays quiet, whatever Go shape it
// is: a clock reading, a string under another name, a value with a text or a
// database value of its own, a list of strings, and the byte slice a bytea
// column wants.
func TestWhatTheDriverEncodesPasses(t *testing.T) {
	out, err := carriers(t, `
// Save writes a row of column types the driver holds.
func Save(ctx context.Context, p *db.Pool, id string, at time.Time, n Name, s Stamp, m Money, tags []string, blob []byte) error {
	_, err := p.Exec(ctx,
		"INSERT INTO t (id, at, n, s, m, tags, blob) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		id, at, n, s, m, tags, blob)
	return err
}
`)
	if err != nil {
		t.Fatalf("a column type the driver holds was reported: %v\n%s", err, out)
	}
}

// A helper that hands the value back as an empty interface is the blind spot
// llm-gateway had six of. The declared type says nothing, so what the helper
// can return is what has to be read.
func TestAHelperThatHandsBackAnEmptyInterfaceIsFollowed(t *testing.T) {
	out, err := carriers(t, `
// nullableJSON hands a document over, or a nil so a COALESCE skips the column.
func nullableJSON(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	return []byte(m)
}

// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, m json.RawMessage) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, nullableJSON(m))
	return err
}
`)
	if err == nil {
		t.Fatalf("a byte slice behind an empty interface passed:\n%s", out)
	}
	if !strings.Contains(out, "this binds a byte slice into parameter $2") {
		t.Errorf("the finding names what the value is:\n%s", out)
	}
}

// The same helper written so every path a json parameter takes is quiet. This
// is what llm-gateway's is today, and a check that reported it would be a
// check reporting the repair.
func TestAHelperWhoseEveryPathIsAcceptedPasses(t *testing.T) {
	out, err := carriers(t, `
// nullableJSON hands a document over, or a nil so a COALESCE skips the column.
func nullableJSON(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, m json.RawMessage) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, nullableJSON(m))
	return err
}
`)
	if err != nil {
		t.Fatalf("a helper whose every path is accepted was reported: %v\n%s", err, out)
	}
}

// The helper llm-gateway laundered through is in its own package, one import
// away from every statement that binds its result, so the summaries cross
// packages.
func TestAHelperInAnotherPackageIsFollowed(t *testing.T) {
	files := map[string]string{
		"internal/db/db.go": dbPackage,
		"internal/pgxutil/pgxutil.go": `package pgxutil

import "encoding/json"

// NullableJSON hands a document over, or a nil so a COALESCE skips the column.
func NullableJSON(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	return []byte(m)
}
`,
		"internal/store/store.go": `package store

import (
	"context"
	"encoding/json"

	"example.com/app/internal/db"
	"example.com/app/internal/pgxutil"
)

// Save writes a document.
func Save(ctx context.Context, p *db.Pool, id string, m json.RawMessage) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, pgxutil.NullableJSON(m))
	return err
}
`,
	}
	out, err := run(t, declared(config.PostgresNone), repo(t, files))
	if err == nil {
		t.Fatalf("a byte slice behind a helper one package over passed:\n%s", out)
	}
	if !strings.Contains(out, "this binds a byte slice into parameter $2") {
		t.Errorf("the finding names what the value is:\n%s", out)
	}
}

// A value whose type this pass cannot name is not reported. Reporting every
// empty interface would report most of the family, and a check that is mostly
// noise is waived and then guards nothing.
func TestAValueOutOfReachIsNotReported(t *testing.T) {
	out, err := carriers(t, `
// Save writes whatever it is handed.
func Save(ctx context.Context, p *db.Pool, id string, v any) error {
	_, err := p.Exec(ctx, "UPDATE t SET doc = $2::jsonb WHERE id = $1", id, v)
	return err
}
`)
	if err != nil {
		t.Fatalf("a value out of reach was reported: %v\n%s", err, out)
	}
}
