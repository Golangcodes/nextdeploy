# NextDeploy Project Map

This map helps new contributors navigate the NextDeploy codebase.

## 🗺️ Codebase Map

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

## 🚀 Contribution Entry Points

- **Want to add a new CLI feature?** Look at `cli/cmd/`.
- **Want to improve VPS deployments?** Check `daemon/internal/daemon/handler_ship.go`.
- **Found a security bug in CSP?** Edit `shared/nextcore/features.go`.
- **Improving Serverless flows?** See `cli/internal/serverless/`.

## 🛠️ Developer Checklist
1. Read the [CONTRIBUTING.md](CONTRIBUTING.md) for architecture details.
2. Check the [RESOURCE_MAPPING.md](RESOURCE_MAPPING.md) to see what resources your code touches.
3. Build locally using:
   ```bash
   # CLI
   go build -o nextdeploy ./cli/main.go
   # Daemon
   go build -o nextdeployd ./daemon/cmd/nextdeployd
   ```
