# NextDeploy

[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=Golangcodes_nextdeploy&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=Golangcodes_nextdeploy)
[![Go Report Card](https://goreportcard.com/badge/github.com/Golangcodes/nextdeploy)](https://goreportcard.com/report/github.com/Golangcodes/nextdeploy)

> **A pure-Go CLI that deploys Next.js apps to AWS or your own server.**
> Single binary. No Node. No IaC framework. No state file.
> Built to show JS/TS developers what Go is actually good at.

---

## What is this?

NextDeploy is a **single Go binary** that takes a Next.js app from `npm run build` to production, on either:

1. **Your own VPS** (Hetzner, DigitalOcean, Linode, Scaleway, anything with SSH) — runs as a managed `systemd` service via the `nextdeployd` companion daemon. No Docker, no Nginx config tedium, no Kubernetes.
2. **AWS serverless** (Lambda + CloudFront + S3 + ACM + SQS + Secrets Manager) — provisioned and reconciled directly from the CLI. No CDK, no Pulumi, no Terraform, no Serverless Framework.

The same `nextdeploy.yml` config works for both targets. The same `nextdeploy deploy` command ships either one.

> [!WARNING]
> **Hobby project, not production-ready.** This is a learning vehicle and a proof of concept. The AWS path is the most mature; the Cloudflare path is scaffolded only. Use it on real workloads at your own risk.

> [!NOTE]
> **Built with AI-assisted coding — and that's a feature, not a confession.**
>
> I'm learning Go, Next.js internals, and AWS infrastructure as I build this, and I use AI assistants (Claude, Copilot, Cursor) as a core part of how I learn and ship. I'm not claiming deep expertise in any of these areas — I'm sharing the work openly so other people learning the same things can read along, find bugs, and improve it.
>
> **If you contribute, please use AI too if it helps you.** This project is also a working example of what thoughtful AI-assisted development looks like: read the code yourself before accepting suggestions, test what lands, document the *why*, and review the AI's output the same way you'd review a junior developer's PR. AI is a tool — like `gofmt`, like `golangci-lint`, like Stack Overflow before it. Pretending we don't use it doesn't make us better engineers; using it well does.
>
> If something in the code looks wrong, it probably is. Open an issue or PR. Honest feedback from people who *do* have the expertise is the whole reason this is public.

---

## Why does this exist?

Three reasons, in order of importance:

### 1. To show JS/TS developers what Go is actually good at

Most developers in the JavaScript world think of Go as "the language Docker is written in". They've never seen what makes it the dominant choice for infrastructure tooling: **single static binaries, sub-millisecond startup, no dependency tree, no runtime to install, real concurrency, and a standard library that takes infrastructure work seriously**.

Every other Next.js deployment tool — Vercel, SST, OpenNext, Netlify CLI, Amplify — is written in TypeScript, by JavaScript developers, for JavaScript developers. Each one needs Node installed before it can do anything. Each one ships hundreds of megabytes of `node_modules`. Each one takes 10–30 seconds to boot before it makes its first AWS API call.

NextDeploy is the opposite. It's a single statically-linked Go binary. It boots in **~38 milliseconds** on a stock Linux laptop. It has zero runtime dependencies. It exists to be a working example of what infrastructure tooling looks like when you use the right language for the job.

You don't have to take that on faith — there's a benchmark in the repo. Run `make bench-startup` and it'll time `nextdeploy --version` against any other deploy CLI you have installed (Vercel, SST, Wrangler, Netlify, Amplify). Sample run from this machine:

```
  nextdeploy (this repo)          min   35 ms   median   38 ms   mean   41 ms
  vercel                          min  200 ms   median  226 ms   mean  228 ms
```

That's ~6× faster than Vercel's CLI on the cheapest possible invocation, before either tool has done any real work. The gap is structural, not optimization: NextDeploy is native code with no interpreter to boot. Vercel (and SST, Wrangler, Netlify CLI, Amplify) all have to start Node first. That cost is unavoidable in their architecture and free in ours.

### 2. To learn Next.js internals from the ground up

Building NextDeploy means understanding:

- How Next.js standalone builds work (`.next/standalone/server.js`, the manifest files, the chunked output).
- How the runtime expects its environment (`process.env`, working directory, port binding).
- How to bridge an event-driven invocation model (AWS Lambda) to a long-running HTTP server (Next.js's `server.js`).
- How ISR, image optimization, middleware/proxy, and edge caching actually communicate with the runtime.
- How Next.js evolves between versions (e.g. Next 15 renamed `middleware.ts` to `proxy.ts`) and how a deployment tool keeps up.

The most interesting file in this repo is `cli/internal/serverless/aws.go`, where a Node.js shim spawns the Next.js server as a child process inside Lambda and proxies HTTP requests to it. That shim is a working laboratory for everything above. It is deliberately not delegated to OpenNext, because the whole point is to learn what OpenNext is solving.

### 3. To prove "fast Go deploy, pure Go" is a real thing

The deploy story today is dominated by tools that assume you already have Node + an IaC framework + a state file + a configured cloud provider plugin. NextDeploy's claim is simpler: **download a binary, write a YAML, run one command**. That's it. No bootstrap, no `npm install -g`, no `cdk init`, no provider plugins to download.

If you want to see what "fast" means in concrete terms, run `nextdeploy --version` next to `sst --version` and time them. The difference is the entire point.

---

## How it works

### VPS path (the original use case)

```
┌────────────┐  SSH/RPC  ┌─────────────────┐
│ nextdeploy │──────────▶│   nextdeployd   │
│   (CLI)    │           │  (daemon, on VPS)│
└────────────┘           └─────────────────┘
       │                          │
       │                          ├─ systemd unit per app
       │                          ├─ secrets via EnvironmentFile
       │                          ├─ live log streaming
       │                          └─ health checks + auto-restart
       │
       └─ build, package, ship via SSH
```

**You don't install the daemon yourself.** Running `nextdeploy prepare` from your laptop is what bootstraps a fresh VPS: the CLI SSHes into the server, hardens the system (firewall, non-root user, sudoers), downloads `nextdeployd`, installs it as a `systemd` unit, and starts it. After that, `nextdeploy ship` builds locally, syncs the standalone build to the server, and tells the now-running daemon to swap the active version. The only thing you ever install by hand is the CLI on your laptop. No Docker layer caching, no image registries, no Kubernetes manifests — just a `systemd` service running `node server.js` with the right env vars, all provisioned for you.

### AWS serverless path

```
nextdeploy deploy
       │
       ▼
┌──────────────────────────────────────────────────────┐
│  1. Package: split standalone build into             │
│     ├─ Lambda zip (server.js + node_modules)         │
│     └─ S3 asset list (public/, .next/static/)        │
│                                                       │
│  2. Sync secrets (3-tier merge → Secrets Manager)    │
│                                                       │
│  3. Upload statics → S3 (parallel + retry)           │
│                                                       │
│  4. Reconcile compute layer:                         │
│     ├─ Main Lambda (Node 20 + bridge.js shim)        │
│     ├─ Secrets Extension layer for runtime injection │
│     ├─ Image-optimization Lambda (provided.al2023)   │
│     ├─ ISR revalidation Lambda + SQS + DLQ           │
│     ├─ CloudFront distribution (custom cache policies)│
│     ├─ ACM cert in us-east-1 (auto-requested)        │
│     └─ S3 bucket policy locked to this CF distro     │
│                                                       │
│  5. Publish numbered Lambda version → rollback ready │
└──────────────────────────────────────────────────────┘
```

There is no state file. NextDeploy discovers existing AWS resources by tag and comment reference, then reconciles them to the desired state. If you delete a resource manually in the AWS console, the next deploy will recreate or repair it. If you run two deploys concurrently, the second will see the first's resources and converge on them. This is the same model as Kubernetes controllers and a deliberate design choice.

The runtime is the most interesting part. The Lambda doesn't run Next.js directly — it runs a small Node.js shim (`bridge.js`, currently embedded in `aws.go`) that:

1. **On cold start**: spawns `node server.js` as a child process on `127.0.0.1:3000`, while concurrently fetching the secret blob from `localhost:2773` (the AWS Parameters & Secrets Lambda Extension). Each secret is set into `process.env` *before* the Next.js server is allowed to receive traffic.
2. **On every request**: translates the Lambda Function URL event into a real HTTP request, proxies it to the child process, captures the response, and base64-encodes the body for the return envelope.
3. **On warm invocations**: the child Node process persists across calls, so subsequent requests skip the spawn cost.

This is the part of the project that could have been delegated to `@opennextjs/aws`. It deliberately isn't, because writing it from scratch is the entire learning exercise.

---

## What's in the box

| Component | Path | Purpose |
|---|---|---|
| **CLI** | `cli/` | Cobra-based command tree: `init`, `deploy`, `ship`, `rollback`, `secrets`, `status`, `logs`, ... |
| **Daemon** | `daemon/` | Long-running process on the VPS. Receives commands from the CLI, manages systemd units, streams logs, syncs secrets. |
| **Serverless engine** | `cli/internal/serverless/` | All AWS + Cloudflare provider code. The `Provider` interface in `provider.go` is the contract; `aws*.go` and `cloudflare.go` are the implementations. |
| **Next.js introspection** | `shared/nextcore/` | Parses `next.config.js`, route manifests, middleware/proxy files. The provider layer reads everything through this — it's the abstraction over Next.js's evolving internals. |
| **Packaging** | `internal/packaging/` | Splits a Next.js standalone build into a Lambda zip + an S3 asset list. |
| **Secret management** | `shared/secrets/` | Local key derivation, dotenv parsing, encrypted storage, Doppler integration. Three-source merge with explicit precedence. |
| **Shared libraries** | `shared/` | Logger, config schema, env store, git helpers, sanitizer, updater. |

---

## Quick start

### Install

**Linux / macOS** — pre-built binary, downloads the latest release from GitHub:

```bash
curl -fsSL https://raw.githubusercontent.com/Golangcodes/nextdeploy-frontend/master/public/install.sh | bash
```

**Windows** — `.bat` script that downloads + extracts the Windows zip and adds it to your `PATH`:

```cmd
curl -fsSL -o install.bat https://raw.githubusercontent.com/Golangcodes/nextdeploy-frontend/master/public/install.bat && install.bat
```

**With Go installed** (any platform) — builds from source:

```bash
go install github.com/Golangcodes/nextdeploy/cli@latest
```

**Manual download** — grab a binary directly from [github.com/Golangcodes/nextdeploy/releases](https://github.com/Golangcodes/nextdeploy/releases) (Linux x64/arm64, macOS x64/arm64, Windows x64).

Both install scripts pull signed release artifacts from `Golangcodes/nextdeploy` and verify checksums before installing. Read [`install.sh`](https://github.com/Golangcodes/nextdeploy-frontend/blob/master/public/install.sh) and [`install.bat`](https://github.com/Golangcodes/nextdeploy-frontend/blob/master/public/install.bat) before piping them to `bash` if you don't already trust the repo — that's good practice for any `curl | bash` install.

### Deploy to a VPS

```bash
nextdeploy init                    # scaffold nextdeploy.yml
nextdeploy prepare                 # SSH in, harden the box, install nextdeployd as a systemd unit
nextdeploy build                   # build the Next.js app locally
nextdeploy ship                    # rsync the build + tell the daemon to swap versions
nextdeploy status                  # check PID, memory, health
nextdeploy logs --follow           # stream real-time logs from the daemon
nextdeploy secrets set DB_URL=...  # push secrets to the server (auto-restarts the app)
```

You only ever install one binary: the CLI on your laptop. `nextdeploy prepare` handles the daemon installation, the system user, the firewall (`ufw`), the `systemd` unit files, and SSH key trust. Re-running `prepare` is safe — it's idempotent and will upgrade `nextdeployd` to whatever version your local CLI was built against.

### Deploy to AWS serverless

```bash
nextdeploy init --target serverless
# edit nextdeploy.yml: set serverless.region, app.domain, app.environment
aws configure                      # standard AWS credentials chain
nextdeploy deploy                  # provisions Lambda + CloudFront + everything
nextdeploy rollback --steps 1      # instant rollback to previous version
nextdeploy rollback --to abc1234   # rollback to a specific git commit
```

---

## Secret management

Three sources are merged with explicit precedence (lowest → highest):

1. **`.env`** at project root (dotenv format) — auto-detected, missing is silent
2. **`secrets.files[]`** declared in `nextdeploy.yml` (dotenv format) — missing is fatal
3. **`.nextdeploy/.env`** managed JSON store, populated by `nextdeploy secrets set/load` — highest precedence because it represents explicit CLI intent

The merged map is pushed to **AWS Secrets Manager** as a single environment-scoped blob (`nextdeploy/apps/<app>/<env>`). The Lambda fetches it at cold start via the AWS-managed Secrets Extension layer and injects each key into `process.env` before starting the Next.js server.

**Diff before write**: if the AWS blob already matches the local merged map, the PUT is skipped entirely. No version pollution, no wasted API calls.

**Optimistic CAS** for concurrent `secrets set` from different machines: read with VersionId, mutate, re-fetch, retry on conflict. Bounded to 5 attempts.

**Refuses to leak secrets to env vars** if the IAM principal lacks `lambda:GetLayerVersion`. The deploy fails loudly with an actionable IAM policy snippet. The insecure fallback (dumping every secret into Lambda environment variables, where they'd be visible in the AWS console and persist in every published Lambda version forever) is opt-in only via `serverless.allow_secrets_in_env: true`.

For VPS targets, secrets are written to a `systemd` `EnvironmentFile` on the server, with the daemon restarting the service to pick them up. They never touch git, CI logs, or unencrypted files in transit.

---

## How NextDeploy compares

| | Vercel | SST + OpenNext | NextDeploy |
|---|---|---|---|
| **Language** | TypeScript SaaS | TypeScript + CDK/Pulumi | Pure Go |
| **Setup** | `vercel login` | `npm install`, learn SST DSL | Download a binary |
| **`--version` boot time** | n/a (SaaS) | seconds (Node + CDK boot) | ~38 ms (measured, see `make bench-startup`) |
| **State management** | Vercel-managed | Pulumi/CDK state file | Stateless reconciliation |
| **Where it runs** | Vercel infra | Your AWS account | Your AWS account or your VPS |
| **Self-host?** | No | AWS only | AWS, Cloudflare (WIP), or any SSH-able server |
| **Vendor lock-in** | Strong | None (you own the AWS) | None |
| **Audience** | Anyone | TypeScript devs | Anyone, with a deliberate "JS devs learning Go" angle |

NextDeploy is not trying to replace Vercel. Vercel is a SaaS — you give them money, they handle everything, you don't think about infra. NextDeploy is for the developer who wants to *understand* what's happening, *own* the AWS account it runs in, and have *one tool* that also works on a $5 Hetzner box when budgets matter.

---

## Learning Go by reading this codebase

If you're a JavaScript or TypeScript developer using this project as a way to learn Go, here's a recommended reading order. Each file teaches a Go pattern in the context of doing real work:

1. **`cli/internal/serverless/provider.go`** — Go interfaces. Small, single-purpose, defined where they're consumed. Compare to TypeScript `interface`: similar idea, very different ergonomics around implementation (no `implements` keyword, satisfied implicitly).
2. **`cli/internal/serverless/aws.go`** — struct definitions, constructors, methods on receivers. Notice how state lives on the struct, not in a closure or global. Notice how `verbose` is just a field, not a magic logger configuration.
3. **`cli/internal/serverless/aws_secrets.go`** — a self-contained reconciler. Read `mutateSecrets` for an example of optimistic concurrency in pure Go: no locks, no promises, just retry-on-conflict. Read `secretsEqual` for the most boring possible Go function and notice it's three lines.
4. **`cli/internal/serverless/aws_lambda.go:DeployCompute`** — orchestration with explicit ordering, error handling, and retries. This is what "imperative reconciliation" looks like before you reach for a framework. Note how `_ = err` never appears; every error is either fatal or explicitly downgraded with a comment.
5. **`shared/secrets/secretmanager.go`** — the functional options pattern (`Option func(*SecretManager)`). One of the most useful Go idioms, almost unknown in the JS world.
6. **`Makefile`** — how Go projects ship. No `package.json`, no `webpack.config.js`, no monorepo orchestrator. Just a Makefile that calls `go build` with `-ldflags` for version metadata.
7. **`.golangci.yml`** — linting in Go. Compare to ESLint config size; Go's tooling is loud but consistent.

For the *why* of these patterns, read [`CODE_QUALITY.md`](./CODE_QUALITY.md) at the repo root. It's the project's house style and it explains every rule.

---

## Project status

| Area | Status |
|---|---|
| **VPS deployment via systemd** | Working, the original use case |
| **AWS Lambda + CloudFront SSR** | Working, the most mature path |
| **AWS static export hosting** | Working |
| **ISR revalidation via SQS** | Working, with DLQ + reserved concurrency |
| **Image optimization Lambda** | Working, with custom CloudFront cache policy |
| **Custom domains + ACM certs** | Working, with apex-aware DNS guidance |
| **Rollback (steps + commit)** | Working for both VPS and Lambda |
| **Secret management** | Working, three-tier merge, CAS, hardened fallback |
| **Cloudflare Workers** | Scaffolded — works for static-export sites; SSR is the deliberate boundary of the pure-Go bridge approach (see "Honest limits") |
| **CI/CD via GitHub Actions** | In progress |
| **Multi-tenant server support** | Not started |

See [`cli/internal/serverless/REVIEW.md`](./cli/internal/serverless/REVIEW.md) for a complete list of known issues with severity and file references.

---

## Honest limits

This section exists because infra tools that don't tell you what they can't do are dangerous.

- **Cold starts**: the `bridge.js` shim spawns `node server.js` on cold start. That's slower than OpenNext's compile-to-handler approach. For latency-sensitive SSR sites, this matters. For most apps, it doesn't.
- **Cloudflare Workers (the deliberate boundary)**: NextDeploy's runtime model is "spawn `node server.js` as a child process and proxy requests to it". On AWS Lambda, that works because Lambda actually gives you a Node.js process to spawn from. **Cloudflare Workers run V8 isolates, not Node**. There is no `node` binary, no child processes, no `fs`, no long-running server — the entire model is incompatible by design. This is not a bug in NextDeploy; it's where the pure-Go bridge approach honestly ends. Static-export Next.js sites (`output: 'export'`) deploy to Cloudflare today via R2 hosting. SSR on Cloudflare requires `@opennextjs/cloudflare`, which is the *correct* tool for that job — we'll integrate it eventually as a wrapped runtime, but wrapping it would be a different project from "pure Go infra tool", so it's intentionally on the back burner.
- **Edge runtime functions**: routes with `export const runtime = 'edge'` cannot run on AWS Lambda. NextDeploy doesn't currently detect or warn on this.
- **Stateless reconciliation has edge cases**: if you manually delete a resource in the AWS console between deploys, NextDeploy will try to recreate it. This is usually fine but can occasionally surprise you. There's no `terraform plan` equivalent yet (though `--dry-run` is on the roadmap).
- **The `bridge.js` shim is currently embedded as a Go raw string**, which is less than ideal. Extracting it to a real `.js` file with `//go:embed` is tracked as C22 in `REVIEW.md`.
- **Hobby project pace**: this is built and maintained by one person learning Go. PRs and issues welcome.

---

## Philosophy

Other platforms abstract until you lose control. NextDeploy flips that. You own the pipeline. You see every step. You can read every line of code that touches your AWS account. There are no black boxes, no proprietary control planes, no usage-based pricing surprises.

If you came here from Vercel and you want the same DX on your own AWS account, NextDeploy is *not* yet that — Vercel is a polished SaaS and we're a hobby project. But if you came here because you want to understand how Next.js actually deploys, or because you want to learn Go by reading real infrastructure code, or because you want one tool that handles both your $5 Hetzner box and your AWS account — this is for you.

**Inspired by**: [Kamal](https://kamal-deploy.org/) for the self-hosted philosophy, and by every Hashicorp tool ever written for the Go-as-infra-language conviction.

---

## Contributing

Contributions especially welcome from:

- **Systems engineers** — daemon, logging, systemd integration
- **AWS specialists** — there's a long list of open issues in `REVIEW.md`
- **JS/TS developers learning Go** — the project is *for you*; PRs are how you learn
- **Security reviewers** — secret handling, IAM scoping, the bridge.js runtime
- **Next.js internals nerds** — the bridge is a perpetual work in progress as Next.js evolves

Read [`CODE_QUALITY.md`](./CODE_QUALITY.md) before opening a PR. The house style is strict but documented.

### AI-assisted contributions are welcome

Parts of this codebase and documentation were written with AI assistance, and contributions made the same way are explicitly welcome. **You don't need to disclose AI usage in your PRs** — it's expected and normalized here. What we do ask:

- **Read what you submit.** If you can't explain a function in your own words, don't open the PR. Ask the AI to explain it to you first, then write it in the PR description in your words. That's the moment you actually learn.
- **Test what you submit.** `make test-unit` and `make build-cli` must pass. AI-generated tests count, but only if they actually exercise the change.
- **Check for hallucinations.** AI confidently invents AWS API method names, Go stdlib functions, and file paths that don't exist. The compiler catches some of this; integration tests catch more; reviewers catch the rest. Don't trust, verify.
- **Match the house style.** Read [`CODE_QUALITY.md`](./CODE_QUALITY.md) and point your AI at it. AI assistants follow style guides much better when you give them the guide explicitly.
- **Don't paste raw AI output as documentation.** Edit it to sound like you. Strip the "Certainly! Here's a comprehensive..." preamble. Cut hedging. Match the tone of the surrounding docs.

The maintainer is not an expert in Go, Next.js internals, or AWS infrastructure — I'm learning all three by building this, and AI is part of how. If you spot something that looks like an AI hallucination, an outdated pattern, or just bad practice, please call it out. That kind of review is exactly what makes this project educational instead of just another tool.

**Not sure where to start?** Open `cli/internal/serverless/REVIEW.md` — it has a numbered list of known issues with severity tags. C3, C11, C12, C16, C17 are good first PRs. Ask Claude or Copilot to help you understand the issue, write the fix, and explain it back to yourself before opening the PR.

---

## Links

* CLI repo: [github.com/Golangcodes/nextdeploy](https://github.com/Golangcodes/nextdeploy)
* Frontend / install scripts: [github.com/Golangcodes/nextdeploy-frontend](https://github.com/Golangcodes/nextdeploy-frontend)
* Releases: [github.com/Golangcodes/nextdeploy/releases](https://github.com/Golangcodes/nextdeploy/releases)
* Twitter/X: [@hersiyussuf](https://x.com/hersiyussuf)

---

**NextDeploy — Pure Go. Real infrastructure. Built to teach.**
