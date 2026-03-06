package serverless

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cfTypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	cfgTypes "github.com/Golangcodes/nextdeploy/shared/config"
)

var cfDistributionIDPattern = regexp.MustCompile(`^E[A-Z0-9]{10,14}$`)

func isPlaceholderDistributionID(id string) bool {
	return id == "E1234567890ABC" || !cfDistributionIDPattern.MatchString(id)
}

func (p *AWSProvider) ensureCloudFrontDistributionExists(ctx context.Context, sCfg *cfgTypes.ServerlessConfig, bucketName, functionUrl, domain string) (string, error) {
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

	// Handle custom domain cert
	var certARN string
	certIssued := false
	if domain != "" {
		certARN, err = p.ensureACMCertificateExists(ctx, domain)
		if err != nil {
			p.log.Warn("Failed to ensure ACM certificate for domain %s: %v", domain, err)
		}
		certIssued = certARN != "" && p.isCertificateIssued(ctx, certARN)
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
	var existingDistID string
	dists, err := p.findManagedDistributions(ctx, client, callerRef, domain)
	if err != nil {
		return "", fmt.Errorf("failed to discover distributions: %w", err)
	}

	if len(dists) > 0 {
		existingDistID = dists[0]
	}

	if existingDistID != "" {
		p.log.Info("Existing CloudFront distribution found: %s. Verifying config...", existingDistID)

		getConfig, err := client.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{
			Id: aws.String(existingDistID),
		})
		if err != nil {
			return "", fmt.Errorf("failed to get distribution config: %w", err)
		}

		distConfig := getConfig.DistributionConfig
		needsUpdate := p.applyDistributionConfig(ctx, distConfig, callerRef, domain, certARN, oacId, lambdaOacId, cachingOptimizedId, cachingDisabledId, allViewerPolicyId, bucketName, functionUrl)

		if needsUpdate {
			p.log.Info("CloudFront configuration update required, applying changes...")
			_, err = client.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
				Id:                 aws.String(existingDistID),
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

		return existingDistID, nil
	}

	p.log.Info("CloudFront distribution not found, creating one (this may take a few minutes to be fully active)...")

	distConfig := &cfTypes.DistributionConfig{}
	p.applyDistributionConfig(ctx, distConfig, callerRef, domain, certARN, oacId, lambdaOacId, cachingOptimizedId, cachingDisabledId, allViewerPolicyId, bucketName, functionUrl)

	createOutput, err := client.CreateDistribution(ctx, &cloudfront.CreateDistributionInput{
		DistributionConfig: distConfig,
	})
	if err != nil {
		if strings.Contains(err.Error(), "CNAMEAlreadyExists") {
			return "", fmt.Errorf("CNAME conflict: The domain %s is already associated with another CloudFront distribution. "+
				"Please ensure this domain is not being used by another project in this or another AWS account. "+
				"Error: %w", domain, err)
		}
		return "", fmt.Errorf("failed to create CloudFront distribution: %w", err)
	}

	cfDomain := *createOutput.Distribution.DomainName
	p.log.Info("CloudFront distribution created: %s (%s)", *createOutput.Distribution.Id, cfDomain)

	// Emit full DNS guide (CloudFront CNAME + ACM validation)
	if domain != "" && certARN != "" {
		acmCfg, acmErr := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion("us-east-1"))
		if acmErr == nil {
			acmClient := acm.NewFromConfig(acmCfg)
			if !certIssued {
				p.printDNSValidationRecordsWithCF(ctx, acmClient, certARN, domain, cfDomain)
			} else {
				p.writeDNSFileCloudFrontOnly(domain, cfDomain)
			}
		}
	}

	return *createOutput.Distribution.Id, nil
}

// applyDistributionConfig centralizes the logic for both creating and updating a distribution.
// It returns true if any changes were made (for updates).
func (p *AWSProvider) applyDistributionConfig(ctx context.Context, dc *cfTypes.DistributionConfig, callerRef, domain, certARN, s3OacId, lambdaOacId, cachingOptimizedId, cachingDisabledId, allViewerPolicyId, bucketName, functionUrl string) bool {
	changed := false

	if dc.CallerReference == nil || *dc.CallerReference == "" {
		dc.CallerReference = aws.String(callerRef)
		changed = true
	}
	if dc.Comment == nil || *dc.Comment != callerRef {
		dc.Comment = aws.String(callerRef)
		changed = true
	}
	if dc.Enabled == nil || !*dc.Enabled {
		dc.Enabled = aws.Bool(true)
		changed = true
	}

	// 1. Handle Domain Aliases
	if domain != "" {
		if certARN != "" && p.isCertificateIssued(ctx, certARN) {
			// Ensure Aliases
			found := false
			if dc.Aliases != nil {
				for _, alias := range dc.Aliases.Items {
					if alias == domain {
						found = true
						break
					}
				}
			}
			if !found {
				if dc.Aliases == nil {
					dc.Aliases = &cfTypes.Aliases{Items: []string{}, Quantity: aws.Int32(0)}
				}
				dc.Aliases.Items = append(dc.Aliases.Items, domain)
				dc.Aliases.Quantity = aws.Int32(int32(len(dc.Aliases.Items)))
				changed = true
			}

			// Ensure Viewer Certificate
			if dc.ViewerCertificate == nil || dc.ViewerCertificate.ACMCertificateArn == nil || *dc.ViewerCertificate.ACMCertificateArn != certARN {
				dc.ViewerCertificate = &cfTypes.ViewerCertificate{
					ACMCertificateArn:            aws.String(certARN),
					SSLSupportMethod:             cfTypes.SSLSupportMethodSniOnly,
					MinimumProtocolVersion:       cfTypes.MinimumProtocolVersionTLSv122021,
					CloudFrontDefaultCertificate: aws.Bool(false),
				}
				changed = true
			}
		} else {
			// If certificate is not issued yet, we keep the default certificate and NO aliases
			if dc.ViewerCertificate == nil {
				dc.ViewerCertificate = &cfTypes.ViewerCertificate{
					CloudFrontDefaultCertificate: aws.Bool(true),
				}
				changed = true
			}
			// Important: Don't add aliases yet if the cert isn't ready.
		}
	} else if dc.ViewerCertificate == nil {
		dc.ViewerCertificate = &cfTypes.ViewerCertificate{
			CloudFrontDefaultCertificate: aws.Bool(true),
		}
		changed = true
	}

	// 3. Handle Origins
	s3OriginId := "S3Assets"
	lambdaOriginId := "LambdaCompute"
	s3Domain := fmt.Sprintf("%s.s3.%s.amazonaws.com", bucketName, p.cfg.Region)

	var lambdaHost string
	if functionUrl != "" {
		lambdaHost = strings.TrimPrefix(functionUrl, "https://")
		lambdaHost = strings.TrimSuffix(lambdaHost, "/")
	}

	expectedOrigins := []cfTypes.Origin{
		{
			Id:                    aws.String(s3OriginId),
			DomainName:            aws.String(s3Domain),
			OriginAccessControlId: aws.String(s3OacId),
			S3OriginConfig: &cfTypes.S3OriginConfig{
				OriginAccessIdentity: aws.String(""),
			},
		},
	}
	if lambdaHost != "" {
		expectedOrigins = append(expectedOrigins, cfTypes.Origin{
			Id:                    aws.String(lambdaOriginId),
			DomainName:            aws.String(lambdaHost),
			OriginAccessControlId: aws.String(lambdaOacId),
			CustomOriginConfig: &cfTypes.CustomOriginConfig{
				HTTPPort:               aws.Int32(80),
				HTTPSPort:              aws.Int32(443),
				OriginProtocolPolicy:   cfTypes.OriginProtocolPolicyHttpsOnly,
				OriginSslProtocols:     &cfTypes.OriginSslProtocols{Quantity: aws.Int32(1), Items: []cfTypes.SslProtocol{cfTypes.SslProtocolTLSv12}},
				OriginReadTimeout:      aws.Int32(30),
				OriginKeepaliveTimeout: aws.Int32(5),
			},
		})
	}

	if dc.Origins == nil || int(aws.ToInt32(dc.Origins.Quantity)) != len(expectedOrigins) {
		dc.Origins = &cfTypes.Origins{
			Quantity: aws.Int32(int32(len(expectedOrigins))),
			Items:    expectedOrigins,
		}
		changed = true
	} else {
		// Verify each origin host/OAC
		for i := range dc.Origins.Items {
			origin := &dc.Origins.Items[i]
			for _, expected := range expectedOrigins {
				if *origin.Id == *expected.Id {
					if *origin.DomainName != *expected.DomainName {
						origin.DomainName = expected.DomainName
						changed = true
					}
					if aws.ToString(origin.OriginAccessControlId) != aws.ToString(expected.OriginAccessControlId) {
						origin.OriginAccessControlId = expected.OriginAccessControlId
						changed = true
					}
					// Ensure CustomOriginConfig for Lambda if missing
					if *origin.Id == lambdaOriginId && origin.CustomOriginConfig == nil {
						origin.CustomOriginConfig = expected.CustomOriginConfig
						changed = true
					}
				}
			}
		}
	}

	// 4. Handle Default Cache Behavior
	targetOrigin := s3OriginId
	if lambdaHost != "" {
		targetOrigin = lambdaOriginId
	}
	cachePolicy := cachingOptimizedId
	if lambdaHost != "" {
		cachePolicy = cachingDisabledId
	}

	var orpId *string
	if lambdaHost != "" {
		orpId = aws.String(allViewerPolicyId)
	}

	allowedMethods := &cfTypes.AllowedMethods{
		Quantity: aws.Int32(2),
		Items:    []cfTypes.Method{cfTypes.MethodGet, cfTypes.MethodHead},
	}
	if lambdaHost != "" {
		allowedMethods = &cfTypes.AllowedMethods{
			Quantity: aws.Int32(7),
			Items:    []cfTypes.Method{cfTypes.MethodGet, cfTypes.MethodHead, cfTypes.MethodOptions, cfTypes.MethodPut, cfTypes.MethodPatch, cfTypes.MethodPost, cfTypes.MethodDelete},
		}
	}

	if dc.DefaultCacheBehavior == nil {
		dc.DefaultCacheBehavior = &cfTypes.DefaultCacheBehavior{
			TargetOriginId:        aws.String(targetOrigin),
			ViewerProtocolPolicy:  cfTypes.ViewerProtocolPolicyRedirectToHttps,
			CachePolicyId:         aws.String(cachePolicy),
			OriginRequestPolicyId: orpId,
			AllowedMethods:        allowedMethods,
		}
		changed = true
	} else {
		dcb := dc.DefaultCacheBehavior
		if *dcb.TargetOriginId != targetOrigin {
			dcb.TargetOriginId = aws.String(targetOrigin)
			changed = true
		}
		if *dcb.CachePolicyId != cachePolicy {
			dcb.CachePolicyId = aws.String(cachePolicy)
			changed = true
		}
		if aws.ToString(dcb.OriginRequestPolicyId) != aws.ToString(orpId) {
			dcb.OriginRequestPolicyId = orpId
			changed = true
		}
		if dcb.AllowedMethods == nil || *dcb.AllowedMethods.Quantity != *allowedMethods.Quantity {
			dcb.AllowedMethods = allowedMethods
			changed = true
		}
	}

	// 5. Handle Cache Behaviors (for static assets when Lambda is active)
	if lambdaHost != "" {
		expectedBehaviors := []cfTypes.CacheBehavior{
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
		updateBehaviors := false
		if dc.CacheBehaviors == nil || int(aws.ToInt32(dc.CacheBehaviors.Quantity)) != len(expectedBehaviors) {
			updateBehaviors = true
		} else {
			for i, eb := range expectedBehaviors {
				ab := dc.CacheBehaviors.Items[i]
				if aws.ToString(ab.PathPattern) != aws.ToString(eb.PathPattern) ||
					aws.ToString(ab.TargetOriginId) != aws.ToString(eb.TargetOriginId) ||
					aws.ToString(ab.CachePolicyId) != aws.ToString(eb.CachePolicyId) {
					updateBehaviors = true
					break
				}
			}
		}

		if updateBehaviors {
			dc.CacheBehaviors = &cfTypes.CacheBehaviors{
				Quantity: aws.Int32(int32(len(expectedBehaviors))),
				Items:    expectedBehaviors,
			}
			changed = true
		}
	} else {
		if dc.CacheBehaviors != nil && aws.ToInt32(dc.CacheBehaviors.Quantity) > 0 {
			dc.CacheBehaviors = &cfTypes.CacheBehaviors{Quantity: aws.Int32(0), Items: []cfTypes.CacheBehavior{}}
			changed = true
		}
	}

	return changed
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
	if isPlaceholderDistributionID(distId) {
		distId = ""
	}

	if distId == "" {
		// 2. Fallback to discovering the managed distribution
		bucketName := p.getS3BucketName(appCfg)
		callerRef := fmt.Sprintf("nextdeploy-%s", strings.ToLower(bucketName))

		client := cloudfront.NewFromConfig(p.cfg)
		var marker *string
		for {
			listOutput, _ := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{
				Marker: marker,
			})
			if listOutput != nil && listOutput.DistributionList != nil {
				for _, dist := range listOutput.DistributionList.Items {
					if dist.Comment != nil && *dist.Comment == callerRef {
						distId = *dist.Id
						break
					}
				}
			}
			if distId != "" || listOutput == nil || listOutput.DistributionList == nil || listOutput.DistributionList.NextMarker == nil || *listOutput.DistributionList.NextMarker == "" {
				break
			}
			marker = listOutput.DistributionList.NextMarker
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
