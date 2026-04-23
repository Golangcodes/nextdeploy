package cmd

// shipExplanation covers the end-to-end flow when `nextdeploy ship` lands
// on a Cloudflare serverless target. The AWS path shares steps 1–4 and
// diverges at DeployCompute — the explanation calls that out inline
// rather than branching the phase list.
var shipExplanation = explanation{
	Name:     "ship",
	Synopsis: "Deploy the build to the configured serverless or VPS target.",
	Summary: "`ship` deploys your Next.js build to the target declared in " +
		"nextdeploy.yml (serverless=AWS or Cloudflare, or VPS). The " +
		"Cloudflare path invokes the nextcompile pipeline to produce a " +
		"single Worker bundle; the AWS path produces a Lambda zip + " +
		"CloudFront distribution. `ship` is aliased to `deploy`.",
	Phases: []phase{
		{
			Num:       1,
			Title:     "Load config",
			Narrative: "Reads nextdeploy.yml from the project root — app metadata, serverless provider, secrets policy, custom domain, bindings.",
			Ref:       "shared/config/loader.go",
			Function:  "config.Load",
			Input:     "nextdeploy.yml on disk",
			Output:    "*config.NextDeployConfig",
		},
		{
			Num:       2,
			Title:     "Git sanity check",
			Narrative: "Warns if the working tree is dirty. Non-fatal — committed deploys produce better provenance but uncommitted deploys still ship.",
			Ref:       "cli/cmd/ship.go:39",
			Function:  "git.IsDirty",
			Output:    "log warning, continue",
		},
		{
			Num:       3,
			Title:     "Load build metadata",
			Narrative: "Reads .nextdeploy/metadata.json produced by an earlier `nextdeploy build`. Contains the NextCorePayload — route classification, middleware, ISR tags, detected features.",
			Ref:       "cli/cmd/ship.go:45",
			Function:  "json.Unmarshal → nextcore.NextCorePayload",
			Input:     ".nextdeploy/metadata.json",
			Output:    "nextcore.NextCorePayload",
		},
		{
			Num:       4,
			Title:     "Resolve effective target",
			Narrative: "Picks between 'serverless' (AWS or Cloudflare) and 'vps' (Caddy + SSH). Routing is driven by cfg + metadata's detected target type.",
			Ref:       "cli/cmd/ship.go:52",
			Function:  "cfg.ResolveTargetType",
			Output:    `"serverless" | "vps"`,
		},
		{
			Num:       5,
			Title:     "Initialize provider",
			Narrative: "Loads Cloudflare credentials (env → credstore → nextdeploy.yml), verifies the API token, wires the CF SDK client + an R2-S3 client for object uploads.",
			Ref:       "cli/internal/serverless/cloudflare.go:153",
			Function:  "CloudflareProvider.Initialize",
			Input:     "*config.NextDeployConfig",
			Output:    "p.cf wired, p.r2s3 wired, token verified",
			Notes:     []string{"AWS path: AWSProvider.Initialize (resolves AWS creds + STS caller)"},
		},
		{
			Num:       6,
			Title:     "Package build artifact",
			Narrative: "Splits the Next.js standalone build into two pieces: a compute payload (Lambda zip or Worker input tree) and a static-asset list bound for the CDN/R2.",
			Ref:       "cli/internal/serverless/deploy.go:55",
			Function:  "packaging.NewPackager().Package",
			Input:     "project dir + nextcore payload",
			Output:    "*packaging.PackageResult (LambdaZipSize, S3Assets, StandaloneTarPath)",
		},
		{
			Num:       7,
			Title:     "Push secrets (order depends on provider)",
			Narrative: "AWS: secrets land in Secrets Manager BEFORE DeployCompute so Lambda can bake them into env. Cloudflare: secrets land AFTER the Worker exists because they attach to the Worker itself.",
			Ref:       "cli/internal/serverless/deploy.go:80",
			Function:  "loadLocalSecrets → UpdateSecrets",
			Input:     ".env + secrets.files[] + .nextdeploy/.env",
			Output:    "secrets written to Secrets Manager / Worker secrets",
		},
		{
			Num:       8,
			Title:     "Deploy static assets",
			Narrative: "Cloudflare: uploads every S3Asset to the R2 bucket via the S3-compatible endpoint, creating the bucket if missing. Concurrency capped at 8 to avoid rate limits.",
			Ref:       "cli/internal/serverless/cloudflare.go:213",
			Function:  "CloudflareProvider.DeployStatic",
			Input:     "*packaging.PackageResult",
			Output:    "R2 bucket populated",
			Notes:     []string{"AWS path: DeployStatic → S3 sync under app prefix"},
		},
		{
			Num:       9,
			Title:     "Build Worker bundle (Cloudflare)",
			Narrative: "Converts the standalone build into a single Worker ESM module. This is where nextcompile runs: detects Next/React versions, scans compiled routes, emits dispatch + manifest + runtime + vendored RSC, then invokes esbuild to produce worker.mjs.",
			Ref:       "cli/internal/serverless/cloudflare_adapter.go:38",
			Function:  "BuildWorkerBundle → nextcompile.Compile → esbuild",
			Input:     "standalone dir + NextCorePayload",
			Output:    "worker.mjs (single ESM bundle)",
			Notes: []string{
				"See --code for the 14 nextcompile sub-phases.",
				"AWS path: Lambda zip upload + layer attach, no esbuild.",
			},
		},
		{
			Num:       10,
			Title:     "Upload compute (Cloudflare Worker or Lambda)",
			Narrative: "Cloudflare: posts worker.mjs via Workers.Scripts.Update with bindings metadata (R2, KV, D1, Hyperdrive, Queues, Vectorize, AI Gateway). AWS: updates Lambda function code + attaches the Secrets Manager layer.",
			Ref:       "cli/internal/serverless/cloudflare.go:318",
			Function:  "CloudflareProvider.DeployCompute",
			Input:     "worker.mjs + bindings config",
			Output:    "Worker active at workers.dev",
		},
		{
			Num:       11,
			Title:     "Wire edges (custom domains, routes, cron, queue consumers)",
			Narrative: "Idempotent wiring — hostnames attached via Workers.Domains.Update, zone-level routes via Workers.Routes, cron schedules replaced wholesale, queue consumers bound.",
			Ref:       "cli/internal/serverless/cloudflare.go:393",
			Function:  "ensureCustomDomain / ensureWorkerRouteForZone / applyCronTriggers / ensureQueueConsumer",
			Output:    "custom domains + routes + crons + queue consumers active",
		},
		{
			Num:       12,
			Title:     "Invalidate CDN cache",
			Narrative: "Cloudflare: purges the zone cache so the new deploy is served immediately. AWS: DeployCompute already triggered a CloudFront invalidation so this hop is skipped.",
			Ref:       "cli/internal/serverless/cloudflare.go:538",
			Function:  "CloudflareProvider.InvalidateCache",
			Output:    "zone purged",
		},
		{
			Num:       13,
			Title:     "Post-deploy smoke verify",
			Narrative: "Probes up to four URLs (root + three static routes) with 3-attempt retry, 5s delay. Non-fatal by default — CI callers can opt into FailOnError to gate the deploy on this check.",
			Ref:       "cli/internal/serverless/smoke.go:59",
			Function:  "SmokeVerify",
			Input:     "cfg.App.Domain + meta.RouteInfo.StaticRoutes",
			Output:    "probe results logged; error returned only when FailOnError is set",
		},
		{
			Num:       14,
			Title:     "Generate resource view",
			Narrative: "Writes an HTML report summarizing the deployment: worker URL, custom domain, resource map, and the exact DNS records the user must set for a custom domain.",
			Ref:       "cli/internal/serverless/deploy.go:140",
			Function:  "GetResourceMap + GenerateResourceView",
			Output:    "HTML report at a printable file:// URL",
		},
	},
	SubPipeline: &subPipeline{
		Title: "nextcompile inner pipeline (runs during phase 9, Cloudflare only)",
		Entry: "shared/nextcompile/compiler.go:28 (Compile)",
		Phases: []phase{
			{Num: 1, Title: "DetectVersions", Narrative: "Parse Next + React versions from standalone package.json to pick the correct runtime variant + vendored react-server-dom-webpack.", Ref: "shared/nextcompile/version_detect.go:17", Function: "nextcompile.DetectVersions", Output: "NextVersion, ReactVersion"},
			{Num: 2, Title: "ScanCompiledServer", Narrative: "Walk .next/server/** in parallel; regex-lite analysis for route kind, RSC markers, action markers, env.X refs, fetch targets, PPR opt-ins.", Ref: "shared/nextcompile/scanner.go:27", Function: "nextcompile.ScanCompiledServer", Output: "[]ModuleRef"},
			{Num: 3, Title: "DetectServerActions", Narrative: "Parse Next's server-reference-manifest.json into a flat actionId → {module, export, runtime} map.", Ref: "shared/nextcompile/actions.go:70", Function: "nextcompile.DetectServerActions", Output: "*ActionManifest"},
			{Num: 4, Title: "Derive binding hints", Narrative: "Surfaces every unique process.env.X reference as a candidate secret binding. Fetch-target analysis is a stub.", Ref: "shared/nextcompile/compiler.go:234", Function: "deriveBindingsStub", Output: "[]BindingHint"},
			{Num: 5, Title: "Elide dead routes", Narrative: "Drops compiled modules that aren't in the route manifest (orphans from refactors). Stub today; production implementation is a set-difference.", Ref: "shared/nextcompile/compiler.go:254", Function: "elideDeadRoutesStub"},
			{Num: 6, Title: "Ensure OutDir", Narrative: "Creates the build output directory with restrictive 0o750 perms since the tree may carry compiled secret references.", Ref: "shared/nextcompile/compiler.go:207", Function: "ensureOutDir"},
			{Num: 7, Title: "Emit manifest.json", Narrative: "Builds + writes the deterministic runtime manifest: routes, ISR tags, middleware matchers, image config, i18n, capability features summary.", Ref: "shared/nextcompile/manifest.go", Function: "BuildManifest + EmitManifest", Output: "_nextdeploy/manifest.json"},
			{Num: 8, Title: "Emit dispatch.mjs", Narrative: "Generates the routes table as ESM: static table (O(1) lookup), dynamic table (specificity-ordered regex), middleware + proxy refs, action loaders.", Ref: "shared/nextcompile/dispatch.go", Function: "EmitDispatchTable", Output: "_nextdeploy/dispatch.mjs"},
			{Num: 9, Title: "Extract embedded JS runtime", Narrative: "Copies the embedded runtime (dispatcher, serve, context, rsc, actions, cache, image, errors + next/ shims) out of the Go binary into the bundle tree.", Ref: "shared/nextcompile/runtime.go:15", Function: "ExtractRuntime", Output: "_nextdeploy/runtime/*.mjs"},
			{Num: 10, Title: "Vendor RSC runtime", Narrative: "For Cloudflare Worker targets: resolves react-server-dom-webpack from node_modules (walks up 5 levels) and copies the matching server.edge bundle. Fails loudly when RSC is used but the package is missing.", Ref: "shared/nextcompile/vendor.go:34", Function: "VendorRSC", Output: "_nextdeploy/runtime/vendor/react-server-dom-webpack/server.edge.mjs"},
			{Num: 11, Title: "Emit action_manifest.json", Narrative: "Writes the flattened Server Actions map the runtime's actions.mjs consults. Always written (even empty) so the worker_entry import never 404s.", Ref: "shared/nextcompile/actions.go:149", Function: "EmitActionManifest", Output: "_nextdeploy/action_manifest.json"},
			{Num: 12, Title: "Emit worker_entry.mjs", Narrative: "The esbuild entrypoint. Imports runtime + dispatch table + manifests and exports the Worker fetch handler.", Ref: "shared/nextcompile/entry.go", Function: "EmitWorkerEntry", Output: "_nextdeploy/worker_entry.mjs"},
			{Num: 13, Title: "Content hash + stats", Narrative: "SHA-256 over every generated and extracted file in sorted order — the reproducible-build fingerprint. Adapter uses this to skip no-op redeploys.", Ref: "shared/nextcompile/compiler.go:216", Function: "hashFiles", Output: "CompileStats{ContentHash, BundleBytes, Duration, …}"},
			{Num: 14, Title: "esbuild", Narrative: "Spawns `npx esbuild` on worker_entry.mjs with --format=esm --conditions=worker,node --external:node:* and --alias:next/{cache,headers,server} redirecting to runtime shims so user imports resolve unchanged.", Ref: "cli/internal/serverless/cloudflare_adapter.go:143", Function: "runEsbuild", Output: "worker.mjs (single Worker-deployable ESM bundle)"},
		},
	},
	DataFlow: `  nextdeploy.yml  ──▶  *config.NextDeployConfig
  metadata.json   ──▶  nextcore.NextCorePayload
                       │
                       ▼
  Payload          ◀──  toCompilePayload (nextcompile_bridge.go)
                       │
                       ▼
  nextcompile.Compile
    ├─ manifest.json
    ├─ dispatch.mjs
    ├─ action_manifest.json
    ├─ runtime/*.mjs (embedded)
    ├─ runtime/vendor/react-server-dom-webpack/server.edge.mjs
    └─ worker_entry.mjs
                       │
                       ▼
  esbuild (--format=esm --alias:next/*=runtime/next_shims/*)
                       │
                       ▼
  worker.mjs  ──▶  CloudflareProvider.DeployCompute
                       │
                       ▼
  Workers.Scripts.Update + bindings metadata
`,
}

func init() {
	registerExplain(shipCmd, &shipExplanation)
}
