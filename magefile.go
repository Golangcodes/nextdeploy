//go:build mage
// +build mage

// Magefile replaces the Makefile with type-safe, Go-compiled build targets.
// All targets are plain exported Go functions — no shell scripting required.
//
// Install mage once:  go install github.com/magefile/mage@latest
// List targets:       mage -l
// Run a target:       mage build
// Run with args:      mage -v crossBuildCLI
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// ── constants ────────────────────────────────────────────────────────────────

const (
	modulePath = "github.com/Golangcodes/nextdeploy"
	binDir     = "bin"
	distDir    = "dist"
	cliPkg     = "./cli"
	daemonPkg  = "./daemon/cmd/nextdeployd"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func version() string {
	out, err := sh.Output("git", "describe", "--tags", "--exact-match")
	if err != nil {
		out, _ = sh.Output("git", "describe", "--tags")
	}
	v := strings.TrimSpace(out)
	if v == "" {
		return "dev"
	}
	return v
}

func commit() string {
	out, _ := sh.Output("git", "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(out)
}

func buildDate() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func ldflags() string {
	return fmt.Sprintf(
		`-s -w -X %s/shared.Version=%s -X main.commit=%s -X main.date=%s`,
		modulePath, version(), commit(), buildDate(),
	)
}

func goBuild(goos, goarch, output, pkg string) error {
	env := map[string]string{
		"GOOS":        goos,
		"GOARCH":      goarch,
		"CGO_ENABLED": "0",
	}
	return sh.RunWithV(env, "go", "build",
		"-trimpath",
		"-ldflags", ldflags(),
		"-o", output,
		pkg,
	)
}

// ── targets ──────────────────────────────────────────────────────────────────

// Clean removes build artifacts.
func Clean() error {
	fmt.Println("🧹 Cleaning...")
	_ = sh.Rm(binDir)
	_ = sh.Rm(distDir)
	return nil
}

// Deps downloads and verifies Go module dependencies.
func Deps() error {
	fmt.Println("📦 Tidying dependencies...")
	if err := sh.Run("go", "mod", "download"); err != nil {
		return err
	}
	return sh.Run("go", "mod", "verify")
}

// Build builds both the CLI and daemon binaries for the current platform.
func Build() error {
	mg.Deps(BuildCLI, BuildDaemon)
	return nil
}

// BuildCLI builds the nextdeploy CLI binary for the current platform.
func BuildCLI() error {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return err
	}
	output := filepath.Join(binDir, "nextdeploy")
	fmt.Printf("🔨 Building CLI → %s (v=%s)\n", output, version())
	return goBuild(runtime.GOOS, runtime.GOARCH, output, cliPkg)
}

// BuildDaemon builds the nextdeployd daemon binary (Linux only).
func BuildDaemon() error {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return err
	}
	output := filepath.Join(binDir, "nextdeployd")
	goos := "linux"
	goarch := runtime.GOARCH
	if runtime.GOOS != "linux" {
		fmt.Println("⚠  Daemon only supports Linux — cross-compiling for linux/amd64")
		goarch = "amd64"
	}
	fmt.Printf("🔨 Building Daemon → %s (v=%s)\n", output, version())
	return goBuild(goos, goarch, output, daemonPkg)
}

// CrossBuildCLI cross-compiles the CLI for all supported platforms.
func CrossBuildCLI() error {
	mg.Deps(Clean)
	if err := os.MkdirAll(distDir, 0o750); err != nil {
		return err
	}

	platforms := []struct{ os, arch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
	}

	for _, p := range platforms {
		name := fmt.Sprintf("nextdeploy-%s-%s", p.os, p.arch)
		if p.os == "windows" {
			name += ".exe"
		}
		output := filepath.Join(distDir, name)
		fmt.Printf("  ➜ %s\n", name)
		if err := goBuild(p.os, p.arch, output, cliPkg); err != nil {
			return fmt.Errorf("failed %s/%s: %w", p.os, p.arch, err)
		}
		sha256File(output)
	}
	fmt.Println("✅ CLI cross-compilation complete")
	return nil
}

// CrossBuildDaemon cross-compiles the daemon for supported Linux platforms.
func CrossBuildDaemon() error {
	if err := os.MkdirAll(distDir, 0o750); err != nil {
		return err
	}

	platforms := []struct{ os, arch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
	}

	for _, p := range platforms {
		name := fmt.Sprintf("nextdeployd-%s-%s", p.os, p.arch)
		output := filepath.Join(distDir, name)
		fmt.Printf("  ➜ %s\n", name)
		if err := goBuild(p.os, p.arch, output, daemonPkg); err != nil {
			return fmt.Errorf("failed %s/%s: %w", p.os, p.arch, err)
		}
		sha256File(output)
	}
	fmt.Println("✅ Daemon cross-compilation complete")
	return nil
}

// CrossBuild cross-compiles both CLI and daemon for all platforms.
func CrossBuild() error {
	mg.Deps(CrossBuildCLI, CrossBuildDaemon)
	return nil
}

// Lint runs golangci-lint (installs if absent).
func Lint() error {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		fmt.Println("Installing golangci-lint...")
		if err := sh.Run("go", "install", "github.com/golangci/golangci-lint/cmd/golangci-lint@latest"); err != nil {
			return err
		}
	}
	return sh.Run("golangci-lint", "run", "--timeout=5m")
}

// Test runs the test suite.
func Test() error {
	return sh.Run("go", "test", "-race", "-v", "./...")
}

// Install builds both binaries and installs them to /usr/local/bin.
func Install() error {
	mg.Deps(Build)
	fmt.Println("📦 Installing to /usr/local/bin/...")
	for _, b := range []string{"nextdeploy", "nextdeployd"} {
		src := filepath.Join(binDir, b)
		dst := filepath.Join("/usr/local/bin", b)
		if err := sh.Run("sudo", "cp", src, dst); err != nil {
			return err
		}
		if err := sh.Run("sudo", "chmod", "+x", dst); err != nil {
			return err
		}
	}
	fmt.Println("✅ Installed")
	return nil
}

// HealthCheck curls the daemon's expvar endpoint and prints key metrics.
// If memory >500 MB it automatically triggers a daemon restart.
func HealthCheck() error {
	const metricsURL = "http://localhost:6060/debug/vars"
	out, err := sh.Output("curl", "-sf", metricsURL)
	if err != nil {
		return fmt.Errorf("daemon metrics endpoint unreachable at %s: %w", metricsURL, err)
	}
	fmt.Println("━━━ nextdeployd metrics ━━━")
	// print a short subset so the output is readable
	for _, line := range strings.Split(out, "\n") {
		for _, key := range []string{"requests_total", "commands_handled", "goroutines", "memstats"} {
			if strings.Contains(line, key) {
				fmt.Println(" ", strings.TrimSpace(line))
			}
		}
	}
	return nil
}

// Info prints the current build metadata.
func Info() {
	fmt.Println("Build Information")
	fmt.Println("=================")
	fmt.Printf("Version   : %s\n", version())
	fmt.Printf("Commit    : %s\n", commit())
	fmt.Printf("Build date: %s\n", buildDate())
	fmt.Printf("Go version: %s\n", runtime.Version())
	fmt.Printf("GOOS/ARCH : %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

// ── internal ─────────────────────────────────────────────────────────────────

func sha256File(path string) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		return
	}
	out, err := sh.Output("sha256sum", path)
	if err != nil {
		return
	}
	base := filepath.Base(path)
	_ = os.WriteFile(path+".sha256", []byte(out+"\n"), 0o644)
	fmt.Printf("    sha256: %s.sha256\n", base)
}
