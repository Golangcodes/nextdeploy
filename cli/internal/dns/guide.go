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

	fmt.Fprintf(f, "#  NextDeploy DNS Setup Guide (Serverless)\n\n")
	fmt.Fprintf(f, "Target Domain: **%s**\n", domain)
	fmt.Fprintf(f, "Generated: `%s`\n\n", time.Now().Format(time.RFC1123))

	fmt.Fprintf(f, "> [!IMPORTANT]\n")
	fmt.Fprintf(f, "> You need to add **TWO** sets of DNS records to your registrar to go live.\n")
	fmt.Fprintf(f, "> Need help? Check the [Full Documentation](%s)\n\n", docsURL)

	fmt.Fprintf(f, "Log into your domain registrar (e.g., Namecheap, GoDaddy, Cloudflare) and navigate to the **DNS Management** or **Advanced DNS** settings for `%s`.\n\n", domain)

	// Step 1: CloudFront
	fmt.Fprintf(f, "## Step 1: Point your domain at CloudFront\n\n")
	if cfDomain != "" {
		fmt.Fprintf(f, "This connects your domain to the AWS global edge network.\n\n")
		fmt.Fprintf(f, "### Add the Root Record\n")
		fmt.Fprintf(f, "1. Click **Add New Record**.\n")
		fmt.Fprintf(f, "2. Select **CNAME Record** as the type.\n")
		fmt.Fprintf(f, "3. For **Host** (or Name), enter `@` (or `%s` depending on your provider).\n", domain)
		fmt.Fprintf(f, "4. For **Value** (or Target), enter exactly: `%s`\n", cfDomain)
		fmt.Fprintf(f, "5. Leave TTL as **Automatic** or **1 min**, then save.\n\n")

		fmt.Fprintf(f, "### Add the WWW Record\n")
		fmt.Fprintf(f, "1. Click **Add New Record** again.\n")
		fmt.Fprintf(f, "2. Select **CNAME Record** as the type.\n")
		fmt.Fprintf(f, "3. For **Host** (or Name), enter `www`.\n")
		fmt.Fprintf(f, "4. For **Value** (or Target), enter exactly: `%s`\n", cfDomain)
		fmt.Fprintf(f, "5. Save the record.\n\n")

		fmt.Fprintf(f, "> [!TIP]\n")
		fmt.Fprintf(f, "> **Cloudflare Users**: Ensure the proxy status cloud icon is **Grey** (DNS Only) for both of these records. AWS handles the edge proxying.\n\n")
	} else {
		fmt.Fprintf(f, "⚠️ **CloudFront Domain: [Pending]**\n")
		fmt.Fprintf(f, "Run `nextdeploy ship` again after SSL validation (Step 2) to get this value.\n\n")
	}

	// Step 2: SSL Validation
	fmt.Fprintf(f, "## Step 2: SSL Certificate Validation (AWS ACM)\n\n")
	if len(records) > 0 {
		fmt.Fprintf(f, "AWS needs to verify you own this domain before issuing the SSL certificate.\n\n")
		for i, r := range records {
			fmt.Fprintf(f, "### Validation Record %d\n", i+1)
			fmt.Fprintf(f, "1. Click **Add New Record**.\n")
			fmt.Fprintf(f, "2. Select **CNAME Record** as the type.\n")
			fmt.Fprintf(f, "3. For **Host** (or Name), copy exactly: `%s`\n", r.Name)
			fmt.Fprintf(f, "4. For **Value** (or Target), copy exactly: `%s`\n", r.Value)
			fmt.Fprintf(f, "5. Save the record.\n\n")
		}
	} else {
		fmt.Fprintf(f, "No validation records needed right now, or the certificate is already validated!\n\n")
	}

	printRegistrarWarnings(f, domain)

	fmt.Fprintf(f, "## 🚀 Final Steps\n")
	fmt.Fprintf(f, "1. Ensure all records from above are saved in your DNS panel.\n")
	fmt.Fprintf(f, "2. Wait 2-5 minutes for the DNS changes to propagate globally.\n")
	fmt.Fprintf(f, "3. Run `nextdeploy ship` in your terminal again to finish the deployment.\n")

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

	fmt.Fprintf(f, "Log into your domain registrar (e.g., Namecheap, GoDaddy, Cloudflare) and navigate to the **DNS Management** or **Advanced DNS** settings for `%s`.\n\n", domain)

	fmt.Fprintf(f, "## Step 1: Add the Root A Record\n\n")
	fmt.Fprintf(f, "This points your main domain directly to your VPS.\n\n")
	fmt.Fprintf(f, "1. Click **Add New Record**.\n")
	fmt.Fprintf(f, "2. Select **A Record** as the type.\n")
	fmt.Fprintf(f, "3. For **Host** (or Name), enter `@` (or leave it blank depending on your provider).\n")
	fmt.Fprintf(f, "4. For **Value** (or IP Address), enter exactly: `%s`\n", serverIP)
	fmt.Fprintf(f, "5. Leave TTL as **Automatic** or **1 min**, then save.\n\n")

	fmt.Fprintf(f, "## Step 2: Add the WWW CNAME Record\n\n")
	fmt.Fprintf(f, "This ensures `www.%s` redirects properly to your main site.\n\n", domain)
	fmt.Fprintf(f, "1. Click **Add New Record**.\n")
	fmt.Fprintf(f, "2. Select **CNAME Record** as the type.\n")
	fmt.Fprintf(f, "3. For **Host** (or Name), enter `www`.\n")
	fmt.Fprintf(f, "4. For **Value** (or Target), enter exactly: `%s` (or `@` if supported).\n", domain)
	fmt.Fprintf(f, "5. Save the record.\n\n")

	printRegistrarWarnings(f, domain)

	fmt.Fprintf(f, "## 🚀 Final Steps\n")
	fmt.Fprintf(f, "1. Ensure both the A and CNAME records are saved in your DNS panel.\n")
	fmt.Fprintf(f, "2. NextDeploy (Caddy) will automatically provision your SSL certificate the first time someone visits your site.\n")

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
