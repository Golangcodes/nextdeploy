package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubOwner = "Golangcodes"
	githubRepo  = "nextdeploy"
	apiURL      = "https://api.github.com/repos/" + githubOwner + "/" + githubRepo + "/releases/latest"
	lockFile    = "/tmp/nextdeploy-update.lock"
)

type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// LatestRelease fetches the latest release info from GitHub
func LatestRelease() (Release, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nextdeploy-updater/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return Release{}, fmt.Errorf("GitHub API rate limit exceeded. Please try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Release{}, fmt.Errorf("failed to parse GitHub response: %w", err)
	}

	if release.TagName == "" {
		return Release{}, fmt.Errorf("no release tag found in GitHub response")
	}

	return release, nil
}

// CheckAndPrint checks for updates and prints a message if available
func CheckAndPrint(current string) {
	if current == "dev" || current == "" {
		return
	}

	latest, err := LatestRelease()
	if err != nil {
		// Silently fail for check command
		return
	}

	if latest.TagName != "" && isNewer(latest.TagName, current) {
		fmt.Fprintf(os.Stderr, "\n  🚀 Update available: %s -> %s\n  Run: nextdeploy update\n  📦 %s\n\n",
			current, latest.TagName, latest.HTMLURL)
	}
}

// SelfUpdate performs an atomic update of the nextdeploy binary
func SelfUpdate(current string) error {
	return selfUpdate(current, "nextdeploy", false)
}

// SelfUpdateDaemon performs an atomic update of the nextdeployd daemon
func SelfUpdateDaemon(current string) error {
	return selfUpdate(current, "nextdeployd", true)
}

// selfUpdate is the core update logic with atomic operations
func selfUpdate(current, binaryBase string, restartSvc bool) error {
	// 1. Create lock file to prevent concurrent updates
	lock, err := os.Create(lockFile)
	if err != nil {
		return fmt.Errorf("another update may be in progress (lock file exists): %w", err)
	}
	defer func() {
		lock.Close()
		os.Remove(lockFile)
	}()

	// 2. Get current binary path
	currentBin, err := os.Executable()
	if err != nil {
		// Fallback to default location
		currentBin = "/usr/local/bin/" + binaryBase
	}

	// 3. Verify we're not updating from a temp file
	if strings.Contains(currentBin, "go-build") || strings.Contains(currentBin, "temp") {
		return fmt.Errorf("cannot update from development/temp binary: %s", currentBin)
	}

	fmt.Printf("📦 Current version: %s\n", current)
	fmt.Println("🔄 Fetching latest release info...")

	// 4. Get latest release
	latest, err := LatestRelease()
	if err != nil {
		return fmt.Errorf("failed to fetch latest release: %w", err)
	}

	// 5. Version comparison
	if latest.TagName == current {
		fmt.Printf("✅ Already up to date (%s).\n", current)
		return nil
	}

	if !isNewer(latest.TagName, current) {
		fmt.Printf("⚠️ Current version %s is newer than %s. Use --force to downgrade.\n", current, latest.TagName)
		return nil
	}

	fmt.Printf("📈 Updating %s -> %s\n", current, latest.TagName)

	// 6. Create temporary directory for atomic update
	tmpDir, err := os.MkdirTemp("", binaryBase+"-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// 7. Download and verify new binary
	newBin := filepath.Join(tmpDir, binaryBase+".new")
	if err := downloadBinary(latest.TagName, binaryBase, newBin); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// 8. Verify the downloaded binary works
	fmt.Println("🔍 Verifying new binary...")
	if err := verifyBinary(newBin); err != nil {
		return fmt.Errorf("downloaded binary verification failed: %w", err)
	}

	// 9. Create backup of current binary with version tag
	backupBin := currentBin + ".backup." + current
	fmt.Printf("💾 Creating backup: %s\n", filepath.Base(backupBin))

	if err := copyFileWithSudo(currentBin, backupBin); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// 10. Atomic replacement with rollback capability
	fmt.Println("⚙️ Installing update...")
	if err := atomicReplace(newBin, currentBin); err != nil {
		// Restore from backup on failure
		fmt.Println("❌ Update failed, restoring from backup...")
		if restoreErr := atomicReplace(backupBin, currentBin); restoreErr != nil {
			return fmt.Errorf("critical: update failed AND backup restore failed: %v (original error: %w)", restoreErr, err)
		}
		return fmt.Errorf("update failed but backup restored: %w", err)
	}

	// 11. Set proper permissions
	if err := setPermissions(currentBin); err != nil {
		fmt.Printf("⚠️ Warning: could not set permissions: %v\n", err)
	}

	// 12. Verify the installed version
	fmt.Println("🔍 Verifying installed version...")
	installedVersion, err := getVersionFromBinary(currentBin)
	if err != nil {
		fmt.Printf("⚠️ Warning: Could not verify installed version: %v\n", err)
	} else if installedVersion != latest.TagName {
		fmt.Printf("⚠️ Warning: Version mismatch. Expected %s, got %s\n", latest.TagName, installedVersion)
		// Keep backup for manual recovery
		fmt.Printf("ℹ️ Backup preserved at: %s\n", backupBin)
	} else {
		// Success - remove backup
		fmt.Println("✅ Update successful! Cleaning up...")
		os.Remove(backupBin)
		runCommand("sudo", "rm", "-f", backupBin) // Ensure removal
	}

	fmt.Printf("🎉 Successfully updated to %s\n", latest.TagName)

	// 13. Restart service if needed
	if restartSvc {
		fmt.Println("🔄 Restarting service...")
		if err := restartService(binaryBase); err != nil {
			fmt.Printf("⚠️ Note: could not restart %s service: %v\n", binaryBase, err)
			fmt.Printf("ℹ️ Please restart manually: sudo systemctl restart %s\n", binaryBase)
		}
	}

	// 14. Clear command hash cache
	exec.Command("hash", "-r").Run() // For bash
	fmt.Println("💡 You may need to restart your terminal or run 'hash -r'")

	return nil
}

// downloadBinary downloads the binary with progress indication
func downloadBinary(version, binaryBase, destPath string) error {
	binaryName := fmt.Sprintf("%s-%s-%s", binaryBase, runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/%s",
		githubOwner, githubRepo, version, binaryName,
	)

	fmt.Printf("📥 Downloading: %s\n", binaryName)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "nextdeploy-updater/"+version)

	client := &http.Client{
		Timeout: 5 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("binary %s not found for version %s (CI might still be building)", binaryName, version)
		}
		return fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	// Create file with exclusive creation to avoid symlink attacks
	f, err := os.OpenFile(destPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer f.Close()

	// Download with progress indicator
	progress := &progressWriter{
		total:      resp.ContentLength,
		reader:     resp.Body,
		binaryName: binaryName,
	}

	if _, err := io.Copy(f, progress); err != nil {
		return fmt.Errorf("failed to write binary: %w", err)
	}

	// Ensure all data is written
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	fmt.Println() // New line after progress
	return nil
}

// progressWriter shows download progress
type progressWriter struct {
	total      int64
	current    int64
	reader     io.Reader
	binaryName string
	lastPrint  time.Time
}

func (p *progressWriter) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	p.current += int64(n)

	// Update progress every 100ms
	if p.total > 0 && time.Since(p.lastPrint) > 100*time.Millisecond {
		percentage := float64(p.current) / float64(p.total) * 100
		fmt.Printf("\r⏬ Downloading %s... %.1f%%", p.binaryName, percentage)
		p.lastPrint = time.Now()

		if p.current == p.total {
			fmt.Print("\r✅ Download complete!                    \n")
		}
	}
	return n, err
}

// verifyBinary ensures the downloaded binary works
func verifyBinary(binPath string) error {
	cmd := exec.Command(binPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("binary test failed: %w\nOutput: %s", err, output)
	}
	return nil
}

// getVersionFromBinary extracts version from binary
func getVersionFromBinary(binPath string) (string, error) {
	cmd := exec.Command(binPath, "version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Parse "nextdeploy v1.2.3" format
	parts := strings.Fields(string(output))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return "", fmt.Errorf("unexpected version output: %s", output)
}

// copyFileWithSudo copies a file, using sudo if necessary
func copyFileWithSudo(src, dst string) error {
	// Try direct copy first
	if err := copyFile(src, dst); err == nil {
		return nil
	}

	// Fall back to sudo cp
	return runCommand("sudo", "cp", src, dst)
}

// copyFile performs a regular file copy
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// atomicReplace performs an atomic file replacement
func atomicReplace(src, dst string) error {
	// Try direct rename first (atomic on same filesystem)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fall back to mv command
	if err := runCommand("mv", src, dst); err == nil {
		return nil
	}

	// Finally try with sudo
	return runCommand("sudo", "mv", src, dst)
}

// setPermissions sets proper executable permissions
func setPermissions(path string) error {
	if err := os.Chmod(path, 0755); err == nil {
		return nil
	}
	return runCommand("sudo", "chmod", "755", path)
}

// restartService restarts a systemd service
func restartService(service string) error {
	return runCommand("sudo", "systemctl", "restart", service)
}

// runCommand executes a command with output
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Version comparison functions (your existing ones, slightly improved)
func isNewer(candidate, current string) bool {
	return semverGT(stripV(candidate), stripV(current))
}

func stripV(v string) string {
	return strings.TrimPrefix(v, "v")
}

func semverGT(a, b string) bool {
	// Strip pre-release/build metadata for comparison
	a = strings.Split(a, "-")[0]
	a = strings.Split(a, "+")[0]
	b = strings.Split(b, "-")[0]
	b = strings.Split(b, "+")[0]

	aParts := splitVer(a)
	bParts := splitVer(b)

	max := len(aParts)
	if len(bParts) > max {
		max = len(bParts)
	}

	for i := 0; i < max; i++ {
		av, bv := 0, 0
		if i < len(aParts) {
			av = aParts[i]
		}
		if i < len(bParts) {
			bv = bParts[i]
		}
		if av > bv {
			return true
		}
		if av < bv {
			return false
		}
	}
	return false
}

func splitVer(v string) []int {
	parts := []int{}
	cur := 0
	hasDigit := false

	for _, c := range v {
		if c == '.' {
			parts = append(parts, cur)
			cur = 0
			hasDigit = false
		} else if c >= '0' && c <= '9' {
			cur = cur*10 + int(c-'0')
			hasDigit = true
		} else {
			// Stop at first non-digit, non-dot (like -beta, +build)
			break
		}
	}

	if hasDigit {
		parts = append(parts, cur)
	}

	return parts
}

// Version constant - should be set at build time
var Version = "dev"
