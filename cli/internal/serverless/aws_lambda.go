package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// secretsExtensionLayerAccount is the AWS-published account ID that hosts the
// Parameters & Secrets Lambda Extension layer. Pinned per AWS documentation.
// The *version* is resolved at deploy time via ListLayerVersions and cached on
// the provider (p.secretsLayerVersion); the default below is only used as a
// fallback when lambda:ListLayerVersions is unavailable. Last verified: 2026-04.
const (
	secretsExtensionLayerAccount        = "177933130628"
	secretsExtensionLayerName           = "AWS-Parameters-and-Secrets-Lambda-Extension"
	defaultSecretsExtensionLayerVersion = "11"
)

// isMissingGetLayerVersion reports whether err is an IAM AccessDenied on
// lambda:GetLayerVersion — the one error code we want to react to with the
// opt-in allow_secrets_in_env fallback. Typed API check + substring match:
// defense in depth in case AWS reformats the message.
func isMissingGetLayerVersion(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.ErrorCode() != "AccessDeniedException" {
		return false
	}
	return strings.Contains(err.Error(), "lambda:GetLayerVersion")
}

// secretsExtensionLayerARN returns the layer ARN for the given region using
// the version cached on the provider, falling back to the pinned default when
// none has been resolved yet.
func (p *AWSProvider) secretsExtensionLayerARN(region string) string {
	version := p.secretsLayerVersion
	if version == "" {
		version = defaultSecretsExtensionLayerVersion
	}
	return fmt.Sprintf(
		"arn:aws:lambda:%s:%s:layer:%s:%s",
		region,
		secretsExtensionLayerAccount,
		secretsExtensionLayerName,
		version,
	)
}

// resolveSecretsExtensionLayerVersion asks AWS for the latest published version
// of the Secrets Extension layer. Returns the version as a string, or "" when
// the caller lacks lambda:ListLayerVersions or the call fails — the caller
// should then fall back to defaultSecretsExtensionLayerVersion.
func (p *AWSProvider) resolveSecretsExtensionLayerVersion(ctx context.Context, client *lambda.Client, region string) string {
	layerNameARN := fmt.Sprintf(
		"arn:aws:lambda:%s:%s:layer:%s",
		region,
		secretsExtensionLayerAccount,
		secretsExtensionLayerName,
	)
	out, err := client.ListLayerVersions(ctx, &lambda.ListLayerVersionsInput{
		LayerName: aws.String(layerNameARN),
		MaxItems:  aws.Int32(1),
	})
	if err != nil {
		p.log.Warn("Could not list Secrets Extension layer versions (falling back to pinned :%s): %v", defaultSecretsExtensionLayerVersion, err)
		return ""
	}
	if len(out.LayerVersions) == 0 {
		return ""
	}
	return fmt.Sprintf("%d", out.LayerVersions[0].Version)
}

// probeSecretsExtensionLayer calls GetLayerVersion as a lightweight IAM probe.
// Runs at Initialize time, before any S3 upload or Lambda mutation, so a
// misconfigured deploy fails before it leaves partial state behind.
//
//   - layer resolves + read succeeds → nil (happy path)
//   - AccessDenied on lambda:GetLayerVersion + AllowSecretsInEnv=false → error
//     (bail before mutating anything)
//   - AccessDenied + AllowSecretsInEnv=true → nil (warn now; the existing
//     retry path in ensureMainLambdaUpdatedToCurrentZip handles the fallback
//     layer drop — defense in depth).
//   - any other error → nil (probe is advisory; don't block deploys on
//     transient list/describe failures)
func (p *AWSProvider) probeSecretsExtensionLayer(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	if appCfg == nil || appCfg.Serverless == nil {
		return nil
	}
	region := p.cfg.Region
	if region == "" {
		return nil
	}

	client := lambda.NewFromConfig(p.cfg)

	// Resolve the latest version up front so every subsequent CreateFunction /
	// UpdateFunctionConfiguration uses the same ARN. Silent fallback to the
	// pinned default if List is unavailable — non-fatal.
	if v := p.resolveSecretsExtensionLayerVersion(ctx, client, region); v != "" {
		p.secretsLayerVersion = v
	}

	layerARN := p.secretsExtensionLayerARN(region)
	probeVersion := p.secretsLayerVersion
	if probeVersion == "" {
		probeVersion = defaultSecretsExtensionLayerVersion
	}

	_, err := client.GetLayerVersion(ctx, &lambda.GetLayerVersionInput{
		LayerName:     aws.String(layerARN),
		VersionNumber: mustAtoi64(probeVersion),
	})
	if err == nil {
		return nil
	}

	if isMissingGetLayerVersion(err) {
		if !appCfg.Serverless.AllowSecretsInEnv {
			p.log.Error("\n%s", missingLayerBlockedBanner)
			return errors.New(missingLayerBlockedErr)
		}
		p.log.Warn("\n%s", missingLayerFallbackBanner)
		return nil
	}

	p.log.Warn("Secrets Extension layer probe returned a non-IAM error (continuing anyway): %v", err)
	return nil
}

func mustAtoi64(s string) *int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil
		}
		n = n*10 + int64(c-'0')
	}
	return &n
}

// missingLayerBlockedBanner is shown when the IAM principal lacks
// `lambda:GetLayerVersion` AND `serverless.allow_secrets_in_env` is NOT set.
// We fail loudly rather than silently leaking secrets into Lambda env vars.
const missingLayerBlockedBanner = `════════════════════ DEPLOYMENT BLOCKED ════════════════════
IAM user lacks 'lambda:GetLayerVersion' for the AWS Secrets Extension Layer.

Recommended fix: add this permission to your IAM policy:
    {
      "Effect": "Allow",
      "Action": "lambda:GetLayerVersion",
      "Resource": "arn:aws:lambda:*:177933130628:layer:AWS-Parameters-and-Secrets-Lambda-Extension:*"
    }

Insecure escape hatch (NOT RECOMMENDED): set ` + "`serverless.allow_secrets_in_env: true`" + `
in nextdeploy.yml. This injects every secret directly into Lambda environment
variables, where they are visible in the AWS console, CloudTrail, and persist
in every published Lambda version forever (you cannot redact a published version).
════════════════════════════════════════════════════════════`

// missingLayerFallbackBanner is shown when the user has opted into the
// insecure fallback and we're about to drop the Secrets Extension layer.
const missingLayerFallbackBanner = `════════════════════ INSECURE FALLBACK ════════════════════
` + "`serverless.allow_secrets_in_env`" + ` is enabled.
Injecting secrets directly into Lambda env vars.
These will be visible in the AWS console and persist in every Lambda version.
Add lambda:GetLayerVersion to IAM and unset this flag as soon as possible.
════════════════════════════════════════════════════════════`

// missingLayerBlockedErr is the error returned to the caller when a deploy is
// refused for lack of lambda:GetLayerVersion. Wrapped with %w at the call site
// so errors.Is/As keep working.
const missingLayerBlockedErr = "missing lambda:GetLayerVersion IAM permission; refusing to leak secrets into Lambda env (set serverless.allow_secrets_in_env to override)"

// ImageOptConfig is the JSON contract consumed by the imgopt Lambda binary.
// AllowedDomains is the flattened list of every Next.js image source — both
// the legacy `images.domains[]` and the hostnames extracted from
// `images.remotePatterns[]`. The nextcore feature detector merges both into
// HasExternalImages, so the imgopt lambda only needs the flat list.
type ImageOptConfig struct {
	AllowedDomains []string `json:"allowed_domains"`
	DeviceSizes    []int    `json:"device_sizes"`
	ImageSizes     []int    `json:"image_sizes"`
	Formats        []string `json:"formats"`
}

func (p *AWSProvider) extractImageConfig(meta *nextcore.NextCorePayload) string {
	cfg := ImageOptConfig{
		AllowedDomains: []string{},
		DeviceSizes:    []int{640, 750, 828, 1080, 1200, 1920, 2048, 3840},
		ImageSizes:     []int{16, 32, 48, 64, 96, 128, 256, 384},
		Formats:        []string{"image/webp"},
	}

	if meta.DetectedFeatures != nil && len(meta.DetectedFeatures.HasExternalImages) > 0 {
		cfg.AllowedDomains = meta.DetectedFeatures.HasExternalImages
	}

	jsonBytes, _ := json.Marshal(cfg)
	return string(jsonBytes)
}

func (p *AWSProvider) getLambdaFunctionName(appCfg *cfgTypes.NextDeployConfig) string {
	return strings.ToLower(fmt.Sprintf("%s-%s", appCfg.App.Name, appCfg.App.Environment))
}

func (p *AWSProvider) DeployCompute(ctx context.Context, pkg *packaging.PackageResult, appCfg *cfgTypes.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	if meta.OutputMode == nextcore.OutputModeExport {
		p.log.Info("Export Mode detected. Skipping Lambda deployment (static only).")
		// Update CloudFront for static only
		bucketName := p.getS3BucketName(appCfg)
		// Determine domain
		domain := meta.Domain
		if domain == "" {
			domain = appCfg.App.Domain
		}

		_, err := p.ensureCloudFrontDistributionExists(ctx, appCfg.Serverless, bucketName, "", "", domain)
		if err != nil {
			p.log.Warn("Failed to ensure CloudFront Distribution for static site: %v", err)
		}
		return nil
	}

	p.log.Info("Deploying Compute Layer to AWS Lambda for app: %s...", appCfg.App.Name)

	if appCfg.Serverless == nil || appCfg.Serverless.IAMRole == "" || strings.Contains(appCfg.Serverless.IAMRole, "role-name") {
		p.log.Info("Ensuring managed 'nextdeploy-serverless-role' policies are up to date...")
		_, roleErr := p.ensureExecutionRoleExists(ctx)
		if roleErr != nil {
			p.log.Warn("Failed to update IAM execution role, permissions may be stale: %v", roleErr)
		}
	}

	client := lambda.NewFromConfig(p.cfg)
	functionName := p.getLambdaFunctionName(appCfg)
	p.verboseLog("  Lambda function name: %s", functionName)

	// Use pre-built zip from packager
	zipPath := pkg.LambdaZipPath
	zipContents, err := os.ReadFile(zipPath)
	if err != nil {
		return fmt.Errorf("failed to read lambda zip package: %w", err)
	}
	p.verboseLog("  Lambda zip size: %s (%s)", formatBytes(int64(len(zipContents))), zipPath)

	// 3a. Save zip to S3 for rollback history (non-fatal)
	bucketForHistory := p.getS3BucketName(appCfg)
	if s3ZipKey, saveErr := p.saveLambdaZipToS3(ctx, bucketForHistory, functionName, zipContents, meta); saveErr != nil {
		p.log.Warn("Could not save deployment zip to S3 history (rollback may not work): %v", saveErr)
	} else {
		p.log.Info("Deployment zip saved for rollback: %s", s3ZipKey)
	}
	// 4. Ensure Log Group and Alarms exist for observability
	if err := p.ensureLogGroupExists(ctx, functionName); err != nil {
		p.log.Warn("Failed to ensure Log Group: %v", err)
	}
	if err := p.ensureLambdaErrorAlarm(ctx, functionName); err != nil {
		p.log.Warn("Failed to ensure CloudWatch Alarm: %v", err)
	}

	// 5. Ensure Lambda function exists (provision if missing).
	functionJustCreated, err := p.ensureLambdaFunctionExists(ctx, client, functionName, appCfg.App.Name, appCfg.Serverless, zipContents)
	if err != nil {
		return err
	}

	// 5. Ensure Lambda Function URL exists
	functionUrl, err := p.ensureLambdaFunctionURLExists(ctx, client, functionName)
	if err != nil {
		p.log.Warn("Failed to ensure Lambda Function URL (distribution might fail): %v", err)
	}

	// 6. Provision auxiliary infrastructure that CloudFront depends on (or is
	// independent of) BEFORE creating the distribution.
	//
	// Ordering rules:
	//   1. SQS queue (cached for reuse — see C1 in REVIEW.md).
	//   2. Image-optimization Lambda + URL (CloudFront needs its origin URL).
	//   3. CloudFront distribution → backfill discovered ID into config.
	//   4. ISR revalidation Lambda LAST, so it sees a real DISTRIBUTION_ID
	//      (see C2 in REVIEW.md — first-deploy bug where reval booted with "").
	p.log.Info("Ensuring Auxiliary Lambdas exist (ISR: %v, ImgOpt: %v)...", appCfg.Serverless.IsrRevalidation, appCfg.Serverless.ImageOptimization)

	// 6a. SQS queue (single source of truth for queueUrl/queueArn)
	var (
		revalQueueUrl string
		revalQueueArn string
	)
	if appCfg.Serverless != nil && appCfg.Serverless.IsrRevalidation {
		qUrl, qArn, qErr := p.ensureRevalidationQueueExists(ctx, appCfg.App.Name)
		if qErr != nil {
			p.log.Warn("Failed to ensure Revalidation SQS Queue: %v", qErr)
		} else {
			revalQueueUrl = qUrl
			revalQueueArn = qArn
		}
	}

	// 6b. Image-optimization Lambda (CloudFront origin)
	var imgOptUrl string
	if appCfg.Serverless != nil && appCfg.Serverless.ImageOptimization {
		imgOptName := functionName + "-imgopt"
		p.verboseLog("  Image Optimization Lambda name: %s", imgOptName)
		zipBytes, err := getEmbeddedLambdaZip("imgopt")
		if err != nil {
			p.log.Warn("Failed to load embedded imgopt lambda (is it built?): %v", err)
		} else {
			// Extract image config from metadata
			imageConfigJSON := p.extractImageConfig(meta)

			envVars := map[string]string{
				"DISTRIBUTION_ID":   appCfg.Serverless.CloudFrontId,
				"SOURCE_BUCKET":     p.getS3BucketName(appCfg),
				"IMAGE_CONFIG_JSON": imageConfigJSON,
			}
			_, err = p.ensureAuxiliaryLambdaExists(ctx, client, imgOptName, zipBytes, envVars)
			if err != nil {
				p.log.Warn("Failed to ensure Image Optimization Lambda: %v", err)
			} else {
				imgOptUrl, err = p.ensureLambdaFunctionURLExists(ctx, client, imgOptName)
				if err != nil {
					p.log.Warn("Failed to ensure Function URL for Image Optimization Lambda: %v", err)
				}
			}
		}
	}

	// 6c. CloudFront distribution
	bucketName := p.getS3BucketName(appCfg)
	domain := meta.Domain
	if domain == "" {
		domain = appCfg.App.Domain
	}

	p.log.Info("Ensuring CloudFront distribution exists for Lambda origin (Domain: %s)...", domain)
	distributionId, err := p.ensureCloudFrontDistributionExists(ctx, appCfg.Serverless, bucketName, functionUrl, imgOptUrl, domain)
	if err != nil {
		p.log.Warn("Failed to ensure CloudFront Distribution: %v", err)
	} else {
		p.verboseLog("  CloudFront distribution ID: %s", distributionId)

		// Backfill so downstream consumers (reval lambda env, resource map)
		// see the discovered ID even on first deploy. See C2 in REVIEW.md.
		if appCfg.Serverless != nil {
			appCfg.Serverless.CloudFrontId = distributionId
		}

		// 7. Update S3 Bucket Policy for OAC
		if err := p.updateS3BucketPolicyForOAC(ctx, bucketName, distributionId); err != nil {
			p.log.Warn("Failed to update S3 Bucket Policy for OAC: %v", err)
		}

		// Get distribution domain name to show the user
		cfClient := cloudfront.NewFromConfig(p.cfg)
		dist, _ := cfClient.GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(distributionId)})
		if dist != nil && dist.Distribution != nil {
			p.log.Info("🚀 Application is accessible at: https://%s", *dist.Distribution.DomainName)
		}

		// Trigger invalidation for the newly managed distribution
		if err := p.invalidateCloudFront(ctx, distributionId); err != nil {
			p.log.Warn("Cache invalidation failed (non-fatal): %v", err)
		}
	}

	// 6d. ISR revalidation Lambda — created AFTER CloudFront so DISTRIBUTION_ID
	// is real, and reuses the cached queueUrl/queueArn from 6a.
	if appCfg.Serverless != nil && appCfg.Serverless.IsrRevalidation && revalQueueUrl != "" {
		revalName := functionName + "-reval"
		p.verboseLog("  ISR Revalidation Lambda name: %s", revalName)
		zipBytes, zipErr := getEmbeddedLambdaZip("revalidator")
		if zipErr != nil {
			p.log.Warn("Failed to load embedded revalidator lambda (is it built?): %v", zipErr)
		} else {
			envVars := map[string]string{
				"DISTRIBUTION_ID": appCfg.Serverless.CloudFrontId,
				"CACHE_BUCKET":    p.getS3BucketName(appCfg),
				"QUEUE_URL":       revalQueueUrl,
			}
			if _, auxErr := p.ensureAuxiliaryLambdaExists(ctx, client, revalName, zipBytes, envVars); auxErr != nil {
				p.log.Warn("Failed to ensure Revalidation Lambda: %v", auxErr)
			} else if trigErr := p.ensureLambdaSQSTrigger(ctx, client, revalName, revalQueueArn); trigErr != nil {
				p.log.Warn("Failed to ensure SQS mapping for Revalidation Lambda: %v", trigErr)
			}
		}
	}

	if !functionJustCreated {
		p.log.Info("Updating Lambda function code...")
		_, err := client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: aws.String(functionName),
			ZipFile:      zipContents,
		})
		if err != nil {
			return fmt.Errorf("failed to update Lambda code: %w", err)
		}
		p.log.Info("Lambda code updated successfully. Waiting for update to stabilize...")

		if err := p.waitForLambdaStable(ctx, client, functionName); err != nil {
			p.log.Warn("Timed out waiting for Lambda stability: %v", err)
		}
	}

	secretName := p.secretName(appCfg.App.Name)

	handler := "bridge.handler"
	if appCfg.Serverless != nil && appCfg.Serverless.Handler != "" {
		handler = appCfg.Serverless.Handler
	}

	p.log.Info("Updating Lambda configuration (Handler: %s)...", handler)
	secretsExtensionLayer := p.secretsExtensionLayerARN(p.cfg.Region)

	// Reuse the queue URL cached in 6a — avoids a second AWS API call and
	// ensures we never silently drop ND_REVALIDATION_QUEUE on transient errors.
	envVars := map[string]string{
		"ND_SECRET_NAME":  secretName,
		"NODE_ENV":        "production",
		"ND_CACHE_BUCKET": fmt.Sprintf("nextdeploy-%s-%s-assets-%s", appCfg.App.Name, appCfg.App.Environment, p.accountID),
	}

	if revalQueueUrl != "" {
		envVars["ND_REVALIDATION_QUEUE"] = revalQueueUrl
	}

	maxRetries := 5
	layersToApply := []string{secretsExtensionLayer}
	layerFallbackApplied := false
	for i := 0; i < maxRetries; i++ {
		// If we're in fallback mode, we need to manually fetch and merge secrets into the environment
		if layerFallbackApplied {
			p.log.Info("Fallback mode active: Fetching and merging secrets into Lambda environment...")
			cloudSecrets, err := p.GetSecrets(ctx, appCfg.App.Name)
			if err != nil {
				p.log.Warn("Failed to fetch cloud secrets for environment injection: %v", err)
			} else {
				for k, v := range cloudSecrets {
					envVars[k] = v
				}
				p.log.Success("Injected %d secrets directly into Lambda environment.", len(cloudSecrets))
			}
		}

		_, err := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(functionName),
			Handler:      aws.String(handler),
			Environment: &lambdaTypes.Environment{
				Variables: envVars,
			},
			Layers: layersToApply,
		})
		if err == nil {
			break
		}

		// Insecure fallback: only triggered when the IAM principal lacks
		// lambda:GetLayerVersion AND the user has explicitly opted in via
		// `serverless.allow_secrets_in_env`. Without opt-in we fail loudly so
		// secrets are never silently leaked into Lambda env vars / console /
		// CloudTrail / version history. Usually the S5 precheck in Initialize
		// has already bailed; this branch is defense in depth.
		if isMissingGetLayerVersion(err) && len(layersToApply) > 0 {
			if !appCfg.Serverless.AllowSecretsInEnv {
				p.log.Error("\n%s", missingLayerBlockedBanner)
				return errors.New(missingLayerBlockedErr)
			}
			if !layerFallbackApplied {
				p.log.Warn("\n%s", missingLayerFallbackBanner)
				layersToApply = nil // remove the layer and retry immediately
				// Signal fallback to bridge.js so it skips the extension fetch
				// and does not FATAL on "no secrets returned".
				envVars["ND_SECRETS_MODE"] = "env"
				layerFallbackApplied = true
				i-- // safe: only happens once, guarded by flag
				continue
			}
		}

		var conflict *lambdaTypes.ResourceConflictException
		if errors.As(err, &conflict) && i < maxRetries-1 {
			p.log.Warn("Lambda is busy, retrying configuration update (%d/%d)...", i+1, maxRetries)
			time.Sleep(2 * time.Second)
			continue
		}
		p.log.Error("Failed to update Lambda configuration: %v", err)
		return fmt.Errorf("failed to update Lambda configuration: %w", err)
	}

	p.log.Info("Waiting for Lambda configuration update to stabilize...")
	if err := p.waitForLambdaStable(ctx, client, functionName); err != nil {
		p.log.Warn("Timed out waiting for Lambda stability after config update: %v", err)
	}

	// Publish a numbered version for rollback support
	pubOutput, err := client.PublishVersion(ctx, &lambda.PublishVersionInput{
		FunctionName: aws.String(functionName),
		Description:  aws.String(fmt.Sprintf("nextdeploy deploy %s", time.Now().Format(time.RFC3339))),
	})
	if err != nil {
		p.log.Warn("Failed to publish Lambda version (rollback may not work): %v", err)
	} else {
		p.log.Info("Published Lambda version %s for rollback support.", *pubOutput.Version)
	}

	return nil
}

// Rollback reverts the Lambda function to a previous deployed zip using the S3
// deployment history. Target selection:
//   - opts.ToCommit: prefix-match against history GitCommit (errors if not found
//     within retention).
//   - opts.Steps (default 1): walk N entries back from the current (last) one.
//
// This is instant — no HTTP download required.
func (p *AWSProvider) Rollback(ctx context.Context, appCfg *cfgTypes.NextDeployConfig, opts RollbackOptions) error {
	client := lambda.NewFromConfig(p.cfg)
	functionName := p.getLambdaFunctionName(appCfg)
	bucketName := p.getS3BucketName(appCfg)

	p.log.Info("Rolling back Lambda function %s...", functionName)

	// Fetch deployment history from S3 manifest
	history, err := p.getLambdaDeploymentHistory(ctx, bucketName, functionName)
	if err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	target, err := selectRollbackTarget(history, opts)
	if err != nil {
		return err
	}

	if target.GitDirty {
		p.log.Warn("Target deployment %s was built from a dirty working tree", shortCommit(target.GitCommit))
	}

	label := target.S3Key
	if target.GitCommit != "" {
		label = fmt.Sprintf("%s (commit %s)", target.S3Key, shortCommit(target.GitCommit))
	}
	p.log.Info("Rolling back to: %s", label)

	// Update Lambda code directly from S3 — no download needed
	_, err = client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		S3Bucket:     aws.String(bucketName),
		S3Key:        aws.String(target.S3Key),
	})
	if err != nil {
		return fmt.Errorf("failed to restore Lambda code from S3: %w", err)
	}

	p.log.Info("Lambda code restored. Waiting for stabilization...")
	if err := p.waitForLambdaStable(ctx, client, functionName); err != nil {
		p.log.Warn("Timed out waiting for Lambda stability after rollback: %v", err)
	}

	// Publish the rollback as a new version, tagged with the target commit so
	// the Lambda console shows what is actually running.
	pubDesc := fmt.Sprintf("nextdeploy rollback at %s", time.Now().Format(time.RFC3339))
	if target.GitCommit != "" {
		pubDesc = fmt.Sprintf("nextdeploy rollback to %s at %s", shortCommit(target.GitCommit), time.Now().Format(time.RFC3339))
	}
	_, _ = client.PublishVersion(ctx, &lambda.PublishVersionInput{
		FunctionName: aws.String(functionName),
		Description:  aws.String(pubDesc),
	})

	// Invalidate CloudFront cache
	if err := p.InvalidateCache(ctx, appCfg); err != nil {
		p.log.Warn("Cache invalidation after rollback failed (non-fatal): %v", err)
	}

	p.log.Info("✅ Rollback complete! Lambda is running the previous deployment.")
	return nil
}

func (p *AWSProvider) ensureLambdaFunctionExists(ctx context.Context, client *lambda.Client, name, appName string, sCfg *cfgTypes.ServerlessConfig, zipContents []byte) (justCreated bool, err error) {
	_, err = client.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(name),
	})
	if err == nil {
		return false, nil // Already exists
	}

	var notFound *lambdaTypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return false, fmt.Errorf("failed to check Lambda function status: %w", err)
	}

	// Function does not exist — create it
	handler := "bridge.handler"
	if sCfg.Handler != "" {
		handler = sCfg.Handler
	}

	runtime := lambdaTypes.RuntimeNodejs20x
	if sCfg.Runtime != "" {
		runtime = lambdaTypes.Runtime(sCfg.Runtime)
	}

	memory := int32(1024)
	if sCfg.MemorySize != 0 {
		memory = sCfg.MemorySize
	}

	timeout := int32(30)
	if sCfg.Timeout != 0 {
		timeout = sCfg.Timeout
	}

	// Determine IAM Role (Manual vs Auto-Provisioned)
	var roleArn string
	if sCfg.IAMRole != "" && !strings.Contains(sCfg.IAMRole, "role-name") {
		roleArn = sCfg.IAMRole
		if strings.Contains(roleArn, "ACCOUNT_ID") && p.accountID != "" {
			roleArn = strings.ReplaceAll(roleArn, "ACCOUNT_ID", p.accountID)
			p.log.Info("Automatically replaced ACCOUNT_ID placeholder in IAM Role ARN.")
		}
	} else {
		p.log.Info("No valid IAM Role provided, attempting to use/create managed 'nextdeploy-serverless-role'...")
		roleArn, err = p.ensureExecutionRoleExists(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to ensure IAM execution role exists: %w", err)
		}
	}

	p.log.Info("Lambda function %s does not exist, creating with role %s (Handler: %s, Runtime: %s)...", name, roleArn, handler, runtime)

	// Managed Layer for Secrets Extension (Node.js 20 compatible)
	secretsExtensionLayer := p.secretsExtensionLayerARN(p.cfg.Region)

	createInput := &lambda.CreateFunctionInput{
		Code: &lambdaTypes.FunctionCode{
			ZipFile: zipContents,
		},
		FunctionName: aws.String(name),
		Role:         aws.String(roleArn),
		Handler:      aws.String(handler),
		Runtime:      runtime,
		Environment: &lambdaTypes.Environment{
			Variables: map[string]string{
				"NODE_ENV":       "production",
				"ND_SECRET_NAME": p.secretName(appName),
			},
		},
		Timeout:    aws.Int32(timeout),
		MemorySize: aws.Int32(memory),
		Layers:     []string{secretsExtensionLayer},
	}

	maxRetries := 10
	retryDelay := 5 * time.Second
	layerFallbackApplied := false
	for i := 0; i < maxRetries; i++ {
		_, createErr := client.CreateFunction(ctx, createInput)
		if createErr == nil {
			p.log.Info("Lambda function %s created successfully.", name)
			return true, nil
		}

		// See UpdateFunctionConfiguration block above for the security rationale.
		if isMissingGetLayerVersion(createErr) && len(createInput.Layers) > 0 {
			if !sCfg.AllowSecretsInEnv {
				p.log.Error("\n%s", missingLayerBlockedBanner)
				return false, fmt.Errorf("%s. underlying: %w", missingLayerBlockedErr, createErr)
			}
			if !layerFallbackApplied {
				p.log.Warn("\n%s", missingLayerFallbackBanner)
				createInput.Layers = nil // remove the layer and retry immediately
				// Mirror the UpdateFunctionConfiguration fallback: tell bridge.js
				// to skip the extension fetch and avoid the FATAL-on-empty guard.
				if createInput.Environment != nil && createInput.Environment.Variables != nil {
					createInput.Environment.Variables["ND_SECRETS_MODE"] = "env"
				}
				layerFallbackApplied = true
				i-- // safe: only happens once, guarded by flag
				continue
			}
		}

		var invalidParam *lambdaTypes.InvalidParameterValueException
		if errors.As(createErr, &invalidParam) && strings.Contains(createErr.Error(), "role") && i < maxRetries-1 {
			p.log.Warn("IAM role not yet propagated, retrying CreateFunction (%d/%d) in %s...", i+1, maxRetries, retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		return false, fmt.Errorf("failed to create Lambda function: %w", createErr)
	}

	return false, fmt.Errorf("failed to create Lambda function after %d retries: IAM role did not propagate in time", maxRetries)
}

func (p *AWSProvider) waitForLambdaStable(ctx context.Context, client *lambda.Client, functionName string) error {
	maxRetries := 20
	for i := 0; i < maxRetries; i++ {
		output, err := client.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String(functionName),
		})
		if err != nil {
			return err
		}

		status := output.Configuration.LastUpdateStatus
		p.verboseLog("  Lambda update status: %s", status)

		if status == lambdaTypes.LastUpdateStatusSuccessful {
			return nil
		}
		if status == lambdaTypes.LastUpdateStatusFailed {
			return fmt.Errorf("lambda update failed: %s", *output.Configuration.LastUpdateStatusReason)
		}

		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for lambda stability")
}

func (p *AWSProvider) ensureLambdaFunctionURLExists(ctx context.Context, client *lambda.Client, functionName string) (string, error) {
	p.log.Info("Ensuring Lambda Function URL exists for %s...", functionName)

	var functionUrl string
	// 1. Check if it already exists
	getOutput, err := client.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(functionName),
	})

	if err == nil {
		functionUrl = *getOutput.FunctionUrl
		p.log.Info("Lambda Function URL found: %s", functionUrl)

		if getOutput.AuthType != lambdaTypes.FunctionUrlAuthTypeAwsIam {
			p.log.Info("Updating Function URL AuthType to AWS_IAM for CloudFront OAC...")
			_, err = client.UpdateFunctionUrlConfig(ctx, &lambda.UpdateFunctionUrlConfigInput{
				FunctionName: aws.String(functionName),
				AuthType:     lambdaTypes.FunctionUrlAuthTypeAwsIam,
			})
			if err != nil {
				return "", fmt.Errorf("failed to update Function URL AuthType to AWS_IAM: %w", err)
			}
		}
	} else {
		var notFound *lambdaTypes.ResourceNotFoundException
		if !errors.As(err, &notFound) {
			return "", fmt.Errorf("failed to check for Function URL: %w", err)
		}

		// 2. Create it
		p.log.Info("Creating new Function URL for %s...", functionName)
		createOutput, err := client.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
			FunctionName: aws.String(functionName),
			AuthType:     lambdaTypes.FunctionUrlAuthTypeAwsIam,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create Function URL: %w", err)
		}
		functionUrl = *createOutput.FunctionUrl
	}

	// 3. Purge existing public permissions to avoid collisions and stale states
	p.log.Info("Hardening Function URL permissions (purging old statements)...")
	policyOutput, err := client.GetPolicy(ctx, &lambda.GetPolicyInput{
		FunctionName: aws.String(functionName),
	})
	if err == nil && policyOutput.Policy != nil {
		sidsToRemove := []string{"AllowPublicFunctionUrl", "AllowCloudFrontOACAccess", "AllowCloudFrontOACAccessInvoke"}
		for i := 0; i < 20; i++ {
			sidsToRemove = append(sidsToRemove, fmt.Sprintf("AllowPublicFunctionUrl-%d", i))
		}

		for _, sid := range sidsToRemove {
			if strings.Contains(*policyOutput.Policy, sid) {
				_, _ = client.RemovePermission(ctx, &lambda.RemovePermissionInput{
					FunctionName: aws.String(functionName),
					StatementId:  aws.String(sid),
				})
			}
		}
	}

	// 4. Add fresh permission for CloudFront OAC access
	p.log.Info("Applying fresh CloudFront OAC access permissions (InvokeFunctionUrl and InvokeFunction)...")

	stsClient := sts.NewFromConfig(p.cfg)
	callerId, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get caller identity: %w", err)
	}
	accountId := *callerId.Account

	// Define permissions we need to add. AWS recently requires both lambda:InvokeFunctionUrl and lambda:InvokeFunction
	// for newer Function URLs behind CloudFront OAC.
	permissions := []struct {
		StatementId string
		Action      string
	}{
		{
			StatementId: "AllowCloudFrontOACAccess",
			Action:      "lambda:InvokeFunctionUrl",
		},
		{
			StatementId: "AllowCloudFrontOACAccessInvoke",
			Action:      "lambda:InvokeFunction",
		},
	}

	for _, perm := range permissions {
		maxRetries := 5
		for i := 0; i < maxRetries; i++ {
			input := &lambda.AddPermissionInput{
				FunctionName:  aws.String(functionName),
				StatementId:   aws.String(perm.StatementId),
				Action:        aws.String(perm.Action),
				Principal:     aws.String("cloudfront.amazonaws.com"),
				SourceAccount: aws.String(accountId),
				SourceArn:     aws.String(fmt.Sprintf("arn:aws:cloudfront::%s:distribution/*", accountId)),
			}

			if perm.Action == "lambda:InvokeFunctionUrl" {
				input.FunctionUrlAuthType = lambdaTypes.FunctionUrlAuthTypeAwsIam
			}

			_, err = client.AddPermission(ctx, input)
			if err == nil {
				p.log.Info("CloudFront OAC access permission '%s' applied successfully.", perm.Action)
				break
			}
			if strings.Contains(err.Error(), "already exists") {
				p.log.Info("CloudFront OAC access permission '%s' already exists.", perm.Action)
				break
			}

			var conflict *lambdaTypes.ResourceConflictException
			if (errors.As(err, &conflict) || strings.Contains(err.Error(), "InProgress")) && i < maxRetries-1 {
				p.log.Warn("Lambda is busy, retrying permission application '%s' (%d/%d)...", perm.Action, i+1, maxRetries)
				time.Sleep(2 * time.Second)
				continue
			}
			p.log.Warn("Failed to add CloudFront permission '%s': %v", perm.Action, err)
			break
		}
	}

	return functionUrl, nil
}

func (p *AWSProvider) ensureAuxiliaryLambdaExists(ctx context.Context, client *lambda.Client, name string, zipContents []byte, envVars map[string]string) (bool, error) {
	_, err := client.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(name),
	})
	if err == nil {
		// Update code directly since it exists
		p.verboseLog("Updating Auxiliary Lambda %s code...", name)
		_, err := client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: aws.String(name),
			ZipFile:      zipContents,
		})
		if err != nil {
			return false, fmt.Errorf("failed to update Auxiliary Lambda code: %w", err)
		}

		err = p.waitForLambdaStable(ctx, client, name)

		// Update env vars
		_, err = client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(name),
			Environment: &lambdaTypes.Environment{
				Variables: envVars,
			},
		})
		if err != nil {
			p.log.Warn("Failed to update config for auxiliary lambda %s: %v", name, err)
		}

		return false, nil
	}

	var notFound *lambdaTypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return false, fmt.Errorf("failed to check Auxiliary Lambda status: %w", err)
	}

	roleArn, err := p.ensureExecutionRoleExists(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to ensure IAM execution role for lambda exists: %w", err)
	}

	p.log.Info("Creating Auxiliary Lambda function %s...", name)

	createInput := &lambda.CreateFunctionInput{
		Code:         &lambdaTypes.FunctionCode{ZipFile: zipContents},
		FunctionName: aws.String(name),
		Role:         aws.String(roleArn),
		Handler:      aws.String("bootstrap"),
		Runtime:      lambdaTypes.RuntimeProvidedal2023,
		Environment: &lambdaTypes.Environment{
			Variables: envVars,
		},
		Timeout:    aws.Int32(30),
		MemorySize: aws.Int32(512),
	}

	maxRetries := 10
	retryDelay := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		_, err := client.CreateFunction(ctx, createInput)
		if err == nil {
			p.log.Info("Auxiliary Lambda function %s created successfully.", name)
			return true, nil
		}

		var invalidParam *lambdaTypes.InvalidParameterValueException
		if errors.As(err, &invalidParam) && strings.Contains(err.Error(), "role") && i < maxRetries-1 {
			p.log.Warn("IAM role not yet propagated, retrying CreateFunction (%d/%d)...", i+1, maxRetries)
			time.Sleep(retryDelay)
			continue
		}

		return false, fmt.Errorf("failed to create Auxiliary Lambda %s: %w", name, err)
	}

	return false, fmt.Errorf("failed to create Auxiliary Lambda %s after retries", name)
}

func (p *AWSProvider) ensureLogGroupExists(ctx context.Context, functionName string) error {
	p.log.Info("Ensuring CloudWatch Log Group exists with 30-day retention for %s...", functionName)
	logGroupName := "/aws/lambda/" + functionName
	client := cloudwatchlogs.NewFromConfig(p.cfg)

	_, err := client.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(logGroupName),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "AlreadyExists") {
			return fmt.Errorf("failed to create log group: %w", err)
		}
	}

	_, err = client.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		LogGroupName:    aws.String(logGroupName),
		RetentionInDays: aws.Int32(30),
	})
	if err != nil {
		return fmt.Errorf("failed to set log retention policy: %w", err)
	}

	return nil
}

func (p *AWSProvider) ensureLambdaErrorAlarm(ctx context.Context, functionName string) error {
	p.log.Info("Ensuring CloudWatch Alarm (Error Rate > 1%%) exists for %s...", functionName)
	client := cloudwatch.NewFromConfig(p.cfg)

	alarmName := fmt.Sprintf("%s-error-rate", functionName)
	_, err := client.PutMetricAlarm(ctx, &cloudwatch.PutMetricAlarmInput{
		AlarmName:          aws.String(alarmName),
		AlarmDescription:   aws.String("Alarm if Lambda error rate exceeds 1% over 5 minutes"),
		MetricName:         aws.String("Errors"),
		Namespace:          aws.String("AWS/Lambda"),
		Statistic:          "Sum",
		Period:             aws.Int32(300), // 5 minutes
		EvaluationPeriods:  aws.Int32(1),
		Threshold:          aws.Float64(1.0),
		ComparisonOperator: "GreaterThanOrEqualToThreshold",
		Dimensions: []cwTypes.Dimension{
			{
				Name:  aws.String("FunctionName"),
				Value: aws.String(functionName),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create cloudwatch alarm: %w", err)
	}
	return nil
}
