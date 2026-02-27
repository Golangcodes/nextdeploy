---

# NextDeploy

NextDeploy is an open-source CLI and daemon for deploying and managing Next.js applications on your own infrastructure.
No lock-in. No magic. Just Docker, SSH, and full control.

---

## Why NextDeploy?

* Builds Docker images optimized for Next.js
* Ships to any VPS (Hetzner, DigitalOcean, AWS, bare metal) via SSH
* Injects secrets securely with Doppler
* Streams logs and metrics from running containers
* Runimage: test production builds locally with real secrets
* Daemon support: health checks, logs, and automation on servers

One tool. One config. Full transparency.

---

## Installation

There are multiple ways to install NextDeploy:

### 1. Download Pre-compiled Binaries (Recommended)

You can download the pre-compiled binaries for Windows, macOS, and Linux from the [GitHub Releases](https://github.com/Golangcodes/nextdeploy/releases) page.
Simply download the appropriate binary for your system architecture, extract it, and place it in your PATH.

### 2. For Go Developers

If you have Go installed on your system, you can easily install the CLI directly:

**Install CLI (Windows, macOS, Linux):**
```bash
go install github.com/Golangcodes/nextdeploy/cli@latest
```

**Install Daemon (Linux only):**
```bash
go install github.com/Golangcodes/nextdeploy/daemon/cmd/daemon@latest
```

### 3. Bash Install Script (Linux Servers)

For a quick setup on a Linux server, you can use our installation script to fetch the latest daemon and CLI securely:

```bash
curl -fsSL https://nextdeploy.one/install.sh | bash
```

---

## Quick Start

```bash
nextdeploy init       # Scaffold Dockerfile + nextdeploy.yml
nextdeploy build      # Build production Docker image
nextdeploy runimage   # Run locally with Doppler secrets
nextdeploy prepare # Prepare a fresh VPS
nextdeploy ship       # Deploy to your server
nextdeploy serve      # Serve app online
```

Test with production config before shipping:

```bash
nextdeploy runimage --prod
```

---

## Secrets Done Right

NextDeploy is Doppler-first. No more `.env` files:

* Secrets injected at deploy/runtime
* Fully encrypted and scoped (dev/staging/prod)
* Update, restart, done
* Works the same locally and in CI

---

## Philosophy

Other platforms abstract until you lose control. NextDeploy flips that. You own the pipeline. You see every step.

No black boxes. No middleware. Just you and your server.

**Inspired by**: [Kamal](https://kamal-deploy.org/) - We loved their approach to self-hosted deployments and specialized it for Next.js.

---

## Perfect For Developers Who

* Deploy Next.js or full-stack apps to VPS/bare metal
* Want transparent, auditable DevOps
* Need strong security practices without complexity
* Care about simplicity over vendor lock-in

---

## Roadmap

* serverless deployment with aws
* Doppler integration
* Logs and metrics
* runimage for local testing
* CI/CD via GitHub webhooks
* Rollbacks and release tracking
* Stack plugins (Rails, Go, Bun, Astro...)
* Dashboard and multitenant support

---

## Links

* Website: [nextdeploy.one](https://nextdeploy.one)
* GitHub: [github.com/Golangcodes/nextdeploy](https://github.com/Golangcodes/nextdeploy)
* Twitter/X: [@hersiyussuf](https://x.com/hersiyussuf)

---

## Community

We welcome contributors:

* Systems engineers (daemon/logging)
* Security reviewers
* Product-minded devs

---
NextDeploy — Transparent Deployment, Under Your Control.
---

