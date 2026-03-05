# NextDeploy Resource Mapping

This document details every file, directory, service, and cloud resource managed by NextDeploy across both VPS and Serverless environments.

## 1. VPS (Linux Server) Mapping

NextDeploy operates primarily under `/opt/nextdeploy` and uses standard Linux systems for orchestration.

### Filesystem Structure
- **`/opt/nextdeploy`**: Root directory for all NextDeploy operations ([command_handler.go:L22](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L22)).
- **`/opt/nextdeploy/apps/`**: Contains subdirectories for each deployed application ([command_handler.go:L23](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L23)).
- **`/opt/nextdeploy/apps/<app>/current`**: A symlink always pointing to the active release directory ([command_handler.go:L406-410](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L406-410)).
- **`/opt/nextdeploy/apps/<app>/releases/`**: Stores historical builds ([command_handler.go:L298](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L298)).
- **`/opt/nextdeploy/apps/<app>/shared_static/`**: **[Persistent]** Stores all `.next/static` assets across all builds ([command_handler.go:L414-429](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L414-429)).
- **`/opt/nextdeploy/apps/<app>/port`**: Stores the unique port assigned to this specific application ([command_handler.go:L379-382](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L379-382)).
- **`/opt/nextdeploy/uploads/`**: Temporary landing zone for deployment tarballs ([command_handler.go:L24](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L24)).
- **`/opt/nextdeploy/tmp/`**: Workspace for extracting and preparing releases ([command_handler.go:L25](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L25)).

### System Configuration
- **`/etc/systemd/system/nextdeploy-<app>-<releaseid>.service`**: Unit file for the process ([process_manager.go:L25-110](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/process_manager.go#L25-110)).
- **`/etc/caddy/Caddyfile`**: Entry point for web server ([caddy_manager.go:L14](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/caddy_manager.go#L14)).
- **`/etc/caddy/nextdeploy.d/<app>.caddy`**: Site-specific Caddy configuration ([caddy_manager.go:L30-41](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/caddy_manager.go#L30-41) and [caddy.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/shared/caddy/caddy.go)).
- **`/var/lib/nextdeployd/state.json`**: **[Critical State]** Persists ports and metadata ([command_handler.go:L39](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/daemon/internal/daemon/command_handler.go#L39)).

### Logs & Binaries
- **`/var/log/nextdeployd/audit.log`**: Audit trail of all commands received by the daemon.
- **`/var/log/caddy/access.log`**: Web traffic and WAF violation logs.
- **`/usr/local/bin/nextdeployd`**: The daemon binary itself.

---

## 2. AWS (Serverless) Mapping

For serverless targets, NextDeploy maps project components to specific AWS primitives.

### Storage (S3)
- **`nextdeploy-assets-<app>-<env>-<random>`**: Static assets and Lambda package ([aws_s3.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_s3.go)).
- **`nextdeploy-logs-<app>-<env>`**: (Optional) CloudFront access logs.

### Compute (Lambda)
- **`<app>-server-<env>`**: Primary compute engine ([aws_lambda.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_lambda.go)).
- **Layer**: Contains common dependencies like `node_modules`.

### Delivery (CloudFront)
- **CloudFront Distribution**: High-performance CDN gateway ([aws_cloudfront.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_cloudfront.go)).
- **Origin Access Control (OAC)**: Restricts S3 access to the CDN only.

### Security & Identity
- **IAM Role (`<app>-lambda-role`)**: Execution role with least-privilege ([aws_iam.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_iam.go)).
- **ACM Certificate**: Public SSL/TLS certificate in `us-east-1` ([aws_acm.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_acm.go)).
- **Secrets Manager (`<app>-secrets-<env>`)**: Encrypted runtime environment variables ([aws_secrets.go](file:///home/hersi/Music/workspace/nextdeploy/NextDeploy/cli/internal/serverless/aws_secrets.go)).

### Networking
- **Lambda@Edge / CloudFront Functions**: (If configured) Used for custom headers or edge-side logic.
- **WAFv2 WebACL**: (If enabled) Layer 7 protection attached to the CloudFront distribution.

---

## 3. Resource Cleanup (Destroy Flow)

When `nextdeploy destroy` is executed, the following logic is applied:

### VPS
1. Stops and removes the systemd service.
2. Deletes the application Caddy configuration and reloads Caddy.
3. Wipes the `/opt/nextdeploy/apps/<app>` directory (including shared statics).
4. Cleans the Daemon state in `state.json`.

### AWS
1. **CloudFront**: Disables and deletes the distribution (including OAC).
2. **Lambda**: Removes the function and its associated log groups.
3. **S3**: Empties and deletes the bucket (including all versions).
4. **ACM**: Revokes/Deletes managed certificates in `us-east-1` to prevent CNAME conflicts.
5. **Secrets**: Deletes the secret (with a 7-day recovery window by default).
