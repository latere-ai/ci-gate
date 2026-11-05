// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

//go:build skipper

package dbmod

import "testing"

func TestNeedsACluster(t *testing.T) {
	t.Skip("no cluster here")
}

func TestHolds(t *testing.T) {}
