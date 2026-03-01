# 🛠️ NextDeploy Command Reference

Detailed documentation for every command in the NextDeploy toolbelt.

---

## 🚀 Core Commands

### `nextdeploy init`
Initializes a new project.
- **What it does**: Detects your framework and generates a `nextdeploy.yml` configuration file.
- **When to use**: On your very first deployment of a project.

### `nextdeploy ship`
The primary deployment command.
- **Options**:
  - `--dopplerToken`: (Optional) Sync secrets from Doppler during the build.
- **Cycle**: Build → Pack → Upload → Deploy → Health Check → Proxy Swap.

### `nextdeploy rollback`
Instantly revert to a previous version.
- **What it does**: Swaps the `current` symlink back to the previous release and reloads Caddy.
- **Safety**: No rebuild required; rollback is instantaneous.

---

## 📊 Monitoring & Logging

### `nextdeploy status`
Checks the pulse of your production app.
- **Output**: PID, Memory Usage, Uptime, and Active Port.

### `nextdeploy logs`
Streams real-time logs to your terminal.
- **Options**:
  - `--route <path>`: Filter logs by a specific Next.js route (e.g., `/api/auth`).
- **Backend**: Uses `journalctl` for native, efficient log streaming.

---

## 🔐 Configuration & Secrets

### `nextdeploy secrets`
The secret management suite.
- `set KEY=VALUE`: Securely uploads a secret to the server.
- `get KEY`: Retrieves a secret value.
- `list`: Lists all active secret names.
- `unset KEY`: Removes a secret from the production environment.

### `nextdeploy generate_ci`
Automates your CI/CD.
- **What it does**: Generates GitHub Action workflows tailored to your `nextdeploy.yml`.

---

## 🛠️ Build & Development

### `nextdeploy build`
Builds the production-ready artifact.
- **Strategy**: Automatically chooses between `standalone` (recommended) or `default` builds based on your config.

### `nextdeploy run`
Development mode.
- **Purpose**: Runs the app locally using the production parameters defined in `nextdeploy.yml` to minimize "works on my machine" issues.

---

## ⚙️ Maintenance

### `nextdeploy update`
Keeps your CLI up to date.
- **Smart Logic**: Automatically detects if it's installed in `/usr/local/bin` or a home directory and updates the correct binary.

### `nextdeploy provision` (Experimental)
Prepares a fresh VPS for NextDeploy.
- **Actions**: Installs Caddy, Bun, Node, and sets up the `nextdeploy` user and permissions.
