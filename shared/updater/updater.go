// Package updater provides GitHub release-based update checking and self-update
// functionality for the NextDeploy CLI and Daemon.
package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

const (
	githubOwner = "Golangcodes"
	githubRepo  = "nextdeploy"
	apiURL      = "https://api.github.com/repos/" + githubOwner + "/" + githubRepo + "/releases/latest"
)

// Release represents the subset of GitHub release API fields we care about.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// LatestRelease fetches the latest release tag from GitHub.
// Returns an empty Release and an error on failure.
func LatestRelease() (Release, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Release{}, err
	}
	return release, nil
}

// CheckAndPrint silently checks GitHub for a newer release and prints a one-line
// hint to stderr. Designed to be called as a goroutine so it never blocks.
func CheckAndPrint(current string) {
	if current == "dev" {
		return
	}
	latest, err := LatestRelease()
	if err != nil {
		return
	}
	if latest.TagName != "" && latest.TagName != current {
		fmt.Fprintf(os.Stderr, "\n  Update available: %s -> %s\n  Run: nextdeploy update\n  %s\n\n",
			current, latest.TagName, latest.HTMLURL)
	}
}

// SelfUpdate downloads the latest nextdeploy CLI binary and replaces the
// binary at /usr/local/bin/nextdeploy.
func SelfUpdate(current string) error {
	return selfUpdateBinary(current, "nextdeploy", "/usr/local/bin/nextdeploy", false)
}

// SelfUpdateDaemon downloads the latest nextdeployd binary, replaces the binary
// at /usr/local/bin/nextdeployd, and restarts the systemd service.
func SelfUpdateDaemon(current string) error {
	return selfUpdateBinary(current, "nextdeployd", "/usr/local/bin/nextdeployd", true)
}

// selfUpdateBinary is the shared implementation for CLI and daemon self-update.
func selfUpdateBinary(current, binaryBase, dest string, restartSvc bool) error {
	fmt.Printf("Current version: %s\n", current)
	fmt.Println("Fetching latest release info...")

	latest, err := LatestRelease()
	if err != nil {
		return fmt.Errorf("failed to fetch latest release: %w", err)
	}
	if latest.TagName == current {
		fmt.Printf("Already up to date (%s).\n", current)
		return nil
	}

	fmt.Printf("Updating %s -> %s\n", current, latest.TagName)

	binaryName := fmt.Sprintf("%s-%s-%s", binaryBase, runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf(
		"https://github.com/%s/%s/releases/download/%s/%s",
		githubOwner, githubRepo, latest.TagName, binaryName,
	)
	fmt.Printf("Downloading: %s\n", downloadURL)

	tmpFile, err := os.CreateTemp("", binaryBase+"-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d - is release %s published?", resp.StatusCode, latest.TagName)
	}
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("failed writing download: %w", err)
	}
	tmpFile.Close() //nolint:errcheck

	if err := os.Chmod(tmpFile.Name(), 0o755); err != nil { //nolint:gosec
		return fmt.Errorf("chmod failed: %w", err)
	}

	// Try with sudo first, then fall back (for root execution).
	// #nosec G204
	mv := exec.Command("sudo", "mv", tmpFile.Name(), dest)
	mv.Stdout = os.Stdout
	mv.Stderr = os.Stderr
	if err := mv.Run(); err != nil {
		// #nosec G204
		mv2 := exec.Command("mv", tmpFile.Name(), dest)
		mv2.Stdout = os.Stdout
		mv2.Stderr = os.Stderr
		if err2 := mv2.Run(); err2 != nil {
			return fmt.Errorf("could not move binary (try running with sudo): %w", err)
		}
	}

	fmt.Printf("Updated to %s successfully.\n", latest.TagName)

	if restartSvc {
		// #nosec G204
		svc := exec.Command("sudo", "systemctl", "restart", binaryBase)
		svc.Stdout = os.Stdout
		svc.Stderr = os.Stderr
		if err := svc.Run(); err != nil {
			fmt.Printf("Note: could not restart %s service: %v\n", binaryBase, err)
		}
	}
	return nil
}
