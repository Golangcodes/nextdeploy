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
