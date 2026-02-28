package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type RepositoryInfo struct {
	CommitHash string
	BranchName string
	IsDirty    bool
}

func GetRepositoryInfo() (*RepositoryInfo, error) {
	hash, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return nil, err
	}

	branch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return nil, err
	}

	dirty, _ := exec.Command("git", "status", "--porcelain").Output()

	return &RepositoryInfo{
		CommitHash: strings.TrimSpace(string(hash)),
		BranchName: strings.TrimSpace(string(branch)),
		IsDirty:    len(strings.TrimSpace(string(dirty))) > 0,
	}, nil
}

func GetCurrentBranch() (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git command not found")
	}

	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New("not a git repository or unable to get branch: " + err.Error())
	}

	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "", errors.New("could not determine current branch")
	}

	return branch, nil
}

func GetSecondLatestCommitHash() (string, error) {
	cmd := exec.Command("git", "log", "--pretty=format:%H", "-n", "2")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git log: %w", err)
	}
	commits := strings.Split(strings.TrimSpace(string(output)), "\n")

	if len(commits) < 2 {
		return "", fmt.Errorf("not enough commits in repository (need at least 2, got %d)", len(commits))
	}

	return commits[1], nil
}

func GetSecondLatestCommitHashAlt() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD~1")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get parent commit: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}
func GetCommitHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short=7", "HEAD")

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()

	if err != nil {
		return "", fmt.Errorf("git command failed: %w", err)
	}

	return strings.TrimSpace(out.String()), nil
}

func IsDirty() bool {
	cmd := exec.Command("git", "status", "--porcelain")

	var out bytes.Buffer

	cmd.Stdout = &out
	_ = cmd.Run()
	return out.Len() > 0
}
func GetGitCommitHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Failed to get Git commit hash: %v\n%s", err, out.String())
	}

	return strings.TrimSpace(out.String()), nil
}
