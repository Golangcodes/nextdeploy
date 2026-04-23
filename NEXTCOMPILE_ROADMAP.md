# nextcompile — remaining work

Pick up here when we resume. This file is the authoritative punch list so
we don't have to re-derive the plan from conversation history.

The commitment is: **our own runtime, composed from public React APIs.
No wrapping of Next's internal `app-render` module.** What we write is a
thin composition + dispatch layer on top of React, not a rendering engine.

---

## Strategic framing — is this worth developer time?

**Not yet.** We'd be a worse OpenNext with extra steps today. OpenNext
already renders real App Router apps on Cloudflare. Our runtime doesn't,
and we have zero production dogfooding. If we only ship runtime parity,
no one picks us over OpenNext + wrangler.

### The one thing that would change that

**Auto-provisioned infrastructure from code.** Not RSC. Not multi-provider.
Not any single runtime feature. The moment that makes someone post about
this tool:

> I wrote `process.env.DATABASE_URL` in my Next app. I ran
> `nextdeploy ship`. It noticed the reference, asked me once for the
> value, created the binding, deployed the Worker, and the app worked.
> No wrangler.toml. No AWS console. No terraform.

Extended cases:

- `fetch("https://my-kv-host/...")` → "looks like you're hitting a KV;
  bind one?"
- `revalidateTag("posts")` → auto-provisions the revalidation queue.
- `<Image src="https://images.example.com/...">` → adds the remote
  pattern, enables CF Images binding.
- `import { Ai } from "cloudflare:ai"` → detects, prompts for binding
  name.
- `fetch("https://*.r2.cloudflarestorage.com/...")` → suggests R2
  binding and prompts for bucket name.

### Why this is the killer feature

- Vercel has zero-config because they built the runtime AND the
  platform. You can't get that elsewhere.
- OpenNext + wrangler has config the developer writes by hand in
  `wrangler.toml`.
- Pulumi + Terraform has config the developer writes by hand.
- **Nobody reads your code and provisions the infrastructure it
  actually needs.**

The gap isn't in rendering. It's in the human step between "I wrote
the code" and "the infrastructure exists."

### Concrete work to ship this

Scope: ~400 Go LOC + a clean interactive prompt UX. Two weeks of
focused work. Ships **before** the RSC runtime phases because it's the
reason someone would pick nextdeploy over existing tools. RSC runtime
becomes a Week 3+ priority once there are users asking for it.

1. **Finish `deriveBindings`.** Move
   `shared/nextcompile/compiler.go:deriveBindingsStub` from "echo every
   env ref" to a real extractor that recognizes:
   - `process.env.X` references → secret binding hints
   - `fetch(...)` URL patterns matching known platform services →
     binding suggestions:
     - `*.r2.cloudflarestorage.com` → R2 binding
     - `api.cloudflare.com/client/v4/accounts/*/d1/*` → D1 binding
     - `api.cloudflare.com/client/v4/accounts/*/storage/kv/*` → KV binding
   - Well-known imports (`cloudflare:ai`, `@cloudflare/workers-types`,
     hyperdrive references) → specific binding suggestions
   - De-duplicate against bindings already declared in
     `cfg.Serverless.Cloudflare.Bindings`
2. **First-deploy interactive flow.** New module
   `cli/cmd/ship_provision.go`. When `CompiledBundle.SuggestedBindings`
   has entries not already declared in cfg:
   - Print the hint + reason + detected source file path
   - Prompt: "Create as <kind>? [Y/n/skip/edit]"
   - On Y: collect the value (for secrets) or the identifier (for
     KV/R2/D1/etc.)
   - Save the accepted decisions back into `nextdeploy.yml` under a
     generated `# auto-provisioned:` section so re-runs skip the prompt
3. **Automatic provisioning.** Extend `CloudflareProvider.Plan` +
   `Provision` to consume `SuggestedBindings`:
   - Plan phase surfaces proposed creations alongside user-declared
     resources in the same drift table
   - Provision phase creates them via existing helpers
     (`cloudflare_resources.go`: `ensureHyperdrive` / `ensureQueue` /
     `ensureVectorize` / `ensureAIGateway`) — secrets flow through the
     existing `UpdateSecrets` path
   - New helpers needed: `ensureKV`, `ensureR2Bucket` (R2 bucket exists
     via `ensureR2BucketExists` at `cloudflare.go:666`), `ensureD1`
4. **Idempotent re-runs.** Once a hint is accepted and written into
   cfg, subsequent scans skip it. The generated section in
   `nextdeploy.yml` is the declarative record of what was provisioned.
   Re-running `ship` against an unchanged codebase does nothing new.
5. **Graceful escape hatches.**
   - `--no-auto-provision` — fail loudly on any missing binding instead
     of prompting. For teams that want manual control.
   - `--yes` / `CI=true` — auto-approve safe hints (secrets-only) and
     hard-fail on ambiguous ones (which binding matches this fetch?).
     Used by CI pipelines.
   - `nextdeploy provision` — run just the prompt/create loop without
     a deploy. Useful for first-time setup.

### Why this goes before runtime phases

Runtime correctness only matters to people who are already using the
tool. The "aha" moment that makes someone install it in the first
place is the auto-provisioning. RSC rendering is the reason they keep
using it.

**Priority order flips to:**

1. Ship auto-provisioning against a real trivial Next app (API routes +
   one env var + one KV access). Smoke-test the flow end-to-end.
2. Dogfood publicly — get 5–10 devs using it. Collect the real
   failures.
3. Then fix what breaks in Phase 1+ of the runtime roadmap based on
   real app failures, not hypothetical ones.

This is the actual hill. Everything in the Phased plan below is secondary.

### What makes this hard (the honest risks)

1. **False positives.** Suggesting a binding for every `fetch()` call
   produces noise. The extractor must be conservative; wrong suggestions
   destroy trust in the auto-provisioning faster than no suggestions.
2. **Ambiguous bindings.** `fetch("https://api.example.com/")` could be
   a third-party API or a custom service binding. Default to no hint;
   prompt only when patterns match known platform services.
3. **Interactive in CI.** Prompting in CI is a deploy-stopper. Must
   detect non-TTY early and either auto-approve safe hints or fail with
   a clear message pointing to `--yes`.
4. **Migration from existing wrangler.toml users.** Some devs already
   have bindings declared. The first-deploy flow must read existing
   declarations and not double-provision. Idempotence is load-bearing.
5. **Secrets UX.** Prompting for a database URL at the terminal is fine
   for first-run. For rotation, operators use `nextdeploy secrets set`.
   Don't re-prompt on every deploy — detect that a secret *binding*
   exists even if the value is only in Secrets Manager / Worker secrets.

---

## Current state

Built and tested (all green, `go build ./...` + `go vet ./...` clean).

- **Go side** (`shared/nextcompile/`, ~3200 LOC)
  - 13-phase `Compile()` pipeline: DetectVersions → ScanCompiledServer →
    DetectServerActions → DeriveBindings (stub) → ElideDeadRoutes (stub)
    → ensure OutDir → EmitManifest → EmitDispatchTable → ExtractRuntime
    → VendorRSC → EmitActionManifest → EmitWorkerEntry → content hash.
  - Reproducible, content-addressable output. Parallel scan via
    errgroup. Version-aware runtime variant picker. Honest vendoring
    step that copies `react-server-dom-webpack/server.edge` from
    node_modules into the bundle.
- **JS runtime** (`shared/nextcompile/runtime_src/`, ~1400 LOC)
  - `dispatcher.mjs` — fetch handler with proxy/middleware → static/SSG
    → dynamic routes dispatch.
  - `context.mjs` — AsyncLocalStorage with async `cookies()`, `headers()`,
    `draftMode()`, `after()`.
  - `rsc.mjs` — RSC renderer skeleton; vendored Flight encoder loads at
    request time or returns 501.
  - `actions.mjs` — Server Actions POST dispatch with CSRF, context,
    JSON-fallback response.
  - `cache.mjs` — `revalidatePath` / `revalidateTag` / `unstable_cache`
    with in-memory + KV tiers.
  - `image.mjs` — `/_next/image` with remote-pattern validation +
    Cloudflare Images binding.
  - `serve.mjs`, `route_match.mjs`, `errors.mjs` — supporting modules.
  - `next_shims/{cache,headers,server}.mjs` — esbuild-aliased shims so
    user imports from `next/cache`, `next/headers`, `next/server` resolve
    into our runtime without source modification.
- **Adapter integration** (`cli/internal/serverless/`)
  - `cloudflare_adapter.go` — runs `nextcompile.Compile()` then
    `esbuild` on the generated entry. Prints a capability report.
  - `nextcompile_bridge.go` — `nextcore.NextCorePayload` →
    `nextcompile.Payload`.
  - `smoke.go` — post-deploy probe with retries, warn-only by default.
- **CLI**
  - `nextdeploy ship` wired through the new pipeline via `serverless.Deploy`.
  - `nextdeploy <cmd> explain [--code]` for all 16 commands.

Capability state of a deployed Worker:

| Feature | Works | Notes |
|---|---|---|
| API routes (App Router + Pages Router) | yes | inside ALS context |
| Static / `/public` / `/_next/static` | yes | from R2 |
| Dynamic routes with params | yes | specificity-ordered |
| Middleware + `proxy.ts` | partial | passthrough OK; `NextResponse.rewrite/redirect` semantics not honored |
| async `cookies()` / `headers()` / `draftMode()` | yes | real ALS |
| Server Actions POST + invoke + result | yes | CSRF + body parse + context; JSON fallback response (not Flight) |
| RSC pages with no client components | partial | renders, minimal shell, no layouts yet |
| RSC pages with `"use client"` | **no** | Phase 1 target |
| Suspense + streaming | **no** | Phase 3 target |
| `loading.js` / `error.js` / `not-found.js` | **no** | Phase 4 target |
| `revalidatePath` / `revalidateTag` | yes | local + KV tier; no cross-worker queue fan-out |
| `/_next/image` via CF Images | yes | binding optional, passthrough fallback |
| `after()` | yes | via ctx.waitUntil |
| PPR | detected → **501** | Phase 6 target |

---

## Phased plan

Every phase is independent and improves correctness on its own. Numbers are
solo-engineer estimates; halve with two focused engineers.

### Phase 1 — Client-reference-manifest threading (3–5 days)

**Unlocks:** RSC pages with `"use client"` components finally render
without throwing in the Flight encoder.

**What already exists**
- `shared/nextcompile/scanner.go:attachClientManifests` already populates
  `ModuleRef.ClientManifestPath` for each page that has a sibling
  `page_client-reference-manifest.json`.
- `shared/nextcompile/dispatch.go:renderEntryFields` already emits
  `loadClientManifest: () => import(...)` per entry.
- `shared/nextcompile/runtime_src/rsc.mjs` already has the slot where
  `clientManifest.clientModules` becomes `bundlerConfig` in the
  `renderToReadableStream` call.

**What's missing**

1. **Go-side copy step.** The Next-emitted client manifests live under
   `.next/server/app/**/page_client-reference-manifest.json`. They stay
   in place as part of the standalone tree esbuild traverses, so the
   `loadClientManifest` `import()` finds them — but we should verify this
   by adding an integration assertion. If esbuild's bundling doesn't
   follow JSON imports with the dynamic-import-attribute form, we need
   to either:
   - pass `--loader:.json=json` (already set) plus ensure the dynamic
     import uses the attribute form (done), OR
   - copy the manifests into `_nextdeploy/client-manifests/` and
     reference those paths instead.
2. **Bundler-config shape translation.** React's `bundlerConfig` is a
   map keyed on `"file:export"` strings with values
   `{id, name, chunks, async}`. Next's manifest sometimes wraps this
   under `.clientModules` or `.ssrModuleMapping`. The rsc.mjs currently
   picks one or the other defensively — needs a real test against a
   real Next 14 + 15 manifest pair.
3. **Vendor `react-server-dom-webpack/client`** (not just `/server.edge`).
   Clients decode the Flight stream via
   `createFromReadableStream(stream, { moduleLoader, ... })`. This is a
   different vendored bundle file. Extend `vendor.go` to copy it too.
4. **Emit chunk script tags in the HTML shell.** When a page has client
   components, the emitted HTML must include `<script>` tags that
   reference the webpack chunks the client manifest lists. Without
   these, hydration will fetch them on demand — works but costs a
   roundtrip per chunk.
5. **Tests:** add a Next 14 App Router fixture with one `"use client"`
   component, assert the emitted bundler config contains the expected
   `clientModules` entry, and that `rsc.mjs` doesn't 501.

**Files touched**
- `shared/nextcompile/vendor.go` — extend for client bundle
- `shared/nextcompile/compiler.go` — vendor both server+client
- `shared/nextcompile/runtime_src/rsc.mjs` — bundlerConfig wiring
- `shared/nextcompile/runtime_src/dispatcher.mjs` — possibly emit
  chunk-preload tags
- tests in `shared/nextcompile/compiler_test.go`

**Done when:** a fixture page importing a `"use client"` component
renders without throwing; emitted HTML references the client chunks.

---

### Phase 2 — Layout → React-tree composition (5–7 days)

**Unlocks:** `app/layout.js` + nested layouts wrap the page correctly.
Currently we only walk the layout chain in the scanner but don't compose
them into the render tree.

**What already exists**
- `scanner.go:attachLayoutChains` walks App Router directories and
  records the `LayoutChain` for each page.
- `dispatch.go` emits `loadLayouts: [() => import(...), ...]` per entry.
- `rsc.mjs:buildLayoutTree` does a primitive composition via direct
  function calls (no JSX).

**What's missing**

1. **Proper JSX composition.** Today we do `LayoutComponent({children: node})`.
   That bypasses React's element creation, breaking things like memoization
   and Suspense boundary detection. Need to use `React.createElement` so
   Suspense / ErrorBoundary wrapping works in Phase 3–4.
2. **`template.js` support.** Templates are like layouts but create a new
   instance on navigation. Our scanner does not yet distinguish them.
3. **Route group segments in the layout chain.** Route groups `(foo)` are
   stripped from the URL but their layouts still apply. Scanner already
   strips groups from `RoutePath`; need to verify `LayoutChain` still
   picks up layouts inside group directories.
4. **Root vs nested layout distinction.** Root layout must include
   `<html>` and `<body>`. Nested layouts must not. React throws a
   hydration mismatch if we produce two `<html>` elements. Simple rule:
   first layout in the chain owns the document shell; all others are
   fragments.
5. **Async layout support.** Next layouts may be async (`async function
   Layout({children})`). React supports this in Server Components but
   our naive invocation may not await correctly.

**Files touched**
- `shared/nextcompile/runtime_src/rsc.mjs:buildLayoutTree`
- `shared/nextcompile/scanner.go:attachLayoutChains` — template support
- `shared/nextcompile/types.go` — possibly a `TemplateChain` field

**Done when:** a page nested three layouts deep renders with the nav
bars + footers the layouts declare, in the right order.

---

### Phase 3 — Real HTML shell via `react-dom/server.edge` (3–5 days)

**Unlocks:** proper streaming HTML with Suspense resolution. Replaces our
hand-rolled `wrapFlightInHtmlShell` function.

**What already exists**
- Minimal hand-rolled shell in `rsc.mjs:wrapFlightInHtmlShell` that
  emits doctype + body + inline Flight payload.
- Vendored `react-server-dom-webpack/server.edge` for Flight encoding.

**What's missing**

1. **Vendor `react-dom/server.edge`.** This is the HTML renderer. Same
   vendoring pattern: resolve from `node_modules/react-dom`, copy the
   matching `server.edge.production.js` build into
   `runtime/vendor/react-dom/server.edge.mjs`.
2. **Two-stage render.** Correct pattern per React 19 docs:
   - Pass RSC tree to `react-server-dom-webpack/server.edge.renderToReadableStream`
     → Flight stream.
   - Tee/consume the Flight stream + pass tree to
     `react-dom/server.edge.renderToReadableStream` which handles HTML
     streaming with Suspense + chunk references.
   - Pipe the resulting HTML stream into the Response body.
3. **Flight payload injection.** The HTML stream must include the Flight
   payload as `<script id="__FLIGHT__" type="application/x-component">`
   so the client can hydrate from it. React DOM handles this
   automatically if we use the `bootstrapScripts`/`bootstrapModules`
   options correctly.
4. **Progressive enhancement.** If the Flight render fails partway
   through, the HTML stream still completes with whatever has been
   emitted. Avoid buffering.

**Files touched**
- `shared/nextcompile/vendor.go` — extend for react-dom
- `shared/nextcompile/runtime_src/rsc.mjs:wrapFlightInHtmlShell` → delete;
  replace with direct `renderToReadableStream` pipe.

**Done when:** a page with three levels of Suspense boundaries emits
HTML progressively — fallback first, resolved content later — without
buffering to end-of-stream.

---

### Phase 4 — Error + loading + not-found (3–4 days)

**Unlocks:** `error.js`, `loading.js`, `not-found.js` conventions work as
Next users expect.

**What's missing**

1. **Scanner detection** of sibling special files (`error.js`, `loading.js`,
   `not-found.js`). Similar shape to `attachClientManifests` —
   per-directory siblings of the page. Attach to `ModuleRef` as
   `ErrorPath`, `LoadingPath`, `NotFoundPath`.
2. **Composition rules** in `rsc.mjs`:
   - `loading.js` → wrap the page subtree in `<Suspense
     fallback={<LoadingComponent />}>`.
   - `error.js` → wrap in an ErrorBoundary component that catches and
     renders the error component.
   - `not-found.js` → catch the `notFound()` sentinel from
     `context.mjs` at the route boundary and render the not-found
     component instead of the page.
3. **Inheritance rules.** An `error.js` at a parent layout covers all
   descendant pages unless overridden. Scanner must walk up the
   directory tree and attach the nearest ancestor's error/loading/not-
   found component — not just the immediate sibling.
4. **ErrorBoundary helper.** React doesn't ship a built-in
   ErrorBoundary component; we provide one in the runtime. Class
   component with `componentDidCatch` + `getDerivedStateFromError`.
   About 20 LOC.

**Files touched**
- `shared/nextcompile/scanner.go` — detect special files per-page
- `shared/nextcompile/types.go` — extend `ModuleRef`
- `shared/nextcompile/dispatch.go` — emit loaders
- `shared/nextcompile/runtime_src/rsc.mjs` — compose wrappers
- `shared/nextcompile/runtime_src/error_boundary.mjs` — new 20-LOC file

**Done when:** a fixture page that throws in a server component renders
via the nearest `error.js`; a page fetching on a slow handler shows
`loading.js` first.

---

### Phase 5 — Parallel + intercepting routes (4–6 days)

**Unlocks:** advanced Next routing features. Optional — only apps using
them need it.

**What's missing**

1. **Parallel routes (`@slot` directories).** A layout can declare named
   slots via `@slot` subdirectories. The slot's page contributes to a
   prop of the same name on the layout. Scanner must find `@slot` dirs
   and attach their compiled paths as additional entries in the layout's
   prop map.
2. **Intercepting routes.** `(.)foo`, `(..)foo`, `(...)foo` directories
   intercept navigation. At dispatch time, if the request carries the
   `Next-Router-State-Tree` header indicating soft navigation, we
   prefer the intercepting route. Otherwise fall through to the real
   route.
3. **Default route behavior.** When a parallel slot has no matching
   page, Next falls back to `default.js`. Scanner must detect and
   dispatch accordingly.

**Files touched**
- `shared/nextcompile/scanner.go` — parallel + interception detection
- `shared/nextcompile/runtime_src/dispatcher.mjs` — interception dispatch
- `shared/nextcompile/runtime_src/rsc.mjs` — slot prop composition

**Done when:** fixture apps using `@modal` slots and `(.)photos/[id]`
intercepting routes work.

---

### Phase 6 — PPR static-shell protocol (2 weeks)

**The only genuinely hard phase.** PPR is Next's newest experimental
feature and its implementation detail leaks into the compiled output.

**What's missing**
1. Detect PPR opt-in at scan time (already have `PPREnabled`).
2. At build time: pre-render the static portion of a PPR page to HTML
   via the Phase 3 renderer, store in R2.
3. At request time: serve the static shell immediately while rendering
   the dynamic holes in background. Dynamic hole markers get replaced
   via a separate Flight stream.
4. Cache coordination — revalidatePath must invalidate both the static
   shell in R2 and the per-hole cache keys.

Skip until an app actually needs it. Current `rsc.mjs:pprNotImplemented()`
returns a clear 501.

---

## Non-RSC remaining work

Independent of the phased plan above.

### Middleware `NextResponse` semantics

`dispatcher.mjs:runMiddlewareLike` currently treats any returned Response
as a short-circuit. Real middleware uses `NextResponse.rewrite(url)`,
`NextResponse.redirect(url)`, `NextResponse.next()` — each with specific
semantics.

- `rewrite` → internal URL rewrite, continues dispatching against new
  URL without redirecting the client
- `redirect` → 30x response
- `next()` → continue with optional header mutations

Requires inspecting response headers for `x-middleware-rewrite`,
`x-middleware-next`, etc. Our `next_shims/server.mjs` already exports a
`NextResponse` class with these factories; dispatcher just needs to read
the markers.

Scope: ~150 JS LOC.

### ISR Queue-based revalidation fan-out

`cache.mjs` has in-memory + KV tiers. Cross-worker propagation of
invalidation (one Worker revalidates, all other Workers pick it up)
needs either:

- short KV TTL (already implemented, lazy consistency)
- dedicated revalidation queue consumer (eager, but requires a second
  Worker + CF Queue binding)

Second-Worker consumer is the Open-Next-style approach. Scope:
~300 JS LOC + Go-side `cloudflare_queues.go` consumer Worker
declaration.

### `BasePath`, `I18n`, `ImageConfig` in bridge

`toCompilePayload` at `cli/internal/serverless/nextcompile_bridge.go`
drops these because they live in `NextConfig` not `NextCorePayload`.

Fix: parse `next.config.js` via `nextcore.ParseNextConfigFile` in the
adapter and thread the result through the bridge.

Scope: ~40 LOC Go.

### Bindings: `deriveBindings` full implementation

Current stub at `shared/nextcompile/compiler.go:deriveBindingsStub` only
handles `process.env.X` references. Real implementation extends with:

- fetch target URL patterns → suggest KV/R2/D1 bindings
- references to `getCloudflareContext` or similar imports → suggest
  platform bindings
- de-duplication against explicit bindings already declared in cfg

Scope: ~200 LOC Go.

### `elideDeadRoutes` real implementation

Stub at `shared/nextcompile/compiler.go:elideDeadRoutesStub`. Real
implementation: set-difference between scanned refs and the declared
routes in `Payload.Routes`. Drop orphaned refs.

Scope: ~50 LOC Go.

### Flight-encoded action responses

`actions.mjs` returns JSON + a header signaling the Next client to do a
full-page reload. Real Flight responses preserve client-side state.

Needs `encodeReply` from `react-server-dom-webpack/server.edge` (already
vendored). Replace the JSON serialization path with a Flight encode
path.

Scope: ~80 JS LOC.

### Full middleware matcher semantics

`toCompilePayload` drops Next's advanced matcher conditions
(`has`/`missing`/header/cookie). Middleware runs on every request
currently; it should honor the configured matchers before invocation.

Scope: ~100 JS LOC + ~30 Go LOC.

---

## Dogfooding plan

Before (or in parallel with) Phase 1, ship against a real Next app.

1. Start with a real Next 14 + App Router project — minimal: static home +
   one API route + one `"use client"` component.
2. Point `nextdeploy.yml` at `provider: cloudflare`.
3. Set Cloudflare creds via `nextdeploy creds set --provider cloudflare`.
4. `nextdeploy build` + `nextdeploy ship`.
5. Whatever breaks first is the real Phase 1 starting point.

Likely first failures (tracked in advance):
- esbuild alias resolution path (adapter uses relative paths; may need
  absolute or different separator).
- `node:*` compat edges — some Next internals touch `perf_hooks`,
  `inspector`, etc. Our `--external:node:*` lets the Worker runtime
  provide them; `nodejs_compat_v2` should cover most.
- Compiled server.js output format across Next 14.0 → 15.x — we've
  scanned for generalities but real builds may have edge cases.
- Client manifest absent (Phase 1 gap).

---

## Risks — eyes open

1. **Next compiled-output drift per minor.** Detect via CI fixture
   matrix. Budget 1–2 weeks per Next minor bump to chase.
2. **React Flight protocol drift per major.** Protocol is stable within
   a major. Pin React version in our vendoring; test against new React
   majors before shipping to users.
3. **Hydration bugs are silent.** Server HTML must match client VDOM
   exactly. Only visible via user reports. Mitigation: test every
   fixture with real browser rendering in CI (Playwright against a real
   Worker).
4. **PPR is Next's frontier.** Changes frequently. Don't chase until an
   app needs it.
5. **Dogfooding will surface unknowns.** Everything above is synthetic
   fixtures. First real deploy will have concrete breakage — that's
   fine, that's what the plan is for.

---

## Decision log

- **Own runtime, not wrap Next internals.** 2026-04. Option B from the
  architectural fork. Wrapping Next's `app-render` was an alternative
  we rejected because it makes us identical to OpenNext with extra
  steps.
- **Compose public React APIs, don't reimplement React.** 2026-04.
  Correction after realizing Phase 2–5 are straight React composition,
  not protocol work.
- **Vendor React server + client bundles at build time.** 2026-04. No
  network at Worker runtime. Per-Next-version vendoring picks the right
  React major from the app's own node_modules.
- **KV-backed revalidation + optional queue consumer (future).** 2026-04.
  Two-tier invalidation; lazy cross-worker consistency until Queue
  consumer lands.
- **PPR deferred.** 2026-04. Honest 501 in the meantime.
- **Auto-provisioning is the killer feature, not runtime parity.**
  2026-04. Strategic reframe. Shipping another OpenNext-equivalent
  runtime earns no new users; shipping "code → infrastructure with no
  wrangler.toml" gives us a reason to exist. Auto-provisioning goes
  before RSC runtime phases in the build order.

---

## Open questions to decide when we resume

1. **Which React version to target in the vendoring story?** React 18.3
   (Next 14 LTS) vs React 19 (Next 15). Vendor both, pick at build
   time based on detected version?
2. **Do we want a `nextdeploy smoke` subcommand** that re-runs `SmokeVerify`
   independently of a deploy? Useful for CI health checks.
3. **Do we ship a minimal hydration-bootstrap client.mjs via embed**
   (~30 LOC) or require the app to provide its own? Shipping it keeps
   the experience zero-config.
4. **Do we bundle the client-side Flight parser** into every deploy, or
   only when the app has client components? Scanner already knows which
   apps need it.
5. **How do we handle apps that use both middleware.ts + proxy.ts?**
   Next's documented behavior says proxy wins; ours currently runs
   proxy then middleware. Decide explicitly.

---

## Estimated timeline

**Ship order (priority-reordered after strategic framing):**

| Order | Work | Time |
|---|---|---|
| 1 | Auto-provisioning (killer feature — see Strategic framing) | 2 weeks |
| 2 | Dogfood a trivial Next 14 app end-to-end, fix what breaks | 1 week |
| 3 | Runtime Phase 1 — client-reference-manifest threading | 3–5 days |
| 4 | Runtime Phase 2 — layout composition | 5–7 days |
| 5 | Runtime Phase 3 — HTML shell via react-dom/server.edge | 3–5 days |
| 6 | Runtime Phase 4 — error/loading/not-found | 3–4 days |
| 7 | Non-RSC work (middleware semantics, deriveBindings full, etc.) | 2–3 weeks in parallel |
| 8 | Runtime Phase 5 — parallel/intercepting routes | 4–6 days (optional) |
| 9 | Runtime Phase 6 — PPR static-shell protocol | 2 weeks (optional) |

**Solo engineer to a credible product (items 1–7): 7–9 weeks.**
**Two engineers: 4–5 weeks.**

Items 8–9 are optional polish — skip until apps actually need them.

---

## How to resume

1. Read this file top-to-bottom.
2. **Build auto-provisioning first, not Phase 1 of the runtime.** See
   the "Strategic framing" section above for the rationale. The scanner
   already extracts env refs + fetch targets; `deriveBindingsStub` is
   where the real work starts. Expected output: a developer writes
   `process.env.X` in Next code, runs `nextdeploy ship`, gets prompted
   once, and the binding materializes.
3. Dogfood against a real trivial Next app in parallel — API routes +
   one env var + one KV fetch. First breakage is the ordering signal
   for everything else.
4. Only after auto-provisioning ships do Phase 1 of the runtime
   roadmap. By then we'll have real deploy feedback driving the runtime
   priorities rather than speculative ordering.
5. Update this file as milestones complete so future-you sees the
   current state.

The architecture is correct. The build pipeline is correct. The runtime
is a thin composition layer waiting to be written. The differentiator
is the auto-provisioning step that sits above all of it. Ship that
first; runtime correctness follows once there are users asking for it.
