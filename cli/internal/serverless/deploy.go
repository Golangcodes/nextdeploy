package serverless

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/envstore"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/Golangcodes/nextdeploy/shared/secrets"
)

// New returns a new serverless provider based on the provider name.
func New(providerName string, verbose bool) (Provider, error) {
	switch providerName {
	case "aws":
		return NewAWSProvider(verbose), nil
	case "cloudflare":
		return NewCloudflareProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported serverless provider: %s (supported: aws, cloudflare)", providerName)
	}
}

// Deploy orchestrates the full serverless deployment pipeline:
//  1. Discovers the build artifact (app.tar.gz)
//  2. Fetches local secrets via SecretManager and pushes them to the cloud secret store
//  3. Uploads static assets to CDN/Storage
//  4. Deploys the compute layer (Lambda, Workers, etc.)
//  5. Invalidates the CDN cache
func Deploy(ctx context.Context, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload, verbose bool) error {
	log := shared.PackageLogger("serverless", "☁️  SERVERLESS")

	// ── 1. Resolve provider ──────────────────────────────────────────────────
	p, err := New(cfg.Serverless.Provider, verbose)
	if err != nil {
		return err
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
		log.Warn("%s", pkgResult.SizeWarning)
	}
	log.Info("Package split: %dMB Lambda zip, %d S3 assets", pkgResult.LambdaZipSize/(1024*1024), len(pkgResult.S3Assets))

	// ── 3. Push secrets (ordering depends on provider) ───────────────────────
	// AWS: secrets must land in Secrets Manager BEFORE DeployCompute, because
	//      the allow_secrets_in_env fallback reads them at deploy time to bake
	//      into Lambda env vars.
	// Cloudflare: secrets are attached to an existing worker, so the worker
	//      must be created by DeployCompute first.
	secretsBeforeCompute := cfg.Serverless.Provider != "cloudflare"

	pushSecrets := func() error {
		appSecrets, err := loadLocalSecrets(cfg)
		if err != nil {
			log.Warn("Failed to load local secrets (non-fatal): %v", err)
			appSecrets = map[string]string{}
		}
		if err := p.UpdateSecrets(ctx, cfg.App.Name, appSecrets); err != nil {
			return fmt.Errorf("failed to push secrets to cloud provider: %w", err)
		}
		return nil
	}

	if secretsBeforeCompute {
		if err := pushSecrets(); err != nil {
			return err
		}
	}

	// ── 4. Deploy static assets ──────────────────────────────────────────────
	t0 := time.Now()
	if err := p.DeployStatic(ctx, pkgResult, cfg, meta); err != nil {
		return fmt.Errorf("failed to deploy static assets: %w", err)
	}
	if verbose {
		log.Info("  Static upload completed in %s", time.Since(t0).Round(time.Millisecond))
	}

	// ── 5. Deploy compute layer ──────────────────────────────────────────────
	t0 = time.Now()
	if err := p.DeployCompute(ctx, pkgResult, cfg, meta); err != nil {
		return fmt.Errorf("failed to deploy compute layer: %w", err)
	}
	if verbose {
		log.Info("  Lambda deployment completed in %s", time.Since(t0).Round(time.Millisecond))
	}

	if !secretsBeforeCompute {
		if err := pushSecrets(); err != nil {
			return err
		}
	}

	// ── 6. Invalidate CDN cache ──────────────────────────────────────────────
	// AWS DeployCompute already triggers an invalidation immediately after the
	// distribution is created/updated. Skip the redundant orchestration-level
	// call for AWS to avoid double-billing and double latency. Other providers
	// (Cloudflare) still need this hop because their compute deploy doesn't
	// touch the CDN cache.
	if cfg.Serverless.Provider != "aws" {
		t0 = time.Now()
		if err := p.InvalidateCache(ctx, cfg); err != nil {
			log.Error("Cache invalidation failed (non-fatal): %v", err)
		} else if verbose {
			log.Info("  CDN invalidation completed in %s", time.Since(t0).Round(time.Millisecond))
		}
	}

	log.Info("Serverless deployment complete! Application is live.")

	// ── 6.5. Post-deploy smoke verify ───────────────────────────────────────
	// Non-fatal by default — CI callers can opt into FailOnError when they
	// want the deploy to gate on the smoke check. Domain-less deploys (no
	// custom domain, workers.dev only) skip automatically.
	if _, err := SmokeVerify(ctx, log, cfg, meta, SmokeOpts{}); err != nil {
		log.Warn("Smoke verify returned error (non-fatal): %v", err)
	}

	// ── 7. Generate Visual Report ───────────────────────────────────────────
	resMap, err := p.GetResourceMap(ctx, cfg)
	if err == nil {
		reportPath, err := GenerateResourceView(&cfg.App, resMap)
		if err == nil {
			absPath, _ := filepath.Abs(reportPath)
			log.Info("┌────────────────────────────────────────────────────────────┐")
			log.Success("│  🚀 DEPLOYMENT REPORT READY                                │")
			log.Info("├────────────────────────────────────────────────────────────┤")
			log.Info("│  Report: file://%s", absPath)
			log.Info("│                                                            │")
			log.Info("│  ⚠️  DNS GUIDANCE: Open this report immediately to see     │")
			log.Info("│     the exact DNS records needed for your custom domain.   │")
			log.Info("└────────────────────────────────────────────────────────────┘")
		} else {
			log.Warn("Failed to generate visual report: %v", err)
		}
	} else {
		log.Warn("Failed to fetch resource map for report: %v", err)
	}

	return nil
}

// Rollback orchestrates the serverless rollback process. Opts forwards
// --steps / --to <commit> from the CLI down to the provider implementation.
func Rollback(ctx context.Context, cfg *config.NextDeployConfig, opts RollbackOptions) error {
	log := shared.PackageLogger("serverless", "☁️  SERVERLESS")

	// ── 1. Resolve provider ──────────────────────────────────────────────────
	p, err := New(cfg.Serverless.Provider, false)
	if err != nil {
		return err
	}

	if err := p.Initialize(ctx, cfg); err != nil {
		return fmt.Errorf("provider initialization failed: %w", err)
	}

	// ── 2. Trigger Rollback ──────────────────────────────────────────────────
	if err := p.Rollback(ctx, cfg, opts); err != nil {
		return fmt.Errorf("serverless rollback failed: %w", err)
	}

	log.Info(" Serverless rollback complete!")
	return nil
}

// loadLocalSecrets merges secrets from every supported source for the current
// project. Precedence (lowest → highest):
//
//  1. Auto-detected dotenv file at project root (`.env`)
//  2. Files declared in `nextdeploy.yml` under `secrets.files[]` (in order)
//  3. The managed JSON store at `.nextdeploy/.env`, populated by
//     `nextdeploy secrets set/load`
//
// Higher-precedence sources override lower ones. The managed store wins
// because it represents explicit user intent via the CLI.
func loadLocalSecrets(cfg *config.NextDeployConfig) (map[string]string, error) {
	log := shared.PackageLogger("serverless", "🔐 SECRETS")
	merged := map[string]string{}

	// 1. Project-root .env (dotenv format) — silently skipped if missing.
	if env, err := envstore.ReadEnvFile(".env"); err == nil {
		mergeInto(merged, env)
		log.Info("Loaded %d secrets from .env", len(env))
	} else if !os.IsNotExist(err) {
		log.Warn("Failed to read .env (non-fatal): %v", err)
	}

	// 2. YAML-declared files (cfg.Secrets.Files). Each file is parsed as
	// dotenv. We do not silently swallow parse errors here — a misconfigured
	// secrets file should be loud.
	for _, sf := range cfg.Secrets.Files {
		if sf.Path == "" {
			continue
		}
		env, err := envstore.ReadEnvFile(sf.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to read secrets file %s: %w", sf.Path, err)
		}
		mergeInto(merged, env)
		log.Info("Loaded %d secrets from %s", len(env), sf.Path)
	}

	// 3. Managed JSON store (.nextdeploy/.env). Highest precedence.
	sm, err := secrets.NewSecretManager(secrets.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("failed to init secret manager: %w", err)
	}
	managedPath := filepath.Join(".nextdeploy", ".env")
	if _, statErr := os.Stat(managedPath); statErr == nil {
		if err := sm.ImportSecrets(managedPath); err != nil {
			return nil, fmt.Errorf("failed to load secrets from %s: %w", managedPath, err)
		}
		managed := sm.FlattenSecrets()
		mergeInto(merged, managed)
		log.Info("Loaded %d secrets from managed store", len(managed))
	}

	log.Info("Total secrets to sync: %d", len(merged))
	return merged, nil
}

// mergeInto copies src into dst, overwriting existing keys.
func mergeInto(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}
