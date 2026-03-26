//go:build tools

// Package tools pins development tool versions so `go mod tidy` tracks them.
package tools

import (
	_ "github.com/securego/gosec/v2/cmd/gosec"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/boyter/scc"
)
