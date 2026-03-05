# NextDeploy Contributor Guide

Welcome to the NextDeploy core team! This document provides the deep technical context you need to contribute to the codebase. 

## 1. System Architecture

NextDeploy is a hybrid deployment platform composed of three primary layers:

### A. CLI (`cli/`)
The entry point for the user. It handles:
- **Project Discovery**: Uses `shared/nextcore` to analyze Next.js projects.
- **Packaging**: Archives the build into a `.tar.gz`.
- **Orchestration**: Communicates with the VPS Daemon or AWS SDK for Serverless targets.
- **Diagnostics**: Aggregates logs via SSH or cloud APIs.

### B. Daemon (`daemon/`)
The agent running on the VPS. It manages:
- **Deployment Lifecycle**: Unpacks releases, manages systemd services, and updates symlinks.
- **Caddy Integration**: Dynamically generates and reloads Caddyfiles with WAF and mTLS support.
- **Persistent Statics**: Merges new build assets into a shared root to prevent chunk load errors.
- **State Management**: Tracks ports and active releases in `/var/lib/nextdeployd/state.json`.

### C. Shared (`shared/`)
Common logic used by both CLI and Daemon:
- **NextCore**: The "brain" that detects features (YouTube, Stripe, etc.) and builds the `metadata.json`.
- **Logging**: Unified colorized logging system.
- **Caddy**: The DSL generator for Caddy server configurations.

---

## 2. Core Mechanisms

### NextCore & `metadata.json`
NextDeploy doesn't just copy files; it *understands* the app. `shared/nextcore` scans `next.config.{js,mjs}` and the project structure to detect:
- **Feature Flags**: Does the app use YouTube? Google Fonts? Stripe?
- **CSP Auto-Generation**: Builds a hardened Content Security Policy based on the detected features.
- **Build Mode**: Detects if the app is a standard Next.js build or a static export (`output: 'export'`).

### Communication Security
The CLI and Daemon communicate over a secure channel:
1. **mTLS**: Mutual TLS ensures only authorized CLI clients can talk to the Daemon.
2. **HMAC Signing**: Every command payload is signed using a `security_secret`. The Daemon rejects any command with an invalid signature, preventing replay attacks.
3. **Socket/IP Whitelisting**: The Daemon can be restricted to specific IPs or Unix Sockets.

### Zero-Downtime Static Assets
To solve the common "Failed to load chunk" error in Next.js:
- New releases are unpacked into `releases/<timestamp>`.
- The `.next/static` assets are synced (via `cp -rn`) into a `shared_static` directory.
- Caddy serves `/_next/static/*` from `shared_static`, so old client sessions can still find their JS chunks even after a new deployment.

---

## 3. Deployment Flows

### VPS Flow (`nextdeploy ship`)
1. CLI prepares local build.
2. CLI uploads tarball to `/opt/nextdeploy/uploads`.
3. CLI sends `ship` command to Daemon.
4. Daemon extracts tarball, allocates a unique port, and starts a systemd service.
5. Daemon health-checks the port.
6. Daemon updates the `current` symlink and reloads Caddy.

### Serverless Flow
1. CLI detects Serverless target.
2. CLI uses `cli/internal/serverless` (AWS Provider) to:
   - Upload assets to S3.
   - Deploy/Update Lambda functions.
   - Configure CloudFront with mTLS and custom aliases.
   - Perform ACM certificate cleanup and OAC management.

---

## 4. Contributing

### Repository Structure
```bash
.
├── cli/           # CLI source code
│   ├── cmd/       # Cobra command definitions
│   └── internal/  # CLI internal logic (AWS, log aggregator)
├── daemon/        # VPS Daemon source code
│   └── internal/  # Command handlers, process management
├── shared/        # Shared packages (NextCore, Caddy logic)
├── docs/          # Project documentation
└── .agents/       # AI agent workflows
```

### Building for Local Testing
```bash
# Build CLI
go build -o nextdeploy ./cli/main.go

# Build Daemon
go build -o nextdeployd ./daemon/cmd/nextdeployd
```

### Security Standards
- **CSP**: Always use `shared/nextcore` for CSP generation. Do not hardcode `unsafe-eval` unless a detected feature strictly requires it.
- **Permissions**: The Daemon runs as `root` to manage systemd, but child processes run as the `nextdeploy` user. Ensure all file operations respect this boundary.

---

## 5. Development Principles
1. **Premium DX**: Every command should output clear, colorized, and actionable feedback.
2. **Deep Clean**: The `destroy` command must leave zero residue.
3. **Resiliency**: Assume the network will fail; implement retries and state-recovery for long-running tasks.
