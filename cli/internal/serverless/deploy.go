package serverless

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/Golangcodes/nextdeploy/shared/secrets"
)

// New returns a new serverless provider based on the provider name.
func New(providerName string) (Provider, error) {
	switch providerName {
	case "aws":
		return NewAWSProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported serverless provider: %s (supported: aws)", providerName)
	}
}

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

	// ── 2. Run Packaging ───────────────────────────────────────────────────
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get project root: %w", err)
	}

	packager, err := packaging.NewPackager(projectDir, meta)
	if err != nil {
		return fmt.Errorf("failed to initialize packager: %w", err)
	}
	defer packager.Cleanup()

	log.Info("Splitting assets for optimized Serverless deployment...")
	pkgResult, err := packager.Package()
	if err != nil {
		return fmt.Errorf("packaging failed: %w", err)
	}

	if pkgResult.SizeWarning != "" {
		log.Warn(pkgResult.SizeWarning)
	}
	log.Info("Package split: %dMB Lambda zip, %d S3 assets", pkgResult.LambdaZipSize/(1024*1024), len(pkgResult.S3Assets))

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
	if err := p.DeployStatic(ctx, pkgResult, cfg, meta); err != nil {
		return fmt.Errorf("failed to deploy static assets: %w", err)
	}

	// ── 5. Deploy compute layer ──────────────────────────────────────────────
	if err := p.DeployCompute(ctx, pkgResult, cfg, meta); err != nil {
		return fmt.Errorf("failed to deploy compute layer: %w", err)
	}

	// ── 6. Invalidate CDN cache ──────────────────────────────────────────────
	if err := p.InvalidateCache(ctx, cfg); err != nil {
		log.Error("Cache invalidation failed (non-fatal): %v", err)
	}

	log.Info("Serverless deployment complete! Application is live.")

	// ── 7. Generate Visual Report ───────────────────────────────────────────
	resMap, err := p.GetResourceMap(ctx, cfg)
	if err == nil {
		reportPath, err := GenerateResourceView(&cfg.App, resMap)
		if err == nil {
			log.Info("════════════════════════════════════════════════════════════")
			log.Success("Visual Deployment Report generated: %s", reportPath)
			log.Info("    Open this file in your browser to see your provisioned resources.")
			log.Info("    DNS setup instructions are included in the report and dns.md!")
			log.Info("════════════════════════════════════════════════════════════")
		} else {
			log.Warn("Failed to generate visual report: %v", err)
		}
	} else {
		log.Warn("Failed to fetch resource map for report: %v", err)
	}

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

	log.Info(" Serverless rollback complete!")
	return nil
}

func loadLocalSecrets(cfg *config.NextDeployConfig) (map[string]string, error) {
	sm, err := secrets.NewSecretManager(
		secrets.WithConfig(cfg),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init secret manager: %w", err)
	}

	envFilePath := filepath.Join(".nextdeploy", ".env")
	if _, statErr := os.Stat(envFilePath); statErr == nil {
		if err := sm.ImportSecrets(envFilePath); err != nil {
			return nil, fmt.Errorf("failed to load secrets from %s: %w", envFilePath, err)
		}
	}

	return sm.FlattenSecrets(), nil
}
