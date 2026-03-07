// NextDeploy CLI is a command-line interface for interacting with and managing
// Next.js app deployments across self-hosted infrastructure.
//
// It allows developers to initialize deployments, push code, monitor logs,
// and configure services using a simple declarative `nextdeploy.yml` file.
//
// Typical usage:
//
//		nextdeploy init        # Scaffold a Dockerfile and config
//	 nextdeploy build       # Build the app locally
//		nextdeploy ship        # Build and deploy app to server
//		nextdeploy update      # Update the CLI to the latest version
//
// Author: Yussuf Hersi <yussuf@hersi.dev>
// License: MIT
// Source: https://github.com/Golangcodes/nextdeploy
//
// ─────────────────────────────────────────────────────────────────────────────
package main

import (
	"github.com/Golangcodes/nextdeploy/cli/cmd"
)

func main() {
	cmd.Execute()
}
