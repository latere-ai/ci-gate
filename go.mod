module latere.ai/x/ci-gate

go 1.27.0

toolchain go1.27.3

require (
	github.com/goccy/go-yaml v1.19.2
	golang.org/x/mod v0.42.0
	golang.org/x/tools v0.52.0
)

require golang.org/x/sync v0.24.0 // indirect

tool latere.ai/x/ci-gate/cmd/lateregate
