package serverless

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmTypes "github.com/aws/aws-sdk-go-v2/service/acm/types"

	"github.com/Golangcodes/nextdeploy/cli/internal/dns"
)

func (p *AWSProvider) ensureACMCertificateExists(ctx context.Context, domain string) (string, error) {
	acmCfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithRegion("us-east-1"))
	if err != nil {
		return "", fmt.Errorf("failed to load ACM config for us-east-1: %w", err)
	}
	client := acm.NewFromConfig(acmCfg)

	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimSuffix(domain, "/")
	domain = strings.ToLower(domain)
	certARN, err := p.findExistingCertificate(ctx, client, domain)
	if err != nil {
		return "", err
	}
	if certARN != "" {
		p.log.Info("Existing ACM certificate found for %s: %s", domain, certARN)
		p.printDNSValidationRecords(ctx, client, certARN, domain)
		return certARN, nil
	}

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

	// Poll until ACM populates DomainValidationOptions (usually 2-10s)
	for i := 0; i < 6; i++ {
		time.Sleep(5 * time.Second)
		desc, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
			CertificateArn: aws.String(certARN),
		})
		if err == nil && desc.Certificate != nil && len(desc.Certificate.DomainValidationOptions) > 0 {
			if desc.Certificate.DomainValidationOptions[0].ResourceRecord != nil {
				break // records are ready
			}
		}
		p.log.Info("Waiting for ACM to populate DNS validation records (%d/6)...", i+1)
	}
	p.printDNSValidationRecords(ctx, client, certARN, domain)

	return certARN, nil
}

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

func (p *AWSProvider) printDNSValidationRecords(ctx context.Context, client *acm.Client, certARN, domain string) {
	p.printDNSValidationRecordsWithCF(ctx, client, certARN, domain, "")
}

const (
	mdTableHeader = "| Type | Host (Name) | Target (Value) |\n"
	mdTableSep    = "| :--- | :--- | :--- |\n"
)

func (p *AWSProvider) printDNSValidationRecordsWithCF(ctx context.Context, client *acm.Client, certARN, domain, cfDomain string) {
	descOutput, err := client.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: aws.String(certARN),
	})
	if err != nil {
		p.log.Warn("Could not fetch DNS validation records: %v", err)
		return
	}

	cert := descOutput.Certificate
	if cert.Status == acmTypes.CertificateStatusIssued && cfDomain != "" {
		// Cert is valid — just show the CloudFront CNAME
		p.writeDNSFileCloudFrontOnly(domain, cfDomain)
		return
	}
	if cert.Status == acmTypes.CertificateStatusIssued {
		p.log.Info("ACM certificate is already validated and issued!")
		return
	}

	var records []dns.ValidationRecord
	for _, dvo := range cert.DomainValidationOptions {
		if dvo.ResourceRecord != nil {
			records = append(records, dns.ValidationRecord{
				Name:  *dvo.ResourceRecord.Name,
				Value: *dvo.ResourceRecord.Value,
			})
		}
	}

	if err := dns.GenerateServerlessGuide(domain, cfDomain, records); err != nil {
		p.log.Warn("Could not generate dns.md: %v", err)
	}

	// High visibility CLI banner
	p.log.Info("════════════ ACTION REQUIRED: DNS SETUP ════════════")
	p.log.Info("SSL Validation and CloudFront setup required.")
	wd, _ := os.Getwd()
	p.log.Info("Detailed Guide Generated: %s/dns.md", wd)
	p.log.Info("Open this file to see exact CNAME records for your provider.")
	p.log.Info("═════════════════════════════════════════════════════")
}

func (p *AWSProvider) writeDNSFileCloudFrontOnly(domain, cfDomain string) {
	if err := dns.GenerateServerlessGuide(domain, cfDomain, nil); err != nil {
		p.log.Warn("Could not generate dns.md: %v", err)
	}

	// Apex (zone root) records cannot be CNAMEs per RFC 1034 §3.6.2; most
	// authoritative DNS providers reject them. The user needs ALIAS, ANAME,
	// or a Route53 alias record at the apex. We emit instructions for both
	// the apex and the www subdomain (which IS allowed to be a CNAME).
	isApex := !strings.Contains(domain, ".") || strings.Count(domain, ".") == 1

	p.log.Info("════ ACTION REQUIRED: POINT DOMAIN AT CLOUDFRONT ════")
	p.log.Info("SSL cert is ready. Add the following DNS records:")
	if isApex {
		p.log.Info("  ⚠ %s is an apex (zone root) — CNAMEs at the apex are NOT valid per RFC 1034.", domain)
		p.log.Info("  Use one of:")
		p.log.Info("    • Route53:  ALIAS A  @    →  %s   (free, recommended if you use Route53)", cfDomain)
		p.log.Info("    • Cloudflare: CNAME  @    →  %s   (their flattening makes this work)", cfDomain)
		p.log.Info("    • Other providers: ALIAS / ANAME @ → %s", cfDomain)
		p.log.Info("  CNAME  www  →  %s   (subdomains can use plain CNAMEs)", cfDomain)
	} else {
		p.log.Info("  CNAME  %s  →  %s", domain, cfDomain)
	}
	p.log.Info("Detailed Guide: dns.md")
	p.log.Info("════════════════════════════════════════════")
}

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
