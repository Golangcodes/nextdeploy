---
title: NextDeploy
homepage: https://github.com/Golangcodes/nextdeploy
tagline: |
  Self-hosted Next.js deployment platform
description: |
  NextDeploy is an open-source CLI + daemon for deploying and managing Next.js applications on your own infrastructure. No lock-in. No magic. Just Docker, SSH, and full control.
---

## Cheat Sheet

> NextDeploy gives you Vercel-like deployment experience on your own VPS. Build, ship, and manage Next.js apps with full transparency and zero vendor lock-in.

### Quick Start

```sh
# Initialize a Next.js project for deployment
nextdeploy init

# Build production Docker image
nextdeploy build

# Test locally with production config
nextdeploy runimage

# Deploy to your VPS
nextdeploy ship

# Serve with automatic HTTPS
nextdeploy ship --serve
```

### Key Features

- **🧱 Builds** Docker images optimized for Next.js
- **🚀 Ships** to any VPS via SSH (Hetzner, DigitalOcean, AWS)
- **🔐 Injects secrets** securely with Doppler integration
- **📊 Streams logs & metrics** from running containers
- **🧪 Runimage** - test production builds locally with real secrets
- **🛠️ Daemon support** - health checks, logs, and automation

### Configuration

Create a `nextdeploy.yml` in your project root:

```yaml
version: "1.0"

app:
  name: my-app
  environment: production
  domain: app.example.com
  port: 3000

docker:
  image: username/my-app
  registry: ghcr.io
  push: true

deployment:
  server:
    host: 192.0.2.123
    user: deploy
    ssh_key: ~/.ssh/id_rsa
```

### Secret Management

NextDeploy is **Doppler-first** for zero `.env` file management:

```sh
# Secrets are injected at deploy/runtime
# Fully encrypted + scoped (dev/staging/prod)
# Update → restart → done
```

### Files

These are the files / directories that are created and/or modified with this install:

```text
~/.config/envman/PATH.env
~/.local/bin/nextdeploy
~/.local/opt/nextdeploy/
```

### Philosophy

Other platforms abstract until you lose control. **NextDeploy flips that.**

- You own the pipeline
- You see every step
- No black boxes
- No middleware
- Just you and your server
