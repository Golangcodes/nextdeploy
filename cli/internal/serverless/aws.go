package serverless

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smTypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/Golangcodes/nextdeploy/shared"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
)

const bridgeJS = `const http = require('http');
const path = require('path');
const { spawn } = require('child_process');

let serverReady = false;
let serverPort = 3000;

// Path to the actual Next.js server.js
const serverPath = path.join(__dirname, 'server.js');

const waitForServer = async () => {
    for (let i = 0; i < 50; i++) {
        try {
            await new Promise((resolve, reject) => {
                const req = http.get({
                    hostname: '127.0.0.1',
                    port: serverPort,
                    path: '/',
                    timeout: 500,
                }, (res) => resolve(true));
                req.on('error', reject);
                req.end();
            });
            console.log('Next.js server is ready');
            return true;
        } catch (e) {
            await new Promise(r => setTimeout(r, 100));
        }
    }
    throw new Error('Server timed out waiting for localhost:' + serverPort);
};

// Start the server in the background
console.log('Starting Next.js server: node ' + serverPath);
const serverProcess = spawn('node', [serverPath], {
    env: { ...process.env, PORT: serverPort, HOSTNAME: '127.0.0.1', NODE_ENV: 'production' },
    stdio: 'inherit'
});

// FIX: Reset serverReady if the child process exits unexpectedly so future
// invocations re-probe instead of proxying to a dead server.
serverProcess.on('exit', (code) => {
    console.error('Next.js server exited with code ' + code + ', resetting ready state.');
    serverReady = false;
});

exports.handler = async (event) => {
    if (!serverReady) {
        await waitForServer();
        serverReady = true;
    }

    return new Promise((resolve, reject) => {
        // Handle both API Gateway v1 and v2 formats
        const method = (event.requestContext && event.requestContext.http) ? event.requestContext.http.method : event.httpMethod;
        const rawPath = event.rawPath || event.path || '/';
        const queryString = event.rawQueryString || '';
        
        const options = {
            hostname: '127.0.0.1',
            port: serverPort,
            path: rawPath + (queryString ? '?' + queryString : ''),
            method: method,
            headers: event.headers || {},
        };

        const req = http.request(options, (res) => {
            const chunks = [];
            res.on('data', (chunk) => chunks.push(chunk));
            res.on('end', () => {
                const body = Buffer.concat(chunks);
                resolve({
                    statusCode: res.statusCode,
                    headers: res.headers,
                    body: body.toString('base64'),
                    isBase64Encoded: true
                });
            });
        });

        if (event.body) {
            req.write(event.isBase64Encoded ? Buffer.from(event.body, 'base64') : event.body);
        }
        req.on('error', (err) => {
            console.error('Proxy error:', err);
            reject(err);
        });
        req.end();
    });
};`

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

func (p *AWSProvider) Destroy(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	p.log.Info("Destroying AWS Serverless resources for app: %s...", appCfg.App.Name)

	functionName := p.getLambdaFunctionName(appCfg)
	bucketName := p.getS3BucketName(appCfg)
	secretName := fmt.Sprintf("nextdeploy/apps/%s/production", appCfg.App.Name)

	clientCF := cloudfront.NewFromConfig(p.cfg)
	listOutput, _ := clientCF.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	if listOutput != nil && listOutput.DistributionList != nil {
		callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))
		for _, dist := range listOutput.DistributionList.Items {
			if dist.Comment != nil && *dist.Comment == callerRef {
				distId := *dist.Id
				p.log.Info("Found CloudFront Distribution to destroy: %s", distId)

				getDist, err := clientCF.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{Id: aws.String(distId)})
				if err != nil {
					p.log.Warn("Failed to fetch CloudFront distribution config (non-fatal): %v", err)
					break
				}

				etag := getDist.ETag

				// Disable if still enabled
				if getDist.DistributionConfig.Enabled != nil && *getDist.DistributionConfig.Enabled {
					p.log.Info("Disabling CloudFront Distribution: %s...", distId)
					getDist.DistributionConfig.Enabled = aws.Bool(false)
					updateOut, err := clientCF.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
						Id:                 aws.String(distId),
						IfMatch:            etag,
						DistributionConfig: getDist.DistributionConfig,
					})
					if err != nil {
						p.log.Warn("Failed to disable CloudFront distribution (non-fatal): %v", err)
						break
					}
					etag = updateOut.ETag
					p.log.Info("Waiting for CloudFront distribution %s to reach Deployed state before deletion...", distId)
					if waitErr := p.waitForCloudFrontDeployed(ctx, clientCF, distId); waitErr != nil {
						p.log.Warn("CloudFront distribution did not reach Deployed state in time, skipping deletion: %v", waitErr)
						break
					}
				}

				p.log.Info("Deleting CloudFront Distribution: %s...", distId)
				_, err = clientCF.DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{
					Id:      aws.String(distId),
					IfMatch: etag,
				})
				if err != nil {
					p.log.Warn("Failed to delete CloudFront distribution (non-fatal): %v", err)
				} else {
					p.log.Info("CloudFront distribution %s deleted.", distId)
				}
				break
			}
		}
	}

	// 2. Lambda Function
	p.log.Info("Deleting Lambda Function: %s...", functionName)
	clientLambda := lambda.NewFromConfig(p.cfg)
	_, err := clientLambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		var notFound *lambdaTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			p.log.Info("Lambda function %s not found.", functionName)
		} else {
			p.log.Warn("Failed to delete Lambda function: %v", err)
		}
	}

	// 3. S3 Bucket (Empty and Delete)
	p.log.Info("Emptying and deleting S3 Bucket: %s...", bucketName)
	clientS3 := s3.NewFromConfig(p.cfg)
	if err := p.emptyS3Bucket(ctx, clientS3, bucketName); err != nil {
		p.log.Warn("Failed to empty S3 bucket: %v", err)
	} else {
		_, err = clientS3.DeleteBucket(ctx, &s3.DeleteBucketInput{
			Bucket: aws.String(bucketName),
		})
		if err != nil {
			var notFound *s3Types.NoSuchBucket
			if errors.As(err, &notFound) {
				p.log.Info("S3 bucket %s not found.", bucketName)
			} else {
				p.log.Warn("Failed to delete S3 bucket: %v", err)
			}
		}
	}

	// 4. Secrets Manager
	p.log.Info("Deleting Secret: %s...", secretName)
	clientSM := secretsmanager.NewFromConfig(p.cfg)
	_, err = clientSM.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId:                   aws.String(secretName),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	})
	if err != nil {
		var notFound *smTypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			p.log.Info("Secret %s not found.", secretName)
		} else {
			p.log.Warn("Failed to delete secret: %v", err)
		}
	}

	p.log.Info("✅ AWS Serverless resources destruction initiated.")
	p.log.Info("Note: IAM role 'nextdeploy-serverless-role' was preserved as it may be used by other apps.")
	return nil
}
