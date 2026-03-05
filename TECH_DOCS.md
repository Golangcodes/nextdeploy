# NextDeploy Technical Documentation

This unified guide provides everything you need to know about NextDeploy's architecture, resource mapping, and contribution standards.

## Table of Contents
1. [🏗️ Project Map](#project-map)
2. [📖 Contributor Guide](#contributor-guide)
3. [🗺️ Resource Mapping](#resource-mapping)
4. [🛠️ Developer Checklist](#developer-checklist)

---

<a name="project-map"></a>
## 🏗️ Project Map

This map helps new contributors navigate the NextDeploy codebase.

### 📦 `cli/` (The User Interface)
The entry point for everyone using NextDeploy. Written in Go using Cobra.
- `cli/main.go`: The root entry point.
- `cli/cmd/`: All CLI commands (ship, logs, destroy, etc.) are defined here.
- `cli/internal/serverless/`: The orchestration engine for AWS deployments.

### 🛡️ `daemon/` (The Server Brain)
The long-running agent that lives on your VPS.
- `daemon/cmd/nextdeployd/`: Entry point for the daemon binary.
- `daemon/internal/daemon/`: Handlers for commands (ship, logs, status) and process management.
- `daemon/internal/server/`: The mTLS-secured server that listens for CLI commands.

### 🧠 `shared/` (The Common Knowledge)
Critical packages shared by both the CLI and Daemon.
- `shared/nextcore/`: **Crucial.** Analyzes Next.js projects, detects features, and generates CSP.
- `shared/caddy/`: Logic for generating Caddyfiles with WAF and mTLS.
- `shared/logger/`: Unified logging system for the entire ecosystem.

---

<a name="contributor-guide"></a>
## 📖 Contributor Guide

### 1. System Architecture
NextDeploy is a hybrid deployment platform composed of three primary layers (CLI, Daemon, Shared) as outlined in the Project Map.

### 2. Core Mechanisms
#### NextCore & `metadata.json`
NextDeploy doesn't just copy files; it *understands* the app. `shared/nextcore` scans `next.config.{js,mjs}` to detect feature flags and build hardened CSPs.

#### Communication Security
1. **mTLS**: Mutual TLS for authorized clients.
2. **HMAC Signing**: All commands are signed using a `security_secret` to prevent replays.

#### Zero-Downtime Static Assets
- New releases go to `releases/<timestamp>`.
- Assets are synced to a `shared_static` directory.
- Caddy serves `/_next/static/*` from this shared root so old client sessions never break.

### 3. Deployment Flows
#### VPS Flow (`nextdeploy ship`)
CLI packages build -> Uploads tarball -> Daemon extracts -> Service starts -> Health check -> Symlink update -> Caddy reload.

#### Serverless Flow
CLI uploads to S3 -> Updates Lambda -> Configures CloudFront -> ACM/OAC Management.

---

<a name="resource-mapping"></a>
## 🗺️ Resource Mapping

### 1. VPS (Linux Server) Mapping
- **`/opt/nextdeploy`**: Root directory ([command_handler.go:L22](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L22)).
- **`/opt/nextdeploy/apps/<app>/current`**: Active release symlink ([command_handler.go:L406-410](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L406-410)).
- **`/opt/nextdeploy/apps/<app>/shared_static/`**: **Persistent** asset storage ([command_handler.go:L414-429](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L414-429)).
- **`/etc/systemd/system/nextdeploy-*.service`**: Process management ([process_manager.go:L25-110](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/process_manager.go#L25-110)).
- **`/etc/caddy/nextdeploy.d/<app>.caddy`**: Caddy config ([caddy_manager.go:L30-41](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/caddy_manager.go#L30-41)).

### 2. AWS (Serverless) Mapping
- **Storage**: S3 Buckets for assets & packages ([aws_s3.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_s3.go)).
- **Compute**: Lambda Functions ([aws_lambda.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_lambda.go)).
- **Delivery**: CloudFront gateway ([aws_cloudfront.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_cloudfront.go)).
- **Security**: IAM Roles, ACM Certs, Secrets Manager.

---

<a name="developer-checklist"></a>
## 🛠️ Developer Checklist
1. Read the **System Architecture** section above.
2. Check **Resource Mapping** before making infrastructure changes.
3. Build locally using:
   ```bash
   go build -o nextdeploy ./cli/main.go
   go build -o nextdeployd ./daemon/cmd/nextdeployd
   ```
