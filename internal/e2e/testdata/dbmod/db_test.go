// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build postgres

package dbmod

import (
	"encoding/binary"
	"net"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestPostgresAnswers opens a connection to the server DATABASE_URL names and
// sends the startup message; a Postgres server answers with an
// authentication request, the byte 'R'.
func TestPostgresAnswers(t *testing.T) {
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		t.Skip("DATABASE_URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	params := "user\x00" + u.User.Username() + "\x00database\x00" + u.Path[1:] + "\x00\x00"
	msg := make([]byte, 8, 8+len(params))
	binary.BigEndian.PutUint32(msg[0:4], uint32(8+len(params)))
	binary.BigEndian.PutUint32(msg[4:8], 196608) // protocol 3.0
	msg = append(msg, params...)
	if _, err := conn.Write(msg); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 1)
	if _, err := conn.Read(reply); err != nil {
		t.Fatal(err)
	}
	if reply[0] != 'R' {
		t.Fatalf("the server answered %q, not an authentication request", reply)
	}
}
