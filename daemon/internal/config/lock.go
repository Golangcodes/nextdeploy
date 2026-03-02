package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type BuildLock struct {
	GitCommit    string `json:"git_commit"`
	GitDirty     bool   `json:"git_dirty"`
	GeneratedAt  string `json:"generated_at"`
	MetadataFile string `json:"metadata_file"`
}

func ReadBuildLock(filePath string) (*BuildLock, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read build.lock file: %w", err)
	}

	var buildLock BuildLock
	err = json.Unmarshal(data, &buildLock)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build.lock JSON: %w", err)
	}
	return &buildLock, nil
}

func GetGitCommit(filePath string) (string, error) {
	buildLock, err := ReadBuildLock(filePath)
	if err != nil {
		return "", err
	}
	return buildLock.GitCommit, nil
}
