package serverless

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/Golangcodes/nextdeploy/shared/secrets"
)

// Deploy orchestrates the full serverless deployment pipeline:
//  1. Discovers the build artifact (app.tar.gz)
//  2. Fetches local secrets via SecretManager and pushes them to the cloud secret store
//  3. Uploads static assets to CDN/Storage
//  4. Deploys the compute layer (Lambda, Workers, etc.)
//  5. Invalidates the CDN cache
func Deploy(ctx context.Context, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	log := shared.PackageLogger("serverless", "☁️  SERVERLESS")

	// ── 1. Resolve provider ──────────────────────────────────────────────────
	var p Provider
	switch cfg.Serverless.Provider {
	case "aws":
		p = NewAWSProvider()
	default:
		return fmt.Errorf("unsupported serverless provider: %s (supported: aws)", cfg.Serverless.Provider)
	}

	if err := p.Initialize(ctx, cfg); err != nil {
		return fmt.Errorf("provider initialization failed: %w", err)
	}

	// ── 2. Discover artifact ─────────────────────────────────────────────────
	tarballPath, err := discoverArtifact()
	if err != nil {
		return fmt.Errorf("no build artifact found — run `nextdeploy build` first: %w", err)
	}
	log.Info("Using build artifact: %s", tarballPath)

	// ── 3. Push secrets ──────────────────────────────────────────────────────
	appSecrets, err := loadLocalSecrets(cfg)
	if err != nil {
		// Non-fatal: warn but proceed with empty secrets if none are configured
		log.Warn("Failed to load local secrets (non-fatal): %v", err)
		appSecrets = map[string]string{}
	}

	if err := p.UpdateSecrets(ctx, cfg.App.Name, appSecrets); err != nil {
		return fmt.Errorf("failed to push secrets to cloud provider: %w", err)
	}

	// ── 4. Deploy static assets ──────────────────────────────────────────────
	if err := p.DeployStatic(ctx, tarballPath, cfg, meta); err != nil {
		return fmt.Errorf("failed to deploy static assets: %w", err)
	}

	// ── 5. Deploy compute layer ──────────────────────────────────────────────
	if err := p.DeployCompute(ctx, tarballPath, cfg, meta); err != nil {
		return fmt.Errorf("failed to deploy compute layer: %w", err)
	}

	// ── 6. Invalidate CDN cache ──────────────────────────────────────────────
	if err := p.InvalidateCache(ctx, cfg); err != nil {
		log.Error("Cache invalidation failed (non-fatal): %v", err)
	}

	log.Info("✅ Serverless deployment complete! Application is live.")
	return nil
}

// Rollback orchestrates the serverless rollback process.
func Rollback(ctx context.Context, cfg *config.NextDeployConfig) error {
	log := shared.PackageLogger("serverless", "☁️  SERVERLESS")

	// ── 1. Resolve provider ──────────────────────────────────────────────────
	var p Provider
	switch cfg.Serverless.Provider {
	case "aws":
		p = NewAWSProvider()
	default:
		return fmt.Errorf("unsupported serverless provider: %s (supported: aws)", cfg.Serverless.Provider)
	}

	if err := p.Initialize(ctx, cfg); err != nil {
		return fmt.Errorf("provider initialization failed: %w", err)
	}

	// ── 2. Trigger Rollback ──────────────────────────────────────────────────
	if err := p.Rollback(ctx, cfg); err != nil {
		return fmt.Errorf("serverless rollback failed: %w", err)
	}

	log.Info("✅ Serverless rollback complete!")
	return nil
}

// discoverArtifact returns the path to the build tarball.
// It searches the standard locations in order of preference:
//  1. .nextdeploy/app.tar.gz  (local build dir)
//  2. app.tar.gz              (cwd fallback)
func discoverArtifact() (string, error) {
	candidates := []string{
		filepath.Join(".nextdeploy", "app.tar.gz"),
		"app.tar.gz",
	}

	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			abs, err := filepath.Abs(cand)
			if err != nil {
				return cand, nil
			}
			return abs, nil
		}
	}

	return "", fmt.Errorf("artifact not found in %v", candidates)
}

// loadLocalSecrets reads plaintext/encrypted secrets from the local SecretManager
// and returns a flat map suitable for cloud injection.
func loadLocalSecrets(cfg *config.NextDeployConfig) (map[string]string, error) {
	sm, err := secrets.NewSecretManager(
		secrets.WithConfig(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init secret manager: %w", err)
	}

	// Load from the .env file associated with this app if it exists
	envFilePath := filepath.Join(".nextdeploy", ".env")
	if _, statErr := os.Stat(envFilePath); statErr == nil {
		if err := sm.ImportSecrets(envFilePath); err != nil {
			return nil, fmt.Errorf("failed to load secrets from %s: %w", envFilePath, err)
		}
	}

	// Also honour process environment variables (NEXT_PUBLIC_*, etc.)
	// Future: add Doppler/1Password/Vault provider here via sm.WithProvider(...)
	return sm.FlattenSecrets(), nil
}
