package dns

import (
	"fmt"
	"os"
	"time"
)

const (
	mdTableHeader = "| Type | Host (Name) | Target (Value) |\n"
	mdTableSep    = "| :--- | :--- | :--- |\n"
	docsURL       = "https://nextdeploy.one/docs"
)

type ValidationRecord struct {
	Name  string
	Value string
}

// GenerateServerlessGuide creates a dns.md file for AWS Serverless deployments.
func GenerateServerlessGuide(domain string, cfDomain string, records []ValidationRecord) error {
	f, err := os.Create("dns.md")
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# 🌐 NextDeploy DNS Setup Guide (Serverless)\n\n")
	fmt.Fprintf(f, "Target Domain: **%s**\n", domain)
	fmt.Fprintf(f, "Generated: `%s`\n\n", time.Now().Format(time.RFC1123))

	fmt.Fprintf(f, "> [!IMPORTANT]\n")
	fmt.Fprintf(f, "> You need to add **TWO** sets of DNS records to your registrar to go live.\n")
	fmt.Fprintf(f, "> Need help? Check the [Full Documentation](%s)\n\n", docsURL)

	// Step 1: CloudFront
	fmt.Fprintf(f, "## 1️⃣ Point your domain at CloudFront\n")
	if cfDomain != "" {
		fmt.Fprintf(f, mdTableHeader)
		fmt.Fprintf(f, mdTableSep)
		fmt.Fprintf(f, "| **CNAME** | `@` (or `%s`) | `%s` |\n", domain, cfDomain)
		fmt.Fprintf(f, "| **CNAME** | `www` | `%s` |\n\n", cfDomain)

		fmt.Fprintf(f, "> [!TIP]\n")
		fmt.Fprintf(f, "> **Cloudflare Users**: Set Proxy status to **DNS Only** (Grey Cloud).\n\n")
	} else {
		fmt.Fprintf(f, "⚠️ **CloudFront Domain: [Pending]**\n")
		fmt.Fprintf(f, "Run `nextdeploy ship` again after SSL validation to get this value.\n\n")
	}

	// Step 2: SSL Validation
	fmt.Fprintf(f, "## 2️⃣ SSL Certificate Validation (AWS ACM)\n")
	fmt.Fprintf(f, mdTableHeader)
	fmt.Fprintf(f, mdTableSep)
	for _, r := range records {
		fmt.Fprintf(f, "| **CNAME** | `%s` | `%s` |\n", r.Name, r.Value)
	}
	fmt.Fprintf(f, "\n")

	printRegistrarWarnings(f, domain)

	fmt.Fprintf(f, "## 🚀 Final Steps\n")
	fmt.Fprintf(f, "1. Add records in your DNS panel.\n")
	fmt.Fprintf(f, "2. Wait 2-5 mins.\n")
	fmt.Fprintf(f, "3. Run `nextdeploy ship` to finish.\n")

	return nil
}

// GenerateVPSGuide creates a dns.md file for VPS deployments.
func GenerateVPSGuide(domain string, serverIP string) error {
	f, err := os.Create("dns.md")
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# 🌐 NextDeploy DNS Setup Guide (VPS)\n\n")
	fmt.Fprintf(f, "Target Domain: **%s**\n", domain)
	fmt.Fprintf(f, "Server IP: **%s**\n", serverIP)
	fmt.Fprintf(f, "Generated: `%s`\n\n", time.Now().Format(time.RFC1123))

	fmt.Fprintf(f, "> [!IMPORTANT]\n")
	fmt.Fprintf(f, "> Point your domain to your server IP to enable automatic SSL (via Caddy).\n")
	fmt.Fprintf(f, "> More info: [VPS Setup Docs](%s)\n\n", docsURL)

	fmt.Fprintf(f, "## 1️⃣ Add A Records\n")
	fmt.Fprintf(f, mdTableHeader)
	fmt.Fprintf(f, mdTableSep)
	fmt.Fprintf(f, "| **A** | `@` (or root) | `%s` |\n", serverIP)
	fmt.Fprintf(f, "| **CNAME** | `www` | `@` (or `%s`) |\n\n", domain)

	printRegistrarWarnings(f, domain)

	fmt.Fprintf(f, "## 🚀 Final Steps\n")
	fmt.Fprintf(f, "1. Add the A record in your DNS panel.\n")
	fmt.Fprintf(f, "2. NextDeploy (Caddy) will automatically provision SSL on first hit.\n")

	return nil
}

func printRegistrarWarnings(f *os.File, domain string) {
	fmt.Fprintf(f, "> [!WARNING]\n")
	fmt.Fprintf(f, "> **NAMECHEAP & GODADDY USERS**: Your registrar automatically adds your domain to the Host field.\n")
	fmt.Fprintf(f, "> **DO NOT** include `%s` in the Name/Host field or it will fail.\n", domain)
	fmt.Fprintf(f, "> \n")
	fmt.Fprintf(f, "> ✅ **Correct Host**: `_5f2eb7...` or `@` \n")
	fmt.Fprintf(f, "> ❌ **Wrong Host**: `_5f2eb7....%s`\n\n", domain)
}
