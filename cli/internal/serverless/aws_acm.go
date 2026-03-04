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

	time.Sleep(5 * time.Second)
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

	dnsFile, _ := os.Create("dns.txt")
	if dnsFile != nil {
		defer dnsFile.Close()
		fmt.Fprintf(dnsFile, "=== NextDeploy Domain Validation Instructions ===\n\n")
		fmt.Fprintf(dnsFile, "Project: %s\n", domain)
		fmt.Fprintf(dnsFile, "Date: %s\n\n", time.Now().Format(time.RFC1123))
		fmt.Fprintf(dnsFile, "To enable your custom domain, you MUST add the following CNAME records to your DNS provider (e.g. Cloudflare, Namecheap, GoDaddy):\n\n")
	}

	// High visibility banner in CLI
	p.log.Info("────────────────────────────────────────────────────────────")
	p.log.Info("� ACTION REQUIRED: DNS VALIDATION NEEDED")
	p.log.Info("────────────────────────────────────────────────────────────")
	p.log.Info("To enable your custom domain (%s), you must add these records:", domain)

	for _, dvo := range cert.DomainValidationOptions {
		if dvo.ResourceRecord != nil {
			name := *dvo.ResourceRecord.Name
			value := *dvo.ResourceRecord.Value
			p.log.Info("  CNAME: %s", name)
			p.log.Info("  VALUE: %s", value)
			p.log.Info("  ───")
			if dnsFile != nil {
				fmt.Fprintf(dnsFile, "RECORD TYPE: CNAME\n")
				fmt.Fprintf(dnsFile, "NAME/HOST:   %s\n", name)
				fmt.Fprintf(dnsFile, "VALUE/TARGET: %s\n", value)
				fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n")
			}
		}
	}

	if dnsFile != nil {
		fmt.Fprintf(dnsFile, "\nNEXT STEPS:\n")
		fmt.Fprintf(dnsFile, "1. Log in to your Domain Registrar (e.g. Cloudflare, GoDaddy, Namecheap).\n")
		fmt.Fprintf(dnsFile, "2. Navigate to DNS Management / Advanced DNS.\n")
		fmt.Fprintf(dnsFile, "3. Add the CNAME record(s) shown above.\n")
		fmt.Fprintf(dnsFile, "4. Wait for AWS to validate (usually 2-10 minutes).\n")
		fmt.Fprintf(dnsFile, "5. Run 'nextdeploy ship' again to finish the setup.\n\n")
		fmt.Fprintf(dnsFile, "NextDeploy will automatically detect when the certificate is ready.\n")
	}

	p.log.Info("✅ Instructions saved to high-visibility file: dns.txt")
	p.log.Info("────────────────────────────────────────────────────────────")
	p.log.Info("Once DNS propagates, run 'nextdeploy ship' again to complete.")
	p.log.Info("────────────────────────────────────────────────────────────")
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
