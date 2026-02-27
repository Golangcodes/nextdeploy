package build

import (
	"fmt"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/git"
	"github.com/Golangcodes/nextdeploy/shared/nextdeploy"
	"strings"
	"time"
)

// ImageNameBuilder handles the construction of Docker image names with proper formatting
type ImageNameBuilder struct {
	baseName     string
	registry     string
	tag          string
	nameStrategy string
	useTimestamp bool
	branchName   string
}

var buildlogger = shared.PackageLogger("BUILD", "🧱 BUILD")

// NewImageNameBuilder creates a new image name builder with default values
func NewImageNameBuilder(baseName, registry string) *ImageNameBuilder {
	// print out the base name and registry
	buildlogger.Info("Creating ImageNameBuilder with base name: %s, registry: %s", baseName, registry)
	return &ImageNameBuilder{
		baseName:     baseName,
		registry:     registry,
		nameStrategy: "commit-hash",
		useTimestamp: true,
	}
}

// WithTag sets the explicit tag for the image
func (b *ImageNameBuilder) WithTag(tag string) *ImageNameBuilder {
	b.tag = tag
	return b
}

// WithNameStrategy sets the naming strategy
func (b *ImageNameBuilder) WithNameStrategy(strategy string) *ImageNameBuilder {
	b.nameStrategy = strategy
	return b
}

// WithTimestamp controls whether to append timestamp
func (b *ImageNameBuilder) WithTimestamp(use bool) *ImageNameBuilder {
	b.useTimestamp = use
	return b
}

// WithBranch sets the branch name for branch-based strategies
func (b *ImageNameBuilder) WithBranch(branch string) *ImageNameBuilder {
	b.branchName = branch
	return b
}

// Build constructs the final image name according to the configured strategy
func (b *ImageNameBuilder) Build() (string, error) {
	// Validate base name
	if strings.TrimSpace(b.baseName) == "" {
		return "", fmt.Errorf("image base name cannot be empty")
	}

	// Get or generate tag
	tag, err := b.generateTag()
	if err != nil {
		return "", fmt.Errorf("failed to generate tag: %w", err)
	}

	// Construct full image name
	imageName := b.baseName
	if b.registry != "" && !strings.Contains(imageName, "/") {
		imageName = strings.TrimSuffix(b.registry, "/") + "/" + imageName
	}

	return imageName + ":" + tag, nil
}

// generateTag creates the appropriate tag based on the naming strategy
func (b *ImageNameBuilder) generateTag() (string, error) {
	// Use explicit tag if provided
	if b.tag != "" {
		return b.tag, nil
	}

	// Generate tag based on strategy
	switch b.nameStrategy {
	case "commit-hash":
		return b.generateCommitHashTag()
	case "branch-commit":
		return b.generateBranchCommitTag()
	case "simple":
		return "latest", nil
	case "semver":
		return "", fmt.Errorf("semver strategy requires explicit --tag")
	default:
		return "", fmt.Errorf("unknown naming strategy: %s", b.nameStrategy)
	}
}

// generateCommitHashTag creates a tag with commit hash (and optional timestamp)
func (b *ImageNameBuilder) generateCommitHashTag() (string, error) {
	hash, err := git.GetCommitHash()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit hash: %w", err)
	}

	if !b.useTimestamp {
		return hash, nil
	}

	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s", hash, timestamp), nil
}

// generateBranchCommitTag creates a tag with branch name and commit hash
func (b *ImageNameBuilder) generateBranchCommitTag() (string, error) {
	branch := b.branchName
	if branch == "" {
		var err error
		branch, err = git.GetCurrentBranch()
		if err != nil {
			return "", fmt.Errorf("failed to get git branch: %w", err)
		}
	}

	// Sanitize branch name
	branch = strings.ToLower(branch)
	branch = strings.ReplaceAll(branch, "/", "-")
	branch = strings.ReplaceAll(branch, " ", "-")

	hash, err := git.GetCommitHash()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit hash: %w", err)
	}

	tag := fmt.Sprintf("%s-%s", branch, hash)

	if b.useTimestamp {
		timestamp := time.Now().Format("20060102-150405")
		tag = fmt.Sprintf("%s-%s", tag, timestamp)
	}

	return tag, nil
}

// constructImageName constructs the image name using the ImageNameBuilder
func ConstructImageName(tag string) string {
	config, err := nextdeploy.Load("nextdeploy.yml")
	if err != nil {
		buildlogger.Error("Failed to load nextdeploy.yml: %v", err)
		return ""
	}
	// the image  name before image name build is
	buildlogger.Info("Image name before image name build: %s", config.Docker.Image)
	//builder := NewImageNameBuilder(config.Docker.Image, config.Docker.Registry)
	// if tag != "" {
	// 	builder.WithTag(tag)
	// }

	/* imageName, err := builder.Build() */
	//println("Image Name:", imageName)
	// buildlogger.Info("Constructed image name: %s", imageName)
	// if err != nil {
	// 	buildlogger.Error("Failed to build image name: %v", err)
	// 	return ""
	// }

	return config.Docker.Image + ":" + tag
}
