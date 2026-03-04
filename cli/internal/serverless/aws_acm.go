package serverless

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmTypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
)

// ensureACMCertificateExists finds or creates an ACM certificate for the given
// domain in us-east-1 (required for CloudFront). Returns the certificate ARN.
func (p *AWSProvider) ensureACMCertificateExists(ctx context.Context, domain string) (string, error) {
	// ACM certificates for CloudFront must be in us-east-1
	acmCfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion("us-east-1"))
	if err != nil {
		return "", fmt.Errorf("failed to load ACM config for us-east-1: %w", err)
	}
	client := acm.NewFromConfig(acmCfg)

	// Normalize domain (strip protocol, trailing slashes)
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimSuffix(domain, "/")
	domain = strings.ToLower(domain)

	// 1. Check if a certificate already exists for this domain
	certARN, err := p.findExistingCertificate(ctx, client, domain)
	if err != nil {
		return "", err
	}
	if certARN != "" {
		p.log.Info("Existing ACM certificate found for %s: %s", domain, certARN)
		return certARN, nil
	}

	// 2. Request a new certificate
	p.log.Info("Requesting new ACM certificate for %s...", domain)
	sans := []string{}
	if !strings.HasPrefix(domain, "*.") {
		sans = append(sans, "www."+domain)
	}

	reqOutput, err := client.RequestCertificate(ctx, &acm.RequestCertificateInput{
		DomainName:              aws.String(domain),
		SubjectAlternativeNames: sans,
		ValidationMethod:        acmTypes.ValidationMethodDns,
	})
	if err != nil {
		return "", fmt.Errorf("failed to request ACM certificate: %w", err)
	}

	certARN = *reqOutput.CertificateArn
	p.log.Info("ACM certificate requested: %s", certARN)

	// 3. Wait briefly for DNS validation records to appear, then print them
	time.Sleep(5 * time.Second)
	p.printDNSValidationRecords(ctx, client, certARN, domain)

	return certARN, nil
}

// findExistingCertificate searches for an existing ISSUED or PENDING_VALIDATION
// ACM certificate that covers the given domain.
func (p *AWSProvider) findExistingCertificate(ctx context.Context, client *acm.Client, domain string) (string, error) {
	paginator := acm.NewListCertificatesPaginator(client, &acm.ListCertificatesInput{
		CertificateStatuses: []acmTypes.CertificateStatus{
			acmTypes.CertificateStatusIssued,
			acmTypes.CertificateStatusPendingValidation,
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list ACM certificates: %w", err)
		}

		for _, cert := range page.CertificateSummaryList {
			if cert.DomainName != nil && *cert.DomainName == domain {
				return *cert.CertificateArn, nil
			}
			// Also check SANs
			if cert.SubjectAlternativeNameSummaries != nil {
				for _, san := range cert.SubjectAlternativeNameSummaries {
					if san == domain {
						return *cert.CertificateArn, nil
					}
				}
			}
		}
	}

	return "", nil
}

// printDNSValidationRecords fetches the DNS validation records for the certificate
// and logs them for the user to add to their DNS provider.
func (p *AWSProvider) printDNSValidationRecords(ctx context.Context, client *acm.Client, certARN, domain string) {
	descOutput, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: aws.String(certARN),
	})
	if err != nil {
		p.log.Warn("Could not fetch DNS validation records: %v", err)
		return
	}

	cert := descOutput.Certificate
	if cert.Status == acmTypes.CertificateStatusIssued {
		p.log.Info("✅ ACM certificate is already validated and issued!")
		return
	}

	p.log.Info("📋 Add these DNS CNAME records to validate your certificate for %s:", domain)
	for _, dvo := range cert.DomainValidationOptions {
		if dvo.ResourceRecord != nil {
			p.log.Info("  CNAME  %s  →  %s", *dvo.ResourceRecord.Name, *dvo.ResourceRecord.Value)
		}
	}
	p.log.Info("Once DNS propagates, the certificate will be automatically validated by AWS.")
}

// isCertificateIssued checks if the ACM certificate is fully issued.
func (p *AWSProvider) isCertificateIssued(ctx context.Context, certARN string) bool {
	acmCfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion("us-east-1"))
	if err != nil {
		return false
	}
	client := acm.NewFromConfig(acmCfg)
	descOutput, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: aws.String(certARN),
	})
	if err != nil {
		return false
	}
	return descOutput.Certificate.Status == acmTypes.CertificateStatusIssued
}
