package config

import (
	"os"
	"path/filepath"
)

const sampleConfig = `
# ==============================
# NEXTDEPLOY CONFIGURATION FILE
# ==============================
# This YAML defines everything needed to build, deploy, monitor, and scale your app on a VPS/SERVERlESS using NextDeploy.
# Think of it as your infrastructure-as-code for end-to-end delivery.

# NOTE: DO NOT ADD YOUR SECRETS AS OF NOW WE WORKING ON SECRET MANAGMENT THIS IS HOW WE INTENT TO USE
version: "1.0" # Config file versioning for forward compatibility with future NextDeploy updates

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
    host: 192.0.2.123 # [REQUIRED] IP or hostname of the server
    username: ubuntu # [REQUIRED] SSH user (e.g., ubuntu, debian, root)
    key_path: ~/.ssh/id_rsa # [REQUIRED] Path to your private SSH key
    # password: "" # Optional: SSH password (key_path takes precedence)

# -----
# DATABASE CONFIG
# -----
database:
  type: postgres # Supported: postgres | mysql
  host: 192.0.2.124 # IP of your database server (managed or self-hosted)
  port: 5432
  username: dbuser
  password: secret
  name: exampledb
  migrate_on_deploy: true # Run database migrations automatically after deployment

# Example:
#   - Use Amazon RDS or DigitalOcean Managed PostgreSQL as the database host.
#   - Schema is updated with migration tools like Goose, Flyway, or Prisma.

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

# Example:
#   - If your Go server crashes due to panic, or memory spikes over 75%, you get a Slack alert.
#   - Alerts also help you pre-emptively scale or investigate.

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
    access_key: YOUR_ACCESS_KEY
    secret_key: YOUR_SECRET_KEY

# Example:
#   - Database and volume data backed up to S3 every day.
#   - Automatically deleted after 7 days unless extended.


# -----
# WEBHOOKS AFTER DEPLOYMENT
# -----
webhook:
  on_success:
    - curl -X POST https://your-api.com/deploy/success # Notify external system (e.g., Slack, Discord, CI dashboard)
  on_failure:
    - curl -X POST https://your-api.com/deploy/failure # Used for alerting, logging, or rollback triggers

# Example:
#   - You can hook this into Notion, Linear, Jira, Slack, or even a custom dashboard.
#   - Also useful for CI/CD chaining (e.g., notify QA team that staging is ready).

## CLOUD PROVIDER instructions

CloudProvider:
  name: aws # Supported: aws | gcp | azure | digitalocean
  region: us-north-1 # AWS region (e.g., us-east-1, eu-west-1)
  access_key: YOUR_AWS_ACCESS_KEY # IAM user access key
  secret_key: YOUR_AWS_SECRET_KEY # IAM user secret key
`

func GetSampleConfigTemplate() string {
	return sampleConfig
}

func GenerateSampleConfig() error {
	// Write the sample config to sample.nextdeploy.yml in the current directory
	path := filepath.Join(".", "nextdeploy.yml")
	return os.WriteFile(path, []byte(sampleConfig), 0600)
}
