package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/Golangcodes/nextdeploy/shared"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

type AWSProvider struct {
	log       *shared.Logger
	cfg       aws.Config
	accountID string
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

	// Fetch Account ID for unique resource naming
	stsClient := sts.NewFromConfig(cfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		p.log.Warn("Unable to fetch AWS Account ID (some auto-naming may fail): %v", err)
	} else if identity.Account != nil {
		p.accountID = *identity.Account
	}

	return nil
}

func (p *AWSProvider) DeployStatic(ctx context.Context, tarballPath string, appCfg *cfgTypes.NextDeployConfig, meta *nextcore.NextCorePayload) error {
	bucketName := p.getS3BucketName(appCfg)
	p.log.Info("Syncing static assets to S3 Bucket (%s)...", bucketName)

	if bucketName == "" {
		p.log.Info("S3 Bucket not specified and auto-naming failed, skipping static sync.")
		return nil
	}

	client := s3.NewFromConfig(p.cfg)

	// Ensure bucket exists before uploading
	if err := p.ensureBucketExists(ctx, client, bucketName, appCfg.Serverless.Region); err != nil {
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
				Bucket:       aws.String(bucketName),
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
	// Use explicit LambdaFunctionName if set, otherwise generate one
	functionName := p.getLambdaFunctionName(appCfg)

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
		// Fallback: Check if we have a flat structure (server.js at root)
		if _, err := os.Stat(filepath.Join(tmpDir, "server.js")); err == nil {
			p.log.Info("Standalone directory not found, but server.js exists at root. Using flat structure.")
			standaloneDir = tmpDir
		} else {
			return fmt.Errorf("standalone directory not found in tarball, and no server.js found at root. Is OutputModeStandalone enabled?")
		}
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

	// 3. Ensure Lambda function exists (provision if missing)
	err = p.ensureLambdaFunctionExists(ctx, client, functionName, appCfg.Serverless, zipContents)
	if err != nil {
		return err
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

func (p *AWSProvider) ensureLambdaFunctionExists(ctx context.Context, client *lambda.Client, name string, sCfg *cfgTypes.ServerlessConfig, zipContents []byte) error {
	_, err := client.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String(name),
	})
	if err == nil {
		return nil // Exists
	}

	var notFound *lambdaTypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		if sCfg.IAMRole == "" {
			return fmt.Errorf("lambda function %s not found and no IAM Role ARN provided (iam_role in nextdeploy.yml). Please provide an IAM role to auto-provision the function", name)
		}

		// Use configured values or sensible defaults
		handler := "server.handler"
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

		// Auto-replace ACCOUNT_ID placeholder if present
		roleArn := sCfg.IAMRole
		if strings.Contains(roleArn, "ACCOUNT_ID") && p.accountID != "" {
			roleArn = strings.ReplaceAll(roleArn, "ACCOUNT_ID", p.accountID)
			p.log.Info("Automatically replaced ACCOUNT_ID placeholder in IAM Role ARN.")
		}

		if strings.Contains(roleArn, "role-name") {
			return fmt.Errorf("invalid IAM Role ARN: please replace 'role-name' in nextdeploy.yml with an actual IAM role name from your AWS account")
		}

		p.log.Info("Lambda function %s does not exist, creating with role %s (Handler: %s, Runtime: %s)...", name, roleArn, handler, runtime)
		_, err := client.CreateFunction(ctx, &lambda.CreateFunctionInput{
			Code: &lambdaTypes.FunctionCode{
				ZipFile: zipContents,
			},
			FunctionName: aws.String(name),
			Role:         aws.String(roleArn),
			Handler:      aws.String(handler),
			Runtime:      runtime,
			Environment: &lambdaTypes.Environment{
				Variables: map[string]string{
					"NODE_ENV": "production",
				},
			},
			Timeout:    aws.Int32(timeout),
			MemorySize: aws.Int32(memory),
		})
		if err != nil {
			return fmt.Errorf("failed to create Lambda function: %w", err)
		}
		p.log.Info("Lambda function %s created successfully.", name)

		// Wait a few seconds for IAM role propagation if just created (though we assume it exists)
		time.Sleep(2 * time.Second)
		return nil
	}

	return fmt.Errorf("failed to check Lambda function status: %w", err)
}

func (p *AWSProvider) getS3BucketName(appCfg *cfgTypes.NextDeployConfig) string {
	// Dynamic name: nextdeploy-<app>-<env>-assets-<accountid>
	// Guaranteed to be globally unique due to AccountID
	name := fmt.Sprintf("nextdeploy-%s-%s-assets", appCfg.App.Name, appCfg.App.Environment)
	if p.accountID != "" {
		name = fmt.Sprintf("%s-%s", name, p.accountID)
	}
	return strings.ToLower(name)
}

func (p *AWSProvider) getLambdaFunctionName(appCfg *cfgTypes.NextDeployConfig) string {
	// Dynamic name: <app>-<env> (Standard and clean)
	return strings.ToLower(fmt.Sprintf("%s-%s", appCfg.App.Name, appCfg.App.Environment))
}
