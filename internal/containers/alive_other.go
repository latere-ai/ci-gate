// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build !unix

package containers

// Alive reports every process alive where this package cannot ask, so a
// sweep there removes nothing rather than a container somebody is using.
func Alive(int) bool { return true }
