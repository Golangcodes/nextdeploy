package serverless

import (
	"context"

	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// Provider defines the interface for deploying to various serverless platforms
// (e.g., AWS, Cloudflare, GCP, Azure).
type Provider interface {
	// Initialize validates credentials and prepares the environment.
	Initialize(ctx context.Context, cfg *config.NextDeployConfig) error

	// DeployStatic uploads static assets (public/, .next/static/) to a CDN/Storage bucket.
	DeployStatic(ctx context.Context, tarballPath string, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error

	// GetSecrets retrieves all secrets for the application.
	GetSecrets(ctx context.Context, appName string) (map[string]string, error)

	// SetSecret sets a single secret for the application.
	SetSecret(ctx context.Context, appName string, key, value string) error

	// UnsetSecret removes a single secret from the application.
	UnsetSecret(ctx context.Context, appName string, key string) error

	// UpdateSecrets securely injects/syncs a batch of secrets.
	UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error

	// DeployCompute packages the standalone build and updates the compute layer
	// (e.g., AWS Lambda + Web Adapter, Cloudflare Workers).
	DeployCompute(ctx context.Context, tarballPath string, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error

	// InvalidateCache clears the CDN cache to ensure fresh assets are served.
	InvalidateCache(ctx context.Context, cfg *config.NextDeployConfig) error

	// Rollback reverts the compute layer to the previous version and
	// invalidates the CDN cache so the old version is served immediately.
	Rollback(ctx context.Context, cfg *config.NextDeployConfig) error

	// Destroy removes all application resources from the cloud provider.
	Destroy(ctx context.Context, cfg *config.NextDeployConfig) error
}
