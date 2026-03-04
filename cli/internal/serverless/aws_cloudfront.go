package serverless

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
)

// cfDistributionIDPattern is used to validate a real CloudFront distribution ID
// and reject placeholder values from config (e.g. "E1234567890ABC", "YOUR_DIST_ID").
var cfDistributionIDPattern = regexp.MustCompile(`^E[A-Z0-9]{10,14}$`)

func (p *AWSProvider) ensureCloudFrontDistributionExists(ctx context.Context, sCfg *cfgTypes.ServerlessConfig, bucketName, functionUrl string) (string, error) {
	client := cloudfront.NewFromConfig(p.cfg)
	callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))

	needsLambdaOrigin := functionUrl != ""

	// 1. Ensure Origin Access Control (OAC) for S3
	oacId, err := p.ensureS3OACExists(ctx, client)
	if err != nil {
		return "", fmt.Errorf("failed to ensure S3 OAC exists: %w", err)
	}

	// 1.5 Ensure Origin Access Control (OAC) for Lambda
	var lambdaOacId string
	if needsLambdaOrigin {
		lambdaOacId, err = p.ensureLambdaOACExists(ctx, client)
		if err != nil {
			return "", fmt.Errorf("failed to ensure Lambda OAC exists: %w", err)
		}
	}

	p.log.Info("Discovering CloudFront policy IDs...")

	cachingOptimizedId, err := p.getManagedCachePolicyID(ctx, client, "Managed-CachingOptimized")
	if err != nil {
		p.log.Warn("Failed to find Managed-CachingOptimized, using default: %v", err)
		cachingOptimizedId = "658327ea-f89d-4fab-a63d-7e88639e58f6"
	}

	cachingDisabledId, err := p.getManagedCachePolicyID(ctx, client, "Managed-CachingDisabled")
	if err != nil {
		p.log.Warn("Failed to find Managed-CachingDisabled, using default: %v", err)
		cachingDisabledId = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad"
	}

	allViewerPolicyId, err := p.getManagedOriginRequestPolicyID(ctx, client, "Managed-AllViewerExceptHostHeader")
	if err != nil {
		return "", fmt.Errorf("failed to discover Managed-AllViewerExceptHostHeader: %w", err)
	}

	p.log.Info("Checking for existing CloudFront distribution...")
	listOutput, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list distributions: %w", err)
	}

	if listOutput.DistributionList != nil {
		for _, dist := range listOutput.DistributionList.Items {
			if dist.Comment != nil && *dist.Comment == callerRef {
				distID := *dist.Id
				p.log.Info("Existing CloudFront distribution found: %s (%s). Verifying config...", distID, *dist.DomainName)

				getConfig, err := client.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{
					Id: aws.String(distID),
				})
				if err != nil {
					return "", fmt.Errorf("failed to get distribution config: %w", err)
				}

				needsUpdate := false
				distConfig := getConfig.DistributionConfig

				// Ensure it's enabled
				if distConfig.Enabled != nil && !*distConfig.Enabled {
					p.log.Info("Distribution is disabled, re-enabling...")
					distConfig.Enabled = aws.Bool(true)
					needsUpdate = true
				}

				if distConfig.Origins != nil && needsLambdaOrigin {
					lambdaHost := strings.TrimPrefix(functionUrl, "https://")
					lambdaHost = strings.TrimSuffix(lambdaHost, "/")

					for i, origin := range distConfig.Origins.Items {
						if origin.Id != nil && *origin.Id == "LambdaCompute" {
							if origin.DomainName != nil && *origin.DomainName != lambdaHost {
								p.log.Info("Lambda origin URL changed, updating distribution: %s -> %s", *origin.DomainName, lambdaHost)
								distConfig.Origins.Items[i].DomainName = aws.String(lambdaHost)
								needsUpdate = true
							}
							if origin.OriginAccessControlId == nil || *origin.OriginAccessControlId != lambdaOacId {
								p.log.Info("Lambda origin OAC changed, updating distribution.")
								distConfig.Origins.Items[i].OriginAccessControlId = aws.String(lambdaOacId)
								needsUpdate = true
							}
							if origin.CustomOriginConfig != nil {
								p.log.Info("Removing CustomOriginConfig from Lambda origin to support OAC.")
								distConfig.Origins.Items[i].CustomOriginConfig = nil
								needsUpdate = true
							}
							break
						}
					}

					// Ensure the Origin Request Policy doesn't forward the Host header (breaks OAC SigV4)
					if distConfig.DefaultCacheBehavior != nil {
						if distConfig.DefaultCacheBehavior.OriginRequestPolicyId == nil || *distConfig.DefaultCacheBehavior.OriginRequestPolicyId != allViewerPolicyId {
							p.log.Info("Updating CloudFront cache behavior to use Managed-AllViewerExceptHostHeader for OAC compatibility...")
							distConfig.DefaultCacheBehavior.OriginRequestPolicyId = aws.String(allViewerPolicyId)
							needsUpdate = true
						}
					}
				}

				if needsUpdate {
					_, err = client.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
						Id:                 aws.String(distID),
						IfMatch:            getConfig.ETag,
						DistributionConfig: distConfig,
					})
					if err != nil {
						return "", fmt.Errorf("failed to update CloudFront distribution: %w", err)
					}
					p.log.Info("CloudFront distribution configuration updated successfully.")
				} else {
					p.log.Info("CloudFront configuration is already up to date.")
				}

				return distID, nil
			}
		}
	}

	p.log.Info("CloudFront distribution not found, creating one (this may take a few minutes to be fully active)...")

	s3OriginId := "S3Assets"
	lambdaOriginId := "LambdaCompute"

	origins := []cfTypes.Origin{
		{
			Id:         aws.String(s3OriginId),
			DomainName: aws.String(fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, p.cfg.Region)),
			S3OriginConfig: &cfTypes.S3OriginConfig{
				OriginAccessIdentity: aws.String(""), // Using OAC instead
			},
			OriginAccessControlId: aws.String(oacId),
		},
	}

	if needsLambdaOrigin {
		// Strip https:// and trailing slash from function URL for CloudFront origin host
		lambdaHost := strings.TrimPrefix(functionUrl, "https://")
		lambdaHost = strings.TrimSuffix(lambdaHost, "/")

		origins = append(origins, cfTypes.Origin{
			Id:         aws.String(lambdaOriginId),
			DomainName: aws.String(lambdaHost),
			CustomOriginConfig: &cfTypes.CustomOriginConfig{
				HTTPPort:             aws.Int32(80),
				HTTPSPort:            aws.Int32(443),
				OriginProtocolPolicy: cfTypes.OriginProtocolPolicyHttpsOnly,
				OriginSslProtocols: &cfTypes.OriginSslProtocols{
					Quantity: aws.Int32(1),
					Items:    []cfTypes.SslProtocol{cfTypes.SslProtocolTLSv12},
				},
				OriginReadTimeout:      aws.Int32(30),
				OriginKeepaliveTimeout: aws.Int32(5),
			},
			OriginAccessControlId: aws.String(lambdaOacId),
		})
	}

	var defaultOriginRequestPolicy *string
	if needsLambdaOrigin {
		defaultOriginRequestPolicy = aws.String(allViewerPolicyId)
	}

	createInput := &cloudfront.CreateDistributionInput{
		DistributionConfig: &cfTypes.DistributionConfig{
			CallerReference: aws.String(callerRef),
			Comment:         aws.String(callerRef),
			Enabled:         aws.Bool(true),
			Origins: &cfTypes.Origins{
				Quantity: aws.Int32(int32(len(origins))),
				Items:    origins,
			},
			DefaultCacheBehavior: &cfTypes.DefaultCacheBehavior{
				TargetOriginId: aws.String(func() string {
					if needsLambdaOrigin {
						return lambdaOriginId
					}
					return s3OriginId
				}()),
				ViewerProtocolPolicy: cfTypes.ViewerProtocolPolicyRedirectToHttps,
				CachePolicyId: aws.String(func() string {
					if needsLambdaOrigin {
						return cachingDisabledId
					}
					return cachingOptimizedId
				}()),
				OriginRequestPolicyId: defaultOriginRequestPolicy,
				AllowedMethods: &cfTypes.AllowedMethods{
					Quantity: aws.Int32(func() int32 {
						if needsLambdaOrigin {
							return 7
						}
						return 2
					}()),
					Items: func() []cfTypes.Method {
						if needsLambdaOrigin {
							return []cfTypes.Method{
								cfTypes.MethodGet,
								cfTypes.MethodHead,
								cfTypes.MethodOptions,
								cfTypes.MethodPut,
								cfTypes.MethodPatch,
								cfTypes.MethodPost,
								cfTypes.MethodDelete,
							}
						}
						return []cfTypes.Method{cfTypes.MethodGet, cfTypes.MethodHead}
					}(),
				},
			},
			CacheBehaviors: &cfTypes.CacheBehaviors{
				Quantity: aws.Int32(func() int32 {
					if needsLambdaOrigin {
						return 2
					}
					return 0
				}()),
				Items: func() []cfTypes.CacheBehavior {
					if needsLambdaOrigin {
						return []cfTypes.CacheBehavior{
							{
								PathPattern:          aws.String("/_next/*"),
								TargetOriginId:       aws.String(s3OriginId),
								ViewerProtocolPolicy: cfTypes.ViewerProtocolPolicyRedirectToHttps,
								CachePolicyId:        aws.String(cachingOptimizedId),
							},
							{
								PathPattern:          aws.String("/assets/*"),
								TargetOriginId:       aws.String(s3OriginId),
								ViewerProtocolPolicy: cfTypes.ViewerProtocolPolicyRedirectToHttps,
								CachePolicyId:        aws.String(cachingOptimizedId),
							},
						}
					}
					return nil
				}(),
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

// waitForCloudFrontDeployed polls until a distribution reaches the Deployed
// state. This is required before a DeleteDistribution call can succeed.
func (p *AWSProvider) waitForCloudFrontDeployed(ctx context.Context, client *cloudfront.Client, distributionId string) error {
	maxRetries := 60 // CloudFront disable can take several minutes
	for i := 0; i < maxRetries; i++ {
		out, err := client.GetDistribution(ctx, &cloudfront.GetDistributionInput{
			Id: aws.String(distributionId),
		})
		if err != nil {
			return err
		}
		if out.Distribution != nil && out.Distribution.Status != nil && *out.Distribution.Status == "Deployed" {
			return nil
		}
		p.log.Info("CloudFront distribution %s status: %s — waiting...", distributionId, aws.ToString(out.Distribution.Status))
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timed out waiting for CloudFront distribution %s to reach Deployed state", distributionId)
}

func (p *AWSProvider) InvalidateCache(ctx context.Context, appCfg *cfgTypes.NextDeployConfig) error {
	// 1. Prioritize configured CloudFront ID.
	distId := appCfg.Serverless.CloudFrontId
	if !cfDistributionIDPattern.MatchString(distId) {
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

// getManagedCachePolicyID finds a managed CloudFront cache policy by name.
func (p *AWSProvider) getManagedCachePolicyID(ctx context.Context, client *cloudfront.Client, name string) (string, error) {
	var marker *string
	for {
		list, err := client.ListCachePolicies(ctx, &cloudfront.ListCachePoliciesInput{
			Marker: marker,
		})
		if err != nil {
			return "", err
		}
		if list.CachePolicyList != nil {
			for _, item := range list.CachePolicyList.Items {
				if item.CachePolicy != nil && item.CachePolicy.CachePolicyConfig != nil && *item.CachePolicy.CachePolicyConfig.Name == name {
					return *item.CachePolicy.Id, nil
				}
			}
			if list.CachePolicyList.NextMarker == nil || *list.CachePolicyList.NextMarker == "" {
				break
			}
			marker = list.CachePolicyList.NextMarker
		} else {
			break
		}
	}
	return "", fmt.Errorf("managed cache policy %s not found", name)
}

// getManagedOriginRequestPolicyID finds a managed CloudFront origin request
// policy by name.
func (p *AWSProvider) getManagedOriginRequestPolicyID(ctx context.Context, client *cloudfront.Client, name string) (string, error) {
	var marker *string
	for {
		list, err := client.ListOriginRequestPolicies(ctx, &cloudfront.ListOriginRequestPoliciesInput{
			Marker: marker,
		})
		if err != nil {
			return "", err
		}
		if list.OriginRequestPolicyList != nil {
			for _, item := range list.OriginRequestPolicyList.Items {
				if item.OriginRequestPolicy != nil && item.OriginRequestPolicy.OriginRequestPolicyConfig != nil && *item.OriginRequestPolicy.OriginRequestPolicyConfig.Name == name {
					return *item.OriginRequestPolicy.Id, nil
				}
			}
			if list.OriginRequestPolicyList.NextMarker == nil || *list.OriginRequestPolicyList.NextMarker == "" {
				break
			}
			marker = list.OriginRequestPolicyList.NextMarker
		} else {
			break
		}
	}
	return "", fmt.Errorf("managed origin request policy %s not found", name)
}

func (p *AWSProvider) ensureLambdaOACExists(ctx context.Context, client *cloudfront.Client) (string, error) {
	name := "nextdeploy-lambda-oac"
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
			OriginAccessControlOriginType: cfTypes.OriginAccessControlOriginTypesLambda,
			SigningBehavior:               cfTypes.OriginAccessControlSigningBehaviorsAlways,
			SigningProtocol:               cfTypes.OriginAccessControlSigningProtocolsSigv4,
		},
	})
	if err != nil {
		return "", err
	}

	return *createOutput.OriginAccessControl.Id, nil
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
