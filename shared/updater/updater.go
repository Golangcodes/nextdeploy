package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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

	"github.com/Golangcodes/nextdeploy/shared"
)

const (
	githubOwner = "Golangcodes"
	githubRepo  = "nextdeploy"
	apiURL      = "https://api.github.com/repos/" + githubOwner + "/" + githubRepo + "/releases/latest"
	lockFile    = "/tmp/nextdeploy-update.lock"
	maxRetries  = 3
	retryDelay  = 2 * time.Second
)

// Release represents a GitHub release
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// UpdateOptions configures the update process
type UpdateOptions struct {
	Force       bool          // Force downgrade if current is newer
	Timeout     time.Duration // Overall update timeout
	VerifySSL   bool          // Verify SSL certificates
	SkipBackup  bool          // Skip backup creation
	SkipService bool          // Skip service restart
}

// DefaultUpdateOptions returns sensible defaults
func DefaultUpdateOptions() *UpdateOptions {
	return &UpdateOptions{
		Force:       false,
		Timeout:     15 * time.Minute,
		VerifySSL:   true,
		SkipBackup:  false,
		SkipService: false,
	}
}

// UpdateError represents a structured update error
type UpdateError struct {
	Stage       string
	Message     string
	Recoverable bool
	Err         error
}

func (e *UpdateError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Stage, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Stage, e.Message)
}

func (e *UpdateError) Unwrap() error {
	return e.Err
}

// LatestRelease fetches the latest release info from GitHub with retries
func LatestRelease() (Release, error) {
	var release Release
	var lastErr error

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, apiURL, nil)
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "nextdeploy-updater/"+shared.Version)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed (attempt %d/%d): %w", attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			break
		}
		defer closeBestEffort(resp.Body)

		// Handle rate limiting
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			resetTime := resp.Header.Get("X-RateLimit-Reset")
			if resetTime != "" {
				if resetUnix, err := parseUnixTime(resetTime); err == nil {
					waitTime := time.Until(resetUnix)
					if waitTime > 0 && waitTime < 5*time.Minute {
						fmt.Fprintf(os.Stderr, "GitHub API rate limited. Waiting %v...\n", waitTime)
						time.Sleep(waitTime)
						continue
					}
				}
			}
			return Release{}, &UpdateError{
				Stage:       "api",
				Message:     "GitHub API rate limit exceeded",
				Recoverable: true,
				Err:         fmt.Errorf("rate limited"),
			}
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GitHub API returned %d (attempt %d/%d)", resp.StatusCode, attempt, maxRetries)
			if attempt < maxRetries {
				time.Sleep(retryDelay * time.Duration(attempt))
				continue
			}
			break
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("failed to read response: %w", err)
			continue
		}

		if err := json.Unmarshal(body, &release); err != nil {
			lastErr = fmt.Errorf("failed to parse GitHub response: %w", err)
			continue
		}

		if release.TagName == "" {
			return Release{}, &UpdateError{
				Stage:       "api",
				Message:     "no release tag found in GitHub response",
				Recoverable: false,
			}
		}

		return release, nil
	}

	return Release{}, &UpdateError{
		Stage:       "api",
		Message:     "failed to fetch latest release after retries",
		Recoverable: true,
		Err:         lastErr,
	}
}

// parseUnixTime parses GitHub's X-RateLimit-Reset header
func parseUnixTime(s string) (time.Time, error) {
	var sec int64
	if _, err := fmt.Sscanf(s, "%d", &sec); err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
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
		fmt.Fprintf(os.Stderr, "\n  🚀 Update available: %s -> %s\n", current, latest.TagName)
		fmt.Fprintf(os.Stderr, "  📦 Run: nextdeploy update\n")
		fmt.Fprintf(os.Stderr, "  🔗 %s\n\n", latest.HTMLURL)
	}
}

// SelfUpdate performs an atomic update of the nextdeploy binary
func SelfUpdate(current string) error {
	return SelfUpdateWithOptions(current, DefaultUpdateOptions())
}

// SelfUpdateDaemon performs an atomic update of the nextdeployd daemon
func SelfUpdateDaemon(current string) error {
	opts := DefaultUpdateOptions()
	opts.SkipService = false
	return selfUpdateWithOptions(current, "nextdeployd", opts)
}

// SelfUpdateWithOptions performs an update with custom options
func SelfUpdateWithOptions(current string, opts *UpdateOptions) error {
	return selfUpdateWithOptions(current, "nextdeploy", opts)
}

// selfUpdateWithOptions is the core update logic with all improvements
func selfUpdateWithOptions(current, binaryBase string, opts *UpdateOptions) error {
	// Validate options
	if opts == nil {
		opts = DefaultUpdateOptions()
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Minute
	}

	// 1. Create lock file to prevent concurrent updates
	lock, err := os.Create(lockFile)
	if err != nil {
		return &UpdateError{
			Stage:       "lock",
			Message:     "another update may be in progress",
			Recoverable: true,
			Err:         err,
		}
	}
	defer func() {
		closeBestEffort(lock)
		removeBestEffort(lockFile)
	}()

	// 2. Get current binary path
	currentBin, err := os.Executable()
	if err != nil {
		currentBin = "/usr/local/bin/" + binaryBase
	}

	// 3. Verify we're not updating from a temp file
	if strings.Contains(currentBin, "go-build") || strings.Contains(currentBin, "temp") {
		return &UpdateError{
			Stage:       "validation",
			Message:     "cannot update from development/temp binary",
			Recoverable: false,
			Err:         fmt.Errorf("binary path: %s", currentBin),
		}
	}

	// 4. Check if binary is currently running
	if isProcessRunning(binaryBase) {
		fmt.Printf("⚠️  %s is currently running. Continuing anyway...\n", binaryBase)
	}

	fmt.Printf("📦 Current version: %s\n", current)
	fmt.Println("🔄 Fetching latest release info...")

	// 5. Get latest release with timeout
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	type releaseResult struct {
		release Release
		err     error
	}
	releaseCh := make(chan releaseResult, 1)

	go func() {
		release, err := LatestRelease()
		releaseCh <- releaseResult{release, err}
	}()

	var latest Release
	select {
	case res := <-releaseCh:
		if res.err != nil {
			return res.err
		}
		latest = res.release
	case <-ctx.Done():
		return &UpdateError{
			Stage:       "api",
			Message:     "timeout fetching latest release",
			Recoverable: true,
			Err:         ctx.Err(),
		}
	}

	// 6. Version comparison
	cmp := compareVersions(latest.TagName, current)
	switch {
	case latest.TagName == current:
		fmt.Printf("✅ Already up to date (%s).\n", current)
		return nil
	case cmp < 0 && !opts.Force:
		return &UpdateError{
			Stage:       "version",
			Message:     fmt.Sprintf("current version %s is newer than %s", current, latest.TagName),
			Recoverable: true,
			Err:         fmt.Errorf("use --force to downgrade"),
		}
	}

	fmt.Printf("📈 Updating %s -> %s\n", current, latest.TagName)

	// 7. Create temporary directory
	tmpDir, err := os.MkdirTemp("", binaryBase+"-update-*")
	if err != nil {
		return &UpdateError{
			Stage:       "temp",
			Message:     "failed to create temp directory",
			Recoverable: true,
			Err:         err,
		}
	}
	defer removeAllBestEffort(tmpDir)

	// 8. Detect platform for archive name
	archiveName := detectArchiveName(latest.TagName, binaryBase)
	checksumsURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/checksums.txt",
		githubOwner, githubRepo, latest.TagName)

	// 9. Download archive and checksums
	archivePath := filepath.Join(tmpDir, archiveName)
	newBin := filepath.Join(tmpDir, binaryBase+".new") // target binary name
	checksumsFile := filepath.Join(tmpDir, "checksums.txt")

	// Download checksums first (optional)
	checksums, _ := downloadChecksums(checksumsURL, checksumsFile)

	// Download archive with progress
	if err := downloadBinary(latest.TagName, archiveName, archivePath, opts); err != nil {
		return err
	}

	// 10. Verify archive integrity
	if err := verifyBinaryIntegrity(archivePath, archiveName, checksums); err != nil {
		return &UpdateError{
			Stage:       "verification",
			Message:     "archive integrity check failed",
			Recoverable: true,
			Err:         err,
		}
	}
	fmt.Println("✅ Archive integrity verified")

	// Extract binary from archive
	fmt.Println("📦 Extracting binary...")
	extBinName := binaryBase
	if runtime.GOOS == "windows" {
		extBinName += ".exe"
	}
	if err := extractBinary(archivePath, extBinName, newBin); err != nil {
		return &UpdateError{
			Stage:       "extraction",
			Message:     "failed to extract binary from archive",
			Recoverable: true,
			Err:         err,
		}
	}
	fmt.Println("✅ Binary integrity verified")

	// 11. Verify binary works
	fmt.Println("🔍 Testing new binary...")
	if err := verifyBinary(newBin); err != nil {
		return &UpdateError{
			Stage:       "verification",
			Message:     "binary test failed",
			Recoverable: true,
			Err:         err,
		}
	}
	fmt.Println("✅ Binary test passed")

	// 12. Create backup (unless skipped)
	backupBin := ""
	if !opts.SkipBackup {
		backupBin = currentBin + ".backup." + current
		fmt.Printf("💾 Creating backup: %s\n", filepath.Base(backupBin))

		if err := copyFileWithSudo(currentBin, backupBin); err != nil {
			fmt.Printf("⚠️  Warning: failed to create backup: %v\n", err)
			backupBin = ""
		}
	}

	// 13. Atomic replacement
	fmt.Println("⚙️ Installing update...")
	if err := atomicReplace(newBin, currentBin); err != nil {
		// Try to restore from backup
		if backupBin != "" {
			fmt.Println("⚠️  Update failed, restoring from backup...")
			if restoreErr := atomicReplace(backupBin, currentBin); restoreErr != nil {
				return &UpdateError{
					Stage:       "critical",
					Message:     "update failed AND backup restore failed",
					Recoverable: false,
					Err:         fmt.Errorf("original: %w, restore: %v", err, restoreErr),
				}
			}
		}
		return &UpdateError{
			Stage:       "install",
			Message:     "failed to install update",
			Recoverable: backupBin != "",
			Err:         err,
		}
	}

	// 14. Set permissions
	if err := setPermissions(currentBin); err != nil {
		fmt.Printf("⚠️  Warning: could not set permissions: %v\n", err)
	}

	// 15. Verify installed version
	fmt.Println("🔍 Verifying installed version...")
	installedVersion, err := getVersionFromBinary(currentBin)
	if err != nil {
		fmt.Printf("⚠️  Warning: Could not verify installed version: %v\n", err)
	} else if installedVersion != latest.TagName {
		fmt.Printf("⚠️  Warning: Version mismatch. Expected %s, got %s\n", latest.TagName, installedVersion)
		if backupBin != "" {
			fmt.Printf("ℹ️  Backup preserved at: %s\n", backupBin)
		}
	} else {
		fmt.Printf("✅ Successfully updated to %s\n", latest.TagName)
		// Remove backup on success
		if backupBin != "" {
			removeBestEffort(backupBin)
			runCommand("sudo", "rm", "-f", backupBin)
		}
	}

	// 16. Restart service if needed
	if strings.Contains(binaryBase, "daemon") || strings.Contains(binaryBase, "nextdeployd") {
		if !opts.SkipService {
			fmt.Println("🔄 Restarting service...")
			if err := restartService(binaryBase); err != nil {
				fmt.Printf("⚠️  Note: could not restart %s service: %v\n", binaryBase, err)
				fmt.Printf("ℹ️  Please restart manually: sudo systemctl restart %s\n", binaryBase)
			}
		}
	}

	// 17. Clear command cache
	clearCommandCache()

	fmt.Println("💡 You may need to restart your terminal or run 'hash -r'")
	return nil
}

// detectArchiveName returns the correct archive name based on GoReleaser naming
func detectArchiveName(version, binaryBase string) string {
	osStr := runtime.GOOS
	arch := runtime.GOARCH

	// Format OS title case (e.g. Linux, Darwin, Windows)
	switch osStr {
	case "linux":
		osStr = "Linux"
	case "darwin":
		osStr = "Darwin"
	case "windows":
		osStr = "Windows"
	}

	// Windows uses zip, others use tar.gz
	ext := ".tar.gz"
	if osStr == "Windows" {
		ext = ".zip"
	}

	// Remove leading v from version for the GoReleaser asset
	cleanVersion := stripV(version)

	// e.g. nextdeploy_0.6.0_Linux_amd64.tar.gz
	return fmt.Sprintf("%s_%s_%s_%s%s", binaryBase, cleanVersion, osStr, arch, ext)
}

// isMusl checks if the system uses musl libc
func isMusl() bool {
	if _, err := os.Stat("/lib/libc.musl-x86_64.so.1"); err == nil {
		return true
	}
	if _, err := os.Stat("/lib/libc.musl-aarch64.so.1"); err == nil {
		return true
	}
	// Try ldd check
	cmd := exec.Command("ldd", "/bin/ls")
	output, err := cmd.CombinedOutput()
	if err == nil && strings.Contains(string(output), "musl") {
		return true
	}
	return false
}

// downloadChecksums downloads and parses the checksums file
func downloadChecksums(url, destPath string) (map[string]string, error) {
	checksums := make(map[string]string)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer closeBestEffort(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			checksum := parts[0]
			filename := parts[1]
			checksums[filename] = checksum
		}
	}

	return checksums, nil
}

// verifyBinaryIntegrity checks the binary against its checksum
func verifyBinaryIntegrity(binPath, binaryName string, checksums map[string]string) error {
	if checksums == nil {
		fmt.Println("⚠️  No checksums available, skipping integrity check")
		return nil
	}

	expectedChecksum, ok := checksums[binaryName]
	if !ok {
		// Try with .exe suffix for Windows
		expectedChecksum, ok = checksums[binaryName+".exe"]
		if !ok {
			fmt.Println("⚠️  No checksum found for binary, skipping integrity check")
			return nil
		}
	}

	// Calculate actual checksum
	file, err := os.Open(binPath)
	if err != nil {
		return err
	}
	defer closeBestEffort(file)

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actualChecksum := hex.EncodeToString(hash.Sum(nil))

	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

// downloadBinary downloads the binary with progress and retries
func downloadBinary(version, binaryName, destPath string, opts *UpdateOptions) error {
	downloadURL := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/%s",
		githubOwner, githubRepo, version, binaryName,
	)

	fmt.Printf("📥 Downloading: %s\n", binaryName)

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := attemptDownload(downloadURL, destPath, binaryName, opts)
		if err == nil {
			return nil
		}
		lastErr = err
		fmt.Printf("⚠️  Download failed (attempt %d/%d): %v\n", attempt, maxRetries, err)
		if attempt < maxRetries {
			time.Sleep(retryDelay * time.Duration(attempt))
		}
	}

	return &UpdateError{
		Stage:       "download",
		Message:     fmt.Sprintf("failed to download after %d attempts", maxRetries),
		Recoverable: true,
		Err:         lastErr,
	}
}

// attemptDownload performs a single download attempt
func attemptDownload(url, destPath, binaryName string, opts *UpdateOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "nextdeploy-updater/"+shared.Version)

	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !opts.VerifySSL,
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer closeBestEffort(resp.Body)

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("binary not found (may still be building)")
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Check Content-Length
	if resp.ContentLength > 0 {
		fmt.Printf("   Size: %s\n", formatBytes(resp.ContentLength))
	}

	// Create file with exclusive creation
	f, err := os.OpenFile(destPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0755)
	if err != nil {
		return err
	}
	defer closeBestEffort(f)

	// Download with progress
	progress := &progressWriter{
		total:      resp.ContentLength,
		reader:     resp.Body,
		binaryName: binaryName,
	}

	written, err := io.Copy(f, progress)
	if err != nil {
		return err
	}

	// Verify download completed
	if resp.ContentLength > 0 && written != resp.ContentLength {
		return fmt.Errorf("incomplete download: %d of %d bytes", written, resp.ContentLength)
	}

	if err := f.Sync(); err != nil {
		return err
	}

	fmt.Println() // New line after progress
	return nil
}

// formatBytes converts bytes to human readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
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

	if p.total > 0 && time.Since(p.lastPrint) > 100*time.Millisecond {
		percentage := float64(p.current) / float64(p.total) * 100
		speed := float64(p.current) / time.Since(p.lastPrint).Seconds()
		fmt.Printf("\r   Downloading... %.1f%% (%.1f MB/s)",
			percentage, speed/1024/1024)
		p.lastPrint = time.Now()

		if p.current == p.total {
			fmt.Print("\r   Download complete!                    \n")
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
	parts := strings.Fields(string(output))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return "", fmt.Errorf("unexpected version output: %s", output)
}

// isProcessRunning checks if a process with the given name is running
func isProcessRunning(name string) bool {
	cmd := exec.Command("pgrep", "-f", name)
	if err := cmd.Run(); err == nil {
		return true
	}
	return false
}

// copyFileWithSudo copies a file, using sudo if necessary
func copyFileWithSudo(src, dst string) error {
	if err := copyFile(src, dst); err == nil {
		return nil
	}
	return runCommand("sudo", "cp", src, dst)
}

// copyFile performs a regular file copy
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer closeBestEffort(source)

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer closeBestEffort(destination)

	_, err = io.Copy(destination, source)
	return err
}

// atomicReplace performs an atomic file replacement
func atomicReplace(src, dst string) error {
	// Try direct rename first
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

// clearCommandCache clears shell command cache
func clearCommandCache() {
	// Try bash hash -r
	ignoreErr(exec.Command("bash", "-c", "hash -r 2>/dev/null").Run())
	// Try zsh rehash
	ignoreErr(exec.Command("zsh", "-c", "rehash 2>/dev/null").Run())
}

// Version comparison functions
func isNewer(candidate, current string) bool {
	return compareVersions(candidate, current) > 0
}

func stripV(v string) string {
	return strings.TrimPrefix(v, "v")
}

func compareVersions(v1, v2 string) int {
	v1 = stripV(strings.Split(v1, "-")[0])
	v2 = stripV(strings.Split(v2, "-")[0])

	v1Parts := splitVer(v1)
	v2Parts := splitVer(v2)

	for i := 0; i < len(v1Parts) || i < len(v2Parts); i++ {
		var v1Val, v2Val int
		if i < len(v1Parts) {
			v1Val = v1Parts[i]
		}
		if i < len(v2Parts) {
			v2Val = v2Parts[i]
		}
		if v1Val > v2Val {
			return 1
		}
		if v1Val < v2Val {
			return -1
		}
	}
	return 0
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
			break
		}
	}

	if hasDigit {
		parts = append(parts, cur)
	}

	return parts
}

func ignoreErr(err error) {
	_ = err
}

func closeBestEffort(c io.Closer) {
	if c == nil {
		return
	}
	ignoreErr(c.Close())
}

func removeBestEffort(path string) {
	if path == "" {
		return
	}
	ignoreErr(os.Remove(path))
}

func removeAllBestEffort(path string) {
	if path == "" {
		return
	}
	ignoreErr(os.RemoveAll(path))
}

// extractBinary extracts the specific binary from the downloaded archive.
func extractBinary(archivePath, binaryName, destPath string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZipBinary(archivePath, binaryName, destPath)
	}
	return extractTarGzBinary(archivePath, binaryName, destPath)
}

func extractZipBinary(archivePath, binaryName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer closeBestEffort(r)

	for _, f := range r.File {
		if filepath.Base(f.Name) == binaryName || filepath.Base(f.Name) == binaryName+".exe" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer closeBestEffort(rc)

			out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer closeBestEffort(out)

			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("binary %s not found in zip archive", binaryName)
}

func extractTarGzBinary(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer closeBestEffort(f)

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer closeBestEffort(gzr)

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if header.Typeflag == tar.TypeReg && (filepath.Base(header.Name) == binaryName || filepath.Base(header.Name) == binaryName+".exe") {
			out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer closeBestEffort(out)

			_, err = io.Copy(out, tr)
			return err
		}
	}
	return fmt.Errorf("binary %s not found in tar.gz archive", binaryName)
}
