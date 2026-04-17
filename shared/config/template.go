package config

import (
	"os"
	"path/filepath"
)

const commonHeader = `
# ==============================
# NEXTDEPLOY CONFIGURATION FILE
# ==============================
# This YAML defines everything needed to build, deploy, monitor, and scale your app on a VPS/SERVERLESS using NextDeploy.
# Think of it as your infrastructure-as-code for end-to-end delivery.

# NOTE: DO NOT ADD YOUR SECRETS AS OF NOW WE WORKING ON SECRET MANAGMENT THIS IS HOW WE INTENT TO USE
version: "1.0" # Config file versioning for forward compatibility with future NextDeploy updates
`

const commonFooter = `
# -----
# LOGGING CONFIGURATION
# -----
logging:
  enabled: true # Enable logging system
  provider: nextdeploy # Use NextDeploy's internal logging daemon (alternatively: syslog, logtail, etc.)
  stream_logs: true # Send live container logs to dashboard (tail -f equivalent)
  log_path: /var/log/containers/example-app.log # Path on server where logs are persisted

# -----
# MONITORING & ALERTING
# -----
monitoring:
  enabled: true # Enables resource monitoring for CPU, memory, disk
  cpu_threshold: 80 # Alert if CPU usage goes over 80%
  memory_threshold: 75 # Alert if memory usage exceeds 75%
  disk_threshold: 90 # Alert if disk usage crosses 90%
  alert:
    email: ops@example.com # Email to send alerts to
    slack_webhook: https://hooks.slack.com/services/... # Slack channel webhook for real-time alerting
    notify_on:
      - crash # App/container crash
      - healthcheck_failed # Failed /api/health checks
      - high_cpu
      - high_memory

# -----
# BACKUP STRATEGY
# -----
backup:
  enabled: true # Enable automatic backups
  frequency: daily # Options: hourly | daily | weekly
  retention_days: 7 # Keep backups for 7 days
  storage:
    provider: s3 # Use S3-compatible storage (AWS S3, MinIO, Wasabi, etc.)
    bucket: nextdeploy-backups # S3 bucket name
    region: us-east-1 # AWS region

# -----
# WEBHOOKS AFTER DEPLOYMENT
# -----
webhook:
  on_success:
    - curl -X POST https://your-api.com/deploy/success # Notify external system (e.g., Slack, Discord, CI dashboard)
  on_failure:
    - curl -X POST https://your-api.com/deploy/failure # Used for alerting, logging, or rollback triggers
`

const vpsTemplate = `
# -----
# TARGET TYPE — choose between "vps" (traditional server) or "serverless" (AWS Lambda + S3 + CloudFront)
# -----
target_type: vps

# -----
# APP METADATA
# -----
app:
  name: example-app # [REQUIRED] Unique app name used for identification
  environment: production # [REQUIRED] production | staging | development
  domain: app.example.com # Public domain for your app
  port: 3000 # [REQUIRED] Internal port your app listens on

# -----
# DEPLOYMENT SERVERS
# -----
servers:
  - name: "production-01" # [REQUIRED] Friendly name for the server
    host: 1.2.3.4 # [REQUIRED] IP or hostname of the server
    username: ubuntu # [REQUIRED] SSH user (e.g., ubuntu, debian, root)
    key_path: ~/.ssh/id_rsa  # [REQUIRED] Path to your private SSH key
    # password: "" # Optional: SSH password (key_path takes precedence)
`

const serverlessTemplate = `
# -----
# TARGET TYPE — choose between "vps" (traditional server) or "serverless" (AWS Lambda + S3 + CloudFront)
# -----
target_type: serverless

# -----
# APP METADATA
# -----
app:
  name: example-app # [REQUIRED] Unique app name used for identification
  environment: production # [REQUIRED] production | staging | development
  domain: app.example.com # Public domain for your app
  port: 3000 # [REQUIRED] Internal port your app listens on

# -----
# CLOUD PROVIDER — RECOMMENDED: USE LOCAL AWS PROFILE
# -----
CloudProvider:
  name: aws
  region: us-east-1
  # access_key: "YOUR_ACCESS_KEY" # Optional: overridden by profile if set
  # secret_key: "YOUR_SECRET_KEY" # Optional: overridden by profile if set
  profile: "default"            # Recommended: uses credentials from aws configure

# -----
# SERVERLESS CONFIGURATION
# -----
serverless:
  provider: aws
  region: us-east-1
  profile: "default"           # AWS CLI profile name
  isrRevalidation: true        # Enable ISR cache listener Lambda via SQS
  imageOptimization: true      # Enable on-the-fly Image Resization Lambda via CloudFront
  warmer: true                 # Keep the Lambda warm
  cloudfront_id: "" # [OPTIONAL] If provided, NextDeploy will trigger an invalidation after deploy
  # iam_role: "arn:aws:iam::ACCOUNT_ID:role/nextdeploy-serverless-role" # [OPTIONAL] Created automatically if not provided
  # handler: "server.handler" # [OPTIONAL] Lambda handler (defaults to server.handler)
  # runtime: "nodejs20.x"    # [OPTIONAL] Lambda runtime (defaults to nodejs20.x)
  # memory_size: 1024        # [OPTIONAL] Memory in MB (defaults to 1024)
  # timeout: 30              # [OPTIONAL] Timeout in seconds (defaults to 30)
`

const cloudflareTemplate = `
# -----
# TARGET TYPE — "serverless" covers both AWS (Lambda + CloudFront) and Cloudflare (Workers + R2)
# -----
target_type: serverless

# -----
# APP METADATA
# -----
app:
  name: example-app # [REQUIRED] Unique app name used for identification
  environment: production # [REQUIRED] production | staging | development
  domain: app.example.com # Public domain for your app
  port: 3000 # [REQUIRED] Internal port your app listens on (used locally; Workers ignore this)

# -----
# CLOUD PROVIDER — CLOUDFLARE
# Credentials are read from env vars (recommended) or the credstore.
# Required env vars:
#   CLOUDFLARE_API_TOKEN  — management plane (Workers, DNS, R2 bucket lifecycle)
#   CLOUDFLARE_ACCOUNT_ID — can also be set via account_id below
#   R2_ACCESS_KEY_ID      — R2 object uploads (S3-compatible)
#   R2_SECRET_ACCESS_KEY  — R2 object uploads (S3-compatible)
# -----
CloudProvider:
  name: cloudflare
  account_id: "YOUR_CLOUDFLARE_ACCOUNT_ID" # Fallback if CLOUDFLARE_ACCOUNT_ID env var is unset

# -----
# SERVERLESS CONFIGURATION (Cloudflare Workers)
# -----
serverless:
  provider: cloudflare
  cloudflare:
    compatibility_date: "2025-04-01"
    compatibility_flags:
      - nodejs_compat_v2
    # custom_domains:
    #   - hostname: app.example.com
    # triggers:
    #   crons:
    #     - "0 * * * *"
    # bindings:
    #   r2:
    #     - name: ASSETS
    # Full schema: bindings (r2, kv, hyperdrive, queues, vectorize, ai, durable_objects),
    # migrations, and resources (hyperdrive, queues, vectorize, ai_gateway, dns, zone_settings).
`

func GetSampleConfigTemplate(targetType string) string {
	switch targetType {
	case "serverless":
		return commonHeader + serverlessTemplate + commonFooter
	case "cloudflare":
		return commonHeader + cloudflareTemplate + commonFooter
	default:
		return commonHeader + vpsTemplate + commonFooter
	}
}

func GenerateSampleConfig() error {
	// Write the sample config to nextdeploy.yml in the current directory
	path := filepath.Join(".", "nextdeploy.yml")
	return os.WriteFile(path, []byte(GetSampleConfigTemplate("vps")), 0600)
}
