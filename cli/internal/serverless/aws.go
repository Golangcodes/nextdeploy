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
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
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

	// 4. Ensure Lambda Function URL exists
	functionUrl, err := p.ensureLambdaFunctionURLExists(ctx, client, functionName)
	if err != nil {
		p.log.Warn("Failed to ensure Lambda Function URL (distribution might fail): %v", err)
	}

	// 5. Ensure CloudFront Distribution exists
	bucketName := p.getS3BucketName(appCfg)
	distributionId, err := p.ensureCloudFrontDistributionExists(ctx, appCfg.Serverless, bucketName, functionUrl)
	if err != nil {
		p.log.Warn("Failed to ensure CloudFront Distribution: %v", err)
	} else {
		// 6. Update S3 Bucket Policy for OAC
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

	if len(zipContents) > 0 {
		_, err := client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
			FunctionName: aws.String(functionName),
			ZipFile:      zipContents,
		})
		if err != nil {
			return fmt.Errorf("failed to update Lambda code: %w", err)
		}
		p.log.Info("Lambda code updated successfully. Waiting for update to stabilize...")

		// Wait for the function to be active and last update to be successful
		if err := p.waitForLambdaStable(ctx, client, functionName); err != nil {
			p.log.Warn("Timed out waiting for Lambda stability: %v", err)
		}
	}

	// Update Lambda config to inject secrets securely
	secretArn := fmt.Sprintf("nextdeploy/apps/%s/production", appCfg.App.Name)
	_, configErr := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
		Environment: &lambdaTypes.Environment{
			Variables: map[string]string{
				"ND_SECRETS_ARN": secretArn,
			},
		},
	})
	if configErr != nil {
		p.log.Error("Failed to update Lambda configuration: %v", configErr)
	}
	return nil
}

func (p *AWSProvider) InvalidateCache(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	// 1. Prioritize configured CloudFront ID
	distId := appCfg.Serverless.CloudFrontId

	// Explicitly ignore placeholder
	if distId == "E1234567890ABC" {
		distId = ""
	}
	if distId == "" {
		// 2. Fallback to discovering the managed distribution
		bucketName := p.getS3BucketName(appCfg)
		callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))

		client := cloudfront.NewFromConfig(p.cfg)
		listOutput, _ := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
		if listOutput != nil && listOutput.DistributionList != nil {
			for _, dist := range listOutput.DistributionList.Items {
				if dist.Comment != nil && *dist.Comment == callerRef {
					distId = *dist.Id
					break
				}
			}
		}
	}

	if distId == "" {
		p.log.Info("No CloudFront distribution found to invalidate.")
		return nil
	}

	return p.invalidateCloudFront(ctx, distId)
}

func (p *AWSProvider) invalidateCloudFront(ctx context.Context, distributionId string) error {
	p.log.Info("Invalidating CloudFront Distribution (%s)...", distributionId)

	client := cloudfront.NewFromConfig(p.cfg)
	callerRef := fmt.Sprintf("nextdeploy-%d", time.Now().UnixNano())

	_, err := client.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(distributionId),
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

	p.log.Info("CloudFront invalidation triggered successfully.")
	return nil
}

func (p *AWSProvider) ensureExecutionRoleExists(ctx context.Context) (string, error) {
	client := iam.NewFromConfig(p.cfg)
	roleName := "nextdeploy-serverless-role"

	p.log.Info("Checking for IAM execution role: %s...", roleName)
	getOutput, err := client.GetRole(ctx, &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	})

	if err == nil {
		p.log.Info("IAM execution role found: %s", *getOutput.Role.Arn)
		return *getOutput.Role.Arn, nil
	}

	var noSuchEntity *types.NoSuchEntityException
	if !errors.As(err, &noSuchEntity) {
		return "", fmt.Errorf("failed to check for IAM role: %w", err)
	}

	p.log.Info("IAM role %s not found, creating one...", roleName)

	trustPolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Principal": map[string]interface{}{
					"Service": "lambda.amazonaws.com",
				},
				"Action": "sts:AssumeRole",
			},
		},
	}
	policyJSON, _ := json.Marshal(trustPolicy)

	createOutput, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(string(policyJSON)),
		Description:              aws.String("Managed by NextDeploy for Serverless Lambda execution"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create IAM role: %w", err)
	}

	p.log.Info("Attaching managed policies to role %s...", roleName)
	managedPolicies := []string{
		"arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole",
		"arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
		"arn:aws:iam::aws:policy/SecretsManagerReadWrite",
	}

	for _, policyArn := range managedPolicies {
		_, err = client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyArn),
		})
		if err != nil {
			p.log.Warn("Failed to attach policy %s: %v", policyArn, err)
		}
	}

	p.log.Info("IAM role created successfully: %s", *createOutput.Role.Arn)
	// Give AWS a moment to propagate the new role
	time.Sleep(5 * time.Second)
	return *createOutput.Role.Arn, nil
}

func (p *AWSProvider) ensureLambdaFunctionURLExists(ctx context.Context, client *lambda.Client, functionName string) (string, error) {
	p.log.Info("Ensuring Lambda Function URL exists for %s...", functionName)

	// 1. Check if it already exists
	getOutput, err := client.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(functionName),
	})

	if err == nil {
		p.log.Info("Lambda Function URL found: %s", *getOutput.FunctionUrl)
		return *getOutput.FunctionUrl, nil
	}

	var notFound *lambdaTypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return "", fmt.Errorf("failed to check for Function URL: %w", err)
	}

	// 2. Create it
	p.log.Info("Creating new Function URL for %s...", functionName)
	createOutput, err := client.CreateFunctionUrlConfig(ctx, &lambda.CreateFunctionUrlConfigInput{
		FunctionName: aws.String(functionName),
		AuthType:     lambdaTypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create Function URL: %w", err)
	}

	// 3. Add permission for public (NONE) access
	_, err = client.AddPermission(ctx, &lambda.AddPermissionInput{
		FunctionName:        aws.String(functionName),
		StatementId:         aws.String("AllowPublicFunctionUrl"),
		Action:              aws.String("lambda:InvokeFunctionUrl"),
		Principal:           aws.String("*"),
		FunctionUrlAuthType: lambdaTypes.FunctionUrlAuthTypeNone,
	})
	if err != nil {
		// Ignore if permission already exists
		if !strings.Contains(err.Error(), "already exists") {
			p.log.Warn("Failed to add public permission to Function URL: %v", err)
		}
	}

	p.log.Info("Lambda Function URL created: %s", *createOutput.FunctionUrl)
	return *createOutput.FunctionUrl, nil
}

func (p *AWSProvider) ensureCloudFrontDistributionExists(ctx context.Context, sCfg *cfgTypes.ServerlessConfig, bucketName, functionUrl string) (string, error) {
	client := cloudfront.NewFromConfig(p.cfg)
	// CloudFront is globally unique for its caller reference, but we want one per app environment
	callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))

	p.log.Info("Checking for existing CloudFront distribution...")
	listOutput, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list distributions: %w", err)
	}

	if listOutput.DistributionList != nil {
		for _, dist := range listOutput.DistributionList.Items {
			if dist.Comment != nil && *dist.Comment == callerRef {
				p.log.Info("Existing CloudFront distribution found: %s", *dist.DomainName)
				return *dist.Id, nil
			}
		}
	}

	p.log.Info("CloudFront distribution not found, creating one (this may take a few minutes to be fully active)...")

	// 1. Ensure Origin Access Control (OAC) for S3
	oacId, err := p.ensureS3OACExists(ctx, client)
	if err != nil {
		return "", fmt.Errorf("failed to ensure S3 OAC exists: %w", err)
	}

	// 2. Define Origins
	s3OriginId := "S3Assets"
	lambdaOriginId := "LambdaCompute"

	// Strip https:// and trailing slash from function URL for CloudFront origin host
	lambdaHost := strings.TrimPrefix(functionUrl, "https://")
	lambdaHost = strings.TrimSuffix(lambdaHost, "/")

	createInput := &cloudfront.CreateDistributionInput{
		DistributionConfig: &cfTypes.DistributionConfig{
			CallerReference: aws.String(callerRef),
			Comment:         aws.String(callerRef),
			Enabled:         aws.Bool(true),
			Origins: &cfTypes.Origins{
				Quantity: aws.Int32(2),
				Items: []cfTypes.Origin{
					{
						Id:         aws.String(s3OriginId),
						DomainName: aws.String(fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, p.cfg.Region)),
						S3OriginConfig: &cfTypes.S3OriginConfig{
							OriginAccessIdentity: aws.String(""), // Using OAC instead
						},
						OriginAccessControlId: aws.String(oacId),
					},
					{
						Id:         aws.String(lambdaOriginId),
						DomainName: aws.String(lambdaHost),
						CustomOriginConfig: &cfTypes.CustomOriginConfig{
							HTTPPort:             aws.Int32(80),
							HTTPSPort:            aws.Int32(443),
							OriginProtocolPolicy: cfTypes.OriginProtocolPolicyHttpsOnly,
							OriginSslProtocols: &cfTypes.OriginSslProtocols{
								Quantity: aws.Int32(1),
								Items: []cfTypes.SslProtocol{
									cfTypes.SslProtocolTLSv12,
								},
							},
						},
					},
				},
			},
			DefaultCacheBehavior: &cfTypes.DefaultCacheBehavior{
				TargetOriginId:        aws.String(lambdaOriginId),
				ViewerProtocolPolicy:  cfTypes.ViewerProtocolPolicyRedirectToHttps,
				CachePolicyId:         aws.String("4135ea2d-6df8-44a3-9df3-4b5a84be39ad"), // Managed-CachingDisabled (Lambda usually manages its own caching or needs fresh responses)
				OriginRequestPolicyId: aws.String("216adef6-5c7d-47e4-b989-5492810d03b2"), // Managed-AllViewer
				AllowedMethods: &cfTypes.AllowedMethods{
					Quantity: aws.Int32(7),
					Items: []cfTypes.Method{
						cfTypes.MethodGet,
						cfTypes.MethodHead,
						cfTypes.MethodOptions,
						cfTypes.MethodPut,
						cfTypes.MethodPatch,
						cfTypes.MethodPost,
						cfTypes.MethodDelete,
					},
				},
			},
			CacheBehaviors: &cfTypes.CacheBehaviors{
				Quantity: aws.Int32(2),
				Items: []cfTypes.CacheBehavior{
					{
						PathPattern:          aws.String("/_next/*"),
						TargetOriginId:       aws.String(s3OriginId),
						ViewerProtocolPolicy: cfTypes.ViewerProtocolPolicyRedirectToHttps,
						CachePolicyId:        aws.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // Managed-CachingOptimized
					},
					{
						PathPattern:          aws.String("/assets/*"),
						TargetOriginId:       aws.String(s3OriginId),
						ViewerProtocolPolicy: cfTypes.ViewerProtocolPolicyRedirectToHttps,
						CachePolicyId:        aws.String("658327ea-f89d-4fab-a63d-7e88639e58f6"), // Managed-CachingOptimized
					},
				},
			},
		},
	}

	createOutput, err := client.CreateDistribution(ctx, createInput)
	if err != nil {
		return "", fmt.Errorf("failed to create CloudFront distribution: %w", err)
	}

	p.log.Info("CloudFront distribution created: %s (%s)", *createOutput.Distribution.Id, *createOutput.Distribution.DomainName)
	return *createOutput.Distribution.Id, nil
}

func (p *AWSProvider) ensureS3OACExists(ctx context.Context, client *cloudfront.Client) (string, error) {
	name := "nextdeploy-s3-oac"
	listOutput, err := client.ListOriginAccessControls(ctx, &cloudfront.ListOriginAccessControlsInput{})
	if err != nil {
		return "", err
	}

	if listOutput.OriginAccessControlList != nil {
		for _, oac := range listOutput.OriginAccessControlList.Items {
			if oac.Name != nil && *oac.Name == name {
				return *oac.Id, nil
			}
		}
	}

	createOutput, err := client.CreateOriginAccessControl(ctx, &cloudfront.CreateOriginAccessControlInput{
		OriginAccessControlConfig: &cfTypes.OriginAccessControlConfig{
			Name:                          aws.String(name),
			OriginAccessControlOriginType: cfTypes.OriginAccessControlOriginTypesS3,
			SigningBehavior:               cfTypes.OriginAccessControlSigningBehaviorsAlways,
			SigningProtocol:               cfTypes.OriginAccessControlSigningProtocolsSigv4,
		},
	})
	if err != nil {
		return "", err
	}

	return *createOutput.OriginAccessControl.Id, nil
}

func (p *AWSProvider) updateS3BucketPolicyForOAC(ctx context.Context, bucketName, distributionId string) error {
	client := s3.NewFromConfig(p.cfg)

	policy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Sid":    "AllowCloudFrontServicePrincipal",
				"Effect": "Allow",
				"Principal": map[string]interface{}{
					"Service": "cloudfront.amazonaws.com",
				},
				"Action":   "s3:GetObject",
				"Resource": fmt.Sprintf("arn:aws:s3:::%s/*", bucketName),
				"Condition": map[string]interface{}{
					"StringEquals": map[string]interface{}{
						"AWS:SourceArn": fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", p.accountID, distributionId),
					},
				},
			},
		},
	}

	policyJSON, _ := json.Marshal(policy)

	_, err := client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucketName),
		Policy: aws.String(string(policyJSON)),
	})
	if err != nil {
		return fmt.Errorf("failed to update S3 bucket policy for OAC: %w", err)
	}

	p.log.Info("S3 Bucket Policy updated to allow CloudFront OAC access.")
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

		// Determine IAM Role (Manual vs Auto-Provisioned)
		var roleArn string
		if sCfg.IAMRole != "" && !strings.Contains(sCfg.IAMRole, "role-name") {
			roleArn = sCfg.IAMRole
			// Auto-replace ACCOUNT_ID placeholder if present
			if strings.Contains(roleArn, "ACCOUNT_ID") && p.accountID != "" {
				roleArn = strings.ReplaceAll(roleArn, "ACCOUNT_ID", p.accountID)
				p.log.Info("Automatically replaced ACCOUNT_ID placeholder in IAM Role ARN.")
			}
		} else {
			// Auto-provision or discover the dedicated NextDeploy role
			p.log.Info("No valid IAM Role provided, attempting to use/create managed 'nextdeploy-serverless-role'...")
			var err error
			roleArn, err = p.ensureExecutionRoleExists(ctx)
			if err != nil {
				return fmt.Errorf("failed to ensure IAM execution role exists: %w", err)
			}
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
		p.log.Info("Lambda update status: %s", status)

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
