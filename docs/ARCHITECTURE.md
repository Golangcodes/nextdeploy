# 🏗️ NextDeploy Technical Architecture

This document provides a deep dive into how NextDeploy manages the lifecycle of a Next.js application, from local build to production serving.

---

## 🧩 The Lifecycle of a Deployment

When you run `nextdeploy ship`, the following sequence occurs:

1. **Local Build**: The CLI runs `next build` (or your custom build command) and collects the output.
2. **Artifact Packaging**: The CLI creates a compressed tarball containing:
    - The compiled server code (e.g., `.next/standalone`)
    - Static assets
    - A `metadata.json` describing the run parameters.
3. **Transport**: The tarball is uploaded to the VPS over an encrypted SSH connection (SFTP).
4. **Daemon Trigger**: The CLI communicates with the NextDeploy Daemon via a root-protected Unix socket.
5. **Release Atomic Swap**: 
    - The Daemon unpacks the new version into a unique timestamped directory.
    - It allocates a fresh internal port.
    - It generates/updates a `.env.nextdeploy` file with your secrets.
6. **Health Check**: The Daemon starts the processes and waits for the app to respond on the new port.
7. **Proxy Reload**: If healthy, the Daemon updates the Caddy configuration and reloads the proxy server.
8. **Cleanup**: Old releases are pruned, keeping only the last N versions for rollback capability.

---

## 📦 Component Responsibility

### 1. The CLI (Go)
- **State Management**: Uses a local `.nextdeploy/` folder to store session data and server keys.
- **Project Discovery**: Detects package managers (Bun, NPM, Yarn, PNPM) and Next.js configuration.
- **Authentication**: Leverages standard SSH keys (`~/.ssh/`).

### 2. The Daemon (Go)
- **Isolation**: Runs as `root` to manage systemd and sockets, but executes applications as a restricted `nextdeploy` user.
- **Process Management**: Uses `systemd` for reliable process supervision, auto-restarts, and log management via `journalctl`.
- **Socket Server**: Listens on `/run/nextdeployd.sock` for local commands.

### 3. Caddy (Web Server)
- **Automatic HTTPS**: Provisions SSL certificates via Let's Encrypt or ZeroSSL automatically.
- **Performance**: Natively supports HTTP/3, Zstd, and Gzip.
- **Dynamic Routing**: Uses the `nextdeploy.d/` inclusion pattern allowing the Daemon to update individual app configs without touching the main `Caddyfile`.

---

## 📂 File System Layout

NextDeploy follows a structured layout on the VPS:

```text
/opt/nextdeploy/
├── bin/            # Binaries (nextdeployd)
├── secrets/        # Root-restricted secret store (JSON)
└── apps/
    └── <app-name>/
        ├── current # Symlink to the active release
        └── releases/
            ├── 1772322955/
            └── 1772321401/
```

---

## ⚡ Native vs. Docker

Unlike many "modern" deployment tools, NextDeploy chooses native execution over Docker by default. 
- **Performance**: Zero virtualization overhead.
- **Resource Usage**: Lower RAM/CPU footprint.
- **Transparency**: Devs can use standard Linux tools (`top`, `ps`, `cd`) to see their app exactly as it is.
