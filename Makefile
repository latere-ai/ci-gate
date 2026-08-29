GO ?= go

.PHONY: build test cover test-hermetic test-race fmt fmt-check lint-modernize spec-lint

build:
	$(GO) build ./...

test:
	$(GO) vet ./...
	$(GO) test ./...

# Coverage per package rather than as a repository average: an average lets a
# well-tested package carry an untested one. Exemptions live in
# .lateregate.yaml with a reason attached.
cover:
	$(GO) test ./... -coverprofile=coverage.out -coverpkg=./...
	@$(GO) run ./cmd/lateregate cover -profile=coverage.out

# The suite with only the Go toolchain on PATH. Tests that depend on what
# happens to be installed pass locally and fail on a runner, which is the
# worst order to find out.
test-hermetic:
	@$(GO) run ./cmd/lateregate hermetic

# The race detector needs cgo, which the shipped binary does not: this is
# about finding a race in the tests, not about what we compile to.
test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

fmt:
	gofmt -w .

fmt-check:
	@$(GO) run ./cmd/lateregate fmt-check

lint-modernize:
	@$(GO) run ./cmd/lateregate modernize

# This repository has no spec tree of its own yet; the design lives in
# ci/specs/go-verify-pipeline.md. The target exists so the contract is
# complete and turns on by adding a spec section to .lateregate.yaml.
spec-lint:
	@$(GO) run ./cmd/lateregate spec-lint
