package serverless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

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

		_, err := p.ensureCloudFrontDistributionExists(ctx, appCfg.Serverless, bucketName, "", domain)
		if err != nil {
			p.log.Warn("Failed to ensure CloudFront Distribution for static site: %v", err)
		}
		return nil
	}

	p.log.Info("Deploying Compute Layer to AWS Lambda for app: %s...", appCfg.App.Name)

	client := lambda.NewFromConfig(p.cfg)
	functionName := p.getLambdaFunctionName(appCfg)

	// Use pre-built zip from packager
	zipPath := pkg.LambdaZipPath
	zipContents, err := os.ReadFile(zipPath)
	if err != nil {
		return fmt.Errorf("failed to read lambda zip package: %w", err)
	}

	// 3a. Save zip to S3 for rollback history (non-fatal)
	bucketForHistory := p.getS3BucketName(appCfg)
	if s3ZipKey, saveErr := p.saveLambdaZipToS3(ctx, bucketForHistory, functionName, zipContents); saveErr != nil {
		p.log.Warn("Could not save deployment zip to S3 history (rollback may not work): %v", saveErr)
	} else {
		p.log.Info("Deployment zip saved for rollback: %s", s3ZipKey)
	}

	// 4. Ensure Lambda function exists (provision if missing).
	functionJustCreated, err := p.ensureLambdaFunctionExists(ctx, client, functionName, appCfg.Serverless, zipContents)
	if err != nil {
		return err
	}

	// 5. Ensure Lambda Function URL exists
	functionUrl, err := p.ensureLambdaFunctionURLExists(ctx, client, functionName)
	if err != nil {
		p.log.Warn("Failed to ensure Lambda Function URL (distribution might fail): %v", err)
	}

	// 6. Ensure CloudFront Distribution exists
	bucketName := p.getS3BucketName(appCfg)
	// Determine domain
	domain := meta.Domain
	if domain == "" {
		domain = appCfg.App.Domain
	}

	p.log.Info("Ensuring CloudFront distribution exists for Lambda origin (Domain: %s)...", domain)
	distributionId, err := p.ensureCloudFrontDistributionExists(ctx, appCfg.Serverless, bucketName, functionUrl, domain)
	if err != nil {
		p.log.Warn("Failed to ensure CloudFront Distribution: %v", err)
	} else {
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

	secretName := fmt.Sprintf("nextdeploy/apps/%s/production", appCfg.App.Name)

	handler := "bridge.handler"
	if appCfg.Serverless != nil && appCfg.Serverless.Handler != "" {
		handler = appCfg.Serverless.Handler
	}

	p.log.Info("Updating Lambda configuration (Handler: %s)...", handler)
	region := p.cfg.Region
	secretsExtensionLayer := fmt.Sprintf("arn:aws:lambda:%s:177933130628:layer:AWS-Parameters-and-Secrets-Lambda-Extension:11", region)

	maxRetries := 5
	layersToApply := []string{secretsExtensionLayer}
	for i := 0; i < maxRetries; i++ {
		_, err := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String(functionName),
			Handler:      aws.String(handler),
			Environment: &lambdaTypes.Environment{
				Variables: map[string]string{
					"ND_SECRET_NAME": secretName,
					"NODE_ENV":       "production",
				},
			},
			Layers: layersToApply,
		})
		if err == nil {
			break
		}

		// Fallback if they lack permissions to grab the AWS-managed Secrets Extension layer
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "AccessDeniedException" && strings.Contains(err.Error(), "lambda:GetLayerVersion") && len(layersToApply) > 0 {
			p.log.Warn("IAM user lacks lambda:GetLayerVersion permissions for the Secrets Extension Layer.")
			p.log.Warn("Falling back to environment variables only (cloud secrets won't be securely injected at runtime).")
			layersToApply = nil // remove the layer and retry immediately
			i--                 // don't count this as a rate-limit retry
			continue
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

// Rollback reverts the Lambda function to the previous deployed zip using the S3 deployment history.
// This is instant — no HTTP download required.
func (p *AWSProvider) Rollback(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	client := lambda.NewFromConfig(p.cfg)
	functionName := p.getLambdaFunctionName(appCfg)
	bucketName := p.getS3BucketName(appCfg)

	p.log.Info("Rolling back Lambda function %s...", functionName)

	// Fetch deployment history from S3 manifest
	history, err := p.getLambdaDeploymentHistory(ctx, bucketName, functionName)
	if err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	if len(history) < 2 {
		return fmt.Errorf("not enough deployment history to rollback (found %d, need at least 2). Ship your app at least once more first", len(history))
	}

	// Current = last, previous = second-to-last
	previousKey := history[len(history)-2]
	p.log.Info("Rolling back to previous deployment: %s", previousKey)

	// Update Lambda code directly from S3 — no download needed
	_, err = client.UpdateFunctionCode(ctx, &lambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(functionName),
		S3Bucket:     aws.String(bucketName),
		S3Key:        aws.String(previousKey),
	})
	if err != nil {
		return fmt.Errorf("failed to restore Lambda code from S3: %w", err)
	}

	p.log.Info("Lambda code restored. Waiting for stabilization...")
	if err := p.waitForLambdaStable(ctx, client, functionName); err != nil {
		p.log.Warn("Timed out waiting for Lambda stability after rollback: %v", err)
	}

	// Publish the rollback as a new version
	_, _ = client.PublishVersion(ctx, &lambda.PublishVersionInput{
		FunctionName: aws.String(functionName),
		Description:  aws.String(fmt.Sprintf("nextdeploy rollback at %s", time.Now().Format(time.RFC3339))),
	})

	// Invalidate CloudFront cache
	if err := p.InvalidateCache(ctx, appCfg); err != nil {
		p.log.Warn("Cache invalidation after rollback failed (non-fatal): %v", err)
	}

	p.log.Info("✅ Rollback complete! Lambda is running the previous deployment.")
	return nil
}

func (p *AWSProvider) ensureLambdaFunctionExists(ctx context.Context, client *lambda.Client, name string, sCfg *cfgTypes.ServerlessConfig, zipContents []byte) (justCreated bool, err error) {
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
	// arn:aws:lambda:<region>:177933130628:layer:AWS-Parameters-and-Secrets-Lambda-Extension:11
	region := p.cfg.Region
	secretsExtensionLayer := fmt.Sprintf("arn:aws:lambda:%s:177933130628:layer:AWS-Parameters-and-Secrets-Lambda-Extension:11", region)

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
				"ND_SECRET_NAME": fmt.Sprintf("nextdeploy/apps/%s/production", strings.Split(name, "-")[0]), // Extract app name
			},
		},
		Timeout:    aws.Int32(timeout),
		MemorySize: aws.Int32(memory),
		Layers:     []string{secretsExtensionLayer},
	}

	maxRetries := 10
	retryDelay := 5 * time.Second
	for i := 0; i < maxRetries; i++ {
		_, createErr := client.CreateFunction(ctx, createInput)
		if createErr == nil {
			p.log.Info("Lambda function %s created successfully.", name)
			return true, nil
		}

		var apiErr smithy.APIError
		if errors.As(createErr, &apiErr) && apiErr.ErrorCode() == "AccessDeniedException" && strings.Contains(createErr.Error(), "lambda:GetLayerVersion") && len(createInput.Layers) > 0 {
			p.log.Warn("IAM user lacks lambda:GetLayerVersion permissions for the Secrets Extension Layer.")
			p.log.Warn("Falling back to environment variables only (cloud secrets won't be securely injected at runtime).")
			createInput.Layers = nil // remove the layer and retry immediately
			i--                      // don't count this as a rate-limit retry
			continue
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

// downloadURL fetches the content from a URL and returns the bytes.
// Used to download Lambda code from a presigned S3 URL during rollback.
func (p *AWSProvider) downloadURL(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}
