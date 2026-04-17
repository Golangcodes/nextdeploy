# Serverless AWS — Architecture & Code Review

> Scope: `cli/internal/serverless/` — DNS, ISR, image-optimization Lambdas, Secrets Manager integration, CloudFront orchestration, and overall Go architecture quality.

---

## TL;DR — Architecture Verdict

**Yes, this reads as junior-to-mid-level architecture.** It works, but it shows the classic symptoms:

- One package, one giant `AWSProvider` struct, with every AWS service hung off it as methods. No internal boundaries.
- Files are 500–850 lines. Functions are 100–300 lines. Single function (`applyDistributionConfig`) is ~340 lines doing config diffing, origin reconciliation, cache-behavior reconciliation, and TLS — four jobs in one.
- "Reconcile" logic is hand-rolled per resource, with inconsistent error handling: some errors are fatal, some are `Warn(...)` and swallowed, some are silently dropped via `_, _ =`.
- Error detection by `strings.Contains(err.Error(), "...")` — fragile and tested against AWS error wording, not types.
- No interfaces between layers — `DeployCompute` directly news up SDK clients (`lambda.NewFromConfig`, `cloudfront.NewFromConfig`, `sts.NewFromConfig`) inside business logic. Untestable without hitting real AWS.
- A Node.js bridge runtime is embedded as a Go raw-string constant (`bridgeJS` in `aws.go`) — not as an `assets/` file. Hard to lint, hard to diff, no syntax checking.
- `Provider` interface is the right idea but leaks abstractions (CloudFront-specific concepts surface in the generic path).
- Secrets are stored as a single JSON blob in one Secrets Manager entry per app — see Secret Management below.

It's not *bad*. Someone clearly understood the AWS surface. But it's the kind of code that will become unmaintainable at the next two features unless decoupled now.

---

## Secret Management — How It Actually Works

### Current flow

1. `loadLocalSecrets` (`deploy.go:160`) reads `.nextdeploy/.env` via `secrets.SecretManager.ImportSecrets`, flattens to `map[string]string`.
2. `UpdateSecrets` (`aws_secrets.go:64`) marshals the whole map to JSON and `PutSecretValue`s a single secret named `nextdeploy/apps/<app>/production`.
3. At runtime, the Lambda boots the AWS Parameters & Secrets Lambda Extension as a layer (ARN pinned to account `177933130628` version `:11`, region-templated).
4. The embedded `bridge.js` (`aws.go:32`) does an HTTP GET to `localhost:2773/secretsmanager/get?secretId=...`, parses the JSON, then `process.env[k] = v` for each key before spawning the Next.js server.
5. **Fallback:** if the IAM user lacks `lambda:GetLayerVersion`, the deploy code drops the layer and instead injects every secret directly into the Lambda function's environment variables (`aws_lambda.go:269–280`).

### What's good

- Single source of truth (Secrets Manager) with the extension is the AWS-recommended pattern.
- Rotation is at least *possible* — the extension caches with TTL, so a rotated secret eventually propagates without redeploy.
- `marked for deletion` recovery in `UpdateSecrets` is a nice touch.

### What's wrong

1. **Whole-blob writes destroy concurrent edits.** `SetSecret`/`UnsetSecret` do read-modify-write with no version check. Two CI jobs running `secrets set` concurrently will lose one of the writes. Use `ClientRequestToken` + `VersionId` to do compare-and-swap.

2. **The fallback path leaks every secret into Lambda env vars** — visible in the AWS console, in CloudTrail, in `GetFunctionConfiguration` to anyone with read access, and persisted in Lambda versions forever (you can't redact a published version). This should be opt-in with a loud warning, not silent fallback. Or refuse to deploy and tell the user to fix IAM.

3. **No KMS CMK.** `CreateSecret` is called without `KmsKeyId`, so AWS uses the default `aws/secretsmanager` key. For multi-account or compliance setups you'll want a customer-managed key. Make it configurable in `ServerlessConfig`.

4. **One secret per app, hardcoded `production` suffix.** `secretName := fmt.Sprintf("nextdeploy/apps/%s/production", appName)` ignores `appCfg.App.Environment`. Deploy the same app to staging and prod and they share secrets. Almost certainly a bug.

5. **No secret diffing.** Every deploy `UpdateSecret`s the full blob, which creates a new version in Secrets Manager (and you pay $0.05/10k API calls + version storage). Diff first, write only on change.

6. **`loadLocalSecrets` only reads `.nextdeploy/.env`.** The FIXME comment at `deploy.go:72` admits the issue: secrets declared in `nextdeploy.yml` are never merged. Users adding `secrets:` to YAML and expecting them to ship will be surprised.

7. **`bridge.js` parses `parsed.SecretString` with `JSON.parse` but doesn't validate.** A malformed secret crashes the bridge before the server starts — no useful error to CloudWatch. Wrap it.

8. **No audit of who set what.** No tagging of secret versions with the deployer identity / git commit. Hard to investigate "who broke prod env at 3am".

9. **The bridge's `cachedSecrets` is module-scoped** (`aws.go:39`) but the fetch happens on every cold start with no TTL. Combined with the extension's own cache, that's two layers of caching with no invalidation story. If you rotate, the *running* warm container holds the old secret until it dies.

10. **`.nextdeploy/.env` on disk is not encrypted at rest** and there's no `.gitignore` enforcement check. A junior dev will commit it.

---

## Detailed Concerns by Area

### Orchestration (`deploy.go`, `DeployCompute`)

#### C1. ISR queue is provisioned twice per deploy
**File:** `aws_lambda.go:158, 248`
**Severity:** High
`ensureRevalidationQueueExists` is called once to wire up the auxiliary lambda, then again 90 lines later just to recover `queueUrl` for the main Lambda's env var. If the second call fails transiently, `ND_REVALIDATION_QUEUE` is silently dropped from the main Lambda env. The main Lambda then can't enqueue revalidations even though the reval Lambda exists and is polling the queue.
**Fix:** Cache the result of the first call and reuse it.

#### C2. Reval Lambda boots with empty `DISTRIBUTION_ID` on first deploy
**File:** `aws_lambda.go:169`
**Severity:** Critical
`appCfg.Serverless.CloudFrontId` is empty until `ensureCloudFrontDistributionExists` runs at line 195 — but the auxiliary lambda block at 156 runs *before* that. On first deploy the reval Lambda is created with `DISTRIBUTION_ID=""` and silently fails every invalidation. ISR appears broken with no error.
**Fix:** Reorder so CloudFront is provisioned first, or write the discovered ID back into `appCfg.Serverless.CloudFrontId` before creating auxiliary lambdas.

#### C3. Auxiliary lambda config-update has no retry and discards stability error
**File:** `aws_lambda.go:687–698`
**Severity:** Medium
`waitForLambdaStable`'s error is overwritten by the next assignment. The `UpdateFunctionConfiguration` that follows can hit `ResourceConflictException` (Lambda still updating) and there's no retry loop like the main path has at 308–313.

#### C4. Cache invalidation runs twice per deploy
**File:** `aws_lambda.go:213` and `deploy.go:104`
**Severity:** Low (cost)
Each invalidation costs $0.005 after the free tier and is user-facing latency. Consolidate.

#### C5. Static-then-compute ordering creates a serving-skew window
**File:** `deploy.go:84–95`
**Severity:** Medium
S3 assets are uploaded before the Lambda is updated. Between the two steps CloudFront serves new HTML/JS hashes against an old Lambda → hydration mismatches, broken chunks. Either deploy compute first, or make the swap atomic via CloudFront staging distribution.

### Image Optimization Lambda

#### C6. `RemotePatterns` is declared, JSON-tagged, and silently dropped
**File:** `aws_lambda.go:27–53`
**Severity:** Medium
`extractImageConfig` populates `AllowedDomains` but never reads `meta.DetectedFeatures`'s remote patterns. Either populate it or remove the field — current state is a lie to the imgopt lambda.

#### C7. `/_next/image*` cache key likely doesn't include query strings
**File:** `aws_cloudfront.go:402–406`
**Severity:** Critical (real bug)
The behavior uses `Managed-CachingOptimized`, which does **not** include query strings in the cache key by default. Next's image loader passes `url`, `w`, `q` as query params. Result: the first request for any `/_next/image?url=X` caches one specific size for *all* size variants. Different `?w=` values collide on the cached response.
**Fix:** Use a custom cache policy that includes `url`, `w`, `q` in the key (or all query strings), and combine with `AllViewerExceptHostHeader` origin request policy.

#### C8. imgopt env-var contract is unverified
**File:** `aws_lambda.go:139–143`, runtime is `Provided.al2023` with handler `bootstrap`
**Severity:** Medium
We pass `IMAGE_CONFIG_JSON`, `SOURCE_BUCKET`, `DISTRIBUTION_ID` — but the actual binary may expect different names. There's no integration test. Add a smoke test that hits the function URL after deploy.

### ISR Revalidation

#### C9. SQS event source mapping likely lacks DLQ
**File:** `aws_lambda.go:177` (calls `ensureLambdaSQSTrigger`, not shown)
**Severity:** High
Without a DLQ, a poison message retries indefinitely, eats Lambda concurrency, and never surfaces. Verify `ensureLambdaSQSTrigger` configures `BatchSize`, `MaximumBatchingWindowInSeconds`, `OnFailure → DLQ`.

#### C10. No reserved concurrency on the reval Lambda
**Severity:** Medium
A revalidation burst can starve the main request Lambda for account concurrency (default 1000) and throttle real traffic.

### DNS / ACM

#### C11. Domain normalization happens in multiple places, inconsistently
**File:** `aws_acm.go:25–28` vs `aws_cloudfront.go:84` vs `aws_lambda.go:189`
**Severity:** Low
`appCfg.App.Domain` and `meta.Domain` may differ in case/scheme. Normalize once at the top of `DeployCompute`.

#### C12. Double `DescribeCertificate` per deploy
**File:** `aws_cloudfront.go:55`
**Severity:** Trivial
`ensureACMCertificateExists` already describes the cert; `isCertificateIssued` describes it again. Return `(arn, status, err)` from one call.

#### C13. DNS guidance only emitted on the create branch
**File:** `aws_cloudfront.go:144–155`
**Severity:** High
If the distribution already exists but the cert is still pending validation (user re-runs deploy before adding DNS records), no fresh guidance is printed. The user thinks the deploy "did nothing" and is stuck.
**Fix:** Move DNS-emit out of the create-only branch.

#### C14. Apex CNAME advice violates RFC 1034
**File:** `aws_acm.go:166`
**Severity:** Medium
`writeDNSFileCloudFrontOnly` says `CNAME @ → cfDomain`. Most DNS providers reject this; you need ALIAS/ANAME or a Route53 alias record. Detect Route53 and offer to create the alias automatically, or call out the apex limitation explicitly.

### CloudFront / Reconciliation

#### C15. `applyDistributionConfig` is 340 lines doing four jobs
**File:** `aws_cloudfront.go:160–500`
**Severity:** High (architecture)
Origins, default cache behavior, ordered cache behaviors, and TLS/aliases — all in one function with shared `changed bool`. This is the worst single function in the package. Split into `reconcileOrigins`, `reconcileDefaultBehavior`, `reconcileCacheBehaviors`, `reconcileViewerCertificate`, each returning `(changed bool, err error)`.

#### C16. Cache behavior comparison is too lenient
**File:** `aws_cloudfront.go:475–478`
**Severity:** Medium
Only compares 4 fields. Misses `OriginRequestPolicyId`, `AllowedMethods`, `Compress`. Manual console edits to those fields are not corrected on next deploy.

#### C17. Origin reconciliation only updates existing IDs
**File:** `aws_cloudfront.go:300–318`
**Severity:** Medium
If `len(origins)` matches but IDs differ (stale state), nothing is fixed.

#### C18. `Managed-CachingDisabled` on default behavior kills SSR caching
**File:** `aws_cloudfront.go:328`
**Severity:** High (cost/perf)
Combined with `AllViewerExceptHostHeader`, every SSR request hits Lambda. Even `Cache-Control: public, max-age=60` from the app is ignored at CloudFront. Switch to a custom cache policy with `MinTTL=0, DefaultTTL=0, MaxTTL=31536000` so app-emitted Cache-Control is honored.

#### C19. `OriginReadTimeout` hardcoded to 30s
**File:** `aws_cloudfront.go:267`
**Severity:** Medium
If the user raises Lambda `Timeout` via config, CloudFront still cuts at 30s. Wire `OriginReadTimeout = min(sCfg.Timeout, 60)`.

### Code Quality / Go Practices

#### C20. Error detection by string substring
**Files:** `aws_lambda.go:296, 494, 653, 659, 759`
**Severity:** Medium
`strings.Contains(err.Error(), "lambda:GetLayerVersion")`, `"already exists"`, `"InProgress"`, `"AlreadyExists"`. Fragile and AWS occasionally reformats error messages. Use typed errors.

#### C21. SDK clients newed up inside business logic
**Files:** Throughout
**Severity:** High (testability)
`lambda.NewFromConfig(p.cfg)`, `cloudfront.NewFromConfig(p.cfg)`, `sts.NewFromConfig(p.cfg)` are constructed inside `DeployCompute` and friends. Cannot mock for tests. Inject a `clients` struct via the provider, or use small interfaces per method.

#### C22. `bridgeJS` embedded as a Go raw string
**File:** `aws.go:32`
**Severity:** Medium
Hundreds of lines of JavaScript inside a Go const. No syntax checking, no linting, painful diffs. Move to `cli/internal/serverless/runtime/bridge.js` and embed via `//go:embed`.

#### C23. Hardcoded layer ARN account `177933130628` and version `:11`
**File:** `aws_lambda.go:243, 462`
**Severity:** Low
Region-specific, version-pinned. Newer regions don't publish this layer. Probe and cache.

#### C24. SID brute-force loop
**File:** `aws_lambda.go:592`
**Severity:** Low
Hardcoded loop of 20 SID variants. Parse the actual policy JSON and remove what's there.

#### C25. Files and functions wildly exceed Go convention
- `aws_lambda.go`: 801 lines
- `resource_view.go`: 853 lines
- `aws_cloudfront.go`: 764 lines
- `aws.go`: 644 lines
- `aws_s3.go`: 531 lines
- `cloudflare.go`: 456 lines
- `applyDistributionConfig`: ~340 lines, 1 function
- `DeployCompute`: ~270 lines, 1 function
- `ensureLambdaFunctionURLExists`: ~125 lines, 1 function

User's house style: **no file > 300 LOC, no function/struct > 20 LOC**. Current state is wildly over.

---

## Top 3 to Fix First

1. **C2** — Reval Lambda DISTRIBUTION_ID empty on first deploy (silent ISR breakage)
2. **C7** — `/_next/image*` cache key collision (wrong-size images served)
3. **C1** — Double provisioning of SQS queue can drop env var

## Top 3 Architecture Refactors

1. **C15** — Split `applyDistributionConfig` into 4 reconcilers
2. **C21** — Inject SDK clients via interfaces (unlocks testing)
3. **C25 + secrets** — Restructure package into subpackages by responsibility (see proposal in chat)

---

## Secrets Follow-up Audit (2026-04-17)

Items marked **S**-prefix are additions from a focused re-audit of the secrets path (`aws_secrets.go`, `aws_lambda.go` layer-fallback code, `aws.go::bridgeJS`, `deploy.go::loadLocalSecrets`). They are ordered by ship-readiness, not severity.

### Tier 1 — pure mechanical refactors, no behavior change

#### S1. `fetchSecretsWithVersion` duplicates `GetSecrets`
**File:** `aws_secrets.go:94` and `:121`
**Severity:** Low (code quality)
Same `GetSecretValue` call, same unmarshal, same not-found handling. Collapse into one private fetch + two wrappers. Cuts ~25 lines.

#### S2. `mutateSecrets` → `UpdateSecrets` does three reads per write
**File:** `aws_secrets.go:53`, `:149`
**Severity:** Low (cost/latency)
Flow today: `fetch → mutate → fetch-for-CAS → UpdateSecrets → fetch-for-diff → PUT`. Three `GetSecretValue` per CLI `secrets set`. Pass the already-fetched pre-image into an internal `updateSecretsUnchecked(ctx, name, new, prev)` so the diff-before-write inside `UpdateSecrets` doesn't re-fetch.

#### S3. Banner-style error output uses 15 sequential `log.Error` calls
**File:** `aws_lambda.go:318–332` and near-duplicate at `:536`
**Severity:** Trivial (code quality)
Replace both with a single `log.Error("\n%s", bannerConst)`. Stops the two copies from drifting out of sync.

### Tier 2 — small behavior improvements

#### S4. `bridge.js` has no try/catch around `JSON.parse(SecretString)`
**File:** `aws.go::bridgeJS` (raw string literal)
**Severity:** Medium (operability)
A malformed secret kills the Node process mid-boot with a stacktrace and no CloudWatch-friendly context. Wrap with try/catch, log `secretName + parse error` at ERROR level, then exit with a specific non-zero code so `nextdeploy logs` can hint at the cause. (Follow-up to C22: do this *after* extracting to a real .js file, while the surface is already open.)

#### S5. `lambda:GetLayerVersion` precheck happens too late
**File:** `aws_lambda.go:316`, `:536`
**Severity:** Medium
Today the IAM-missing-permission detection lives inside the UpdateFunctionConfiguration / CreateFunction retry path — after S3 upload, after IAM role creation, sometimes after a partial Lambda update. Move the probe to `Initialize`/`Setup`: call `GetLayerVersion` once as a permission check. If it fails and `AllowSecretsInEnv=false`, bail before any mutation. Keep the current retry-path gating as defense in depth.

### Tier 3 — small feature adds (already tracked in "Secret-area open" above, restating here for ordering)

- **KMS CMK option** — ✅ LANDED. `serverless.kms_key_id` plumbs through to `CreateSecret.KmsKeyId` and `UpdateSecret.KmsKeyId` (see `aws_secrets.go::writeSecretBlob`, `aws.go::Initialize`).
- **Audit tagging** — ✅ LANDED. `auditTags()` applies `LastDeployedBy` (STS caller ARN), `LastGitCommit` (`shared.Commit` via ldflags), `LastDeployedAt`, `ManagedBy=nextdeploy` to every write. Tags set on Create; refreshed via `TagResource` after Update; failures are warned, not fatal (avoids blocking writes when `secretsmanager:TagResource` is missing). Per-version audit history lives in CloudTrail.
- **Drop bridge `cachedSecrets` module var** — ✅ LANDED. `cachedSecrets` removed; `startServer` holds the fetched map locally and passes it to the child `node server.js` env only.

### Tier 4 — architectural

- **Extract `secrets/` subpackage.** Today secrets logic spans `deploy.go::loadLocalSecrets`, `aws_secrets.go`, `aws_lambda.go` (layer wiring + fallback gating), and `internal/packaging/runtime/bridge.js`. One subpackage with a clear API gives Cloudflare a template and removes ~500 lines from the god-files. Defer until C21 (SDK injection) lands — that's the natural seam.

### S6. ISR revalidation endpoint + FATAL-on-empty-secrets missing from shipped bridge.js
**File:** `internal/packaging/runtime/bridge.js` (canonical runtime, post-C22 extraction)
**Severity:** High (silent production bug)
Discovered during C22 extraction. Prior to the extraction there were two diverged `bridgeJS` copies: a dead const in `cli/internal/serverless/aws.go` (never shipped) and the packager's literal (actually shipped). The *dead* copy had two features the shipped one lacked:

1. A `POST /_nextdeploy/revalidate` handler that forwards `{tag, path}` payloads to the SQS revalidation queue via `ND_REVALIDATION_QUEUE`. Without it, Next.js `revalidatePath`/`revalidateTag` calls do nothing — the main Lambda has the queue URL in env but no code reads it. ISR appears to work (no error) but cache never flushes.
2. A FATAL `process.exit(1)` when `ND_SECRET_NAME` is set but the extension returned zero secrets. Without it, the Next.js process starts with a blank `process.env` and typically 500s on its first DB call with cryptic stack traces.

**Plan:** Port both behaviors into `runtime/bridge.js`. Pair with S4 (the JSON.parse hardening) since we'll already be editing the file. Add a minimal Node integration test (feed a fake Lambda event, assert the response shape) before merging — currently zero test coverage on this runtime.

**Status (2026-04-17):** ✅ LANDED.

- `POST /_nextdeploy/revalidate` now forwards `{tag?, path?}` bodies to `ND_REVALIDATION_QUEUE` via `@aws-sdk/client-sqs` (pre-installed in Node 20 Lambda runtime). Returns 202 on enqueue, 400 on bad body, 502 on SQS failure, 503 if `ND_REVALIDATION_QUEUE` is unset.
- FATAL guard: if `ND_SECRET_NAME` is set AND `ND_SECRETS_MODE !== "env"` AND the extension returned an empty map, `bridge.js` now `process.exit(1)`s with a message pointing at IAM / layer / blob-shape root causes rather than booting into a 500-loop.
- `ND_SECRETS_MODE=env` is set by the Go side (`aws_lambda.go` UpdateFunctionConfiguration + CreateFunction fallback branches) when the insecure-env fallback applies, so the guard doesn't misfire when secrets are legitimately in `process.env`.

Still open: a standalone `bridge.test.js` under `node --test` — deferred to a separate change; the integration test here is smoke-verification via actual deploy.

## Top 3 to Fix First (updated)

Tier-1 items (S1, S2, S3) are pre-approved cleanup — land them boy-scout style next time anyone touches the file. Real priority for new effort:

1. **S5** — `GetLayerVersion` precheck (stops partial-deploy damage)
2. **C20** — typed errors for the marked-for-deletion + GetLayerVersion string matches
3. **C22 + S4** — bridge.js extraction and hardening (paired refactor)
