// NextDeploy CLI is a command-line interface for interacting with and managing
// Next.js app deployments across self-hosted infrastructure.
//
// It allows developers to initialize deployments, push code, monitor logs,
// and configure services using a simple declarative `nextdeploy.yml` file.
//
// Typical usage:
//   nextdeploy init        # Scaffold a Dockerfile and config
//   nextdeploy ship    # Build and deploy app to server
//
// Author: Yussuf Hersi <yussuf@hersi.dev>
// License: MIT
// Source: https://github.com/aynaash/nextdeploy
//
// ─────────────────────────────────────────────────────────────────────────────

package main

import (
	"fmt"
	"github.com/Golangcodes/nextdeploy/cli/cmd"
	"os"
	"os/exec"
	"runtime"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		update()
		return
	}
	cmd.Execute()
}

func update() {
	owner := "aynaash"
	repo := "NextDeploy"
	latestURL := fmt.Sprintf("https://github.com/%s/%s/releases/latest/download/nextdeploy-%s-%s", owner, repo, runtime.GOOS, runtime.GOARCH)

	fmt.Println("Fetching latest release from:", latestURL)
	// #nosec G204
	cmd := exec.Command("curl", "-L", latestURL, "-o", "/usr/local/bin/nextdeploy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()

	if err != nil {
		fmt.Println("Error downloading the latest version:", err)
		return
	}
	_ = exec.Command("chmod", "+x", "/usr/local/bin/nextdeploy").Run()
	fmt.Println("NextDeploy has been updated to the latest version!")
}
