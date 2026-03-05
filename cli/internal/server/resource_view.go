package server

import (
	"fmt"
	"os"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
)

// VPSResourceMap holds the metadata for the visual report
type VPSResourceMap struct {
	AppName        string
	Environment    string
	ServerIP       string
	CustomDomain   string
	Port           int
	DeploymentTime time.Time
	DNSProvider    string // "namecheap", "cloudflare", "godaddy", "route53", "other"
}

// GenerateVPSResourceView creates a premium HTML report of the provisioned resources
func GenerateVPSResourceView(appCfg *config.AppConfig, resMap VPSResourceMap) (string, error) {
	// Re-using DNSProvider rules from serverless (internally they should be shared but for now we'll use a local logic or default)
	providerName := "other"
	if resMap.DNSProvider != "" {
		providerName = resMap.DNSProvider
	}

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
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root {
            --bg: #0a0b10;
            --bg-gradient: linear-gradient(135deg, #0a0b10 0%%, #15171e 100%%);
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

        .container { max-width: 1200px; width: 100%%; }

        header { text-align: center; margin-bottom: 50px; position: relative; }
        header::after {
            content: ''; position: absolute; bottom: -20px; left: 50%%; transform: translateX(-50%%);
            width: 100px; height: 2px; background: linear-gradient(90deg, transparent, var(--accent), transparent);
        }

        h1 {
            font-size: 3rem; margin: 0;
            background: linear-gradient(135deg, #fff 0%%, #4f46e5 50%%, #818cf8 100%%);
            -webkit-background-clip: text; -webkit-text-fill-color: transparent;
            font-weight: 700; letter-spacing: -0.02em;
        }

        .badge {
            display: inline-block; padding: 6px 16px; border-radius: 99px;
            font-size: 0.8rem; font-weight: 600; text-transform: uppercase;
            letter-spacing: 0.05em; background: var(--accent-glow); color: #c7d2fe;
        }

        .container {
            max-width: 1200px;
            margin: 0 auto;
            padding: 60px 20px;
        }

        header {
            margin-bottom: 50px;
            display: flex;
            justify-content: space-between;
            align-items: flex-end;
        }

        .brand-group h1 {
            font-size: 3rem;
            font-weight: 800;
            background: linear-gradient(to right, #fff, #94a3b8);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            letter-spacing: -0.04em;
        }

        .status-badge {
            padding: 8px 18px;
            border-radius: 99px;
            font-size: 0.8rem;
            font-weight: 700;
            background: rgba(139, 92, 246, 0.1);
            color: var(--primary);
            border: 1px solid var(--primary-glow);
            box-shadow: 0 0 20px var(--primary-glow);
        }

        .bento-grid {
            display: grid;
            grid-template-columns: repeat(12, 1fr);
            grid-gap: 24px;
        }

        .card {
            background: var(--surface);
            backdrop-filter: var(--glass);
            -webkit-backdrop-filter: var(--glass);
            border: 1px solid var(--surface-border);
            border-radius: 32px;
            padding: 32px;
            transition: all 0.5s cubic-bezier(0.16, 1, 0.3, 1);
        }

        .card:hover { border-color: rgba(255,255,255,0.2); transform: translateY(-5px); }

        .card-main { grid-column: span 8; }
        .card-side { grid-column: span 4; }
        .card-full { grid-column: span 12; }

        .label {
            font-size: 0.75rem;
            font-weight: 700;
            color: var(--text-muted);
            text-transform: uppercase;
            letter-spacing: 0.15em;
            margin-bottom: 12px;
            display: block;
        }

        .hero-value {
            font-size: 2.5rem;
            font-weight: 800;
            color: var(--text-main);
            margin-bottom: 15px;
        }

        .mono { font-family: 'JetBrains Mono', monospace; font-size: 1rem; color: var(--accent); }

        .diagram-container {
            margin-top: 30px;
            background: rgba(0,0,0,0.3);
            border-radius: 20px;
            padding: 20px;
            border: 1px solid rgba(255,255,255,0.05);
        }

        .dns-table {
            width: 100%%;
            border-collapse: collapse;
        }

        .dns-table th {
            text-align: left;
            font-size: 0.7rem;
            color: var(--text-muted);
            padding: 15px;
            border-bottom: 1px solid var(--surface-border);
        }

        .dns-table td {
            padding: 18px 15px;
            font-size: 0.95rem;
            border-bottom: 1px solid rgba(255,255,255,0.05);
        }

        .footer {
            margin-top: 80px;
            text-align: center;
            color: var(--text-muted);
            font-size: 0.9rem;
            opacity: 0.6;
        }

        @media (max-width: 1024px) {
            .card-main, .card-side { grid-column: span 12; }
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="brand-group">
                <span class="label">Production Instance Ready</span>
                <h1>%s</h1>
            </div>
            <div class="status-badge">Instance Running</div>
        </header>

        <div class="bento-grid">
            <!-- Access Section -->
            <div class="card card-main">
                <span class="label">Live URL</span>
                <div class="hero-value"><a href="https://%s" style="color: inherit; text-decoration: none;">https://%s</a></div>
                
                <div class="diagram-container">
                    <span class="label">Infrastructure Flow</span>
                    <div class="mermaid">
                        graph LR
                            U["👤 User"] -- "HTTPS (Port 443)" --> C["🏗️ Caddy Edge"]
                            subgraph VPS ["Target Server: %s"]
                                C -- "Internal Proxy" --> APP["🚀 %s (Port %d)"]
                            end
                            style VPS fill:rgba(255,255,255,0.02),stroke:var(--primary),stroke-dasharray: 5 5
                            style C fill:var(--primary),color:#fff
                            style APP fill:var(--accent),color:#000
                    </div>
        </div>

        <div class="grid">
            <div class="card">
                <h3><span class="status-dot"></span> Server Info</h3>
                <p>Public IP Address</p>
                <code class="mono">%s</code>
                <p>Environment</p>
                <code class="mono">%s</code>
            </div>

            <div class="card">
                <h3><span class="status-dot"></span> Application</h3>
                <p>Internal Port</p>
                <code class="mono">%d</code>
                <p>Public Domain</p>
                <code class="mono"><a href="https://%s" target="_blank" style="color: #a5b4fc;">%s</a></code>
            </div>

            <div class="card">
                <h3><span class="status-dot"></span> Security</h3>
                <p>SSL Provider</p>
                <code class="mono">Caddy Automatic (Let's Encrypt)</code>
                <p>Cert Status</p>
                <code class="mono">Pending First Visit</code>
            </div>
        </div>

        <div class="footer">
            <p>Generated by NextDeploy <strong>%s</strong> | %s</p>
            <p style="margin-top: 8px;">
                <a href="https://nextdeploy.one/docs" target="_blank">Documentation</a> • 
                <a href="https://github.com/Golangcodes/nextdeploy" target="_blank">GitHub</a>
            </p>
        </div>
    </div>

    <script>
        try {
            mermaid.initialize({
                theme: 'dark',
                startOnLoad: true,
                securityLevel: 'loose',
                flowchart: { useMaxWidth: true, htmlLabels: true, curve: 'basis' },
                themeVariables: {
                    'primaryColor': '#4f46e5',
                    'primaryTextColor': '#fff',
                    'lineColor': '#4f46e5',
                    'secondaryColor': '#15171e',
                    'tertiaryColor': '#10b981'
                }
            });
        } catch (e) {
            console.error('Mermaid failed:', e);
            document.getElementById('diagram-container').innerHTML = '<p style="text-align:center;color:#94a3b8;padding:60px;">Diagram failed to load.</p>';
        }
    </script>
</body>
</html>`

	htmlContent := fmt.Sprintf(template,
		resMap.AppName,
		resMap.Environment,
		resMap.AppName,
		resMap.ServerIP,
		resMap.Port,
		providerName,
		resMap.ServerIP,
		resMap.ServerIP,
		resMap.CustomDomain,
		resMap.ServerIP,
		resMap.Environment,
		resMap.Port,
		resMap.CustomDomain,
		resMap.CustomDomain,
		shared.Version,
		resMap.DeploymentTime.Format("Mon Jan 2 15:04:05 MST 2006"),
	)

	// Save in current working directory as requested
	timestamp := resMap.DeploymentTime.Format("20060102-150405")
	reportPath := fmt.Sprintf("%s-vps-%s-report.html", resMap.AppName, timestamp)

	if err := os.WriteFile(reportPath, []byte(htmlContent), 0644); err != nil {
		return "", err
	}

	// Latest copy in project root
	latestPath := fmt.Sprintf("%s-vps-latest.html", resMap.AppName)
	_ = os.WriteFile(latestPath, []byte(htmlContent), 0644)

	return reportPath, nil
}
