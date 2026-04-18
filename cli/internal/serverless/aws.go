package serverless

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smTypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/Golangcodes/nextdeploy/assets"
	"github.com/Golangcodes/nextdeploy/cli/internal/dns"
	"github.com/Golangcodes/nextdeploy/shared"
	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/credstore"
	"github.com/Golangcodes/nextdeploy/shared/sensitive"

	"archive/zip"
	"bytes"
)

type AWSProvider struct {
	log                 *shared.Logger
	cfg                 aws.Config
	accountID           string
	callerArn           string // STS caller ARN, used for audit tagging of secret writes
	environment         string // populated in Initialize from appCfg.App.Environment
	lambdaTimeout       int32  // configured Lambda timeout in seconds, used to size CloudFront OriginReadTimeout
	secretsLayerVersion string // resolved AWS-Parameters-and-Secrets layer version; "" = use default const
	secretsKmsKeyId     string // optional customer-managed KMS key for Secrets Manager; "" = default aws/secretsmanager
	verbose             bool
}

// originReadTimeout returns the CloudFront OriginReadTimeout value to use for
// the Lambda origin. CloudFront caps this at 60 seconds for non-Enterprise
// distributions, so we clamp to that ceiling. See C19 in REVIEW.md.
func (p *AWSProvider) originReadTimeout() int32 {
	t := p.lambdaTimeout
	if t <= 0 {
		t = 30
	}
	if t > 60 {
		t = 60
	}
	return t
}

// secretName returns the canonical AWS Secrets Manager name for an app.
// It is environment-scoped so that staging and production never share secrets.
// Falls back to "production" only when Environment is unset (legacy configs).
func (p *AWSProvider) secretName(appName string) string {
	env := p.environment
	if env == "" {
		env = "production"
	}
	return fmt.Sprintf("nextdeploy/apps/%s/%s", appName, env)
}

func NewAWSProvider(verbose bool) *AWSProvider {
	return &AWSProvider{
		log:     shared.PackageLogger("aws_serverless", "☁️  AWS::"),
		verbose: verbose,
	}
}

// verboseLog logs a message only when --verbose is enabled.
func (p *AWSProvider) verboseLog(msg string, args ...interface{}) {
	if p.verbose {
		p.log.Info(msg, args...)
	}
}

func getEmbeddedLambdaZip(name string) ([]byte, error) {
	binPath := fmt.Sprintf("lambda/%s/bootstrap", name)
	content, err := assets.LambdaBinaries.ReadFile(binPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded lambda %s: %w", name, err)
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	header := &zip.FileHeader{
		Name:   "bootstrap",
		Method: zip.Deflate,
	}
	header.SetMode(0755)

	f, err := w.CreateHeader(header)
	if err != nil {
		return nil, err
	}

	_, err = f.Write(content)
	if err != nil {
		return nil, err
	}

	err = w.Close()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// awsStaticCreds is what loadAWSStaticCreds returns; empty accessKey means
// "fall through to SDK default chain (env vars / profile / IAM role)".
type awsStaticCreds struct {
	accessKey    string
	secretKey    string
	sessionToken string
	source       string // human-readable, used in logs
}

// loadAWSStaticCreds resolves explicit static credentials in this order:
//  1. credstore (encrypted at rest, mode 0600)
//  2. nextdeploy.yml CloudProvider.AccessKey/SecretKey (LEGACY — emits WARN)
//
// Env vars and AWS profiles are NOT handled here — the AWS SDK's default
// credential chain picks those up automatically when this returns empty.
func loadAWSStaticCreds(cfg *cfgTypes.NextDeployConfig, log *shared.Logger) awsStaticCreds {
	if stored, err := credstore.Load("aws"); err == nil {
		ak := stored["access_key_id"]
		sk := stored["secret_access_key"]
		if ak != "" && sk != "" {
			return awsStaticCreds{
				accessKey:    ak,
				secretKey:    sk,
				sessionToken: stored["session_token"],
				source:       "credstore",
			}
		}
	}
	if cfg.CloudProvider != nil && cfg.CloudProvider.AccessKey != "" && cfg.CloudProvider.SecretKey != "" {
		log.Warn("⚠️  AWS access key loaded from nextdeploy.yml — committing this file leaks creds.")
		log.Warn("⚠️  Recommended: 'nextdeploy creds set --provider aws' (encrypted, mode 0600).")
		return awsStaticCreds{
			accessKey: cfg.CloudProvider.AccessKey,
			secretKey: cfg.CloudProvider.SecretKey,
			source:    "nextdeploy.yml",
		}
	}
	return awsStaticCreds{}
}

func (p *AWSProvider) Initialize(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	p.log.Info("Initializing AWS Serverless Deployment session...")

	p.environment = appCfg.App.Environment
	if appCfg.Serverless != nil {
		p.lambdaTimeout = appCfg.Serverless.Timeout
		p.secretsKmsKeyId = appCfg.Serverless.KmsKeyId
	}

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

	// Explicit credentials precedence: credstore > yaml > SDK default chain
	// (which itself covers env vars, profile, and IAM role).
	staticCreds := loadAWSStaticCreds(appCfg, p.log)
	if staticCreds.accessKey != "" && staticCreds.secretKey != "" {
		sensitive.Register(staticCreds.accessKey, staticCreds.secretKey, staticCreds.sessionToken)
		p.log.Info("Using explicit credentials from %s.", staticCreds.source)
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				staticCreds.accessKey,
				staticCreds.secretKey,
				staticCreds.sessionToken,
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
	} else {
		if identity.Account != nil {
			p.accountID = *identity.Account
		}
		if identity.Arn != nil {
			p.callerArn = *identity.Arn
		}
	}

	// 4. Check Service Quotas (CloudFront distributions limit)
	if err := p.CheckServiceQuotas(ctx); err != nil {
		p.log.Warn("Quota check: %v", err)
	}

	// 5. Probe lambda:GetLayerVersion *before* any deploy-time mutation so a
	// missing-permission config fails loudly upfront instead of mid-way through
	// an UpdateFunctionConfiguration retry loop after we've already uploaded to
	// S3 and touched IAM. See S5 in REVIEW.md.
	if err := p.probeSecretsExtensionLayer(ctx, appCfg); err != nil {
		return err
	}

	return nil
}

func (p *AWSProvider) Destroy(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	p.log.Info("Destroying AWS Serverless resources for app: %s...", appCfg.App.Name)

	functionName := p.getLambdaFunctionName(appCfg)
	bucketName := p.getS3BucketName(appCfg)
	secretName := p.secretName(appCfg.App.Name)

	// 1. CloudFront Distribution (Paginated discovery by comment or CNAME)
	clientCF := cloudfront.NewFromConfig(p.cfg)
	callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))
	domain := appCfg.App.Domain

	dists, err := p.findManagedDistributions(ctx, clientCF, callerRef, domain)
	if err != nil {
		p.log.Warn("Failed to list CloudFront distributions: %v", err)
	}

	for _, distId := range dists {
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
	}

	// 2. Lambda Function
	p.log.Info("Deleting Lambda Function: %s...", functionName)
	clientLambda := lambda.NewFromConfig(p.cfg)
	_, err = clientLambda.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
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

	p.log.Info("AWS Serverless resources destruction initiated.")
	p.log.Info("Note: IAM role 'nextdeploy-serverless-role' was preserved as it may be used by other apps.")
	return nil
}

func (p *AWSProvider) GetResourceMap(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) (ServerlessResourceMap, error) {
	functionName := p.getLambdaFunctionName(appCfg)
	bucketName := p.getS3BucketName(appCfg)

	res := ServerlessResourceMap{
		AppName:        appCfg.App.Name,
		Environment:    "production",
		Region:         p.cfg.Region,
		S3BucketName:   bucketName,
		DeploymentTime: time.Now(),
	}

	// 1. Lambda Info
	clientLambda := lambda.NewFromConfig(p.cfg)
	fn, err := clientLambda.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(functionName)})
	if err == nil && fn.Configuration != nil {
		res.LambdaARN = *fn.Configuration.FunctionArn
	}

	fUrl, err := clientLambda.GetFunctionUrlConfig(ctx, &lambda.GetFunctionUrlConfigInput{FunctionName: aws.String(functionName)})
	if err == nil {
		res.FunctionURL = *fUrl.FunctionUrl
	}

	// 2. CloudFront Info
	clientCF := cloudfront.NewFromConfig(p.cfg)
	callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))
	cfDists, _ := p.findManagedDistributions(ctx, clientCF, callerRef, appCfg.App.Domain)
	if len(cfDists) > 0 {
		distID := cfDists[0]
		res.CloudFrontID = distID
		// Get domain name for report
		getDist, _ := clientCF.GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(distID)})
		if getDist != nil && getDist.Distribution != nil {
			res.CloudFrontDomain = *getDist.Distribution.DomainName
		}
	}

	// 3. Custom Domain & cert
	res.CustomDomain = appCfg.App.Domain
	if appCfg.SSLConfig != nil {
		res.DNSProvider = appCfg.SSLConfig.DNSProvider
	} else if appCfg.SSL != nil {
		res.DNSProvider = appCfg.SSL.DNSProvider
	}

	if res.CustomDomain != "" {
		// ACM certs for CloudFront must be in us-east-1
		acmCfg, acmErr := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
		if acmErr == nil {
			acmClient := acm.NewFromConfig(acmCfg)
			certARN, _ := p.findExistingCertificate(ctx, acmClient, res.CustomDomain)
			if certARN != "" {
				res.CertificateARN = certARN
				// Fetch validation records
				desc, descErr := acmClient.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
					CertificateArn: aws.String(certARN),
				})
				if descErr == nil && desc.Certificate != nil {
					res.CertificateStatus = string(desc.Certificate.Status)
					for _, dvo := range desc.Certificate.DomainValidationOptions {
						if dvo.ResourceRecord != nil {
							res.ValidationRecords = append(res.ValidationRecords, dns.ValidationRecord{
								Name:  *dvo.ResourceRecord.Name,
								Value: *dvo.ResourceRecord.Value,
							})
						}
					}
				}
			}
		}
	}

	return res, nil
}

// findManagedDistributions finds CloudFront distributions by matching comment or domain aliases.
func (p *AWSProvider) findManagedDistributions(ctx context.Context, client *cloudfront.Client, callerRef, domain string) ([]string, error) {
	var distIDs []string
	var marker *string

	for {
		listOutput, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{
			Marker: marker,
		})
		if err != nil {
			return nil, err
		}

		if listOutput.DistributionList != nil {
			for _, dist := range listOutput.DistributionList.Items {
				matched := false
				// Match by comment
				if dist.Comment != nil && *dist.Comment == callerRef {
					matched = true
				}
				// Match by domain alias (CNAME conflict prevention)
				if !matched && domain != "" && dist.Aliases != nil {
					for _, alias := range dist.Aliases.Items {
						if alias == domain {
							matched = true
							p.log.Warn("Found distribution %s matching domain alias %s", *dist.Id, domain)
							break
						}
					}
				}

				if matched {
					distIDs = append(distIDs, *dist.Id)
				}
			}
		}

		if listOutput.DistributionList == nil || listOutput.DistributionList.NextMarker == nil || *listOutput.DistributionList.NextMarker == "" {
			break
		}
		marker = listOutput.DistributionList.NextMarker
	}

	return distIDs, nil
}

func (p *AWSProvider) CheckServiceQuotas(ctx context.Context) error {
	p.log.Info("Checking AWS service quotas (CloudFront distribution limit)...")
	client := cloudfront.NewFromConfig(p.cfg)

	listRes, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	if err != nil {
		return fmt.Errorf("failed to list distributions for quota check: %w", err)
	}

	currentCount := int32(0)
	if listRes.DistributionList != nil {
		currentCount = aws.ToInt32(listRes.DistributionList.Quantity)
	}

	limit := int32(200) // Default CloudFront distribution limit

	if currentCount >= limit {
		p.log.Warn("⚠️ CloudFront distribution limit reached (%d/%d used). Deploys may fail.", currentCount, limit)
		return fmt.Errorf("CloudFront distribution limit reached (%d/%d)", currentCount, limit)
	}

	p.log.Info("CloudFront quota check passed (%d/%d used).", currentCount, limit)
	return nil
}
