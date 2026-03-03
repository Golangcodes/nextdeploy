package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gabriel-vasile/mimetype"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smTypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/Golangcodes/nextdeploy/shared"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

type AWSProvider struct {
	log *shared.Logger
	cfg aws.Config
}

func NewAWSProvider() *AWSProvider {
	return &AWSProvider{
		log: shared.PackageLogger("aws_serverless", "☁️  AWS::"),
	}
}

func (p *AWSProvider) Initialize(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	p.log.Info("Initializing AWS Serverless Deployment session...")

	var opts []func(*config.LoadOptions) error

	// Determine region (priority: serverless block > cloudprovider block)
	region := appCfg.Serverless.Region
	if region == "" && appCfg.CloudProvider != nil {
		region = appCfg.CloudProvider.Region
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	// Determine Profile (priority: serverless block > cloudprovider block)
	profile := appCfg.Serverless.Profile
	if profile == "" && appCfg.CloudProvider != nil {
		profile = appCfg.CloudProvider.Profile
	}

	if profile != "" {
		p.log.Info("Using AWS Profile: %s", profile)
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}

	// Explicit credentials (if still used, though profiles are preferred)
	if appCfg.CloudProvider != nil && appCfg.CloudProvider.AccessKey != "" && appCfg.CloudProvider.SecretKey != "" {
		p.log.Info("Using explicit credentials from CloudProvider config.")
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				appCfg.CloudProvider.AccessKey,
				appCfg.CloudProvider.SecretKey,
				"",
			),
		))
	} else if profile == "" {
		p.log.Info("No profile or explicit credentials found, falling back to default SDK resolution (env/IAM).")
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("unable to load AWS SDK config: %w", err)
	}
	p.cfg = cfg
	return nil
}

func (p *AWSProvider) DeployStatic(ctx context.Context, tarballPath string, appCfg *cfgTypes.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	p.log.Info("Syncing static assets to S3 Bucket (%s)...", appCfg.Serverless.S3Bucket)

	if appCfg.Serverless.S3Bucket == "" {
		p.log.Info("S3 Bucket not specified, skipping static sync.")
		return nil
	}

	client := s3.NewFromConfig(p.cfg)

	// Ensure bucket exists before uploading
	if err := p.ensureBucketExists(ctx, client, appCfg.Serverless.S3Bucket, appCfg.Serverless.Region); err != nil {
		return fmt.Errorf("failed to ensure S3 bucket exists: %w", err)
	}

	uploader := transfermanager.New(client)

	// We need to unpack the tarball first to access static files
	tmpDir, err := os.MkdirTemp("", "nd-serverless-deploy-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := shared.ExtractTarGz(tarballPath, tmpDir); err != nil {
		return fmt.Errorf("failed to extract tarball: %w", err)
	}

	distDir := meta.DistDir
	if distDir == "" {
		distDir = ".next"
	}

	// Directories to upload to S3
	uploadDirs := []struct {
		Src  string
		Dest string
	}{
		{Src: filepath.Join(tmpDir, "public"), Dest: ""},
		{Src: filepath.Join(tmpDir, distDir, "static"), Dest: "_next/static"},
	}

	for _, dir := range uploadDirs {
		if _, err := os.Stat(dir.Src); os.IsNotExist(err) {
			continue
		}

		err = filepath.Walk(dir.Src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			relPath, err := filepath.Rel(dir.Src, path)
			if err != nil {
				return err
			}

			s3Key := filepath.Join(dir.Dest, relPath)
			// Normalize path for S3
			s3Key = filepath.ToSlash(s3Key)

			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			defer file.Close()

			mtype, err := mimetype.DetectFile(path)
			contentType := "application/octet-stream"
			if err == nil {
				contentType = mtype.String()
			}

			// Add basic Cache-Control
			cacheControl := "public, max-age=31536000, immutable"
			if dir.Dest == "" { // e.g. public directory (favicon, etc) shouldn't be cached forever usually
				cacheControl = "public, max-age=3600"
			}

			_, err = uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
				Bucket:       aws.String(appCfg.Serverless.S3Bucket),
				Key:          aws.String(s3Key),
				Body:         file,
				ContentType:  aws.String(contentType),
				CacheControl: aws.String(cacheControl),
			})

			if err != nil {
				return fmt.Errorf("failed to upload %s to S3: %w", s3Key, err)
			}

			return nil
		})

		if err != nil {
			return fmt.Errorf("failed walking directory %s: %w", dir.Src, err)
		}
	}

	p.log.Info("Static assets successfully synced to S3.")
	return nil
}

func (p *AWSProvider) UpdateSecrets(ctx context.Context, appName string, secrets map[string]string) error {
	p.log.Info("Securing secrets in AWS Secrets Manager for app: %s...", appName)

	client := secretsmanager.NewFromConfig(p.cfg)
	secretName := fmt.Sprintf("nextdeploy/apps/%s/production", appName)

	secretString, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("failed to marshal secrets: %w", err)
	}
	strVal := string(secretString)

	// Attempt update first. If the secret doesn't exist yet, create it.
	_, err = client.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
		SecretId:     aws.String(secretName),
		SecretString: aws.String(strVal),
	})

	if err != nil {
		// ResourceNotFoundException = secret doesn't exist yet → create it
		var notFound *smTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			p.log.Info("Secret %s does not exist yet, creating...", secretName)
			_, createErr := client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
				Name:         aws.String(secretName),
				SecretString: aws.String(strVal),
			})
			if createErr != nil {
				return fmt.Errorf("failed to create secret in AWS Secrets Manager: %w", createErr)
			}
		} else {
			// Any other AWS error is a real failure
			return fmt.Errorf("failed to update secret %s: %w", secretName, err)
		}
	}

	p.log.Info("Secrets securely stored: %s", secretName)
	return nil
}

func (p *AWSProvider) DeployCompute(ctx context.Context, tarballPath string, appCfg *cfgTypes.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	p.log.Info("Deploying Compute Layer to AWS Lambda for app: %s...", appCfg.App.Name)

	client := lambda.NewFromConfig(p.cfg)
	// Use explicit LambdaFunctionName if set, otherwise fall back to app name
	functionName := appCfg.App.Name
	if appCfg.Serverless.LambdaFunctionName != "" {
		functionName = appCfg.Serverless.LambdaFunctionName
	}

	// 1. Extract tarball
	tmpDir, err := os.MkdirTemp("", "nd-lambda-deploy-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := shared.ExtractTarGz(tarballPath, tmpDir); err != nil {
		return fmt.Errorf("failed to extract tarball: %w", err)
	}

	standaloneDir := filepath.Join(tmpDir, "standalone")
	if _, err := os.Stat(standaloneDir); os.IsNotExist(err) {
		return fmt.Errorf("standalone directory not found in tarball. Is OutputModeStandalone enabled?")
	}

	// 2. Zip the standalone folder for Lambda
	zipPath := filepath.Join(tmpDir, "lambda.zip")
	if err := shared.CreateZip(standaloneDir, zipPath); err != nil {
		return fmt.Errorf("failed to create zip package: %w", err)
	}

	zipContents, err := os.ReadFile(zipPath)
	if err != nil {
		return fmt.Errorf("failed to read zip package: %w", err)
	}

	if len(zipContents) > 0 {
		_, err := client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: aws.String(functionName),
			ZipFile:      zipContents,
		})
		if err != nil {
			return fmt.Errorf("failed to update Lambda code: %w", err)
		}
	}

	// Update Lambda config to inject secrets securely
	secretArn := fmt.Sprintf("nextdeploy/apps/%s/production", appCfg.App.Name) // In a real scenario, use actual ARN
	_, configErr := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
		Environment: &lambdaTypes.Environment{
			Variables: map[string]string{
				"ND_SECRETS_ARN": secretArn, // App pulls this on startup
			},
		},
	})
	if configErr != nil {
		p.log.Error("Failed to update Lambda configuration (are you sure the function exists?): %v", configErr)
		// We log error but don't fail completely as the function might need initial IaC provisioning
	}
	p.log.Info("Lambda deployment payload simulated successfully.")
	return nil
}

func (p *AWSProvider) InvalidateCache(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	if appCfg.Serverless.CloudFrontId == "" {
		p.log.Info("No CloudFront ID provided, skipping cache invalidation.")
		return nil
	}
	p.log.Info("Invalidating CloudFront Distribution (%s)...", appCfg.Serverless.CloudFrontId)

	client := cloudfront.NewFromConfig(p.cfg)
	callerRef := fmt.Sprintf("nextdeploy-%d", time.Now().UnixNano())

	_, err := client.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(appCfg.Serverless.CloudFrontId),
		InvalidationBatch: &cfTypes.InvalidationBatch{
			CallerReference: aws.String(callerRef),
			Paths: &cfTypes.Paths{
				Quantity: aws.Int32(1),
				Items: []string{
					"/*",
				},
			},
		},
	})

	if err != nil {
		return fmt.Errorf("failed to create invalidation: %w", err)
	}

	p.log.Info("CloudFront invalidation triggered.")
	return nil
}

func (p *AWSProvider) ensureBucketExists(ctx context.Context, client *s3.Client, bucketName, region string) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		return nil // Bucket exists and we have access
	}

	p.log.Info("S3 Bucket %s does not exist, creating in region %s...", bucketName, region)

	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}

	// S3 US-EAST-1 (us-east-1) does not require a LocationConstraint
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3Types.CreateBucketConfiguration{
			LocationConstraint: s3Types.BucketLocationConstraint(region),
		}
	}

	_, err = client.CreateBucket(ctx, createInput)
	if err != nil {
		// Ignore if another user owns the bucket name (global namespace issue)
		// but the SDK error should be clear if that's the case
		return fmt.Errorf("failed to create S3 bucket: %w", err)
	}

	p.log.Info("S3 Bucket %s created successfully.", bucketName)
	return nil
}
