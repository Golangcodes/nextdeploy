package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// BuildLock represents the structure of the build.lock file
type BuildLock struct {
	GitCommit    string `json:"git_commit"`
	GitDirty     bool   `json:"git_dirty"`
	GeneratedAt  string `json:"generated_at"`
	MetadataFile string `json:"metadata_file"`
}

// ReadBuildLock reads and parses the build.lock file
func ReadBuildLock(filePath string) (*BuildLock, error) {
	// #nosec G304
	// Read the file content
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read build.lock file: %w", err)
	}

	// Parse the JSON content
	var buildLock BuildLock
	err = json.Unmarshal(data, &buildLock)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build.lock JSON: %w", err)
	}

	return &buildLock, nil
}

// GetGitCommit reads the build.lock file and returns the git commit hash
func GetGitCommit(filePath string) (string, error) {
	buildLock, err := ReadBuildLock(filePath)
	if err != nil {
		return "", err
	}
	return buildLock.GitCommit, nil
}
