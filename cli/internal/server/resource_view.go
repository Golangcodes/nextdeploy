package server

import (
	"fmt"
	"os"
	"path/filepath"
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
}

// GenerateVPSResourceView creates a premium HTML report of the provisioned resources
func GenerateVPSResourceView(appCfg *config.AppConfig, resMap VPSResourceMap) (string, error) {
	template := `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NextDeploy | Deployment Report: %s</title>
    <script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;700&family=JetBrains+Mono&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg: #0a0b10;
            --card: #15171e;
            --accent: #4f46e5;
            --accent-glow: rgba(79, 70, 229, 0.4);
            --text: #e2e8f0;
            --text-muted: #94a3b8;
            --success: #10b981;
            --border: rgba(255, 255, 255, 0.05);
        }

        body {
            background: var(--bg);
            color: var(--text);
            font-family: 'Outfit', sans-serif;
            margin: 0;
            padding: 40px 20px;
            display: flex;
            flex-direction: column;
            align-items: center;
            min-height: 100vh;
        }

        .container {
            max-width: 1000px;
            width: 100%%;
        }

        header {
            text-align: center;
            margin-bottom: 50px;
        }

        h1 {
            font-size: 2.5rem;
            margin: 0;
            background: linear-gradient(135deg, #fff 0%%, #4f46e5 100%%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            font-weight: 700;
        }

        .badge {
            display: inline-block;
            padding: 4px 12px;
            border-radius: 99px;
            font-size: 0.75rem;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.05em;
            background: var(--accent-glow);
            color: #c7d2fe;
            margin-top: 10px;
        }

        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
            gap: 20px;
            margin-top: 40px;
        }

        .card {
            background: var(--card);
            border: 1px solid var(--border);
            border-radius: 16px;
            padding: 24px;
            transition: transform 0.2s, box-shadow 0.2s;
            position: relative;
            overflow: hidden;
        }

        .card:hover {
            transform: translateY(-4px);
            box-shadow: 0 12px 24px -10px rgba(0, 0, 0, 0.5);
            border-color: rgba(79, 70, 229, 0.3);
        }

        .card h3 {
            margin: 0 0 16px 0;
            font-size: 1.1rem;
            display: flex;
            align-items: center;
            gap: 10px;
            color: #fff;
        }

        .card p {
            margin: 8px 0;
            font-size: 0.9rem;
            color: var(--text-muted);
        }

        .mono {
            font-family: 'JetBrains Mono', monospace;
            background: rgba(0,0,0,0.3);
            padding: 4px 8px;
            border-radius: 4px;
            font-size: 0.8rem;
            word-break: break-all;
            display: block;
            margin-top: 4px;
            color: #a5b4fc;
        }

        .diagram {
            background: var(--card);
            border: 1px solid var(--border);
            border-radius: 16px;
            padding: 40px;
            margin-top: 40px;
            min-height: 300px;
        }

        .footer {
            margin-top: 60px;
            text-align: center;
            color: var(--text-muted);
            font-size: 0.8rem;
        }

        .status-dot {
            width: 8px;
            height: 8px;
            background: var(--success);
            border-radius: 50%%;
            display: inline-block;
            box-shadow: 0 0 10px var(--success);
            margin-right: 6px;
        }

        .dns-guide {
            background: linear-gradient(135deg, #1e1b4b 0%%, #312e81 100%%);
            border: 2px solid var(--accent);
            border-radius: 16px;
            padding: 32px;
            margin-top: 40px;
            box-shadow: 0 0 30px rgba(79, 70, 229, 0.2);
        }

        .dns-guide h2 {
            margin: 0 0 20px 0;
            color: #fff;
            display: flex;
            align-items: center;
            gap: 12px;
        }

        .dns-table {
            width: 100%%;
            border-collapse: collapse;
            margin-top: 20px;
            background: rgba(0,0,0,0.2);
            border-radius: 8px;
            overflow: hidden;
        }

        .dns-table th, .dns-table td {
            padding: 16px;
            text-align: left;
            border-bottom: 1px solid rgba(255,255,255,0.05);
        }

        .dns-table th {
            background: rgba(255,255,255,0.05);
            color: var(--text-muted);
            font-size: 0.8rem;
            text-transform: uppercase;
        }

        .dns-table td {
            font-family: 'JetBrains Mono', monospace;
            color: #fff;
        }

        .loud-notice {
            background: #ef4444;
            color: #fff;
            padding: 16px 24px;
            border-radius: 8px;
            font-weight: 800;
            margin-bottom: 24px;
            display: flex;
            flex-direction: column;
            align-items: center;
            gap: 8px;
            animation: pulse-red 2s infinite;
            text-align: center;
            border: 2px solid #fff;
        }

        @keyframes pulse-red {
            0%% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0.7); }
            70%% { box-shadow: 0 0 0 15px rgba(239, 68, 68, 0); }
            100%% { box-shadow: 0 0 0 0 rgba(239, 68, 68, 0); }
        }

        a {
            color: var(--accent);
            text-decoration: none;
        }
        a:hover {
            text-decoration: underline;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <h1>Deployment Map (VPS)</h1>
            <div class="badge">%s</div>
            <p style="color: var(--text-muted); margin-top: 15px;">Resources provisioned for <strong>%s</strong> on <strong>%s</strong></p>
        </header>

        <div class="diagram" id="diagram-container">
            <div class="mermaid">
                graph LR
                    subgraph T ["Traffic"]
                        U["User"] --> C["Caddy (SSL)"]
                    end
                    subgraph V ["VPS Server"]
                        C --> N["Next.js App (Port: %d)"]
                        N --> DB["Local/Remote DB"]
                    end

                    style T fill:#1e1b4b,stroke:#4f46e5,color:#fff
                    style V fill:#15171e,stroke:#334155,color:#fff
            </div>
        </div>

        <div class="dns-guide">
            <div class="loud-notice">
                <span style="font-size: 1.2rem;">⚠️ CRITICAL: DNS SETUP REQUIRED ⚠️</span>
                <span style="font-size: 0.9rem; font-weight: 400;">Your application is NOT live until you complete these steps.</span>
            </div>
            <h2>🌐 Step-by-Step DNS Guide</h2>
            <p style="color: var(--text-muted); margin-bottom: 20px;">
                Point your domain to your VPS server IP to enable automatic SSL and traffic routing.
            </p>
            
            <table class="dns-table">
                <thead>
                    <tr>
                        <th>Type</th>
                        <th>Host / Name</th>
                        <th>Value / Points To</th>
                    </tr>
                </thead>
                <tbody>
                    <tr>
                        <td>A Record</td>
                        <td>@ (Root)</td>
                        <td style="color: var(--accent)">%s</td>
                    </tr>
                    <tr>
                        <td>CNAME</td>
                        <td>www</td>
                        <td style="color: var(--accent)">%s</td>
                    </tr>
                </tbody>
            </table>
            
            <div style="background: rgba(79, 70, 229, 0.1); border-left: 4px solid var(--accent); padding: 20px; margin-top: 30px; border-radius: 0 8px 8px 0;">
                <p style="margin: 0; color: #fff; font-weight: 600;">💡 Caddy Automatic SSL</p>
                <p style="margin: 10px 0 0 0; font-size: 0.9rem; color: var(--text-muted);">
                    Once DNS propagates, Caddy will automatically provision a Let's Encrypt certificate on the first request.
                </p>
            </div>

            <p style="font-size: 0.9rem; color: #facc15; margin-top: 30px; font-weight: 600;">
                📅 Propagation usually takes 5-10 minutes.
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
                <code class="mono"><a href="https://%s" target="_blank">%s</a></code>
            </div>
        </div>

        <div class="footer">
            Generated by NextDeploy %s | %s
        </div>
    </div>

    <script shadow>
        try {
            mermaid.initialize({ 
                theme: 'dark',
                startOnLoad: true,
                securityLevel: 'loose',
                themeVariables: {
                    primaryColor: '#4f46e5',
                    primaryTextColor: '#fff',
                    lineColor: '#4f46e5',
                    secondaryColor: '#15171e',
                    tertiaryColor: '#10b981'
                }
            });
        } catch (e) {
            console.error('Mermaid failed to initialize:', e);
            document.getElementById('diagram-container').innerHTML = '<p style="text-align:center;color:#94a3b8;padding:40px;">Diagram failed to load. Check console for details.</p>';
        }
    </script>
</body>
</html>
`

	htmlContent := fmt.Sprintf(template,
		resMap.AppName,
		resMap.Environment,
		resMap.AppName,
		resMap.ServerIP,
		resMap.Port,
		resMap.ServerIP,
		resMap.CustomDomain,
		resMap.ServerIP,
		resMap.Environment,
		resMap.Port,
		resMap.CustomDomain,
		resMap.CustomDomain,
		shared.Version,
		resMap.DeploymentTime.Format(time.RFC1123),
	)

	reportPath := filepath.Join(os.Getenv("HOME"), ".nextdeploy", "reports", fmt.Sprintf("%s-report.html", resMap.AppName))
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		return "", err
	}

	if err := os.WriteFile(reportPath, []byte(htmlContent), 0644); err != nil {
		return "", err
	}

	return reportPath, nil
}
