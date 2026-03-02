## 📚 Documentation

For a deep dive into the architecture, commands, and security of NextDeploy, visit our **[Documentation Site](docs/README.md)**.

- **[Introduction](docs/INTRODUCTION.md)**: Why NextDeploy?
- **[Architecture](docs/ARCHITECTURE.md)**: How it works under the hood.
- **[Command Reference](docs/COMMANDS.md)**: Complete CLI guide.

NextDeploy is an open-source CLI and daemon for deploying and managing Next.js applications on your own infrastructure.
No lock-in. No magic. Just Native Execution, SSH, and full control.

> [!WARNING]
> **Hobby Project**: NextDeploy is currently a hobby project and is **not intended for production use** as of now. Use it at your own risk for testing and personal projects.

---

## Why NextDeploy?

* **Native Execution**: No Docker overhead. Runs as a managed systemd service.
* **VPS & Serverless**: Ship to any VPS (Hetzner, DigitalOcean, AWS) or AWS Serverless.
* **Secret Management**: Integrated remote secrets (`set`, `get`, `unset`, `list`).
* **Live Logs**: Real-time log streaming from the daemon via SSH.
* **Status Metrics**: Real-time PID and memory tracking for your apps.
* **Security**: Non-root execution with systemd sandboxing.

One tool. One config. Full transparency.

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
go install github.com/Golangcodes/nextdeploy/daemon/cmd/nextdeployd@latest
```
```
curl -fsSL https://nextdeploy.one/daemon.sh | bash
```
### 3. Bash Install Script (Linux Servers)

For a quick setup on a Linux server, you can use our installation script to fetch the latest daemon and CLI securely:

```bash
curl -fsSL https://nextdeploy.one/install.sh | bash
```

---

## Quick Start

```bash
nextdeploy init       # Scaffold nextdeploy.yml
nextdeploy build      # Build production assets natively
nextdeploy prepare    # Provision a fresh VPS with security hardening
nextdeploy ship       # Deploy to your server
nextdeploy status     # Check app PID, memory, and health
nextdeploy logs       # Stream real-time logs from the daemon
nextdeploy secrets set KEY=VALUE # Manage secrets remotely
```

---

## Secrets Done Right

NextDeploy provides a secure, remote secret management system. No manual `.env` file editing on the server:

* **Remote Management**: Set, get, list, and unset secrets from your local CLI.
* **Auto-Sync**: Daemon automatically updates application environment and restarts services.
* **Secure Storage**: Secrets are stored safely on the server, handled as systemd `EnvironmentFile`.
* **Zero-Leak**: Secrets never touch your git history or CI build logs.

---

## Philosophy

Other platforms abstract until you lose control. NextDeploy flips that. You own the pipeline. You see every step. No black boxes. Just you and your server.

**Inspired by**: [Kamal](https://kamal-deploy.org/) - We loved their approach to self-hosted deployments and specialized it for Next.js native execution.

---

## Roadmap

* [x] Native VPS Deployment (systemd)
* [ ] AWS Serverless support (Lambda + CloudFront)
* [x] Integrated Secret Management
* [x] Live Logs and Status metrics
* [ ] CI/CD via GitHub Actions / Webhooks
* [x] Rollbacks and release tracking
* [ ] Multi-tenant server support

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

NextDeploy — Transparent Deployment, Under Your Control.
