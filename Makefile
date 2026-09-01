# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: MIT

GO ?= go

.PHONY: build check fmt hooks

build:
	$(GO) build ./...

# The whole bar. Every gate lives in the binary, pinned as a tool in go.mod,
# so this target is a name for `go tool lateregate` and nothing else: there
# is no recipe here to drift from the one every other repository runs.
# One gate at a time: `go tool lateregate cover`. The plan: `go tool
# lateregate list`.
check:
	@$(GO) tool lateregate

fmt:
	gofmt -w .

# Point git at the delegating pre-commit hook. Per clone, so it is a target.
hooks:
	git config core.hooksPath .githooks
