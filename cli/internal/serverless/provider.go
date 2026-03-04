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

	// UpdateSecrets securely injects secrets from the SecretManager into the cloud provider's
	// secret store (e.g., AWS Secrets Manager, Cloudflare Secrets).
	// This ensures raw secrets are never packed into the deployment artifact.
	UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error

	// DeployCompute packages the standalone build and updates the compute layer
	// (e.g., AWS Lambda + Web Adapter, Cloudflare Workers).
	DeployCompute(ctx context.Context, tarballPath string, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error

	// InvalidateCache clears the CDN cache to ensure fresh assets are served.
	InvalidateCache(ctx context.Context, cfg *config.NextDeployConfig) error

	// Destroy removes all application resources from the cloud provider.
	Destroy(ctx context.Context, cfg *config.NextDeployConfig) error
}
