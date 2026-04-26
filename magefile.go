//go:build mage
// +build mage
package main

import (
	"bufio"
	"fmt"
	"io"
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

func gitQuiet(args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func version() string {
	if v := gitQuiet("describe", "--tags", "--exact-match"); v != "" {
		return v
	}
	if v := gitQuiet("describe", "--tags"); v != "" {
		return v
	}
	return "dev"
}

func commit() string {
	return gitQuiet("rev-parse", "--short", "HEAD")
}

func buildDate() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func ldflags() string {
	return fmt.Sprintf(
		`-s -w -X %s/shared.Version=%s -X %s/shared.Commit=%s -X main.commit=%s -X main.date=%s`,
		modulePath, version(), modulePath, commit(), commit(), buildDate(),
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

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return os.Getenv("HOME")
	}
	return h
}

func devBinDir() string {
	return filepath.Join(home(), ".nextdeploy", "bin")
}

// testPkgs returns the set of Go packages under ./... excluding vendor and the
// test-serverless-app fixture.
func testPkgs() ([]string, error) {
	out, err := sh.Output("go", "list", "./...")
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "/test-serverless-app/") || strings.Contains(line, "/vendor/") {
			continue
		}
		pkgs = append(pkgs, line)
	}
	return pkgs, nil
}

func ensureTool(name, installSpec string) error {
	if _, err := exec.LookPath(name); err == nil {
		return nil
	}
	fmt.Printf("Installing %s...\n", name)
	return sh.Run("go", "install", installSpec)
}

// ensureDevBinOnPath appends ~/.nextdeploy/bin to the user's ~/.bashrc if not
// already present. Idempotent. Skips silently if .bashrc can't be opened.
func ensureDevBinOnPath() {
	bashrc := filepath.Join(home(), ".bashrc")
	data, err := os.ReadFile(bashrc)
	if err == nil && strings.Contains(string(data), devBinDir()) {
		return
	}
	f, err := os.OpenFile(bashrc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, `export PATH="$HOME/.nextdeploy/bin:$PATH"`)
	fmt.Println("Added ~/.nextdeploy/bin to ~/.bashrc — run 'source ~/.bashrc' in other shells to pick it up.")
}

// ── build targets ────────────────────────────────────────────────────────────

// Clean removes local build artifacts (bin/, dist/, coverage files).
func Clean() error {
	fmt.Println("Cleaning build artifacts...")
	_ = sh.Rm(binDir)
	_ = sh.Rm(distDir)
	_ = os.Remove("coverage.out")
	_ = os.Remove("coverage.html")
	return nil
}

// CleanAll is Clean plus wiping ~/.nextdeploy/bin dev binaries.
func CleanAll() error {
	mg.Deps(Clean)
	_ = os.Remove(filepath.Join(devBinDir(), "nextdeploy"))
	_ = os.Remove(filepath.Join(devBinDir(), "nextdeployd"))
	fmt.Println("Dev binaries removed")
	return nil
}

// Deps downloads and verifies Go module dependencies.
func Deps() error {
	fmt.Println("Tidying dependencies...")
	if err := sh.Run("go", "mod", "download"); err != nil {
		return err
	}
	return sh.Run("go", "mod", "verify")
}

// Build builds both the CLI and daemon for the current platform.
func Build() error {
	mg.Deps(BuildCLI, BuildDaemon)
	return nil
}

// BuildCLI builds the nextdeploy CLI binary into ./bin/.
func BuildCLI() error {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return err
	}
	output := filepath.Join(binDir, "nextdeploy")
	fmt.Printf("Building CLI -> %s (v=%s)\n", output, version())
	return goBuild(runtime.GOOS, runtime.GOARCH, output, cliPkg)
}

// BuildCLIDev builds the CLI directly into ~/.nextdeploy/bin for local dev.
func BuildCLIDev() error {
	dst := devBinDir()
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	output := filepath.Join(dst, "nextdeploy")
	fmt.Printf("Building CLI (dev) -> %s\n", output)
	if err := goBuild(runtime.GOOS, runtime.GOARCH, output, cliPkg); err != nil {
		return err
	}
	ensureDevBinOnPath()
	return nil
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
		fmt.Println("Daemon only supports Linux — cross-compiling for linux/amd64")
		goarch = "amd64"
	}
	fmt.Printf("Building Daemon -> %s (v=%s)\n", output, version())
	return goBuild(goos, goarch, output, daemonPkg)
}

// BuildDaemonDev builds the daemon into ~/.nextdeploy/bin for local dev.
func BuildDaemonDev() error {
	dst := devBinDir()
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	output := filepath.Join(dst, "nextdeployd")
	fmt.Printf("Building Daemon (dev) -> %s\n", output)
	return goBuild(runtime.GOOS, runtime.GOARCH, output, daemonPkg)
}

// CrossBuildCLI cross-compiles the CLI for all supported platforms into dist/.
func CrossBuildCLI() error {
	if err := os.MkdirAll(distDir, 0o750); err != nil {
		return err
	}
	platforms := []struct{ os, arch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"},
	}
	for _, p := range platforms {
		name := fmt.Sprintf("nextdeploy-%s-%s", p.os, p.arch)
		if p.os == "windows" {
			name += ".exe"
		}
		output := filepath.Join(distDir, name)
		fmt.Printf("  -> %s\n", name)
		if err := goBuild(p.os, p.arch, output, cliPkg); err != nil {
			return fmt.Errorf("failed %s/%s: %w", p.os, p.arch, err)
		}
		sha256File(output)
	}
	fmt.Println("CLI cross-compilation complete")
	return nil
}

// CrossBuildDaemon cross-compiles the daemon for supported Linux platforms.
func CrossBuildDaemon() error {
	if err := os.MkdirAll(distDir, 0o750); err != nil {
		return err
	}
	platforms := []struct{ os, arch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
	}
	for _, p := range platforms {
		name := fmt.Sprintf("nextdeployd-%s-%s", p.os, p.arch)
		output := filepath.Join(distDir, name)
		fmt.Printf("  -> %s\n", name)
		if err := goBuild(p.os, p.arch, output, daemonPkg); err != nil {
			return fmt.Errorf("failed %s/%s: %w", p.os, p.arch, err)
		}
		sha256File(output)
	}
	fmt.Println("Daemon cross-compilation complete")
	return nil
}

// CrossBuild cross-compiles both CLI and daemon for all supported platforms.
func CrossBuild() error {
	mg.Deps(CrossBuildCLI, CrossBuildDaemon)
	return nil
}

// BuildAll runs local Build + full CrossBuild.
func BuildAll() error {
	mg.SerialDeps(Build, CrossBuild)
	return nil
}

// ── format / lint / security ─────────────────────────────────────────────────

// Fmt formats all Go files with gofmt -s and goimports.
func Fmt() error {
	fmt.Println("Formatting Go files...")
	if err := sh.Run("gofmt", "-s", "-w", "."); err != nil {
		return err
	}
	if err := ensureTool("goimports", "golang.org/x/tools/cmd/goimports@latest"); err != nil {
		return err
	}
	return sh.Run("goimports", "-w", ".")
}

// FmtCheck fails if any Go file needs reformatting (CI-friendly).
func FmtCheck() error {
	out, err := sh.Output("gofmt", "-s", "-l", ".")
	if err != nil {
		return err
	}
	var bad []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "vendor/") {
			continue
		}
		bad = append(bad, line)
	}
	if len(bad) > 0 {
		fmt.Println("Files need formatting:")
		for _, f := range bad {
			fmt.Println("  ", f)
		}
		return fmt.Errorf("gofmt check failed: %d files need formatting", len(bad))
	}
	fmt.Println("Formatting OK")
	return nil
}

// Lint runs golangci-lint (installing it if missing).
func Lint() error {
	if err := ensureTool("golangci-lint", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"); err != nil {
		return err
	}
	return sh.Run("golangci-lint", "run", "--timeout=5m")
}

// SecurityScan runs gosec and govulncheck.
func SecurityScan() error {
	if err := ensureTool("gosec", "github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"); err != nil {
		return err
	}
	if err := sh.Run("gosec", "./..."); err != nil {
		return err
	}
	if err := ensureTool("govulncheck", "golang.org/x/vuln/cmd/govulncheck@latest"); err != nil {
		return err
	}
	return sh.Run("govulncheck", "./...")
}

// ── tests ────────────────────────────────────────────────────────────────────

// Test is an alias for TestUnit.
func Test() error { return TestUnit() }

// TestUnit runs unit tests (skips integration build tag).
func TestUnit() error {
	mg.Deps(TestBridge)
	pkgs, err := testPkgs()
	if err != nil {
		return err
	}
	fmt.Println("Running unit tests...")
	args := append([]string{"test", "-race"}, pkgs...)
	return sh.RunV("go", args...)
}

// TestBridge runs the Node.js bridge.js runtime tests.
func TestBridge() error {
	fmt.Println("Running bridge.js tests...")
	return sh.RunWithV(nil, "bash", "-c", "cd internal/packaging/runtime && node --test bridge.test.js")
}

// TestCover runs tests per-package to produce coverage.out (works around the
// go1.25 covdata issue where a single -coverprofile across ./... corrupts).
func TestCover() error {
	pkgs, err := testPkgs()
	if err != nil {
		return err
	}
	_ = os.Remove("coverage.out")
	if err := os.WriteFile("coverage.out", []byte("mode: atomic\n"), 0o644); err != nil {
		return err
	}
	for _, p := range pkgs {
		tmp := "cover.tmp"
		_ = sh.Run("go", "test", "-race", "-covermode=atomic", "-coverprofile="+tmp, p)
		if data, err := os.ReadFile(tmp); err == nil {
			scanner := bufio.NewScanner(strings.NewReader(string(data)))
			var lines []string
			first := true
			for scanner.Scan() {
				if first {
					first = false
					continue // skip "mode: atomic" header
				}
				lines = append(lines, scanner.Text())
			}
			if len(lines) > 0 {
				f, err := os.OpenFile("coverage.out", os.O_APPEND|os.O_WRONLY, 0o644)
				if err == nil {
					fmt.Fprintln(f, strings.Join(lines, "\n"))
					f.Close()
				}
			}
			_ = os.Remove(tmp)
		}
	}
	return sh.RunV("go", "tool", "cover", "-func=coverage.out")
}

// TestIntegration runs tests with the integration build tag (needs AWS creds).
func TestIntegration() error {
	pkgs, err := testPkgs()
	if err != nil {
		return err
	}
	fmt.Println("Running integration tests...")
	args := append([]string{"test", "-race", "-tags=integration", "-timeout=10m"}, pkgs...)
	return sh.RunV("go", args...)
}

// TestVerbose runs unit tests with verbose output and coverage.
func TestVerbose() error {
	pkgs, err := testPkgs()
	if err != nil {
		return err
	}
	args := append([]string{"test", "-race", "-v", "-coverprofile=coverage.out", "-covermode=atomic"}, pkgs...)
	return sh.RunV("go", args...)
}

// TestPkg runs tests for a single package. Usage: mage testPkg ./cli/internal/serverless
func TestPkg(pkg string) error {
	return sh.RunV("go", "test", "-race", "-v", pkg)
}

// Coverage runs full test suite with coverage and opens the HTML report path.
func Coverage() error {
	if err := sh.RunV("go", "test", "-race", "-coverprofile=coverage.out", "-covermode=atomic", "./..."); err != nil {
		return err
	}
	if err := sh.Run("go", "tool", "cover", "-html=coverage.out", "-o", "coverage.html"); err != nil {
		return err
	}
	fmt.Println("Coverage report: coverage.html")
	return sh.RunV("bash", "-c", "go tool cover -func=coverage.out | tail -1")
}

// ── bench / stats ────────────────────────────────────────────────────────────

// Bench runs `go test -bench` across every package.
func Bench() error {
	return sh.RunV("go", "test", "-bench=.", "-benchmem", "-run=^$", "./...")
}

// BenchStartup measures CLI startup time and compares against vercel/sst/wrangler.
func BenchStartup() error {
	mg.Deps(BuildCLI)
	return sh.RunV("./scripts/bench-startup.sh")
}

// Loc prints lines-of-code stats (requires scc).
func Loc() error {
	if err := ensureTool("scc", "github.com/boyter/scc/v3@latest"); err != nil {
		return err
	}
	return sh.RunV("scc", "--format", "wide", "--exclude-dir", "vendor,test-serverless-app,.next")
}

// Stats is an alias for Loc.
func Stats() error { return Loc() }

// ── meta workflows ───────────────────────────────────────────────────────────

// Quality runs the full quality suite: lint + security + coverage + LOC.
func Quality() error {
	mg.SerialDeps(Lint, SecurityScan, Coverage, Loc)
	return nil
}

// DevCheck runs deps + lint + test + security — the local preflight.
func DevCheck() error {
	mg.SerialDeps(Deps, Lint, Test, SecurityScan)
	return nil
}

// ReleasePrep runs clean + DevCheck + BuildAll.
func ReleasePrep() error {
	mg.SerialDeps(Clean, DevCheck, BuildAll)
	return nil
}

// Install copies bin/nextdeploy and bin/nextdeployd into /usr/local/bin (sudo).
func Install() error {
	mg.Deps(Build)
	fmt.Println("Installing to /usr/local/bin/...")
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
	fmt.Println("Installed")
	return nil
}

// PreCommitInstall wires .githooks/pre-commit as the active git hook.
func PreCommitInstall() error {
	if _, err := os.Stat(".githooks/pre-commit"); err != nil {
		return fmt.Errorf(".githooks/pre-commit not found")
	}
	if err := sh.Run("git", "config", "core.hooksPath", ".githooks"); err != nil {
		return err
	}
	if err := os.Chmod(".githooks/pre-commit", 0o755); err != nil {
		return err
	}
	fmt.Println("Pre-commit hook installed (git core.hooksPath = .githooks)")
	return nil
}

// ── dev / watch ──────────────────────────────────────────────────────────────

// Dev is an alias for DevCLI — watch CLI source and rebuild on change.
func Dev() error { return DevCLI() }

// DevCLI watches CLI source with air, rebuilding ~/.nextdeploy/bin/nextdeploy
// on every change. Run `nextdeploy <cmd>` in a second terminal.
func DevCLI() error {
	if err := ensureTool("air", "github.com/air-verse/air@latest"); err != nil {
		return err
	}
	if err := os.MkdirAll(devBinDir(), 0o750); err != nil {
		return err
	}
	ensureDevBinOnPath()
	fmt.Println("Watching cli/ and shared/ — rebuilds ~/.nextdeploy/bin/nextdeploy on change.")
	return sh.RunV("air", "-c", ".air.cli.toml")
}

// DevDaemon watches daemon source with air and restarts it on change.
func DevDaemon() error {
	if err := ensureTool("air", "github.com/air-verse/air@latest"); err != nil {
		return err
	}
	return sh.RunV("air", "-c", ".air.daemon.toml")
}

// ── docker ───────────────────────────────────────────────────────────────────

// DockerBuild builds a local Docker image tagged with the current version.
func DockerBuild() error {
	tag := version()
	if err := sh.RunV("docker", "build", "-t", "nextdeploy:"+tag, "."); err != nil {
		return err
	}
	return sh.RunV("docker", "build", "-t", "nextdeploy:latest", ".")
}

// DockerBuildx builds a multi-platform image (linux/amd64 + linux/arm64).
func DockerBuildx() error {
	tag := version()
	return sh.RunV("docker", "buildx", "build",
		"--platform", "linux/amd64,linux/arm64",
		"-t", "nextdeploy:"+tag,
		"-t", "nextdeploy:latest",
		".")
}

// ── diagnostics ──────────────────────────────────────────────────────────────

// HealthCheck pings the daemon's expvar endpoint and prints key metrics.
func HealthCheck() error {
	const metricsURL = "http://localhost:6060/debug/vars"
	out, err := sh.Output("curl", "-sf", metricsURL)
	if err != nil {
		return fmt.Errorf("daemon metrics endpoint unreachable at %s: %w", metricsURL, err)
	}
	fmt.Println("━━━ nextdeployd metrics ━━━")
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
