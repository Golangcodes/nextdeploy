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
		p.log.Info("✅ ACM certificate is already validated and issued!")
		return
	}

	dnsFile, _ := os.Create("dns.md")
	if dnsFile != nil {
		defer dnsFile.Close()
		fmt.Fprintf(dnsFile, "# 🌐 NextDeploy DNS Setup Guide\n\n")
		fmt.Fprintf(dnsFile, "Target Domain: **%s**\n", domain)
		fmt.Fprintf(dnsFile, "Generated: `%s`\n\n", time.Now().Format(time.RFC1123))

		fmt.Fprintf(dnsFile, "> [!IMPORTANT]\n")
		fmt.Fprintf(dnsFile, "> You need to add **TWO** sets of DNS records to your registrar (e.g. Namecheap, Godaddy, Cloudflare) to go live.\n\n")

		// Step 1: CloudFront CNAME
		fmt.Fprintf(dnsFile, "## 1️⃣ Point your domain at CloudFront\n")
		fmt.Fprintf(dnsFile, "This record makes your website actually load.\n\n")

		if cfDomain != "" {
			fmt.Fprintf(dnsFile, mdTableHeader)
			fmt.Fprintf(dnsFile, mdTableSep)
			fmt.Fprintf(dnsFile, "| **CNAME** | `@` (or `%s`) | `%s` |\n", domain, cfDomain)
			fmt.Fprintf(dnsFile, "| **CNAME** | `www` | `%s` |\n\n", cfDomain)

			fmt.Fprintf(dnsFile, "> [!TIP]\n")
			fmt.Fprintf(dnsFile, "> **Cloudflare Users**: Set Proxy status to **DNS Only** (Grey Cloud) for these records.\n\n")
		} else {
			fmt.Fprintf(dnsFile, "⚠️ **CloudFront Domain: [Pending]**\n")
			fmt.Fprintf(dnsFile, "Once your SSL certificate (Step 2) is validated, run `nextdeploy ship` again and this field will update automatically.\n\n")
		}

		// Step 2: ACM Validation
		fmt.Fprintf(dnsFile, "## 2️⃣ SSL Certificate Validation (AWS ACM)\n")
		fmt.Fprintf(dnsFile, "This secure your site with HTTPS. Without these, your site will show \"Not Secure\".\n\n")

		fmt.Fprintf(dnsFile, "| Type | Host (Name) | Target (Value) |\n")
		fmt.Fprintf(dnsFile, "| :--- | :--- | :--- |\n")
		for _, dvo := range cert.DomainValidationOptions {
			if dvo.ResourceRecord != nil {
				fmt.Fprintf(dnsFile, "| **CNAME** | `%s` | `%s` |\n", *dvo.ResourceRecord.Name, *dvo.ResourceRecord.Value)
			}
		}
		fmt.Fprintf(dnsFile, "\n")

		fmt.Fprintf(dnsFile, "> [!WARNING]\n")
		fmt.Fprintf(dnsFile, "> **NAMECHEAP & GODADDY USERS**: Your registrar automatically adds your domain to the Host field. **DO NOT** include `.nextdeploy.one` in the Name/Host field or it will fail.\n")
		fmt.Fprintf(dnsFile, "> \n")
		fmt.Fprintf(dnsFile, "> ✅ **Correct Host**: `_5f2eb7...` \n")
		fmt.Fprintf(dnsFile, "> ❌ **Wrong Host**: `_5f2eb7....nextdeploy.one`\n\n")

		fmt.Fprintf(dnsFile, "## 🚀 Final Steps\n")
		fmt.Fprintf(dnsFile, "1. Add the records above in your DNS panel.\n")
		fmt.Fprintf(dnsFile, "2. Wait 2-5 minutes for propagation.\n")
		fmt.Fprintf(dnsFile, "3. Run `nextdeploy ship` again to finish setup.\n")
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
	dnsFile, err := os.Create("dns.md")
	if err != nil {
		return
	}
	defer dnsFile.Close()
	fmt.Fprintf(dnsFile, "# 🌐 NextDeploy DNS Setup Guide\n\n")
	fmt.Fprintf(dnsFile, "SSL status: **Issued ✅**\n\n")
	fmt.Fprintf(dnsFile, "Just point your domain to CloudFront to go live:\n\n")
	fmt.Fprintf(dnsFile, mdTableHeader)
	fmt.Fprintf(dnsFile, mdTableSep)
	fmt.Fprintf(dnsFile, "| **CNAME** | `@` (or `%s`) | `%s` |\n", domain, cfDomain)
	fmt.Fprintf(dnsFile, "| **CNAME** | `www` | `%s` |\n\n", cfDomain)

	fmt.Fprintf(dnsFile, "After adding these, run `nextdeploy ship` to finish.\n")

	p.log.Info("════ ACTION REQUIRED: POINT DOMAIN AT CLOUDFRONT ════")
	p.log.Info("SSL cert is ready! Now add these DNS records:")
	p.log.Info("  CNAME  @    →  %s", cfDomain)
	p.log.Info("  CNAME  www  →  %s", cfDomain)
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
