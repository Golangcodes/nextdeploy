package dns

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	mdTableHeader = "| Type | Host (Name) | Target (Value) |\n"
	mdTableSep    = "| :--- | :--- | :--- |\n"
	docsURL       = "https://nextdeploy.one/docs"
)

// ValidationRecord represents an SSL validation CNAME record
type ValidationRecord struct {
	Name   string // The host/subdomain part (e.g., "_5f2eb7..." or "_5ab8c33.www")
	Value  string // The target validation URL
	Domain string // The full domain this record is for
}

// RoutingRecord represents a traffic routing record
type RoutingRecord struct {
	Host  string // "@", "www", or subdomain
	Value string // Target (CloudFront domain or IP)
	Type  string // "CNAME" or "A"
}

// Provider-specific formatting rules
type ProviderRules struct {
	Name             string
	HostFormat       func(host string) string // How to format the host field
	NeedsTrailingDot bool                     // Whether values need trailing dots
	Notes            []string
}

var (
	// Namecheap specific rules
	NamecheapRules = ProviderRules{
		Name: "Namecheap",
		HostFormat: func(host string) string {
			// Remove any trailing dots and the domain part
			host = strings.TrimSuffix(host, ".")
			if strings.Contains(host, ".") {
				// Keep subdomain structure but remove domain
				parts := strings.SplitN(host, ".", 2)
				return parts[0]
			}
			return host
		},
		NeedsTrailingDot: false,
		Notes: []string{
			"NEVER include your domain name in the Host field",
			"For www SSL records: include '.www' in Host (e.g., '_hash.www')",
			"Root domain uses '@' or leave blank",
			"TTL can be left as 'Automatic'",
		},
	}

	// Cloudflare specific rules
	CloudflareRules = ProviderRules{
		Name: "Cloudflare",
		HostFormat: func(host string) string {
			return strings.TrimSuffix(host, ".")
		},
		NeedsTrailingDot: false,
		Notes: []string{
			"Set proxy status to DNS only (gray cloud) for SSL validation records",
			"Orange cloud (proxied) can be used for root/www AFTER SSL is issued",
		},
	}

	// GoDaddy specific rules
	GoDaddyRules = ProviderRules{
		Name: "GoDaddy",
		HostFormat: func(host string) string {
			return strings.TrimSuffix(host, ".")
		},
		NeedsTrailingDot: false,
		Notes: []string{
			"Points to field should NOT have trailing dot",
			"Use '@' for root domain",
		},
	}
)

// GenerateServerlessGuide creates a comprehensive dns.md file for AWS Serverless deployments
func GenerateServerlessGuide(domain string, cfDomain string, records []ValidationRecord) error {
	f, err := os.Create("dns.md")
	if err != nil {
		return fmt.Errorf("failed to create DNS guide: %w", err)
	}
	defer f.Close()

	writeHeader(f, domain, "Serverless (AWS Lambda + CloudFront)")
	writeImportantNotice(f)
	writePropagationInfo(f)

	// Step 1: CloudFront Routing Records
	writeRoutingSection(f, domain, cfDomain)

	// Step 2: SSL Validation Records
	writeSSLValidationSection(f, domain, records)

	// Provider-specific guidance
	writeProviderGuidance(f, domain)

	// Common pitfalls
	writePitfallsSection(f, domain)

	// Verification instructions
	writeVerificationSection(f)

	// Final steps
	writeFinalSteps(f)

	return nil
}

// GenerateVPSGuide creates a dns.md file for VPS deployments
func GenerateVPSGuide(domain string, serverIP string) error {
	f, err := os.Create("dns.md")
	if err != nil {
		return fmt.Errorf("failed to create DNS guide: %w", err)
	}
	defer f.Close()

	writeHeader(f, domain, "VPS (Direct Server)")

	fmt.Fprintf(f, "Server IP: **%s**\n\n", serverIP)
	writeImportantNotice(f)
	writePropagationInfo(f)

	// Step 1: Root A Record
	fmt.Fprintf(f, "## 📍 Step 1: Root Domain A Record\n\n")
	fmt.Fprintf(f, "Points your main domain directly to your server.\n\n")

	writeARecordInstructions(f, "@", serverIP)
	fmt.Fprintf(f, "\n")

	// Step 2: WWW CNAME Record
	fmt.Fprintf(f, "## 🔄 Step 2: WWW Subdomain\n\n")
	fmt.Fprintf(f, "Ensures `www.%s` works properly.\n\n", domain)

	writeCNAMERecordInstructions(f, "www", domain, "CNAME")
	fmt.Fprintf(f, "\n")

	writeProviderGuidance(f, domain)
	writePitfallsSection(f, domain)
	writeVerificationSection(f)

	// VPS-specific final steps
	fmt.Fprintf(f, "## 🚀 Final Steps\n\n")
	fmt.Fprintf(f, "1. ✅ Save both records in your DNS panel\n")
	fmt.Fprintf(f, "2. ⏱️ Wait 5-10 minutes for propagation\n")
	fmt.Fprintf(f, "3. 🔒 SSL will be automatically provisioned by Caddy on first visit\n")
	fmt.Fprintf(f, "4. 🌐 Visit https://%s to test\n\n", domain)

	return nil
}

func writeHeader(f *os.File, domain string, deploymentType string) {
	fmt.Fprintf(f, "# 🌐 NextDeploy DNS Setup Guide\n\n")
	fmt.Fprintf(f, "Target Domain: **%s**\n", domain)
	fmt.Fprintf(f, "Deployment Type: **%s**\n", deploymentType)
	fmt.Fprintf(f, "Generated: `%s`\n\n", time.Now().Format("2006-01-02 15:04:05 MST"))
}

func writeImportantNotice(f *os.File) {
	fmt.Fprintf(f, "> [!IMPORTANT]\n")
	fmt.Fprintf(f, "> This guide contains **exact** values for your domain. Copy them precisely.\n")
	fmt.Fprintf(f, "> DNS changes can take 5-60 minutes to propagate worldwide.\n")
	fmt.Fprintf(f, "> 📚 [Full Documentation](%s)\n\n", docsURL)
}

func writePropagationInfo(f *os.File) {
	fmt.Fprintf(f, "### ⏱️ DNS Propagation Timeline\n\n")
	fmt.Fprintf(f, "| DNS Server | Typical Time |\n")
	fmt.Fprintf(f, "| :--- | :--- |\n")
	fmt.Fprintf(f, "| Namecheap/Provider | ⚡ Instant (once saved) |\n")
	fmt.Fprintf(f, "| Google (8.8.8.8) | 5-30 minutes |\n")
	fmt.Fprintf(f, "| Cloudflare (1.1.1.1) | 5-30 minutes |\n")
	fmt.Fprintf(f, "| Worldwide | 24-48 hours max |\n\n")
}

func writeRoutingSection(f *os.File, domain string, cfDomain string) {
	fmt.Fprintf(f, "## 🎯 Step 1: Point Domain to CloudFront\n\n")

	if cfDomain == "" {
		fmt.Fprintf(f, "> [!WARNING]\n")
		fmt.Fprintf(f, "> CloudFront domain not yet available. Run `nextdeploy ship` after SSL validation.\n\n")
		return
	}

	fmt.Fprintf(f, "These records connect your domain to AWS's global edge network.\n\n")

	// Root record
	fmt.Fprintf(f, "### 📍 Root Domain Record\n\n")
	writeCNAMERecordInstructions(f, "@", cfDomain, "CNAME")

	// WWW record
	fmt.Fprintf(f, "### 🔄 WWW Subdomain Record\n\n")
	writeCNAMERecordInstructions(f, "www", cfDomain, "CNAME")
}

func writeSSLValidationSection(f *os.File, domain string, records []ValidationRecord) {
	fmt.Fprintf(f, "## 🔒 Step 2: SSL Certificate Validation\n\n")

	if len(records) == 0 {
		fmt.Fprintf(f, "No validation records needed - certificate may already be issued!\n")
		fmt.Fprintf(f, "Check AWS Certificate Manager console to verify.\n\n")
		return
	}

	fmt.Fprintf(f, "AWS needs to verify you own `%s` and `www.%s` before issuing HTTPS certificates.\n\n", domain, domain)
	fmt.Fprintf(f, "Add **BOTH** of these CNAME records exactly as shown:\n\n")

	for i, record := range records {
		fmt.Fprintf(f, "### Validation Record %d\n", i+1)
		fmt.Fprintf(f, "**Purpose**: %s\n\n", getRecordPurpose(record, domain))
		writeCNAMERecordInstructions(f, record.Name, record.Value, "CNAME")
	}
}

func writeCNAMERecordInstructions(f *os.File, host string, value string, recordType string) {
	fmt.Fprintf(f, "| Field | Value |\n")
	fmt.Fprintf(f, "| :--- | :--- |\n")
	fmt.Fprintf(f, "| **Type** | `%s` |\n", recordType)
	fmt.Fprintf(f, "| **Host/Name** | `%s` |\n", host)
	fmt.Fprintf(f, "| **Value/Target** | `%s` |\n", value)
	fmt.Fprintf(f, "| **TTL** | `Automatic` (or 5-30 minutes) |\n\n")
}

func writeARecordInstructions(f *os.File, host string, ip string) {
	fmt.Fprintf(f, "| Field | Value |\n")
	fmt.Fprintf(f, "| :--- | :--- |\n")
	fmt.Fprintf(f, "| **Type** | `A Record` |\n")
	fmt.Fprintf(f, "| **Host/Name** | `%s` |\n", host)
	fmt.Fprintf(f, "| **Value/IP** | `%s` |\n", ip)
	fmt.Fprintf(f, "| **TTL** | `Automatic` |\n\n")
}

func writeProviderGuidance(f *os.File, domain string) {
	fmt.Fprintf(f, "## 📋 Provider-Specific Instructions\n\n")

	// Namecheap
	fmt.Fprintf(f, "### Namecheap\n\n")
	fmt.Fprintf(f, "| Do | Don't |\n")
	fmt.Fprintf(f, "| :--- | :--- |\n")
	fmt.Fprintf(f, "| ✅ Use `@` for root domain | ❌ Never include `.%s` in Host field |\n", domain)
	fmt.Fprintf(f, "| ✅ For www SSL: `_hash.www` in Host | ❌ Don't add trailing dots |\n")
	fmt.Fprintf(f, "| ✅ Copy values exactly as shown | ❌ Don't add extra spaces |\n\n")

	// Cloudflare
	fmt.Fprintf(f, "### Cloudflare\n\n")
	fmt.Fprintf(f, "⚠️ **Critical**: For SSL validation records, ensure the cloud icon is **gray** (DNS only)\n\n")
	fmt.Fprintf(f, "| Record Type | Proxy Status |\n")
	fmt.Fprintf(f, "| :--- | :--- |\n")
	fmt.Fprintf(f, "| SSL Validation Records | ⚪ Gray cloud (DNS only) |\n")
	fmt.Fprintf(f, "| Root/WWW (after SSL) | 🟠 Orange cloud (proxied) optional |\n\n")

	// GoDaddy
	fmt.Fprintf(f, "### GoDaddy\n\n")
	fmt.Fprintf(f, "- Use **@** for root domain\n")
	fmt.Fprintf(f, "- Points to field should NOT have trailing dot\n")
	fmt.Fprintf(f, "- TTL can be left as 1 hour\n\n")
}

func writePitfallsSection(f *os.File, domain string) {
	fmt.Fprintf(f, "## ⚠️ Common Pitfalls to Avoid\n\n")

	pitfalls := []struct {
		Bad  string
		Good string
		Why  string
	}{
		{
			Bad:  "_5f2eb7...nextdeploy.one",
			Good: "_5f2eb7...",
			Why:  "Host field should NOT include your domain name",
		},
		{
			Bad:  "_hash.www.nextdeploy.one",
			Good: "_hash.www",
			Why:  "For www SSL records, stop at '.www'",
		},
		{
			Bad:  "value.acm-validations.aws.",
			Good: "value.acm-validations.aws",
			Why:  "Most providers don't want trailing dots in the Value field",
		},
		{
			Bad:  "Waiting 2 minutes and giving up",
			Good: "Waiting 30+ minutes for propagation",
			Why:  "DNS propagation takes time - be patient!",
		},
	}

	fmt.Fprintf(f, "| ❌ Wrong | ✅ Correct | Why |\n")
	fmt.Fprintf(f, "| :--- | :--- | :--- |\n")
	for _, p := range pitfalls {
		fmt.Fprintf(f, "| `%s` | `%s` | %s |\n", p.Bad, p.Good, p.Why)
	}
	fmt.Fprintf(f, "\n")
}

func writeVerificationSection(f *os.File) {
	fmt.Fprintf(f, "## 🔍 How to Verify Records\n\n")

	fmt.Fprintf(f, "After adding records, verify they're working:\n\n")

	fmt.Fprintf(f, "```bash\n")
	fmt.Fprintf(f, "# Check root domain\n")
	fmt.Fprintf(f, "dig nextdeploy.one CNAME +short\n\n")
	fmt.Fprintf(f, "# Check SSL validation records\n")
	fmt.Fprintf(f, "dig _5f2eb7...nextdeploy.one CNAME +short\n")
	fmt.Fprintf(f, "dig @8.8.8.8 _hash.www.nextdeploy.one CNAME +short  # Use Google DNS\n\n")
	fmt.Fprintf(f, "# Watch for propagation\n")
	fmt.Fprintf(f, "watch -n 60 'dig @8.8.8.8 _hash.www.nextdeploy.one CNAME +short'\n")
	fmt.Fprintf(f, "```\n\n")

	fmt.Fprintf(f, "**Expected output**: You should see the target value (CloudFront domain or validation string)\n\n")
}

func writeFinalSteps(f *os.File) {
	fmt.Fprintf(f, "## Final Checklist\n\n")

	fmt.Fprintf(f, "- [ ] All 4 records added (2 routing + 2 SSL validation)\n")
	fmt.Fprintf(f, "- [ ] Host fields don't include domain name\n")
	fmt.Fprintf(f, "- [ ] No trailing dots in any fields\n")
	fmt.Fprintf(f, "- [ ] Waited 10+ minutes for propagation\n")
	fmt.Fprintf(f, "- [ ] Verified with `dig @8.8.8.8`\n")
	fmt.Fprintf(f, "- [ ] Run `nextdeploy ship` to complete\n\n")

	fmt.Fprintf(f, "---\n")
	fmt.Fprintf(f, "*Need help? Visit [nextdeploy.one/docs](%s) or run `nextdeploy support`*\n", docsURL)
}

// Helper function to determine record purpose
func getRecordPurpose(record ValidationRecord, domain string) string {
	if strings.Contains(record.Name, ".www") {
		return fmt.Sprintf("Validates www.%s", domain)
	}
	return fmt.Sprintf("Validates %s (root domain)", domain)
}

// FormatHostForProvider formats a host value according to provider rules
func FormatHostForProvider(provider ProviderRules, host string) string {
	return provider.HostFormat(host)
}

// GenerateQuickReference generates a quick reference table for all records
func GenerateQuickReference(domain string, cfDomain string, records []ValidationRecord) string {
	var sb strings.Builder

	sb.WriteString("# Quick DNS Reference\n\n")
	sb.WriteString(mdTableHeader)
	sb.WriteString(mdTableSep)

	// Routing records
	sb.WriteString(fmt.Sprintf("| CNAME | `@` | `%s` |\n", cfDomain))
	sb.WriteString(fmt.Sprintf("| CNAME | `www` | `%s` |\n", cfDomain))

	// Validation records
	for _, r := range records {
		sb.WriteString(fmt.Sprintf("| CNAME | `%s` | `%s` |\n", r.Name, r.Value))
	}

	return sb.String()
}
