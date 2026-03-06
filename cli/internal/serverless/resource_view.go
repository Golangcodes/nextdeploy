package serverless

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/dns"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
)

// ServerlessResourceMap holds the metadata for the visual report
type ServerlessResourceMap struct {
	AppName           string
	Environment       string
	Region            string
	LambdaARN         string
	FunctionURL       string
	S3BucketName      string
	CloudFrontID      string
	CloudFrontDomain  string
	CustomDomain      string
	CertificateARN    string
	CertificateStatus string // "PENDING_VALIDATION", "ISSUED", "FAILED"
	ValidationRecords []dns.ValidationRecord
	DeploymentTime    time.Time
	DNSProvider       string // "namecheap", "cloudflare", "godaddy", "route53", "other"
}

// ProviderRules holds DNS provider-specific display instructions
type ProviderRules struct {
	Name         string
	Icon         string
	RootFormat   string
	WwwFormat    string
	SSLFormat    func(record dns.ValidationRecord) string
	Warning      string
	ProTip       string
	ProxyWarning string
}

// DNSProviderRules maps provider names to their specific instructions
var DNSProviderRules = map[string]ProviderRules{
	"namecheap": {
		Name:       "Namecheap",
		Icon:       "🧢",
		RootFormat: "@",
		WwwFormat:  "www",
		SSLFormat: func(record dns.ValidationRecord) string {
			if strings.Contains(record.Name, ".www") {
				return record.Name
			}
			return strings.Split(record.Name, ".")[0]
		},
		Warning: "⚠️ CRITICAL: NEVER include your domain name in the Host field! Use only the hash or '@'.",
		ProTip:  "For www SSL records, the Host must include '.www' (e.g., '_5ab8c33b39a.www')",
	},
	"cloudflare": {
		Name:       "Cloudflare",
		Icon:       "☁️",
		RootFormat: "@",
		WwwFormat:  "www",
		SSLFormat: func(record dns.ValidationRecord) string {
			return strings.TrimSuffix(record.Name, ".")
		},
		Warning:      "⚠️ IMPORTANT: Set proxy status to DNS only (gray cloud) for SSL validation records!",
		ProTip:       "After SSL is issued, you can enable the orange cloud (proxied) for better performance.",
		ProxyWarning: "🔴 SSL validation WILL FAIL if the cloud is orange! Keep it gray until certificate is issued.",
	},
	"godaddy": {
		Name:       "GoDaddy",
		Icon:       "🇬",
		RootFormat: "@",
		WwwFormat:  "www",
		SSLFormat: func(record dns.ValidationRecord) string {
			return strings.TrimSuffix(record.Name, ".")
		},
		Warning: "⚠️ Do not include trailing dots in the 'Points to' field.",
		ProTip:  "Use '@' for root domain, leave TTL as 1 hour.",
	},
	"route53": {
		Name:       "Route 53",
		Icon:       "📡",
		RootFormat: "@",
		WwwFormat:  "www",
		SSLFormat: func(record dns.ValidationRecord) string {
			return record.Name
		},
		Warning: "✅ Use Alias records (A type) for better performance!",
		ProTip:  "Route 53 handles validation automatically if domain is hosted here.",
	},
	"other": {
		Name:       "Other Provider",
		Icon:       "🌐",
		RootFormat: "@",
		WwwFormat:  "www",
		SSLFormat: func(record dns.ValidationRecord) string {
			return strings.TrimSuffix(record.Name, ".")
		},
		Warning: "⚠️ Check your provider's documentation for exact field names.",
		ProTip:  "Common field names: Host, Name, Alias, Points to.",
	},
}

// templateData is the struct passed into the HTML template — all fields are named, no %s juggling
type templateData struct {
	AppName           string
	Region            string
	DisplayDomain     string
	CloudFrontID      string
	CloudFrontDomain  string
	FunctionURL       string
	S3BucketName      string
	CertificateARN    string
	CertificateStatus string

	// Derived display fields
	CertStatusClass string // "success" | "pending" | "danger"
	CertStatusText  string
	RoutingStatus   string
	IsPending       bool
	IsIssued        bool

	// DNS provider
	ProviderIcon    string
	ProviderName    string
	ProviderWarning string
	ProviderProTip  string

	// SSL validation records (pre-formatted for display)
	ValidationRows []validationRow
	DNSNotice      template.HTML // safe HTML for the notice banner

	// Verify commands
	VerifyCmd1 string
	VerifyCmd2 string

	// Footer
	Version        string
	DeploymentTime string
}

type validationRow struct {
	Host    string
	Value   string
	Purpose string
}

var reportTemplate = template.Must(template.New("report").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NextDeploy | Deployment Report: {{.AppName}}</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Syne:wght@400;600;700;800&family=JetBrains+Mono:wght@400;700&display=swap" rel="stylesheet">
    <style>
        *, *::before, *::after { margin: 0; padding: 0; box-sizing: border-box; }

        :root {
            --bg:            #080910;
            --card:          #10121a;
            --card-hover:    #161923;
            --accent:        #4f46e5;
            --accent-glow:   rgba(79, 70, 229, 0.25);
            --accent-soft:   #6366f1;
            --text:          #e2e8f0;
            --text-muted:    #94a3b8;
            --text-dim:      #4b5563;
            --success:       #10b981;
            --success-bg:    rgba(16, 185, 129, 0.12);
            --success-glow:  rgba(16, 185, 129, 0.3);
            --warning:       #f59e0b;
            --warning-bg:    rgba(245, 158, 11, 0.12);
            --danger:        #ef4444;
            --danger-bg:     rgba(239, 68, 68, 0.12);
            --danger-glow:   rgba(239, 68, 68, 0.3);
            --border:        rgba(255, 255, 255, 0.06);
            --border-accent: rgba(79, 70, 229, 0.4);
            --surface:       rgba(255, 255, 255, 0.03);
        }

        body {
            background: var(--bg);
            color: var(--text);
            font-family: 'Syne', sans-serif;
            padding: 48px 24px 80px;
            display: flex;
            flex-direction: column;
            align-items: center;
            min-height: 100vh;
        }

        /* Subtle grid background */
        body::before {
            content: '';
            position: fixed;
            inset: 0;
            background-image:
                linear-gradient(rgba(79,70,229,0.03) 1px, transparent 1px),
                linear-gradient(90deg, rgba(79,70,229,0.03) 1px, transparent 1px);
            background-size: 48px 48px;
            pointer-events: none;
            z-index: 0;
        }

        .container {
            max-width: 1100px;
            width: 100%;
            position: relative;
            z-index: 1;
        }

        /* ─── Header ─────────────────────────────────────── */
        header {
            text-align: center;
            margin-bottom: 56px;
        }

        .brand {
            display: inline-flex;
            align-items: center;
            gap: 8px;
            font-size: 0.72rem;
            font-weight: 600;
            letter-spacing: 0.14em;
            text-transform: uppercase;
            color: var(--accent-soft);
            background: var(--accent-glow);
            border: 1px solid var(--border-accent);
            padding: 5px 14px;
            border-radius: 99px;
            margin-bottom: 20px;
        }

        h1 {
            font-size: clamp(2rem, 5vw, 3.5rem);
            font-weight: 800;
            letter-spacing: -0.03em;
            background: linear-gradient(135deg, #fff 0%, #a5b4fc 60%, #818cf8 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            line-height: 1.1;
        }

        .subtitle {
            margin-top: 12px;
            color: var(--text-muted);
            font-size: 0.95rem;
        }

        /* ─── DNS Notice Banner ───────────────────────────── */
        .notice-pending {
            background: var(--danger-bg);
            border: 1px solid var(--danger);
            border-radius: 16px;
            padding: 28px 32px;
            margin-bottom: 40px;
            display: flex;
            align-items: center;
            gap: 20px;
            animation: pulse-border 2s ease infinite;
        }

        .notice-issued {
            background: var(--success-bg);
            border: 1px solid var(--success);
            border-radius: 16px;
            padding: 28px 32px;
            margin-bottom: 40px;
            display: flex;
            align-items: center;
            gap: 20px;
        }

        @keyframes pulse-border {
            0%, 100% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.4); }
            50%       { box-shadow: 0 0 0 8px rgba(239, 68, 68, 0); }
        }

        .notice-icon { font-size: 2rem; flex-shrink: 0; }

        .notice-body strong {
            display: block;
            font-size: 1.05rem;
            font-weight: 700;
            margin-bottom: 4px;
        }

        .notice-body p { font-size: 0.88rem; color: var(--text-muted); }

        /* ─── DNS Guide ───────────────────────────────────── */
        .dns-guide {
            background: linear-gradient(135deg, #0f0e1f 0%, #141228 100%);
            border: 1px solid var(--border-accent);
            border-radius: 24px;
            padding: 40px;
            margin-bottom: 40px;
        }

        .dns-guide h2 {
            font-size: 1.4rem;
            font-weight: 700;
            margin-bottom: 28px;
            display: flex;
            align-items: center;
            gap: 10px;
        }

        .section-title {
            font-size: 1rem;
            font-weight: 700;
            margin: 32px 0 14px;
            display: flex;
            align-items: center;
            gap: 8px;
            color: #c7d2fe;
        }

        /* ─── Tables ──────────────────────────────────────── */
        .dns-table {
            width: 100%;
            border-collapse: collapse;
            border-radius: 12px;
            overflow: hidden;
            border: 1px solid var(--border);
            font-size: 0.85rem;
        }

        .dns-table thead tr { background: rgba(79, 70, 229, 0.15); }

        .dns-table th {
            padding: 10px 14px;
            text-align: left;
            font-size: 0.7rem;
            font-weight: 600;
            letter-spacing: 0.08em;
            text-transform: uppercase;
            color: var(--text-muted);
        }

        .dns-table td {
            padding: 12px 14px;
            border-top: 1px solid var(--border);
            color: var(--text-muted);
            vertical-align: middle;
        }

        .dns-table tr:hover td { background: var(--surface); }

        code {
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.8rem;
            background: rgba(0,0,0,0.4);
            padding: 3px 8px;
            border-radius: 6px;
            color: #a5b4fc;
            word-break: break-all;
        }

        /* ─── Alert boxes ─────────────────────────────────── */
        .alert {
            border-radius: 10px;
            padding: 14px 18px;
            font-size: 0.87rem;
            margin-top: 20px;
            line-height: 1.6;
        }

        .alert-warning {
            background: var(--warning-bg);
            border: 1px solid rgba(245,158,11,0.3);
            color: #fcd34d;
        }

        .alert-info {
            background: var(--accent-glow);
            border: 1px solid var(--border-accent);
            color: #c7d2fe;
        }

        .alert strong { font-weight: 700; }

        /* ─── Verify block ────────────────────────────────── */
        .verify-block {
            background: #000;
            border: 1px solid var(--border);
            border-radius: 12px;
            padding: 20px 24px;
            margin-top: 24px;
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.82rem;
            color: #6ee7b7;
            line-height: 1.9;
            white-space: pre-wrap;
        }

        .next-step {
            margin-top: 28px;
            background: rgba(250,204,21,0.08);
            border: 1px solid rgba(250,204,21,0.25);
            border-radius: 10px;
            padding: 16px 20px;
            font-size: 0.9rem;
            color: #fde68a;
            text-align: center;
        }

        /* ─── Resource cards grid ─────────────────────────── */
        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
            gap: 20px;
            margin-bottom: 40px;
        }

        .card {
            background: var(--card);
            border: 1px solid var(--border);
            border-radius: 18px;
            padding: 24px;
            transition: transform 0.25s ease, box-shadow 0.25s ease, border-color 0.25s ease;
            position: relative;
            overflow: hidden;
        }

        .card::after {
            content: '';
            position: absolute;
            inset: 0;
            border-radius: 18px;
            background: linear-gradient(135deg, var(--accent-glow) 0%, transparent 60%);
            opacity: 0;
            transition: opacity 0.3s ease;
        }

        .card:hover {
            transform: translateY(-4px);
            border-color: var(--border-accent);
            box-shadow: 0 16px 32px -8px rgba(0,0,0,0.6);
        }

        .card:hover::after { opacity: 1; }

        .card-label {
            font-size: 0.68rem;
            font-weight: 600;
            letter-spacing: 0.1em;
            text-transform: uppercase;
            color: var(--text-dim);
            margin-bottom: 6px;
        }

        .card-title {
            font-size: 1rem;
            font-weight: 700;
            color: #fff;
            margin-bottom: 16px;
            display: flex;
            align-items: center;
            gap: 8px;
        }

        .card-field { margin-bottom: 14px; }

        .card-field .field-name {
            font-size: 0.72rem;
            color: var(--text-dim);
            margin-bottom: 4px;
            letter-spacing: 0.04em;
        }

        .card-field code {
            display: block;
            padding: 8px 10px;
            font-size: 0.78rem;
        }

        .card-field a { color: #a5b4fc; text-decoration: none; }
        .card-field a:hover { text-decoration: underline; }

        /* Status dot */
        .dot {
            width: 9px;
            height: 9px;
            border-radius: 50%;
            display: inline-block;
            flex-shrink: 0;
        }

        .dot-success { background: var(--success); box-shadow: 0 0 8px var(--success-glow); }
        .dot-pending { background: var(--warning); box-shadow: 0 0 8px rgba(245,158,11,0.4); }
        .dot-danger  { background: var(--danger);  box-shadow: 0 0 8px var(--danger-glow); }

        /* ─── Footer ──────────────────────────────────────── */
        footer {
            text-align: center;
            font-size: 0.78rem;
            color: var(--text-dim);
            padding-top: 32px;
            border-top: 1px solid var(--border);
        }

        footer a { color: var(--accent-soft); text-decoration: none; }
        footer a:hover { text-decoration: underline; }

        @media (max-width: 680px) {
            .grid { grid-template-columns: 1fr; }
            .dns-guide { padding: 24px 20px; }
        }
    </style>
</head>
<body>
<div class="container">

    <!-- Header -->
    <header>
        <div class="brand">⚡ NextDeploy · Deployment Report</div>
        <h1>{{.AppName}}</h1>
        <p class="subtitle">Region: {{.Region}} · {{.DeploymentTime}}</p>
    </header>

    <!-- Notice Banner -->
    {{.DNSNotice}}

    <!-- DNS Guide (always shown) -->
    <div class="dns-guide">
        <h2>{{.ProviderIcon}} DNS Setup for {{.ProviderName}}</h2>

        <div class="section-title">🔐 1. SSL Validation Records (Required for HTTPS)</div>
        <table class="dns-table">
            <thead>
                <tr>
                    <th>Type</th>
                    <th>Host / Name</th>
                    <th>Value / Points To</th>
                    <th>Purpose</th>
                </tr>
            </thead>
            <tbody>
                {{if .ValidationRows}}
                    {{range .ValidationRows}}
                    <tr>
                        <td>CNAME</td>
                        <td><code>{{.Host}}</code></td>
                        <td><code>{{.Value}}</code></td>
                        <td>{{.Purpose}}</td>
                    </tr>
                    {{end}}
                {{else}}
                    <tr>
                        <td colspan="4" style="text-align:center;padding:28px;color:var(--text-muted);">
                            ✅ SSL Certificate already issued — no pending validation records.
                        </td>
                    </tr>
                {{end}}
            </tbody>
        </table>

        <div class="section-title">🎯 2. Traffic Routing Records</div>
        <table class="dns-table">
            <thead>
                <tr>
                    <th>Type</th>
                    <th>Host / Name</th>
                    <th>Value / Points To</th>
                    <th>Status</th>
                </tr>
            </thead>
            <tbody>
                <tr>
                    <td>CNAME</td>
                    <td><code>@ (Root)</code></td>
                    <td><code>{{.CloudFrontDomain}}</code></td>
                    <td>{{.RoutingStatus}}</td>
                </tr>
                <tr>
                    <td>CNAME</td>
                    <td><code>www</code></td>
                    <td><code>{{.CloudFrontDomain}}</code></td>
                    <td>{{.RoutingStatus}}</td>
                </tr>
            </tbody>
        </table>

        <div class="alert alert-warning">
            <strong>{{.ProviderName}} Warning:</strong> {{.ProviderWarning}}
        </div>

        <div class="alert alert-info">
            <strong>💡 Pro Tip:</strong> {{.ProviderProTip}}
        </div>

        {{if .VerifyCmd1}}
        <div class="section-title">🔍 Verify Your Records</div>
        <div class="verify-block">{{.VerifyCmd1}}{{if .VerifyCmd2}}
{{.VerifyCmd2}}{{end}}
# Expected output: an acm-validations.aws domain</div>
        {{end}}

        <div class="next-step">
            📅 After adding records, wait 5–10 minutes then run <code>nextdeploy ship</code> again to finish.
        </div>
    </div>

    <!-- Resource Cards -->
    <div class="grid">

        <div class="card">
            <div class="card-label">Edge Delivery</div>
            <div class="card-title">
                <span class="dot dot-{{.CertStatusClass}}"></span>
                CloudFront
            </div>
            <div class="card-field">
                <div class="field-name">Distribution ID</div>
                <code>{{.CloudFrontID}}</code>
            </div>
            <div class="card-field">
                <div class="field-name">Public Domain</div>
                <code><a href="https://{{.DisplayDomain}}" target="_blank">{{.DisplayDomain}}</a></code>
            </div>
        </div>

        <div class="card">
            <div class="card-label">Compute</div>
            <div class="card-title">
                <span class="dot dot-{{.CertStatusClass}}"></span>
                Lambda
            </div>
            <div class="card-field">
                <div class="field-name">Function Name</div>
                <code>{{.AppName}}</code>
            </div>
            <div class="card-field">
                <div class="field-name">Function URL</div>
                <code><a href="{{.FunctionURL}}" target="_blank">Open Endpoint ↗</a></code>
            </div>
        </div>

        <div class="card">
            <div class="card-label">Static Assets</div>
            <div class="card-title">
                <span class="dot dot-{{.CertStatusClass}}"></span>
                S3 Bucket
            </div>
            <div class="card-field">
                <div class="field-name">Bucket Name</div>
                <code>{{.S3BucketName}}</code>
            </div>
            <div class="card-field">
                <div class="field-name">Origin Access</div>
                <code>CloudFront OAC Enabled</code>
            </div>
        </div>

        <div class="card">
            <div class="card-label">Security</div>
            <div class="card-title">
                <span class="dot dot-{{.CertStatusClass}}"></span>
                ACM Certificate
            </div>
            <div class="card-field">
                <div class="field-name">Certificate ARN</div>
                <code>{{.CertificateARN}}</code>
            </div>
            <div class="card-field">
                <div class="field-name">Status</div>
                <code>{{.CertStatusText}}</code>
            </div>
        </div>

    </div>

    <!-- Footer -->
    <footer>
        <p>Generated by NextDeploy <strong>{{.Version}}</strong> · {{.DeploymentTime}}</p>
        <p style="margin-top:10px;">
            <a href="https://nextdeploy.one/docs" target="_blank">Documentation</a> ·
            <a href="https://github.com/Golangcodes/nextdeploy" target="_blank">GitHub</a> ·
            <a href="#" onclick="window.print();return false;">Print Report</a>
        </p>
    </footer>

</div>
</body>
</html>
`))

// GenerateResourceView creates a premium HTML report of the provisioned resources
func GenerateResourceView(appCfg *config.AppConfig, resMap ServerlessResourceMap) (string, error) {
	provider := getProviderRules(resMap.DNSProvider)

	// ── Cert status ────────────────────────────────────────────────────────────
	certStatusClass := "success"
	certStatusText := "✅ ISSUED"
	routingStatus := "✅ Live"

	switch resMap.CertificateStatus {
	case "PENDING_VALIDATION":
		certStatusClass = "pending"
		certStatusText = "⏳ PENDING VALIDATION"
		routingStatus = "⏳ Waiting for SSL"
	case "FAILED":
		certStatusClass = "danger"
		certStatusText = "❌ FAILED"
		routingStatus = "❌ Check DNS"
	}

	// ── DNS notice banner ──────────────────────────────────────────────────────
	var dnsNotice template.HTML
	if len(resMap.ValidationRecords) > 0 {
		dnsNotice = template.HTML(fmt.Sprintf(`
<div class="notice-pending">
    <div class="notice-icon">⚠️</div>
    <div class="notice-body">
        <strong>DNS Setup Required — Your app is NOT live yet</strong>
        <p>Add the %d DNS record(s) below, then re-run <code>nextdeploy ship</code>.</p>
    </div>
</div>`, len(resMap.ValidationRecords)))
	} else {
		domain := resMap.CustomDomain
		if domain == "" {
			domain = resMap.CloudFrontDomain
		}
		dnsNotice = template.HTML(fmt.Sprintf(`
<div class="notice-issued">
    <div class="notice-icon">✅</div>
    <div class="notice-body">
        <strong>SSL Certificate Issued — Your site is live!</strong>
        <p>Visit: <a href="https://%s" target="_blank" style="color:#6ee7b7;">https://%s</a></p>
    </div>
</div>`, domain, domain))
	}

	// ── Validation rows ────────────────────────────────────────────────────────
	var rows []validationRow
	for _, rec := range resMap.ValidationRecords {
		purpose := "Root Domain Validation"
		if strings.Contains(rec.Name, ".www") {
			purpose = "WWW Subdomain Validation"
		}
		rows = append(rows, validationRow{
			Host:    provider.SSLFormat(rec),
			Value:   rec.Value,
			Purpose: purpose,
		})
	}

	// ── Verify commands ────────────────────────────────────────────────────────
	verifyCmd1, verifyCmd2 := "", ""
	if len(resMap.ValidationRecords) > 0 {
		verifyCmd1 = fmt.Sprintf("dig @8.8.8.8 %s CNAME +short",
			strings.TrimSuffix(resMap.ValidationRecords[0].Name, "."))
		if len(resMap.ValidationRecords) > 1 {
			verifyCmd2 = fmt.Sprintf("dig @8.8.8.8 %s CNAME +short",
				strings.TrimSuffix(resMap.ValidationRecords[1].Name, "."))
		}
	}

	displayDomain := resMap.CustomDomain
	if displayDomain == "" {
		displayDomain = resMap.CloudFrontDomain
	}

	// ── Build template data ────────────────────────────────────────────────────
	data := templateData{
		AppName:          resMap.AppName,
		Region:           resMap.Region,
		DisplayDomain:    displayDomain,
		CloudFrontID:     resMap.CloudFrontID,
		CloudFrontDomain: resMap.CloudFrontDomain,
		FunctionURL:      resMap.FunctionURL,
		S3BucketName:     resMap.S3BucketName,
		CertificateARN:   resMap.CertificateARN,
		CertStatusClass:  certStatusClass,
		CertStatusText:   certStatusText,
		RoutingStatus:    routingStatus,
		IsPending:        resMap.CertificateStatus == "PENDING_VALIDATION",
		IsIssued:         resMap.CertificateStatus == "ISSUED",
		ProviderIcon:     provider.Icon,
		ProviderName:     provider.Name,
		ProviderWarning:  provider.Warning,
		ProviderProTip:   provider.ProTip,
		ValidationRows:   rows,
		DNSNotice:        dnsNotice,
		VerifyCmd1:       verifyCmd1,
		VerifyCmd2:       verifyCmd2,
		Version:          shared.Version,
		DeploymentTime:   resMap.DeploymentTime.Format("Mon Jan 2 15:04:05 MST 2006"),
	}

	// ── Render ─────────────────────────────────────────────────────────────────
	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render report template: %w", err)
	}

	htmlContent := buf.Bytes()

	// ── Write files ────────────────────────────────────────────────────────────
	timestamp := resMap.DeploymentTime.Format("20060102-150405")
	reportPath := fmt.Sprintf("%s-%s-report.html", resMap.AppName, timestamp)

	if err := os.WriteFile(reportPath, htmlContent, 0644); err != nil {
		return "", fmt.Errorf("failed to write report: %w", err)
	}

	// Also keep a "latest" copy
	latestPath := fmt.Sprintf("%s-latest.html", resMap.AppName)
	_ = os.WriteFile(latestPath, htmlContent, 0644)

	return reportPath, nil
}

// getProviderRules returns DNS provider rules, falling back to "other"
func getProviderRules(provider string) ProviderRules {
	if rules, exists := DNSProviderRules[provider]; exists {
		return rules
	}
	return DNSProviderRules["other"]
}

// GenerateQuickReference creates a markdown quick reference
func GenerateQuickReference(resMap ServerlessResourceMap) string {
	var sb strings.Builder

	sb.WriteString("# NextDeploy DNS Quick Reference\n\n")
	sb.WriteString(fmt.Sprintf("Domain: **%s**\n", resMap.CustomDomain))
	sb.WriteString(fmt.Sprintf("CloudFront: **%s**\n\n", resMap.CloudFrontDomain))

	sb.WriteString("## Routing Records\n")
	sb.WriteString("```\n")
	sb.WriteString(fmt.Sprintf("@ → %s\n", resMap.CloudFrontDomain))
	sb.WriteString(fmt.Sprintf("www → %s\n", resMap.CloudFrontDomain))
	sb.WriteString("```\n\n")

	if len(resMap.ValidationRecords) > 0 {
		sb.WriteString("## SSL Validation Records\n")
		sb.WriteString("```\n")
		for _, rec := range resMap.ValidationRecords {
			sb.WriteString(fmt.Sprintf("%s → %s\n", rec.Name, rec.Value))
		}
		sb.WriteString("```\n")
	}

	return sb.String()
}
