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
            border: 1px solid var(--accent); margin-top: 15px; backdrop-filter: blur(4px);
        }

        .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 24px; margin-top: 40px; }
        .card {
            background: var(--card); border: 1px solid var(--border); border-radius: 20px;
            padding: 24px; transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
            position: relative; overflow: hidden; backdrop-filter: blur(10px);
        }
        .card:hover {
            transform: translateY(-6px); background: var(--card-hover);
            border-color: var(--border-hover); box-shadow: 0 20px 30px -15px rgba(0, 0, 0, 0.7);
        }

        .card h3 { margin: 0 0 16px 0; font-size: 1.1rem; display: flex; align-items: center; gap: 10px; color: #fff; font-weight: 600; }
        .card p { margin: 8px 0; font-size: 0.9rem; color: var(--text-muted); }

        .mono {
            font-family: 'JetBrains Mono', monospace; background: rgba(0,0,0,0.4);
            padding: 8px 12px; border-radius: 8px; font-size: 0.85rem; word-break: break-all;
            display: block; margin-top: 6px; color: #a5b4fc; border: 1px solid rgba(255,255,255,0.05);
        }

        .diagram {
            background: var(--card); border: 1px solid var(--border); border-radius: 24px;
            padding: 40px; margin-top: 40px; min-height: 400px; position: relative; overflow: hidden;
        }

        .dns-guide {
            background: linear-gradient(135deg, #1e1b4b 0%%, #312e81 100%%);
            border: 2px solid var(--accent); border-radius: 24px; padding: 40px; margin: 40px 0;
            position: relative; overflow: hidden;
        }

        .loud-notice {
            background: var(--danger); color: #fff; padding: 24px 32px; border-radius: 16px;
            font-weight: 800; margin-bottom: 32px; display: flex; flex-direction: column;
            align-items: center; gap: 12px; animation: pulse-danger 2s infinite;
            text-align: center; border: 2px solid #fff; position: relative; z-index: 10;
        }

        @keyframes pulse-danger {
            0%% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.7); }
            70%% { box-shadow: 0 0 0 20px rgba(239, 68, 68, 0); }
            100%% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0); }
        }

        .dns-table {
            width: 100%%; border-collapse: collapse; margin: 20px 0 30px;
            background: rgba(0,0,0,0.3); border-radius: 16px; overflow: hidden; border: 1px solid var(--border);
        }
        .dns-table th { background: rgba(79, 70, 229, 0.2); color: var(--text); padding: 16px; text-align: left; font-weight: 600; font-size: 0.8rem; text-transform: uppercase; }
        .dns-table td { padding: 16px; border-bottom: 1px solid var(--border); font-family: 'JetBrains Mono', monospace; font-size: 0.8rem; color: #fff; }

        .status-dot { width: 10px; height: 10px; background: var(--success); border-radius: 50%%; display: inline-block; box-shadow: 0 0 15px var(--success-glow); margin-right: 8px; }

        .footer { margin-top: 80px; text-align: center; color: var(--text-dim); font-size: 0.8rem; padding: 20px; border-top: 1px solid var(--border); }
        a { color: var(--accent); text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>🚀 Deployment Map (VPS)</h1>
            <div class="badge">
                <span class="status-dot"></span>
                %s
            </div>
            <p style="color: var(--text-muted); margin-top: 20px;">
                Resources provisioned for <strong>%s</strong> on <strong>%s</strong>
            </p>
        </header>

        <div class="diagram" id="diagram-container">
            <div class="mermaid">
                graph TB
                    subgraph T ["🌐 Traffic Layer"]
                        U["👤 User"] --> C["🛡️ Caddy Server<br/><small>Edge Proxy + SSL</small>"]
                    end
                    
                    subgraph CS ["⚡ Application Layer"]
                        C --> N["🟢 Next.js App<br/><small>Port %d</small>"]
                        N --> DB["🗄️ Database"]
                    </div>

                    style T fill:#1e1b4b,stroke:#4f46e5,stroke-width:2px
                    style CS fill:#15171e,stroke:#334155,stroke-width:2px
                    style U fill:#2d3748,stroke:#718096
                    style C fill:#4f46e5,stroke:#6366f1
                    style N fill:#4f46e5,stroke:#6366f1
            </div>
        </div>

        <div class="dns-guide">
            <div class="loud-notice">
                <span style="font-size: 1.5rem;">⚠️ CRITICAL: DNS SETUP REQUIRED ⚠️</span>
                <span style="font-size: 1rem;">Your application is NOT live until you point your domain to this IP.</span>
            </div>

            <h2 style="font-size: 1.8rem; margin-bottom: 10px;">🌐 %s DNS Guide</h2>
            <p style="color: var(--text-muted); margin-bottom: 30px; font-size: 1.1rem;">
                Point your domain to **%s** using these records:
            </p>

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
                    <tr>
                        <td>A Record</td>
                        <td><code>@</code> (Root)</td>
                        <td><code>%s</code></td>
                        <td>Primary Traffic</td>
                    </tr>
                    <tr>
                        <td>CNAME</td>
                        <td><code>www</code></td>
                        <td><code>%s</code></td>
                        <td>WWW Redirection</td>
                    </tr>
                </tbody>
            </table>

            <div style="background: rgba(79, 70, 229, 0.1); border-left: 4px solid var(--accent); padding: 20px; border-radius: 0 8px 8px 0; color: #c7d2fe;">
                <strong>💡 Automatic SSL (TLS):</strong>
                <p style="margin-top: 8px; font-size: 0.9rem;">Once DNS propagates, Caddy will automatically provision a Let's Encrypt certificate on the first request. No extra steps needed!</p>
            </div>

            <p style="font-size: 1rem; color: #facc15; margin-top: 30px; padding: 15px; background: rgba(250, 204, 21, 0.1); border-radius: 8px; text-align: center;">
                <strong>📅 Propagation usually takes 5-10 minutes.</strong>
            </p>
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
