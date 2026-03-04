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
	p.printDNSValidationRecordsWithCF(ctx, client, certARN, domain, "")
}

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
		p.log.Info("✅ ACM certificate is already validated and issued!")
		return
	}

	dnsFile, _ := os.Create("dns.txt")
	if dnsFile != nil {
		defer dnsFile.Close()
		sep := "════════════════════════════════════════════════════════════\n"
		fmt.Fprintf(dnsFile, sep)
		fmt.Fprintf(dnsFile, "  NextDeploy — DNS Setup for: %s\n", domain)
		fmt.Fprintf(dnsFile, "  Generated: %s\n", time.Now().Format(time.RFC1123))
		fmt.Fprintf(dnsFile, sep)
		fmt.Fprintf(dnsFile, "\nYou need to add TWO sets of DNS records:\n\n")

		// Step 1: CloudFront CNAME (the most important one)
		fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n")
		fmt.Fprintf(dnsFile, "  STEP 1 — POINT YOUR DOMAIN AT CLOUDFRONT\n")
		fmt.Fprintf(dnsFile, "  (This is what makes your site actually load)\n")
		fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n\n")
		if cfDomain != "" {
			fmt.Fprintf(dnsFile, "  TYPE:   CNAME  (or ALIAS for root domains on Cloudflare)\n")
			fmt.Fprintf(dnsFile, "  NAME:   @  (or %s)\n", domain)
			fmt.Fprintf(dnsFile, "  VALUE:  %s\n\n", cfDomain)
			fmt.Fprintf(dnsFile, "  TYPE:   CNAME\n")
			fmt.Fprintf(dnsFile, "  NAME:   www\n")
			fmt.Fprintf(dnsFile, "  VALUE:  %s\n\n", cfDomain)
			fmt.Fprintf(dnsFile, "  💡 Cloudflare tip: Use \"CNAME\" with proxy OFF (grey cloud).\n\n")
		} else {
			fmt.Fprintf(dnsFile, "  CLOUD FRONT DOMAIN: [Pending — Run 'nextdeploy ship' again]\n")
			fmt.Fprintf(dnsFile, "  Once your certificate below is validated, CloudFront will be\n")
			fmt.Fprintf(dnsFile, "  fully provisioned and this value will be updated here.\n\n")
		}

		// Step 2: ACM Validation CNAMEs
		fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n")
		fmt.Fprintf(dnsFile, "  STEP 2 — SSL CERTIFICATE VALIDATION (AWS ACM)\n")
		fmt.Fprintf(dnsFile, "  (These prove you own the domain so HTTPS works)\n")
		fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n\n")

		for _, dvo := range cert.DomainValidationOptions {
			if dvo.ResourceRecord != nil {
				fmt.Fprintf(dnsFile, "  TYPE:   CNAME\n")
				fmt.Fprintf(dnsFile, "  NAME:   %s\n", *dvo.ResourceRecord.Name)
				fmt.Fprintf(dnsFile, "  VALUE:  %s\n\n", *dvo.ResourceRecord.Value)
			}
		}

		fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n")
		fmt.Fprintf(dnsFile, "  NEXT STEPS\n")
		fmt.Fprintf(dnsFile, "────────────────────────────────────────────────────────────\n\n")
		fmt.Fprintf(dnsFile, "  1. Log in to your DNS provider (Cloudflare, GoDaddy, etc.)\n")
		fmt.Fprintf(dnsFile, "  2. Add ALL records above (both Step 1 and Step 2)\n")
		fmt.Fprintf(dnsFile, "  3. Wait 2-10 minutes for propagation\n")
		fmt.Fprintf(dnsFile, "  4. Run: nextdeploy ship\n")
		fmt.Fprintf(dnsFile, "     NextDeploy will detect the validated cert and finalize\n\n")
		fmt.Fprintf(dnsFile, sep)
	}

	// High visibility CLI banner
	p.log.Info("════ ACTION REQUIRED: DNS SETUP NEEDED ════")
	if cfDomain != "" {
		p.log.Info("STEP 1 — Point %s at CloudFront:", domain)
		p.log.Info("  CNAME  @  →  %s", cfDomain)
		p.log.Info("  CNAME  www  →  %s", cfDomain)
		p.log.Info("────")
	}
	p.log.Info("STEP 2 — SSL Validation CNAMEs:")
	for _, dvo := range cert.DomainValidationOptions {
		if dvo.ResourceRecord != nil {
			p.log.Info("  CNAME  %s", *dvo.ResourceRecord.Name)
			p.log.Info("  →      %s", *dvo.ResourceRecord.Value)
			p.log.Info("  ───")
		}
	}
	p.log.Info("✅ Full instructions saved to: dns.txt")
	p.log.Info("Once done, run 'nextdeploy ship' again to complete setup.")
	p.log.Info("════════════════════════════════════════════")
}

func (p *AWSProvider) writeDNSFileCloudFrontOnly(domain, cfDomain string) {
	dnsFile, err := os.Create("dns.txt")
	if err != nil {
		return
	}
	defer dnsFile.Close()
	sep := "════════════════════════════════════════════════════════════\n"
	fmt.Fprintf(dnsFile, sep)
	fmt.Fprintf(dnsFile, "  NextDeploy — DNS Setup for: %s\n", domain)
	fmt.Fprintf(dnsFile, "  Generated: %s\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(dnsFile, sep)
	fmt.Fprintf(dnsFile, "\nSSL certificate is ISSUED ✅ — just point your domain at CloudFront:\n\n")
	fmt.Fprintf(dnsFile, "  TYPE:   CNAME  (or ALIAS on Cloudflare)\n")
	fmt.Fprintf(dnsFile, "  NAME:   @  (root domain)\n")
	fmt.Fprintf(dnsFile, "  VALUE:  %s\n\n", cfDomain)
	fmt.Fprintf(dnsFile, "  TYPE:   CNAME\n")
	fmt.Fprintf(dnsFile, "  NAME:   www\n")
	fmt.Fprintf(dnsFile, "  VALUE:  %s\n\n", cfDomain)
	fmt.Fprintf(dnsFile, "Then run: nextdeploy ship\n")
	fmt.Fprintf(dnsFile, sep)

	p.log.Info("════ ACTION REQUIRED: POINT DOMAIN AT CLOUDFRONT ════")
	p.log.Info("SSL cert is ready! Now add these DNS records:")
	p.log.Info("  CNAME  @    →  %s", cfDomain)
	p.log.Info("  CNAME  www  →  %s", cfDomain)
	p.log.Info("Then run: nextdeploy ship")
	p.log.Info("Instructions saved to: dns.txt")
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
