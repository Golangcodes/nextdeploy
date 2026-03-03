# 🚀 NextDeploy: YouTube Demo Guide

This guide outlines the perfect flow for a YouTube demo of **NextDeploy**. Follow these steps to showcase the power of native Next.js deployments, security hardening, and real-time management.

---

## 🏗️ Phase 1: The Setup (Zero to Config)

Before starting, ensure you have a standard Next.js project ready.

### 1. Initialize NextDeploy
```bash
nextdeploy init
```
*   **Explanation**: This scaffolds a `nextdeploy.yml` file. It detects your project name and asks for your production domain.
*   **Demo Tip**: Show the generated `nextdeploy.yml` and explain how it defines the bridge between your code and your server.

---

## 📦 Phase 2: The Build (Native Efficiency)

NextDeploy doesn't use Docker. It builds your app natively for maximum performance.

### 2. Build for Production
```bash
nextdeploy build
```
*   **Explanation**: This runs your package manager's build command (npm/yarn/pnpm/bun) and bundles the `.next` output into a deployment artifact in the `.nextdeploy` folder.
*   **Demo Tip**: Point out that there is No Docker magic here—just raw, native speed.

---

## 🛡️ Phase 3: The Infrastructure (Security First)

Provision your server with industry-standard hardening in one command.

### 3. Prepare the Server
```bash
nextdeploy prepare
```
*   **Explanation**: This uses Ansible under the hood to:
    *   Install Node.js, Bun, and Go.
    *   Setup the **NextDeploy Daemon** (`nextdeployd`).
    *   Configure **Caddy** with automatic HTTPS and **Security Headers**.
    *   Install and configure **Fail2Ban** to automatically jail malicious IPs.
    *   Harden the server (non-root execution, systemd sandboxing).
*   **Demo Tip**: Explain that NextDeploy doesn't just install tools; it builds a fortress by automatically configuring firewalls and intrusion prevention (`Fail2Ban`) out of the box.

---

## 🚀 Phase 4: The Deployment (The "Ship" Moment)

The moment of truth. Zero-downtime deployment.

### 4. Ship to Production
```bash
nextdeploy ship
```
*   **Explanation**: This uploads your artifact, extracts it on the server, starts a new systemd service, and updates Caddy.
*   **Security Note**: NextDeploy automatically injects **DDoS protection**, **Rate Limiting** placeholders, and **Strict Security Headers** (CSP, HSTS) into your Caddy configuration during this step.
*   **Demo Tip**: Mention that every deployment includes an **Access Log** in JSON format (`/var/log/caddy/access.log`), which Fail2Ban monitors in real-time to protect your app.
*   **The Big Reveal**: Open your browser to your domain (e.g., `https://nextdeploy.one`) and show the secure lock icon!

---

## 📊 Phase 5: Live Management (Control Center)

Show how you manage the app once it's live.

### 5. Check Status
```bash
nextdeploy status
```
*   **Explanation**: Shows the real-time **PID**, **Memory usage**, and **Health status** of your running service.

### 6. Stream Live Logs
```bash
nextdeploy logs
```
*   **Explanation**: Streams high-speed production logs directly to your terminal via the daemon—no need to manually SSH and tail files.

---

## 🔑 Phase 6: Dynamic Secrets (No More .env Files)

Manage production secrets securely without touching the server's filesystem.

### 7. Set a Secret
```bash
nextdeploy secrets set STRIPE_KEY=sk_test_123
```
*   **Explanation**: The daemon securely injects this into the systemd environment and performs a graceful restart. No manual `.env` editing required.

---

## 🔙 Phase 7: The Safety Net (Instant Rollback)

The ultimate "oops" button.

### 8. Rollback
```bash
nextdeploy rollback
```
*   **Explanation**: Instantly reverts the `current` symlink to the previous successful release and updates Caddy. Zero downtime, zero stress.

---

## 🎯 Conclusion

Summarize the philosophy: **No lock-in. No magic. Just Native Execution and Full Control.**

> "NextDeploy — Transparent Deployment, Under Your Control."
