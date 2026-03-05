# Secured & Reliable VPS Process Management

Harden the `nextdeployd` daemon and Next.js process lifecycle with modern security standards and robust recovery mechanisms.

## User Review Required

> [!IMPORTANT]
> **Mutual Authentication & TLS**: We will introduce an optional TCP+mTLS listener. For standard local deployments, Unix socket permissions are the primary security layer, but TLS can be layered on top if required.
> **Request Signing**: Commands from the CLI will now require an HMAC-SHA256 signature. A shared secret will be generated during `nextdeploy prepare`.

## Proposed Changes

### [nextdeployd] Core Security & Listener Architecture

#### [NEW] [security.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/security.go)
-   **Request Signing**: Implement `VerifySignature(payload, signature, secret)` using HMAC-SHA256.
-   **Rate Limiter**: Token-bucket based rate limiter per command type/source.
-   **IP Whitelisting**: CIDR-based whitelist for the TCP listener.

#### [NEW] [audit.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/audit.go)
-   **Audit Logger**: Structured JSON logging to `/var/log/nextdeployd/audit.log`. Logs: `timestamp`, `command_type`, `client_identity`, `result`, `error_details`.

#### [MODIFY] [socket_server.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/socket_server.go)
-   **Unified Listener**: Support both Unix Domain Sockets and TCP+mTLS.
-   **TLS Configuration**: Load server certificates and CA for client verification (mTLS).

### [cli] Enhanced Diagnostics & Logs

#### [MODIFY] [logs.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/cmd/logs.go)
-   **Audit Logs**: Add `--audit` flag to stream `/var/log/nextdeployd/audit.log`.
-   **Daemon Logs**: Add `--daemon` flag to stream `/var/log/nextdeployd/nextdeployd.log`.
-   **Unified View**: Ensure the command robustly finds the active service name via the daemon.

### [nextdeployd] Advanced Process Management

#### [MODIFY] [process_manager.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/process_manager.go)
-   **Harden Systemd Template**:
    -   `DynamicUser=yes`: Automatically manage transient users for each app.
    -   `ProtectSystem=strict`, `PrivateTmp=yes`: Restrict filesystem access.
    -   `Restart=on-failure`, `RestartSec=5s`: Auto-recovery.
-   **Port File Generation**: Atomic write to `/opt/nextdeploy/apps/{appName}/port` for Caddy dynamic resolution.

#### [NEW] [state_manager.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/state_manager.go)
-   **Persistent State**: Store assigned ports and last-known-good release IDs in `/var/lib/nextdeployd/state.json`.

---

### [caddy] Coraza WAF Integration

#### [MODIFY] [caddy.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/shared/caddy/caddy.go)
-   **Security Directives**: Add `coraza_waf` block to `GenerateCaddyfile`.
-   **Rules Management**: Include basic OWASP Coraza Core Ruleset (CRS) configuration.
-   **Conditional Activation**: Add a way to toggle WAF via app config/metadata.

### [serverless] CloudFront & Lambda Fixes

#### [MODIFY] [aws_cloudfront.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_cloudfront.go)
-   **Discovery Logic**: Update `ensureCloudFrontDistributionExists` to check `dist.Aliases` for the target domain if the comment-based search fails. 
-   **Conflict Resolution**: If the domain is found on a distribution with a different comment, log a clear message and attempt to update that distribution instead of creating a new one.

#### [MODIFY] [aws_lambda.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_lambda.go)
-   **Permission Check**: Wrap the `lambda:GetLayerVersion` check in a more helpful error message.
-   **Secrets Handling**: Suggest the user adds the `lambda:GetLayerVersion` permission to their IAM policy if they want to use secure secrets.

## Verification Plan

### Automated Tests
1.  **Security Tests**: Verify that unsigned or incorrectly signed requests are rejected.
2.  **Rate Limit Test**: Flood the daemon with requests and verify 429-like behavior.
3.  **Audit Log Verification**: Check that every action creates a valid JSON entry in the audit log.

### Manual Verification
1.  **Isolation Check**: Verify `systemd-analyze security nextdeploy-{app}` score.
2.  **Auto-Restart Proof**: Kill a running app and observe systemd logs for immediate, graceful restart.
3.  **mTLS Handshake**: Test connection from a client with/without the correct certificate.
