# NextDeploy Architecture Overview

## 🧩 Purpose

NextDeploy is a deployment engine that allows developers to deploy and manage full-stack Next.js applications to their own virtual servers (VPS), bypassing services like Vercel. It is built in Go, with a CLI and daemon-driven backend, and uses Caddy for automatic HTTPS and flexible reverse proxying.

---

## 🧱 Current Architecture

### 🧠 Core Components

1. **CLI (Go-based)**  
   - Initializes projects (`init`)
   - Builds and deploys Docker containers to target VPS
   - Handles SSH authentication and file transfer

2. **Daemon (Go-based)**  
   - Runs on the VPS
   - Responsible for:
     - Parsing `nextdeploy.yml`
     - Building/running Docker containers
     - Health checks, system monitoring, and logging
     - Future: GitHub webhook listener for CI/CD

3. **Caddy Server**  
   - Handles HTTPS with automatic TLS via Let's Encrypt
   - Acts as a reverse proxy for the deployed Next.js app and internal services
   - Provides per-project routing and static file hosting

4. **nextdeploy.yml**
   - Unified configuration file defining:
     - Build strategy
     - Environment variables
     - Ports, domains, and routing
     - Database configuration
     - Services required (e.g., Redis, Postgres)

---

## 📦 Data & Observability

- Server metrics: CPU, RAM, disk usage
- Docker container status and logs
- HTTP traffic (inbound/outbound)
- Build status and error traces
- Git revision deployed, per app
- SSL and DNS status checks
- Active daemon health status

---

## 🛣️ Roadmap / What’s Next

### 🔁 1. GitHub Webhook Integration (CI/CD)
- Automatically rebuild and redeploy app when new commits are pushed
- Authenticated via GitHub App or personal token

### 🔒 2. Hardened Security
- Encrypted config + secrets store
- Zero-trust SSH access
- Resource limits per container
- Audit logs

### 🧠 3. Smart Daemons
- Add capability for daemons to self-repair
- Failover support: container dies? Auto-restart
- Mirror environments for testing vs. production

### 🌐 4. Multi-tenant Dashboard (Optional SaaS Layer)
- Web UI for managing:
  - Apps
  - Servers
  - Logs
  - Deploy history
- Stripe integration (after US company setup)

### ☁️ 5. Optional Infra Provisioner (AWS Free Tier Tool)
- Spins up EC2, RDS, DNS records, and VPS instances for users
- Plug-and-play for those who don’t bring their own infrastructure

---

## ⚙️ Guiding Principles

- **Stack Agnostic**: As long as a Dockerfile + `nextdeploy.yml` exists, we can deploy it.
- **Self-hosted First**: You bring the server. We bring the orchestration.
- **CLI-First**: Developer-native experience — command line and config-driven
- **Extendable by Design**: Each component should be replaceable with custom implementations (e.g., use Nginx instead of Caddy)

---

## 📊 System Diagram (Text-based)

```text
+------------------+
| Developer Laptop |
+--------+---------+
         |
         | nextdeploy CLI
         v
+------------------------+
| Target VPS (Daemon)   |
| --------------------- |
| - Go Daemons          |
| - Node/Bun Runtime      |
| - Caddy Reverse Proxy |
+------------------------+
         |
         | serves via HTTPS
         v
+--------------------+
| Next.js Web App(s) |
+--------------------+
