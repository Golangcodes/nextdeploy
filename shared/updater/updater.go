package updater

import (
	"context"
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

type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func LatestRelease() (Release, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	// #nosec G704
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func CheckAndPrint(current string) {
	if current == "dev" {
		return
	}
	latest, err := LatestRelease()
	if err != nil {
		return
	}
	if latest.TagName != "" && isNewer(latest.TagName, current) {
		fmt.Fprintf(os.Stderr, "\n  Update available: %s -> %s\n  Run: nextdeploy update\n  %s\n\n",
			current, latest.TagName, latest.HTMLURL)
	}
}

func isNewer(candidate, current string) bool {
	return semverGT(stripV(candidate), stripV(current))
}

func stripV(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v[1:]
	}
	return v
}

func semverGT(a, b string) bool {
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
	n, cur := 0, 0
	for _, c := range v {
		if c == '-' {
			break
		}
		if c == '.' {
			parts = append(parts, cur)
			cur, n = 0, 0
		} else if c >= '0' && c <= '9' {
			cur = cur*10 + int(c-'0')
			n++
		}
	}
	if n > 0 {
		parts = append(parts, cur)
	}
	return parts
}

func SelfUpdate(current string) error {
	dest, err := os.Executable()
	if err != nil {
		dest = "/usr/local/bin/nextdeploy"
	}
	return selfUpdateBinary(current, "nextdeploy", dest, false)
}

func SelfUpdateDaemon(current string) error {
	return selfUpdateBinary(current, "nextdeployd", "/usr/local/bin/nextdeployd", true)
}

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
	if !isNewer(latest.TagName, current) {
		fmt.Printf("Already at the latest release (%s is newer than %s). No downgrade performed.\n", current, latest.TagName)
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
	// #nosec G703
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := &http.Client{} // no Timeout — context handles it
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	// #nosec G704
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("download returned HTTP 404 - the release %s exists but the binary %s was not found. This can happen if the build CI is still running. Please try again in 1-2 minutes.", latest.TagName, binaryName)
		}
		return fmt.Errorf("download failed with HTTP %d. Please check your internet connection and try again.", resp.StatusCode)
	}
	fmt.Printf("Downloading %s (this may take a minute)...\n", binaryName)
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("failed writing download: %w", err)
	}
	_ = tmpFile.Close()

	// #nosec G302 G703
	if err := os.Chmod(tmpFile.Name(), 0o755); err != nil { // NOSONAR: Public CLI binary needs to be executable by all users
		return fmt.Errorf("chmod failed: %w", err)
	}

	// #nosec G204 G702
	mv := exec.Command("mv", tmpFile.Name(), dest)
	mv.Stdout = os.Stdout
	mv.Stderr = os.Stderr
	if err := mv.Run(); err != nil {
		fmt.Println("Permission denied, attempting with sudo...")
		// #nosec G204 G702
		mvSudo := exec.Command("sudo", "mv", tmpFile.Name(), dest)
		mvSudo.Stdout = os.Stdout
		mvSudo.Stderr = os.Stderr
		if err := mvSudo.Run(); err != nil {
			return fmt.Errorf("could not move binary even with sudo: %w", err)
		}
	}
	fmt.Println("Updated to", latest.TagName)

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
