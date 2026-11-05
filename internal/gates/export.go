// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package gates

// HostPort reads the host port from `port` output such as "127.0.0.1:49153",
// or returns "" when it names none.
func HostPort(s string) string { return hostPort(s) }

// VerifySHA256 refuses a file whose SHA-256 is not want.
func VerifySHA256(file, want string) error { return checkSHA256(file, want) }

// ExtractFile writes the regular file named name in a gzipped tar archive to
// dst, executable, refusing one over limit bytes.
func ExtractFile(archive, name, dst string, limit int64) error {
	return extractFile(archive, name, dst, limit)
}
