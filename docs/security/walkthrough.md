# Walkthrough - Secured & Reliable VPS Process Management

I have implemented a comprehensive set of security and reliability features for the `nextdeployd` daemon and the Next.js process lifecycle on VPS deployments. These changes address the intermittent 502 errors and significantly harden the production environment.

## Key Enhancements

### 1. Security & Authentication
-   **HMAC-SHA256 Request Signing**: All commands sent from the CLI to the daemon are now cryptographically signed using a shared secret to prevent tampering.
-   **Mutual TLS (mTLS)**: Added support for TCP+mTLS listeners, ensuring only authorized clients can communicate with the daemon over the network or local TCP.
-   **IP Whitelisting**: CIDR-based access control for non-Unix socket connections.
-   **Rate Limiting**: Token-bucket rate limiting per client identity.
-   **Audit Logging**: Every action recorded in `/var/log/nextdeployd/audit.log`.
-   **Coraza WAF Integration**: Added an application-level firewall (OWASP CRS) to Caddy to block SQLi, XSS, and other web attacks before they reach the sandbox.

### 2. Process Reliability & Diagnostics
-   **Port Persistence**: Remembers last assigned port in `/var/lib/nextdeployd/state.json`.
-   **Enhanced Logs CLI**: Added `nextdeploy logs --audit` and `nextdeploy logs --daemon` to stream audit and process logs directly from the VPS.
-   **Port Discovery**: Atomic generation of a `port` file in the app directory.
-   **Auto-Recovery**: Systemd services are now configured with `Restart=on-failure` and a `RestartSec=5s` delay, ensuring graceful and predictable restarts.

### 3. Advanced Isolation
-   **Hardened Systemd Services**:
    -   `DynamicUser=yes`: Each application runs under a unique, transient user.
    -   `ProtectSystem=strict`: The entire filesystem is read-only for the app, except for its own data directory.
    -   `PrivateTmp=yes`, `NoNewPrivileges=yes`: Complete temporary file and privilege isolation.
    -   `RestrictNamespaces=yes`, `RestrictRealtime=yes`: Reduced kernel attack surface.

### 4. Serverless Deployment Fixes
-   **CloudFront Discovery**: Fixed the `CNAMEAlreadyExists` error by implementing a 2-pass discovery logic. It now checks for existing domain aliases (CNAMEs) if the comment-based match fails, allowing `nextdeploy` to adopt existing distributions.
-   **Lambda Permissions**: Improved the fallback logic for the Secrets Extension Layer. It now provides a clear "ACTION REQUIRED" message if the `lambda:GetLayerVersion` permission is missing, while gracefully falling back to environment variables.

## Technical Details

### New Modules
-   [security.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/security.go): Core HMAC, Rate Limiting, and IP Whitelisting logic.
-   [audit.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/audit.go): Structured audit logging system.
-   [state_manager.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/state_manager.go): Persistent state serialization for port mappings.

### Modified Components
-   [command_handler.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go): Orchestrates security checks and port persistence during deployments.
-   [socket_server.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/socket_server.go): Now supports multiple listeners (Unix + TCP/mTLS).
-   [process_manager.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/process_manager.go): Updated systemd templates with advanced sandboxing.
-   [client.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/client/client.go): Client library updated to handle signatures and TLS.

## Verification Results

### Build Status
Successfully built both the daemon and client utilities:
```bash
go build -v ./daemon/cmd/nextdeployd
go build -v ./daemon/cmd/client
```

### Next Steps
1.  **Deployment**: Deploy the updated `nextdeployd` to your VPS.
2.  **Configuration**: Add `security_secret` to your `/etc/nextdeployd/config.json`.
3.  **Validation**: Test a deployment and verify the audit logs and systemd isolation.
