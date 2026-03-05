package serverless

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
)

// ServerlessResourceMap holds the metadata for the visual report
type ServerlessResourceMap struct {
	AppName          string
	Environment      string
	Region           string
	LambdaARN        string
	FunctionURL      string
	S3BucketName     string
	CloudFrontID     string
	CloudFrontDomain string
	CustomDomain     string
	CertificateARN   string
	DeploymentTime   time.Time
}

// GenerateResourceView creates a premium HTML report of the provisioned resources
func GenerateResourceView(appCfg *config.AppConfig, resMap ServerlessResourceMap) (string, error) {
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
            <h1>Deployment Map</h1>
            <div class="badge">%s</div>
            <p style="color: var(--text-muted); margin-top: 15px;">Resources provisioned for <strong>%s</strong> in <strong>%s</strong></p>
        </header>

        <div class="diagram" id="diagram-container">
            <div class="mermaid">
                graph LR
                    subgraph T ["Traffic"]
                        U["User"] --> CF["CloudFront: %s"]
                    end
                    subgraph CS ["Compute & Storage"]
                        CF --> L["Lambda: %s"]
                        L --> S3["Assets: %s"]
                        L --> SM["Secrets Manager"]
                    end
                    subgraph S ["Security"]
                        ACM["SSL Certificate"] -.-> CF
                    end

                    style T fill:#1e1b4b,stroke:#4f46e5,color:#fff
                    style CS fill:#15171e,stroke:#334155,color:#fff
                    style S fill:#064e3b,stroke:#10b981,color:#fff
            </div>
        </div>

        <div class="grid">
            <div class="card">
                <h3><span class="status-dot"></span> Edge Delivery</h3>
                <p>CloudFront Distribution</p>
                <code class="mono">%s</code>
                <p>Public Domain</p>
                <code class="mono"><a href="https://%s" target="_blank">%s</a></code>
            </div>

            <div class="card">
                <h3><span class="status-dot"></span> Compute (Lambda)</h3>
                <p>Function Name</p>
                <code class="mono">%s</code>
                <p>Internal URL</p>
                <code class="mono"><a href="%s" target="_blank">Lambda Endpoint</a></code>
            </div>

            <div class="card">
                <h3><span class="status-dot"></span> Static Assets</h3>
                <p>S3 Bucket Name</p>
                <code class="mono">%s</code>
                <p>Origin Access</p>
                <code class="mono">CloudFront OAC Enabled</code>
            </div>

            <div class="card">
                <h3><span class="status-dot"></span> Security</h3>
                <p>ACM Certificate</p>
                <code class="mono">%s</code>
                <p>Secrets Engine</p>
                <code class="mono">AWS Secrets Manager</code>
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

	// Fill template
	displayDomain := resMap.CustomDomain
	if displayDomain == "" {
		displayDomain = resMap.CloudFrontDomain
	}

	htmlContent := fmt.Sprintf(template,
		resMap.AppName,
		resMap.Environment,
		resMap.AppName,
		resMap.Region,
		displayDomain,
		resMap.AppName,
		resMap.S3BucketName,
		resMap.CloudFrontID,
		displayDomain,
		displayDomain,
		resMap.AppName,
		resMap.FunctionURL,
		resMap.S3BucketName,
		resMap.CertificateARN,
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
