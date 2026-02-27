package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type UpdateInfo struct {
	Updated    bool
	NewVersion string
	UpdateFrom string
}

func UpdateDaemon() (UpdateInfo, error) {
	const (
		owner = "aynaash"
		repo  = "NextDeploy"
	)

	// Get current version
	currentVersion, err := getCurrentVersion()
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("failed to get current version: %w", err)
	}

	// Get latest release info
	latestTag, err := getLatestReleaseTag(owner, repo)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("failed to get latest release: %w", err)
	}

	// Check if update is needed
	if currentVersion == latestTag {
		return UpdateInfo{
			Updated:    false,
			NewVersion: latestTag,
			UpdateFrom: currentVersion,
		}, nil
	}

	// Download new version
	downloadURL := fmt.Sprintf(
		"https://github.com/%s/%s/releases/latest/download/nextdeployd-%s-%s",
		owner, repo, runtime.GOOS, runtime.GOARCH,
	)

	if err := downloadBinary(downloadURL, "/tmp/nextdeployd-new"); err != nil {
		return UpdateInfo{}, fmt.Errorf("download failed: %w", err)
	}

	// Replace binary
	if err := replaceBinary("/tmp/nextdeployd-new", "/usr/local/bin/nextdeployd"); err != nil {
		return UpdateInfo{}, fmt.Errorf("binary replacement failed: %w", err)
	}

	// Restart service
	if err := restartService("nextdeployd"); err != nil {
		return UpdateInfo{
			Updated:    true,
			NewVersion: latestTag,
			UpdateFrom: currentVersion,
		}, fmt.Errorf("failed to restart service: %w", err)
	}

	return UpdateInfo{
		Updated:    true,
		NewVersion: latestTag,
		UpdateFrom: currentVersion,
	}, nil
}

func getCurrentVersion() (string, error) {
	cmd := exec.Command("/usr/local/bin/nextdeployd", "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func getLatestReleaseTag(owner, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	// #nosec G107
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func downloadBinary(url, path string) error {
	// #nosec G107
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// #nosec G304
	outFile, err := os.Create(path)
	if err != nil {
		return err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return err
	}
	// #nosec G302
	return os.Chmod(path, 0750)
}

func replaceBinary(source, destination string) error {
	return os.Rename(source, destination)
}

func restartService(serviceName string) error {
	// #nosec G204
	cmd := exec.Command("systemctl", "restart", serviceName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
