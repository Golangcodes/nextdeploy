// NextDeploy CLI is a command-line interface for interacting with and managing
// Next.js app deployments across self-hosted infrastructure.
//
// It allows developers to initialize deployments, push code, monitor logs,
// and configure services using a simple declarative `nextdeploy.yml` file.
//
// Typical usage:
//
//	nextdeploy init        # Scaffold a Dockerfile and config
//	nextdeploy ship        # Build and deploy app to server
//	nextdeploy update      # Update the CLI to the latest version
//
// Author: Yussuf Hersi <yussuf@hersi.dev>
// License: MIT
// Source: https://github.com/Golangcodes/nextdeploy
//
// ─────────────────────────────────────────────────────────────────────────────
package main

import (
	"fmt"
	"os"

	"github.com/Golangcodes/nextdeploy/cli/cmd"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/updater"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "update":
			if err := updater.SelfUpdate(shared.Version); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "version", "--version", "-v":
			fmt.Printf("nextdeploy %s\n", shared.Version)
			return
		}
	}
	cmd.Execute()
}
