package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// CloudflareProvider implements Provider for Cloudflare Workers + R2 + Pages.
type CloudflareProvider struct {
	log       *shared.Logger
	accountID string
	apiToken  string
	client    *http.Client
}

func NewCloudflareProvider() *CloudflareProvider {
	return &CloudflareProvider{
		log:    shared.PackageLogger("cloudflare", "☁️  CF::"),
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// Initialize validates the Cloudflare API token and resolves the account ID.
func (p *CloudflareProvider) Initialize(ctx context.Context, cfg *config.NextDeployConfig) error {
	p.log.Info("Initializing Cloudflare deployment session...")

	p.apiToken = os.Getenv("CLOUDFLARE_API_TOKEN")
	if p.apiToken == "" && cfg.CloudProvider != nil {
		p.apiToken = cfg.CloudProvider.AccessKey // reuse AccessKey field for CF token
	}
	if p.apiToken == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN env var or cloudprovider.access_key is required")
	}

	p.accountID = os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	if p.accountID == "" && cfg.CloudProvider != nil {
		p.accountID = cfg.CloudProvider.AccountID
	}
	if p.accountID == "" {
		return fmt.Errorf("CLOUDFLARE_ACCOUNT_ID env var or cloudprovider.account_id is required")
	}

	// Verify token is valid
	var result struct {
		Success bool `json:"success"`
	}
	if err := p.cfRequest(ctx, "GET", "/user/tokens/verify", nil, &result); err != nil {
		return fmt.Errorf("cloudflare token verification failed: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("cloudflare API token is invalid")
	}

	p.log.Info("Cloudflare session initialized (account: %s)", p.accountID)
	return nil
}

// DeployStatic uploads static assets to an R2 bucket.
func (p *CloudflareProvider) DeployStatic(ctx context.Context, pkg *packaging.PackageResult, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	p.log.Info("Uploading %d static assets to R2...", len(pkg.S3Assets))

	bucketName := p.getBucketName(cfg)
	if err := p.ensureR2BucketExists(ctx, bucketName); err != nil {
		return fmt.Errorf("failed to ensure R2 bucket: %w", err)
	}

	for _, asset := range pkg.S3Assets {
		if err := p.uploadToR2(ctx, bucketName, asset.S3Key, asset.LocalPath, asset.ContentType); err != nil {
			return fmt.Errorf("failed to upload %s: %w", asset.S3Key, err)
		}
	}

	p.log.Info("Static assets uploaded to R2 bucket: %s", bucketName)
	return nil
}

// DeployCompute deploys the Next.js app as a Cloudflare Worker.
func (p *CloudflareProvider) DeployCompute(ctx context.Context, pkg *packaging.PackageResult, cfg *config.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	p.log.Info("Deploying compute layer to Cloudflare Workers...")

	workerName := p.getWorkerName(cfg)
	zipContents, err := os.ReadFile(pkg.LambdaZipPath)
	if err != nil {
		return fmt.Errorf("failed to read worker bundle: %w", err)
	}

	// Upload worker script via multipart form
	if err := p.uploadWorker(ctx, workerName, zipContents); err != nil {
		return fmt.Errorf("failed to upload worker: %w", err)
	}

	p.log.Info("Worker deployed: %s", workerName)

	// Bind R2 bucket to worker
	bucketName := p.getBucketName(cfg)
	if err := p.bindR2ToWorker(ctx, workerName, "ASSETS", bucketName); err != nil {
		p.log.Warn("Failed to bind R2 bucket to worker (non-fatal): %v", err)
	}

	// Set custom domain route if configured
	if cfg.App.Domain != "" {
		if err := p.ensureWorkerRoute(ctx, workerName, cfg.App.Domain); err != nil {
			p.log.Warn("Failed to set worker route for %s (non-fatal): %v", cfg.App.Domain, err)
		}
	}

	return nil
}

// UpdateSecrets pushes secrets as Cloudflare Worker environment variables.
func (p *CloudflareProvider) UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error {
	if len(secrets) == 0 {
		return nil
	}
	p.log.Info("Pushing %d secrets to Cloudflare Workers...", len(secrets))

	workerName := p.getWorkerNameFromApp(appName)
	for key, value := range secrets {
		if err := p.putWorkerSecret(ctx, workerName, key, value); err != nil {
			return fmt.Errorf("failed to set secret %s: %w", key, err)
		}
	}
	return nil
}

// GetSecrets retrieves secret names (values are write-only in CF API).
func (p *CloudflareProvider) GetSecrets(ctx context.Context, appName string) (map[string]string, error) {
	workerName := p.getWorkerNameFromApp(appName)
	url := fmt.Sprintf("/accounts/%s/workers/scripts/%s/secrets", p.accountID, workerName)

	var result struct {
		Success bool `json:"success"`
		Result  []struct {
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := p.cfRequest(ctx, "GET", url, nil, &result); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(result.Result))
	for _, s := range result.Result {
		out[s.Name] = "[secret]" // CF API never returns secret values
	}
	return out, nil
}

// SetSecret sets a single Worker secret.
func (p *CloudflareProvider) SetSecret(ctx context.Context, appName, key, value string) error {
	return p.putWorkerSecret(ctx, p.getWorkerNameFromApp(appName), key, value)
}

// UnsetSecret deletes a single Worker secret.
func (p *CloudflareProvider) UnsetSecret(ctx context.Context, appName, key string) error {
	workerName := p.getWorkerNameFromApp(appName)
	url := fmt.Sprintf("/accounts/%s/workers/scripts/%s/secrets/%s", p.accountID, workerName, key)
	return p.cfRequest(ctx, "DELETE", url, nil, nil)
}

// InvalidateCache purges the Cloudflare zone cache.
func (p *CloudflareProvider) InvalidateCache(ctx context.Context, cfg *config.NextDeployConfig) error {
	if cfg.App.Domain == "" {
		p.log.Info("No domain configured, skipping cache purge.")
		return nil
	}

	zoneID, err := p.getZoneID(ctx, cfg.App.Domain)
	if err != nil {
		return fmt.Errorf("failed to find zone for %s: %w", cfg.App.Domain, err)
	}

	url := fmt.Sprintf("/zones/%s/purge_cache", zoneID)
	body := map[string]interface{}{"purge_everything": true}
	if err := p.cfRequest(ctx, "POST", url, body, nil); err != nil {
		return fmt.Errorf("cache purge failed: %w", err)
	}

	p.log.Info("Cloudflare cache purged for zone %s", zoneID)
	return nil
}

// Rollback reverts the Worker to the previous deployment version.
func (p *CloudflareProvider) Rollback(ctx context.Context, cfg *config.NextDeployConfig) error {
	workerName := p.getWorkerName(cfg)
	p.log.Info("Fetching deployment history for worker: %s...", workerName)

	var listResult struct {
		Success bool `json:"success"`
		Result  struct {
			Deployments []struct {
				ID       string `json:"id"`
				Versions []struct {
					VersionID  string  `json:"version_id"`
					Percentage float64 `json:"percentage"`
				} `json:"versions"`
			} `json:"deployments"`
		} `json:"result"`
	}

	listURL := fmt.Sprintf("/accounts/%s/workers/scripts/%s/deployments", p.accountID, workerName)
	if err := p.cfRequest(ctx, "GET", listURL, nil, &listResult); err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	deployments := listResult.Result.Deployments
	if len(deployments) < 2 {
		return fmt.Errorf("not enough deployment history to rollback (found %d, need at least 2)", len(deployments))
	}

	// Index 0 is the active deployment, index 1 is the previous one
	previousVersionID := deployments[1].Versions[0].VersionID
	p.log.Info("Rolling back to version: %s", previousVersionID)

	body := map[string]interface{}{
		"versions": []map[string]interface{}{
			{"version_id": previousVersionID, "percentage": 100},
		},
	}

	if err := p.cfRequest(ctx, "POST", listURL, body, nil); err != nil {
		return fmt.Errorf("failed to activate previous deployment: %w", err)
	}

	p.log.Info("Rollback complete. Worker is now running version %s", previousVersionID)
	return nil
}

// Destroy removes the Worker, R2 bucket, and routes.
func (p *CloudflareProvider) Destroy(ctx context.Context, cfg *config.NextDeployConfig) error {
	workerName := p.getWorkerName(cfg)
	bucketName := p.getBucketName(cfg)

	p.log.Info("Deleting Worker: %s...", workerName)
	_ = p.cfRequest(ctx, "DELETE", fmt.Sprintf("/accounts/%s/workers/scripts/%s", p.accountID, workerName), nil, nil)

	p.log.Info("Deleting R2 bucket: %s...", bucketName)
	_ = p.cfRequest(ctx, "DELETE", fmt.Sprintf("/accounts/%s/r2/buckets/%s", p.accountID, bucketName), nil, nil)

	p.log.Info("Cloudflare resources destroyed.")
	return nil
}

// GetResourceMap returns a summary of provisioned Cloudflare resources.
func (p *CloudflareProvider) GetResourceMap(ctx context.Context, cfg *config.NextDeployConfig) (ServerlessResourceMap, error) {
	return ServerlessResourceMap{
		AppName:        cfg.App.Name,
		Environment:    cfg.App.Environment,
		Region:         "global",
		S3BucketName:   p.getBucketName(cfg),
		CustomDomain:   cfg.App.Domain,
		DeploymentTime: time.Now(),
	}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func (p *CloudflareProvider) getWorkerName(cfg *config.NextDeployConfig) string {
	return fmt.Sprintf("%s-%s", cfg.App.Name, cfg.App.Environment)
}

func (p *CloudflareProvider) getWorkerNameFromApp(appName string) string {
	return fmt.Sprintf("%s-production", appName)
}

func (p *CloudflareProvider) getBucketName(cfg *config.NextDeployConfig) string {
	return fmt.Sprintf("nextdeploy-%s-%s-assets", cfg.App.Name, cfg.App.Environment)
}

func (p *CloudflareProvider) cfRequest(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cloudflare API error %d: %s", resp.StatusCode, string(b))
	}

	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (p *CloudflareProvider) ensureR2BucketExists(ctx context.Context, name string) error {
	url := fmt.Sprintf("/accounts/%s/r2/buckets/%s", p.accountID, name)
	var result struct{ Success bool `json:"success"` }
	err := p.cfRequest(ctx, "GET", url, nil, &result)
	if err == nil {
		return nil // already exists
	}
	return p.cfRequest(ctx, "POST", fmt.Sprintf("/accounts/%s/r2/buckets", p.accountID), map[string]string{"name": name}, nil)
}

func (p *CloudflareProvider) uploadToR2(ctx context.Context, bucket, key, filePath, contentType string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "PUT",
		fmt.Sprintf("%s/accounts/%s/r2/buckets/%s/objects/%s", cfAPIBase, p.accountID, bucket, key),
		bytes.NewReader(data),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Content-Type", contentType)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("R2 upload failed %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (p *CloudflareProvider) uploadWorker(ctx context.Context, name string, zipContents []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Worker metadata part
	meta := map[string]interface{}{
		"main_module": "index.js",
		"bindings":    []interface{}{},
	}
	metaBytes, _ := json.Marshal(meta)
	metaPart, _ := mw.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="metadata"`},
		"Content-Type":        {"application/json"},
	})
	_, _ = metaPart.Write(metaBytes)

	// Worker script part
	scriptPart, _ := mw.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="script"; filename="worker.zip"`},
		"Content-Type":        {"application/zip"},
	})
	_, _ = scriptPart.Write(zipContents)
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, "PUT",
		fmt.Sprintf("%s/accounts/%s/workers/scripts/%s", cfAPIBase, p.accountID, name),
		&buf,
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("worker upload failed %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (p *CloudflareProvider) bindR2ToWorker(ctx context.Context, workerName, bindingName, bucketName string) error {
	url := fmt.Sprintf("/accounts/%s/workers/scripts/%s/bindings", p.accountID, workerName)
	body := map[string]interface{}{
		"type":        "r2_bucket",
		"name":        bindingName,
		"bucket_name": bucketName,
	}
	return p.cfRequest(ctx, "PUT", url, body, nil)
}

func (p *CloudflareProvider) ensureWorkerRoute(ctx context.Context, workerName, domain string) error {
	zoneID, err := p.getZoneID(ctx, domain)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("/zones/%s/workers/routes", zoneID)
	body := map[string]string{
		"pattern": domain + "/*",
		"script":  workerName,
	}
	return p.cfRequest(ctx, "POST", url, body, nil)
}

func (p *CloudflareProvider) putWorkerSecret(ctx context.Context, workerName, key, value string) error {
	url := fmt.Sprintf("/accounts/%s/workers/scripts/%s/secrets", p.accountID, workerName)
	body := map[string]string{"name": key, "text": value, "type": "secret_text"}
	return p.cfRequest(ctx, "PUT", url, body, nil)
}

func (p *CloudflareProvider) getZoneID(ctx context.Context, domain string) (string, error) {
	var result struct {
		Success bool `json:"success"`
		Result  []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := p.cfRequest(ctx, "GET", fmt.Sprintf("/zones?name=%s", domain), nil, &result); err != nil {
		return "", err
	}
	if len(result.Result) == 0 {
		return "", fmt.Errorf("no Cloudflare zone found for domain: %s", domain)
	}
	return result.Result[0].ID, nil
}
