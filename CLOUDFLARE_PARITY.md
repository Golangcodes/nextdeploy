# Cloudflare Parity: What nextdeploy needs to ship Pesastream-shaped apps in one shot

> Status: scoping doc, not yet implemented. Written 2026-04-18 during Pesastream's OpenNext-on-CF migration.
> Target: `nextdeploy deploy --target cloudflare` completes for a complex Next.js app with DOs, crons, queues,
> Hyperdrive, middleware, and server actions — with no post-deploy manual fixes.

## 1. What "Pesastream-shaped" means

This doc uses Pesastream (a real Next.js 16 app) as the reference target, because if nextdeploy can deploy
this, it can deploy anything a Kenyan fintech / SaaS startup is likely to build. The feature surface:

- **Next.js 16.1.6**, App Router, `output: 'standalone'`, Turbopack, strict TypeScript
- **428 routes** — 209 static + 219 dynamic; 207 of them are `/api/*` (auth, payments, webhooks, crons, admin)
- **`src/middleware.ts`** — subdomain routing, critical path
- **Server Actions** (`"use server"`) used across 54 files
- **6 Durable Object classes** — `ConversationThread`, `CallSession`, `RafikiAgent`, `JibuAgent`, `ZidiAgent`, `AkibaAgent`
- **Cron** — `* * * * *` payment-fanout
- **Hyperdrive** — single binding, pg → `hyperdrive.local:5432` TCP proxy
- **45+ Doppler secrets** deployed via `wrangler secret put`
- **R2** for static assets + Next image originals
- **Streaming SSR** — POS UI depends on progressive render
- **PWA + Service Worker** — `sw.js` must carry `X-Build-ID` header per deploy

## 2. What nextdeploy can do today (2026-04-18)

Confirmed by reading `cli/internal/serverless/cloudflare_adapter.go` and `templates/worker_shim.mjs`:

- Drop an embedded `worker_shim.mjs` into the standalone dir
- Invoke `npx esbuild` with `--bundle --platform=node --format=esm --target=esnext --conditions=worker,node --external:node:* --external:cloudflare:*`
- Bundle the Next standalone server into one ESM worker module
- The shim bridges workerd `fetch` → Next's Node-style request handler (buffered response)
- Static asset short-circuit against `env.ASSETS` (R2 binding)
- `.nextdeploy/metadata.json` emission with: buildId, route classification, static asset inventory,
  prerender manifest, detected features

**What this is good for today:** blog / marketing site / simple SSR app, 1 route handler, no DOs, no crons.

**What it cannot ship today:** everything on the list below.

## 3. Gap list — blocking items for Pesastream-shape deploy

Ranked by "will Pesastream run at all without this" → "cosmetic."

### Tier A — Pesastream won't function without these

| # | Gap | Why it blocks |
|---|---|---|
| A1 | **`scheduled()` handler export** | Cron fires the CF runtime's `scheduled()` entry. nextdeploy shim only exports `fetch`. Payment fanout never runs. |
| A2 | **Durable Object class re-exports** | `wrangler.toml` `durable_objects.bindings[].class_name` must resolve to a class exported by the worker module. 6 classes missing → deploy fails at `wrangler deploy` validation. |
| A3 | **`queue()` handler export** | Any CF Queue consumer binding dispatches to `queue()`. Same mechanism as A1. |
| A4 | **workerd esbuild conditions** | Current flag `--conditions=worker,node` misses the `workerd` condition. Causes `pg-cloudflare` and any library with `workerd` conditional exports to resolve to empty stubs at build, producing `TypeError: r2 is not a constructor` at runtime. |
| A5 | **`cloudflare:sockets` external + ESM preservation** | Required by Hyperdrive/pg. If esbuild wraps the dynamic `import('cloudflare:sockets')` in its CJS `__require` shim, runtime dies with `Dynamic require of "cloudflare:sockets" is not supported`. Must (a) mark external, (b) ensure the consuming module is bundled as ESM. |
| A6 | **Hyperdrive binding injection** | Shim must read `env.HYPERDRIVE.connectionString` and make it visible to the pg pool before any handler runs. Pesastream does this with a `globalThis.__PS_DATABASE_URL` override — nextdeploy needs a generic equivalent (config-declared binding → `process.env` + `globalThis` injection at isolate init). |
| A7 | **Middleware invocation** | nextdeploy metadata reports `middleware: null` even when `src/middleware.ts` exists. Detector must find it, and the shim must invoke Next's middleware proxy before the route handler — otherwise subdomain routing (`{subdomain}.pesastream.com` → `/store/{subdomain}/*`) is dead. |
| A8 | **Streaming SSR** | Shim's `res.end()` path accumulates into `chunks[]` and resolves a single `Response(body)`. POS UI degrades visibly. Replace with `TransformStream` writer; pipe as chunks arrive. |

### Tier B — production-grade but not first-deploy blockers

| # | Gap | Why it matters |
|---|---|---|
| B1 | **Server Actions detection + routing** | `detected_features.HasServerActions = false` today. Pesastream has 54 files with `"use server"`. Action requests go through Next's RSC handler — should be invoked, but the flow isn't tested without detection/audit. |
| B2 | **R2 asset upload with per-type Cache-Control** | Currently expects pre-uploaded assets. Deploy should upload `.next/static/*` (immutable) + `public/*` (short TTL) in a single pass. Metadata has absolute paths + sizes already; just needs a driver. |
| B3 | **Secret sync from Doppler / .env / KV** | Pesastream deploys 45+ secrets. Current pipeline uses `wrangler secret put` 45 times serially and hits rate limit (error 10013) at request ~13. Needs batched `wrangler secret bulk` or equivalent. |
| B4 | **API route inventory** | Metadata has `api_routes: null`. 207 API routes should be emitted, both for audit and for smoke-testing. |
| B5 | **Build-ID injection** | `BUILD_ID` must land in both (a) `env` for the worker, (b) SW headers (`X-Build-ID`), (c) asset URLs. nextdeploy has the `buildId` in metadata — needs wiring into deploy. |
| B6 | **WebSocket upgrade** | Shim explicitly states unsupported. Pesastream doesn't use WS today (uses Ably over HTTPS), but OpenClaw agents may. Implement via workerd `WebSocketPair`. |
| B7 | **Image optimization** | `/_next/image` currently returns 501. Options: (a) bounce to Cloudflare Images, (b) embed WASM resizer. Pick one and document. |
| B8 | **Incremental cache / tag cache** | Pesastream doesn't use ISR → not strictly blocking. But any Next app reaching for `revalidate` / `unstable_cache` breaks. Define a pluggable interface (R2-backed or DO-backed); ship a no-op default. |

### Tier C — polish

- C1: route preloading behavior (cold-start optimization)
- C2: skew protection (multi-version coexistence during rollouts)
- C3: sourcemap upload (observability)
- C4: custom error pages beyond Next's built-in
- C5: `robots.txt` + `sitemap.xml` dynamic generation passthrough

## 4. Metadata.json extensions required

Currently missing fields that must be emitted for the deploy pipeline to be driveable:

```jsonc
{
  // Existing:
  "nextbuildmetadata": { "...": "..." },
  "static_assets": { "...": "..." },
  "route_info": { "static_routes": [...], "dynamic_routes": [...] },
  "detected_features": { "...": "..." },

  // NEW — required:
  "middleware": {
    "path": "src/middleware.ts",
    "matcher": ["/((?!api|_next|...).*)"],
    "runtime": "nodejs"
  },
  "server_actions": {
    "files": ["src/actions/invoice.ts", "..."],   // found via `"use server"` scan
    "count": 54
  },
  "api_routes": [
    { "path": "/api/webhooks/paystack", "file": "src/app/(api)/api/webhooks/paystack/route.ts",
      "methods": ["POST"], "runtime": "nodejs", "dynamic": "force-dynamic" },
    "..."
  ],
  "cloudflare": {
    "durable_objects": [
      { "class_name": "ConversationThread", "source_file": "src/worker/do/conversation-thread.ts" },
      "..."
    ],
    "queues": { "producers": [], "consumers": [] },
    "crons": [{ "schedule": "* * * * *", "path": "/api/cron/payment-fanout" }],
    "bindings": {
      "hyperdrive": [{ "binding": "HYPERDRIVE", "id": "eea12bef..." }],
      "r2": [{ "binding": "ASSETS", "bucket": "pesastream-assets" }],
      "kv": [], "d1": [], "vectorize": [], "ai": []
    },
    "compatibility_flags": ["nodejs_compat", "global_fetch_strictly_public"],
    "compatibility_date": "2025-09-15"
  },
  "secrets": {
    "required": ["DATABASE_URL", "PAYSTACK_SECRET_KEY", "..."],   // scanned from process.env usage
    "source": "doppler"                                           // detected from scripts/.env/CLI
  }
}
```

Detectors to add:

- `src/middleware.ts` + `middleware.ts` at root
- Regex scan for `^"use server"$` directive at top of `.ts`/`.tsx` files
- Parse `src/app/**/route.{ts,js,tsx,jsx}` for HTTP method exports + `dynamic`/`runtime` consts
- Read `wrangler.toml` if present → DO classes, queue bindings, crons, bindings, compat flags
- AST-scan `src/**/*.{ts,tsx}` for `new DurableObject(...)` subclasses → DO class list
- Env var usage scan for required secret names

## 5. Worker shim extensions required

Current shim exports only `{ fetch }`. Target shape:

```js
// generated worker.mjs (not hand-written)
import { RafikiAgent, JibuAgent, /* ... */ } from "./_user_do_classes.mjs"; // re-exported
export { RafikiAgent, JibuAgent, ConversationThread, CallSession, ZidiAgent, AkibaAgent };

export default {
  async fetch(request, env, ctx) {
    applyBindingOverrides(env);        // Hyperdrive → globalThis + process.env
    await runMiddlewareIfPresent(...); // if metadata.middleware, invoke Next's middleware proxy
    return runNextHandler(request, env, ctx);  // existing path, but returning streaming Response
  },
  async scheduled(event, env, ctx) {
    applyBindingOverrides(env);
    const path = resolveCronPath(event.cron); // from metadata.cloudflare.crons
    return invokeRouteAsRequest("GET", path, env, ctx);
  },
  async queue(batch, env, ctx) {
    applyBindingOverrides(env);
    const handlerPath = resolveQueueHandler(batch.queue);
    return invokeQueueHandler(handlerPath, batch, env, ctx);
  },
};
```

New helpers needed:

- `applyBindingOverrides(env)` — per metadata.cloudflare.bindings, push into `globalThis.__PS_*` + `process.env.*`
- `runMiddlewareIfPresent(req, env, ctx)` — only if metadata.middleware is present
- `invokeRouteAsRequest(method, path, env, ctx)` — synthetic Request for cron
- `invokeQueueHandler(path, batch, env, ctx)` — dispatch queue message to user code
- Streaming response wiring — use `TransformStream`, return `Response` with readable half immediately

DO class sourcing is the tricky part: the shim needs to import the user's DO classes, but they live in `src/worker/do/*.ts`. Options:
- User declares them in `nextdeploy.config.ts`, the deploy step generates `_user_do_classes.mjs` that imports and re-exports them, esbuild bundles it in
- Or AST-scan + auto-generate the same file

The config-declared approach is safer (explicit + type-checkable). AST scan is nice as a "did you forget one?" warning.

## 6. Deploy pipeline additions required

Order of operations for a one-shot `nextdeploy deploy --target cloudflare`:

```
1. Discovery
   ├─ build metadata.json (enhanced, per §4)
   └─ validate: all DO classes found, all bindings declared, compat flags set

2. Build
   ├─ next build (standalone)
   └─ esbuild pass with --conditions=workerd,import,default
      - externals: node:*, cloudflare:*, user-declared
      - alias map for known quirky packages (pg-cloudflare ESM swap, Ably lazy, etc.)
      - single-pass, sourcemap on

3. Asset upload (R2)
   ├─ batch upload .next/static/* (immutable cache)
   ├─ batch upload public/* (short TTL)
   └─ dedupe by sha256 against remote manifest

4. Secret sync (if declared)
   └─ wrangler secret bulk from .env / Doppler / user-provided

5. Deploy (wrangler)
   ├─ render wrangler.toml from metadata.cloudflare.* if not already present
   ├─ wrangler deploy worker.mjs
   └─ poll for deploy ID

6. Post-deploy smoke
   ├─ curl every route_info.static_routes entry → expect 200/308
   ├─ curl sample dynamic_routes → expect non-500
   ├─ curl /api/diag/db if present → expect ok:true
   └─ fail + auto-rollback if any check fails

7. Emit deploy record
   └─ .nextdeploy/deploys/<timestamp>.json: built_id, route results, size deltas
```

Each step should be independently runnable (`nextdeploy build`, `nextdeploy upload`, `nextdeploy smoke`) so debugging doesn't require re-running the world.

## 7. Suggested phasing

Assumes one focused developer (you).

**Phase 1 — "Pesastream deploys without crashing" (target: 1 week)**

- Fix A4 (workerd conditions) + A5 (cloudflare:sockets ESM preservation) — unblocks any pg user, ~2 hours
- A1/A2/A3 — DO + cron + queue exports via user-declared config in `nextdeploy.config.ts`, ~2 days
- A6 — generic binding → `globalThis`/`process.env` override, ~0.5 day
- A7 — middleware detector + invocation, ~1 day
- A8 — streaming response, ~0.5 day
- B2 — R2 asset upload with per-type Cache-Control, ~1 day
- B3 — secret sync with `wrangler secret bulk`, ~0.5 day
- Validation gate: Pesastream deploys via nextdeploy → `/api/diag/db` returns `{ok: true}`, subdomain routing works, cron fires every minute

**Phase 2 — production-grade (target: 1 week)**

- B1, B4, B5, B6 — server actions audit, API route inventory, build-id wiring, WebSocket support
- Post-deploy smoke test runner
- Auto-rollback on smoke failure
- Sourcemap upload

**Phase 3 — framework parity (target: 1 week)**

- B7 — image optimization (Cloudflare Images route recommended)
- B8 — incremental cache / tag cache pluggable
- Skew protection, route preloading
- `nextdeploy.config.ts` typed config + validation

## 8. Non-goals — things nextdeploy should NOT match from OpenNext

Deliberate simplifications worth keeping:

- **No runtime bundle patching.** OpenNext applies 11+ esbuild plugins rewriting source mid-bundle. nextdeploy's single-pass + user-declared config model is the point. Patches go into the *pre-esbuild* source tree (e.g. `patch-pg-cloudflare.mjs`-style install-time rewrites) where they're debuggable.
- **No implicit cache behaviour.** If the user doesn't declare an incremental cache, nextdeploy deploys without one. OpenNext silently provisions DO-backed caches that cost money whether you asked or not.
- **No AWS fallback path.** OpenNext is aws-shaped first, CF as overlay. nextdeploy-cloudflare should be CF-native, no AWS shims.
- **No hidden magic.** Everything that ends up in the deploy (bindings, crons, DO classes, secrets) must be either declared in `nextdeploy.config.ts` or emitted into `metadata.json` where the user can inspect it. No reading env vars and "doing the right thing."

## 9. Test matrix to call nextdeploy CF-ready

A minimal Pesastream-shaped fixture app that the CI must deploy green:

- [ ] App Router + 1 static page + 1 dynamic `[slug]` page + 1 API route + 1 server action + middleware
- [ ] 1 Durable Object class with SQLite storage
- [ ] 1 cron at `*/5 * * * *` hitting an internal API route
- [ ] 1 Queue consumer
- [ ] Hyperdrive binding pointing to a real Postgres (Neon dev project)
- [ ] R2 binding serving `/public/*`
- [ ] 3 secrets via `wrangler secret bulk`
- [ ] `nodejs_compat` + `global_fetch_strictly_public` compat flags
- [ ] One `"use server"` action that writes through Hyperdrive to Postgres
- [ ] Smoke test: every route returns expected status, cron fires within 10 min of deploy, DO method round-trips

When this fixture deploys and stays green for 24h: nextdeploy-cloudflare is ready for Pesastream.

---

**Bottom line:** OpenNext is ~3 years of Vercel-adjacent plumbing ported to CF. nextdeploy's architecture (single-pass esbuild, explicit shim, metadata-as-contract, Go-owned pipeline) is the correct long-term shape — but hitting parity for a real app like Pesastream is ~2–3 weeks of focused work, not a weekend. Do not cut over any production traffic until the Phase 1 + Phase 2 gates are green.

---

## 10. Init, config, and build-flag concerns (added 2026-04-18)

Surfaced while adding Cloudflare to the `init` flow (`cli/internal/initialcommand/init.go`,
`shared/config/template.go`) and wiring Turbopack/webpack builder detection
(`shared/nextcore/build_flags.go`). None of these block today's commits but each will bite
before the Phase 1 gate closes.

### 10.1 `init` template is a dead end for Pesastream-shaped apps

The new `cloudflareTemplate` in `shared/config/template.go` emits:

- `CloudProvider.name: cloudflare` + `account_id`
- `serverless.provider: cloudflare`
- `serverless.cloudflare: { compatibility_date, compatibility_flags: [nodejs_compat_v2] }`
- commented stubs for `custom_domains`, `triggers.crons`, `bindings.r2`

Missing — every item below is called out as required in §3/§4/§5 above, but `init` gives the
user zero scaffolding for any of it:

- **Durable Object class declarations** (A2) — no prompt, no `durable_objects:` stub in yaml.
  Worse: the yaml schema has `serverless.cloudflare.bindings.durable_objects`, but §5 proposes a
  separate `nextdeploy.config.ts` as the canonical place to declare DO *source files* for the
  shim's `_user_do_classes.mjs` generation. We have not picked one. Pick before users start
  writing either form.
- **`scheduled()` / `queue()` handler wiring** (A1, A3) — yaml has a commented `triggers.crons`
  stub but no syntax for pointing a cron at a route handler. The Pesastream reference uses
  `/api/cron/payment-fanout` — nextdeploy has no way to express "cron `* * * * *` invokes this
  path" in the current yaml.
- **Hyperdrive binding injection** (A6) — `bindings.hyperdrive` slot exists, but nothing in the
  init flow documents that the shim is supposed to read `env.HYPERDRIVE.connectionString` and
  push it onto `process.env` / `globalThis` before the first request. Users will declare the
  binding, deploy, and fail at runtime with confusing `pg` connection errors.
- **Middleware** (A7) — no yaml block; detection is meant to be automatic but §4 says the
  detector currently misses `src/middleware.ts`. Subdomain routing silently dies.

**Concern:** the template compiles via `config.Load` and passes every static check, so users who
pick "Cloudflare (Workers + R2)" in `init` get a green yaml that is feature-incomplete for any
non-trivial app. We should either (a) keep the template minimal and put a `# See docs before
deploying a DO/queue/cron app` banner at the top, or (b) expand it with commented sections for
each of the items above so users see the shape of what's missing.

### 10.2 Config contract: two provider fields for one decision

`init` writes both `CloudProvider.name: cloudflare` AND `serverless.provider: cloudflare`.
Nothing in `shared/config` validates they agree. A user editing between providers can end up
with `CloudProvider.name: aws` + `serverless.provider: cloudflare` and either (a) silently pick
up AWS credentials for a CF deploy, or (b) fail opaquely at credential resolution.

**Fix options:**

- Make `serverless.provider` the single source of truth for dispatch; treat `CloudProvider.name`
  as a hint for credential file lookup only, OR
- Add a `config.Validate()` pass that errors loudly if the two disagree.

Second option is cheaper and preserves existing yaml.

### 10.3 Turbopack / webpack builder injection not verified on the CF path

`MaybeInjectWebpackFlag` runs unconditionally in `GenerateMetadata` for every target type,
appending `-- --webpack` to the `next build` command when Next ≥ 16 + `webpack:` key present +
no `turbopack:` key.

Unknowns on the Cloudflare path:

- `next build --webpack` produces the same `.next/standalone` layout as `next build
  --turbopack`. *Assumed*, not verified. The CF adapter (`cloudflare_adapter.go`) consumes that
  dir — if layouts diverge we'll bundle a broken worker silently.
- For a project that migrates to Turbopack (drops `webpack:`), the CF esbuild bundler still
  needs A4's `--conditions=workerd` fix. The flag injector has no opinion on this; the CF
  adapter has to own the workerd condition regardless.
- `build_flags.go` has zero unit tests. `scriptEndsWithNextBuild` uses prefix-match heuristics
  that will silently fail on scripts like `next build && next-sitemap`, `NODE_OPTIONS='...' next
  build`, or monorepo forms like `turbo run build --filter=web`. Users will see the old
  Turbopack error with no indication that nextdeploy tried and gave up.

**Minimum ask:** unit tests for `scriptEndsWithNextBuild` covering at least:

- `next build` (trivial)
- `prisma generate && next build`
- `next build && next-sitemap` (should warn, not inject)
- `NODE_OPTIONS='--max-old-space-size=4096' next build`
- `turbo run build --filter=web` (monorepo — can't reason about, should warn)
- `bun run build` (already the wrapper — detection should look at the resolved script body)

### 10.4 `nextdeploy build` is target-agnostic where it shouldn't be

`GenerateMetadata` runs the same `next build` regardless of `cfg.Serverless.Provider`. The CF
path will need target-specific build-time decisions:

- Force `output: 'standalone'` (CF adapter assumes it)
- Set `serverActions.bodySizeLimit` ≤ CF's 100MB request cap (hard fail if higher)
- Error if `experimental.turbo` is set but the CF worker shim isn't turbopack-compatible

The build-flags layer should take `cfg.Serverless.Provider` as input and branch. Right now it
only branches on Next version. Same applies to metadata emission: §4's CF-specific metadata
extensions should only be computed when deploying to CF, to keep the AWS pipeline unchanged.

### 10.5 End-to-end smoke: `init → build → deploy` has never run for Cloudflare

Memory note `cloudflare_state.md` says the IaC + adapter + shim compile but have never been
exercised against a real CF account. With §10.1's init now pointing users at that path, the
risk shifts: a user can pick "Cloudflare" in `init`, hit `nextdeploy build` (works — it's just
`next build`), then `nextdeploy deploy` and land in the bug-iceberg §3 enumerates.

**Ask:** gate the `init` Cloudflare option behind an explicit "experimental — see
CLOUDFLARE_PARITY.md" prompt until Phase 1 closes, OR run a minimum fixture deploy in CI (the §9
test matrix) before advertising CF-as-a-target via `init`.
