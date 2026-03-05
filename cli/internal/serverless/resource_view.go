package serverless

import (
	"fmt"
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

// DNSProviderRules maps provider names to their specific instructions
var DNSProviderRules = map[string]struct {
	Name         string
	Icon         string
	RootFormat   string
	WwwFormat    string
	SSLFormat    func(record dns.ValidationRecord) string
	Warning      string
	ProTip       string
	ProxyWarning string
}{
	"namecheap": {
		Name:       "Namecheap",
		Icon:       "🧢",
		RootFormat: "@",
		WwwFormat:  "www",
		SSLFormat: func(record dns.ValidationRecord) string {
			if strings.Contains(record.Name, ".www") {
				// For www validation: keep the .www
				return record.Name
			}
			// For root validation: just the hash
			return strings.Split(record.Name, ".")[0]
		},
		Warning: "⚠️ **CRITICAL**: NEVER include your domain name in the Host field! Use only the hash or '@'.",
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
		Warning:      "⚠️ **IMPORTANT**: Set proxy status to DNS only (gray cloud) for SSL validation records!",
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

// GenerateResourceView creates a premium HTML report of the provisioned resources
func GenerateResourceView(appCfg *config.AppConfig, resMap ServerlessResourceMap) (string, error) {
	provider := getProviderRules(resMap.DNSProvider)

	template := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NextDeploy | Deployment Report: %s</title>
    <script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;700&family=JetBrains+Mono:wght@400;700&display=swap" rel="stylesheet">
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }

        :root {
            --bg: #0a0b10;
            --bg-gradient: linear-gradient(135deg, #0a0b10 0%, #15171e 100%);
            --card: #15171e;
            --card-hover: #1e1f2b;
            --accent: #4f46e5;
            --accent-glow: rgba(79, 70, 229, 0.3);
            --accent-soft: #6366f1;
            --text: #e2e8f0;
            --text-muted: #94a3b8;
            --text-dim: #64748b;
            --success: #10b981;
            --success-glow: rgba(16, 185, 129, 0.3);
            --warning: #f59e0b;
            --danger: #ef4444;
            --danger-glow: rgba(239, 68, 68, 0.3);
            --border: rgba(255, 255, 255, 0.05);
            --border-hover: rgba(79, 70, 229, 0.3);
        }

        body {
            background: var(--bg);
            background: var(--bg-gradient);
            color: var(--text);
            font-family: 'Outfit', sans-serif;
            margin: 0;
            padding: 40px 20px;
            display: flex;
            flex-direction: column;
            align-items: center;
            min-height: 100vh;
            line-height: 1.6;
        }

        .container {
            max-width: 1200px;
            width: 100%%;
        }

        header {
            text-align: center;
            margin-bottom: 50px;
            position: relative;
        }

        header::after {
            content: '';
            position: absolute;
            bottom: -20px;
            left: 50%%;
            transform: translateX(-50%%);
            width: 100px;
            height: 2px;
            background: linear-gradient(90deg, transparent, var(--accent), transparent);
        }

        h1 {
            font-size: 3rem;
            margin: 0;
            background: linear-gradient(135deg, #fff 0%%, #4f46e5 50%%, #818cf8 100%%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            font-weight: 700;
            letter-spacing: -0.02em;
        }

        .badge {
            display: inline-block;
            padding: 6px 16px;
            border-radius: 99px;
            font-size: 0.8rem;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            background: var(--accent-glow);
            color: #c7d2fe;
            border: 1px solid var(--accent);
            margin-top: 15px;
            backdrop-filter: blur(4px);
        }

        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
            gap: 24px;
            margin-top: 40px;
        }

        .card {
            background: var(--card);
            border: 1px solid var(--border);
            border-radius: 20px;
            padding: 24px;
            transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
            position: relative;
            overflow: hidden;
            backdrop-filter: blur(10px);
        }

        .card::before {
            content: '';
            position: absolute;
            top: 0;
            left: 0;
            right: 0;
            height: 2px;
            background: linear-gradient(90deg, transparent, var(--accent), transparent);
            transform: translateX(-100%%);
            transition: transform 0.6s ease;
        }

        .card:hover {
            transform: translateY(-6px);
            background: var(--card-hover);
            border-color: var(--border-hover);
            box-shadow: 0 20px 30px -15px rgba(0, 0, 0, 0.7);
        }

        .card:hover::before {
            transform: translateX(100%%);
        }

        .card h3 {
            margin: 0 0 16px 0;
            font-size: 1.1rem;
            display: flex;
            align-items: center;
            gap: 10px;
            color: #fff;
            font-weight: 600;
        }

        .card p {
            margin: 8px 0;
            font-size: 0.9rem;
            color: var(--text-muted);
        }

        .mono {
            font-family: 'JetBrains Mono', monospace;
            background: rgba(0,0,0,0.4);
            padding: 8px 12px;
            border-radius: 8px;
            font-size: 0.85rem;
            word-break: break-all;
            display: block;
            margin-top: 6px;
            color: #a5b4fc;
            border: 1px solid rgba(255,255,255,0.05);
            transition: all 0.2s;
        }

        .mono:hover {
            background: rgba(79, 70, 229, 0.1);
            border-color: var(--accent);
        }

        .diagram {
            background: var(--card);
            border: 1px solid var(--border);
            border-radius: 24px;
            padding: 40px;
            margin-top: 40px;
            min-height: 400px;
            position: relative;
            overflow: hidden;
        }

        .diagram::after {
            content: '';
            position: absolute;
            top: -50%%;
            left: -50%%;
            width: 200%%;
            height: 200%%;
            background: radial-gradient(circle at center, var(--accent-glow) 0%%, transparent 70%%);
            opacity: 0.1;
            animation: rotate 20s linear infinite;
        }

        @keyframes rotate {
            from { transform: rotate(0deg); }
            to { transform: rotate(360deg); }
        }

        .footer {
            margin-top: 80px;
            text-align: center;
            color: var(--text-dim);
            font-size: 0.8rem;
            padding: 20px;
            border-top: 1px solid var(--border);
        }

        .status-dot {
            width: 10px;
            height: 10px;
            background: var(--success);
            border-radius: 50%%;
            display: inline-block;
            box-shadow: 0 0 15px var(--success-glow);
            margin-right: 8px;
            position: relative;
        }

        .status-dot.pending {
            background: var(--warning);
            box-shadow: 0 0 15px rgba(245, 158, 11, 0.3);
        }

        .status-dot.danger {
            background: var(--danger);
            box-shadow: 0 0 15px var(--danger-glow);
        }

        .dns-guide {
            background: linear-gradient(135deg, #1e1b4b 0%%, #312e81 100%%);
            border: 2px solid var(--accent);
            border-radius: 24px;
            padding: 40px;
            margin: 40px 0;
            position: relative;
            overflow: hidden;
        }

        .dns-guide::before {
            content: '';
            position: absolute;
            top: -20px;
            right: -20px;
            width: 200px;
            height: 200px;
            background: radial-gradient(circle, var(--accent-glow) 0%%, transparent 70%%);
            border-radius: 50%%;
            opacity: 0.3;
        }

        .loud-notice {
            background: var(--danger);
            color: #fff;
            padding: 24px 32px;
            border-radius: 16px;
            font-weight: 800;
            margin-bottom: 32px;
            display: flex;
            flex-direction: column;
            align-items: center;
            gap: 12px;
            animation: pulse-danger 2s infinite;
            text-align: center;
            border: 2px solid #fff;
            position: relative;
            z-index: 10;
        }

        @keyframes pulse-danger {
            0%% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.7); }
            70%% { box-shadow: 0 0 0 20px rgba(239, 68, 68, 0); }
            100%% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0); }
        }

        .dns-table {
            width: 100%%;
            border-collapse: collapse;
            margin: 20px 0 30px;
            background: rgba(0,0,0,0.3);
            border-radius: 16px;
            overflow: hidden;
            border: 1px solid var(--border);
        }

        .dns-table th {
            background: rgba(79, 70, 229, 0.2);
            color: var(--text);
            font-size: 0.8rem;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            padding: 16px;
            text-align: left;
            font-weight: 600;
        }

        .dns-table td {
            padding: 16px;
            border-bottom: 1px solid var(--border);
            font-family: 'JetBrains Mono', monospace;
            font-size: 0.8rem;
            color: #fff;
        }

        .dns-table tr:last-child td {
            border-bottom: none;
        }

        .dns-table tr:hover td {
            background: rgba(79, 70, 229, 0.1);
        }

        .provider-badge {
            display: inline-flex;
            align-items: center;
            gap: 6px;
            background: rgba(255,255,255,0.05);
            padding: 4px 12px;
            border-radius: 99px;
            font-size: 0.75rem;
            margin-left: 10px;
            border: 1px solid var(--border);
        }

        .warning-box {
            background: rgba(245, 158, 11, 0.1);
            border-left: 4px solid var(--warning);
            padding: 16px 24px;
            margin: 20px 0;
            border-radius: 0 8px 8px 0;
            color: #fcd34d;
        }

        .tip-box {
            background: rgba(16, 185, 129, 0.1);
            border-left: 4px solid var(--success);
            padding: 16px 24px;
            margin: 20px 0;
            border-radius: 0 8px 8px 0;
            color: #6ee7b7;
        }

        .propagation-timeline {
            display: grid;
            grid-template-columns: repeat(4, 1fr);
            gap: 10px;
            margin: 20px 0;
            text-align: center;
        }

        .timeline-item {
            background: rgba(255,255,255,0.03);
            padding: 12px;
            border-radius: 8px;
            border: 1px solid var(--border);
        }

        .timeline-item .time {
            font-size: 1.1rem;
            font-weight: 700;
            color: var(--accent);
            display: block;
        }

        .timeline-item .label {
            font-size: 0.7rem;
            color: var(--text-muted);
        }

        .verification-command {
            background: #0d0f14;
            padding: 16px 20px;
            border-radius: 12px;
            font-family: 'JetBrains Mono', monospace;
            border: 1px solid var(--border);
            margin: 20px 0;
            position: relative;
        }

        .verification-command::before {
            content: '$';
            position: absolute;
            left: -10px;
            top: 50%%;
            transform: translateY(-50%%);
            color: var(--accent);
            font-size: 1.2rem;
        }

        .copy-btn {
            background: var(--accent);
            color: #fff;
            border: none;
            padding: 4px 12px;
            border-radius: 4px;
            font-size: 0.7rem;
            cursor: pointer;
            margin-left: 10px;
            transition: all 0.2s;
        }

        .copy-btn:hover {
            background: var(--accent-soft);
            transform: scale(1.05);
        }

        @media (max-width: 768px) {
            h1 { font-size: 2rem; }
            .dns-guide { padding: 20px; }
            .propagation-timeline { grid-template-columns: repeat(2, 1fr); }
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>🚀 Deployment Map</h1>
            <div class="badge">
                <span class="status-dot %s"></span>
                %s
            </div>
            <p style="color: var(--text-muted); margin-top: 20px;">
                Resources provisioned for <strong>%s</strong> in <strong>%s</strong>
            </p>
        </header>

        <div class="diagram" id="diagram-container">
            <div class="mermaid">
                graph TB
                    subgraph T ["🌐 Traffic Layer"]
                        U["👤 User"] --> CF["📡 CloudFront<br/><small>%s</small>"]
                    end

                    subgraph CS ["⚡ Compute & Storage Layer"]
                        CF --> L["🔷 Lambda<br/><small>%s</small>"]
                        L --> S3["📦 S3 Assets<br/><small>%s</small>"]
                        L --> SM["🔐 Secrets Manager"]
                    end

                    subgraph S ["🔒 Security Layer"]
                        ACM["📜 SSL Certificate"] -.-> CF
                    end

                    style T fill:#1e1b4b,stroke:#4f46e5,stroke-width:2px
                    style CS fill:#15171e,stroke:#334155,stroke-width:2px
                    style S fill:#064e3b,stroke:#10b981,stroke-width:2px
                    style U fill:#2d3748,stroke:#718096
                    style CF fill:#4f46e5,stroke:#6366f1
                    style L fill:#4f46e5,stroke:#6366f1
            </div>
        </div>

        <div class="dns-guide">
            %s

            <h2 style="font-size: 1.8rem; margin-bottom: 10px;">%s %s DNS Setup</h2>
            <p style="color: var(--text-muted); margin-bottom: 30px; font-size: 1.1rem;">
                Follow these exact steps for %s to make your site live.
            </p>

            <div class="warning-box">
                <strong>⏱️ DNS Propagation Timeline:</strong>
                <div class="propagation-timeline">
                    <div class="timeline-item">
                        <span class="time">⚡</span>
                        <span class="label">Your Provider</span>
                    </div>
                    <div class="timeline-item">
                        <span class="time">5-30m</span>
                        <span class="label">Google DNS</span>
                    </div>
                    <div class="timeline-item">
                        <span class="time">5-30m</span>
                        <span class="label">Cloudflare</span>
                    </div>
                    <div class="timeline-item">
                        <span class="time">24-48h</span>
                        <span class="label">Worldwide</span>
                    </div>
                </div>
            </div>

            <h3 style="font-size: 1.3rem; margin: 30px 0 15px;">🔐 1. SSL Validation Records (Required for HTTPS)</h3>
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
                    %s
                </tbody>
            </table>

            <h3 style="font-size: 1.3rem; margin: 30px 0 15px;">🎯 2. Traffic Routing Records</h3>
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
                        <td><code>@</code> (Root)</td>
                        <td><code>%s</code></td>
                        <td>%s</td>
                    </tr>
                    <tr>
                        <td>CNAME</td>
                        <td><code>www</code></td>
                        <td><code>%s</code></td>
                        <td>%s</td>
                    </tr>
                </tbody>
            </table>

            <div class="warning-box">
                <strong>%s Notice</strong>
                <p style="margin-top: 8px;">%s</p>
            </div>

            <div class="tip-box">
                <strong>💡 %s Pro Tip:</strong>
                <p style="margin-top: 8px;">%s</p>
            </div>

            <h3 style="font-size: 1.1rem; margin: 30px 0 15px;">🔍 Verify Your Records</h3>
            <div class="verification-command">
                # Check SSL validation record 1<br>
                dig @8.8.8.8 %s CNAME +short<br><br>
                # Check SSL validation record 2<br>
                dig @8.8.8.8 %s CNAME +short<br><br>
                # Expected output: acm-validations.aws domain
            </div>

            <p style="font-size: 1rem; color: #facc15; margin-top: 30px; padding: 15px; background: rgba(250, 204, 21, 0.1); border-radius: 8px; text-align: center;">
                <strong>📅 After adding records, wait 5-10 minutes and run <code style="background: #000; padding: 4px 8px; border-radius: 4px;">nextdeploy ship</code> again to finish.</strong>
            </p>
        </div>

        <div class="grid">
            <div class="card">
                <h3><span class="status-dot %s"></span> Edge Delivery</h3>
                <p>CloudFront Distribution ID</p>
                <code class="mono">%s</code>
                <p>Public Domain</p>
                <code class="mono"><a href="https://%s" target="_blank" style="color: #a5b4fc;">%s</a></code>
            </div>

            <div class="card">
                <h3><span class="status-dot %s"></span> Compute (Lambda)</h3>
                <p>Function Name</p>
                <code class="mono">%s</code>
                <p>Function URL</p>
                <code class="mono"><a href="%s" target="_blank" style="color: #a5b4fc;">Lambda Endpoint</a></code>
            </div>

            <div class="card">
                <h3><span class="status-dot %s"></span> Static Assets</h3>
                <p>S3 Bucket Name</p>
                <code class="mono">%s</code>
                <p>Origin Access</p>
                <code class="mono">CloudFront OAC Enabled</code>
            </div>

            <div class="card">
                <h3><span class="status-dot %s"></span> Security</h3>
                <p>ACM Certificate</p>
                <code class="mono">%s</code>
                <p>Certificate Status</p>
                <code class="mono">%s</code>
            </div>
        </div>

        <div class="footer">
            <p>Generated by NextDeploy <strong>%s</strong> | %s</p>
            <p style="margin-top: 8px;">
                <a href="https://nextdeploy.one/docs" target="_blank" style="color: var(--accent);">Documentation</a> •
                <a href="https://github.com/Golangcodes/nextdeploy" target="_blank" style="color: var(--accent);">GitHub</a> •
                <a href="#" onclick="window.print();return false;" style="color: var(--accent);">Print Report</a>
            </p>
        </div>
    </div>

    <script>
        // Initialize Mermaid with better error handling
        try {
            mermaid.initialize({
                theme: 'dark',
                startOnLoad: true,
                securityLevel: 'loose',
                flowchart: {
                    useMaxWidth: true,
                    htmlLabels: true,
                    curve: 'basis'
                },
                themeVariables: {
                    'primaryColor': '#4f46e5',
                    'primaryTextColor': '#fff',
                    'primaryBorderColor': '#6366f1',
                    'lineColor': '#4f46e5',
                    'secondaryColor': '#15171e',
                    'tertiaryColor': '#10b981'
                }
            });

            // Add copy functionality
            document.querySelectorAll('.copy-btn').forEach(btn => {
                btn.addEventListener('click', function() {
                    const target = this.dataset.target;
                    const text = document.querySelector(target).innerText;
                    navigator.clipboard.writeText(text);
                    this.innerText = 'Copied!';
                    setTimeout(() => {
                        this.innerText = 'Copy';
                    }, 2000);
                });
            });

        } catch (e) {
            console.error('Mermaid failed to initialize:', e);
            document.getElementById('diagram-container').innerHTML =
                '<div style="text-align:center;color:#94a3b8;padding:60px;">' +
                '<span style="font-size:2rem;">📊</span>' +
                '<p>Diagram failed to load. Resources are still provisioned!</p>' +
                '</div>';
        }
    </script>
</body>
</html>`

	// Determine certificate status styling
	certStatusClass := "success"
	certStatusText := "ISSUED"
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

	// Build SSL validation table rows
	var validationRows string
	if len(resMap.ValidationRecords) == 0 {
		validationRows = `<tr><td colspan="4" style="text-align:center;padding:30px;color:var(--text-muted)">✅ SSL Certificate Issued! No pending validation records.</td></tr>`
	} else {
		for _, rec := range resMap.ValidationRecords {
			purpose := "Root Domain Validation"
			hostValue := provider.SSLFormat(rec)
			if strings.Contains(rec.Name, ".www") {
				purpose = "WWW Subdomain Validation"
			}

			validationRows += fmt.Sprintf(`<tr>
				<td>CNAME</td>
				<td><code>%s</code></td>
				<td><code style="font-size:0.7rem">%s</code></td>
				<td>%s</td>
			</tr>`, hostValue, rec.Value, purpose)
		}
	}

	// Build DNS notice with provider-specific warnings
	dnsNotice := ""
	if len(resMap.ValidationRecords) > 0 {
		dnsNotice = fmt.Sprintf(`<div class="loud-notice">
			<span style="font-size: 1.5rem;">⚠️ CRITICAL: DNS SETUP REQUIRED ⚠️</span>
			<span style="font-size: 1rem;">Your application is NOT live until you add these %d DNS records.</span>
		</div>`, len(resMap.ValidationRecords))
	} else {
		dnsNotice = `<div class="loud-notice" style="background: #10b981;">
			<span style="font-size: 1.5rem;">✅ SSL CERTIFICATE ISSUED!</span>
			<span style="font-size: 1rem;">Your site should be live at https://` + resMap.CustomDomain + `</span>
		</div>`
	}

	// Get first and second validation record names for verification commands
	verifyCmd1 := ""
	verifyCmd2 := ""
	if len(resMap.ValidationRecords) > 0 {
		verifyCmd1 = strings.TrimSuffix(resMap.ValidationRecords[0].Name, ".") + "." + resMap.CustomDomain
		if len(resMap.ValidationRecords) > 1 {
			verifyCmd2 = strings.TrimSuffix(resMap.ValidationRecords[1].Name, ".") + "." + resMap.CustomDomain
		}
	}

	displayDomain := resMap.CustomDomain
	if displayDomain == "" {
		displayDomain = resMap.CloudFrontDomain
	}

	htmlContent := fmt.Sprintf(template,
		resMap.AppName,
		certStatusClass,
		certStatusText,
		resMap.AppName,
		resMap.Region,
		displayDomain,
		resMap.AppName,
		resMap.S3BucketName,
		dnsNotice,
		provider.Icon,
		provider.Name,
		provider.Name,
		validationRows,
		resMap.CloudFrontDomain,
		routingStatus,
		resMap.CloudFrontDomain,
		routingStatus,
		provider.Name,
		provider.Warning,
		provider.Name,
		provider.ProTip,
		verifyCmd1,
		verifyCmd2,
		certStatusClass,
		resMap.CloudFrontID,
		displayDomain,
		displayDomain,
		certStatusClass,
		resMap.AppName,
		resMap.FunctionURL,
		certStatusClass,
		resMap.S3BucketName,
		certStatusClass,
		resMap.CertificateARN,
		certStatusText,
		shared.Version,
		resMap.DeploymentTime.Format("Mon Jan 2 15:04:05 MST 2006"),
	)

	// Save in current working directory as requested
	timestamp := resMap.DeploymentTime.Format("20060102-150405")
	reportPath := fmt.Sprintf("%s-%s-report.html", resMap.AppName, timestamp)

	if err := os.WriteFile(reportPath, []byte(htmlContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write report: %w", err)
	}

	// Also save a latest copy in the project root
	latestPath := fmt.Sprintf("%s-latest.html", resMap.AppName)
	_ = os.WriteFile(latestPath, []byte(htmlContent), 0644)

	return reportPath, nil
}

// Helper function to get provider rules
func getProviderRules(provider string) struct {
	Name         string
	Icon         string
	RootFormat   string
	WwwFormat    string
	SSLFormat    func(dns.ValidationRecord) string
	Warning      string
	ProTip       string
	ProxyWarning string
} {
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
