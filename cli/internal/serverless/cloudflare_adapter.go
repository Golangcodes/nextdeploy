package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcompile"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// BuildWorkerBundle adapts a Next.js standalone build into a single
// Cloudflare Worker module bundle.
//
// Pipeline:
//  1. Run nextcompile.Compile against the standalone tree + nextcore payload.
//     This produces <standaloneDir>/.nextdeploy-cf/_nextdeploy/{manifest.json,
//     dispatch.mjs, action_manifest.json, worker_entry.mjs}, extracts the
//     embedded JS runtime, and (when RSC is used) vendors
//     react-server-dom-webpack.
//  2. Log the capability summary so operators see what the bundle supports
//     before it deploys.
//  3. Invoke `npx esbuild` to bundle worker_entry.mjs + all compiled Next
//     modules it transitively imports into a single ESM Worker bundle.
//  4. Return the final bundle path.
//
// Requires Node + npx on PATH for step 3. Every other step is pure Go.
//
// `meta` may be nil for the static-export path (DeployCompute skips early in
// that case), but every Worker-deploy call path supplies it.
func BuildWorkerBundle(
	ctx context.Context,
	standaloneDir string,
	meta *nextcore.NextCorePayload,
	cfg *config.NextDeployConfig,
	publicAssetKeys []string,
	log *shared.Logger,
) (string, error) {
	if _, err := os.Stat(standaloneDir); err != nil {
		return "", fmt.Errorf("standalone dir not found: %w", err)
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return "", fmt.Errorf("npx not found on PATH (install Node.js): %w", err)
	}

	outDir := filepath.Join(standaloneDir, ".nextdeploy-cf")

	// Blow away any previous build to guarantee a clean output tree. The
	// compile is cheap and reproducible; incremental reuse isn't worth the
	// risk of stale dispatch tables referencing removed routes.
	if err := os.RemoveAll(outDir); err != nil {
		return "", fmt.Errorf("clean prior build dir: %w", err)
	}

	payload := toCompilePayload(meta, cfg, publicAssetKeys)
	bundle, err := nextcompile.Compile(ctx, nextcompile.CompileOpts{
		StandaloneDir: standaloneDir,
		Payload:       payload,
		OutDir:        outDir,
		Target:        nextcompile.TargetCloudflareWorker,
		Log:           log,
	})
	if err != nil {
		return "", fmt.Errorf("nextcompile: %w", err)
	}
	logBundleSummary(log, bundle)

	esbuildOut := filepath.Join(outDir, "worker.mjs")
	if err := runEsbuild(ctx, bundle.EntryPath, esbuildOut, standaloneDir, log); err != nil {
		return "", fmt.Errorf("esbuild: %w", err)
	}

	info, err := os.Stat(esbuildOut)
	if err != nil {
		return "", fmt.Errorf("bundle output missing: %w", err)
	}
	log.Info("Worker bundle ready: %s (%.1f KB)", esbuildOut, float64(info.Size())/1024)
	return esbuildOut, nil
}

// logBundleSummary prints a one-screen capability report. Operators should
// be able to eyeball this and know whether the deploy will serve their app,
// without having to grep the manifest.
func logBundleSummary(log *shared.Logger, bundle *nextcompile.CompiledBundle) {
	f := bundleFeaturesFromManifest(bundle)
	log.Info("─────────── nextcompile bundle ───────────")
	log.Info("  Next.js:        %s", bundle.DetectedVersion.Raw)
	log.Info("  React:          %s", bundle.DetectedReact.Raw)
	log.Info("  Routes:         %d (elided %d)", bundle.Stats.RouteCount, bundle.Stats.DeadRoutesElided)
	log.Info("  Server Actions: %d", bundle.Stats.ActionCount)
	log.Info("  Features:       %s", featureStatusLine(f))
	if bundle.VendoredRSC != nil {
		log.Info("  RSC runtime:    vendored %s (%s build, %s)",
			bundle.VendoredRSC.Version, bundle.VendoredRSC.BuildKind,
			humanBytes(bundle.VendoredRSC.Bytes))
	}
	log.Info("  Bundle hash:    %s", bundle.Stats.ContentHash[:12])
	log.Info("  Bundle bytes:   %s (pre-esbuild)", humanBytes(bundle.Stats.BundleBytes))
	log.Info("──────────────────────────────────────────")

	for _, hint := range bundle.SuggestedBindings {
		log.Debug("  binding hint: %s %q — %s", hint.Kind, hint.Name, hint.Reason)
	}
}

// bundleFeaturesFromManifest re-reads the emitted manifest to get the
// Features struct. Compile doesn't currently return it on the bundle; doing
// it here keeps the adapter independent of internal manifest structure.
func bundleFeaturesFromManifest(bundle *nextcompile.CompiledBundle) nextcompile.ManifestFeatures {
	var m nextcompile.Manifest
	data, err := os.ReadFile(bundle.ManifestPath) // #nosec G304 — reading our own output
	if err != nil {
		return nextcompile.ManifestFeatures{}
	}
	_ = json.Unmarshal(data, &m)
	return m.Features
}

func featureStatusLine(f nextcompile.ManifestFeatures) string {
	tag := func(on bool, label string) string {
		if on {
			return label + ":on"
		}
		return label + ":off"
	}
	return fmt.Sprintf("%s %s %s %s %s %s %s %s %s",
		tag(f.RSC, "rsc"),
		tag(f.ServerActions, "actions"),
		tag(f.Middleware, "middleware"),
		tag(f.Proxy, "proxy"),
		tag(f.ISR, "isr"),
		tag(f.ImageOptimize, "imgopt"),
		tag(f.I18n, "i18n"),
		tag(f.PPR, "ppr"),
		tag(f.After, "after"),
	)
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// runEsbuild invokes `npx esbuild` against the generated entrypoint. Flags
// are tuned for Cloudflare Workers:
//
//   - --format=esm        Workers require ESM
//   - --bundle            resolve all transitively-imported modules
//   - --platform=node     use Node-style resolution (import "node:*")
//   - --conditions=workerd,worker,node  prefer workerd-specific exports (needed for
//                                        pg-cloudflare, @neondatabase/serverless,
//                                        and any lib with CF-specific builds);
//                                        fall back to worker, then node
//   - --main-fields=workerd,module,main  same reason — pick the workerd build first
//   - --external:node:*   let the Worker runtime provide Node polyfills
//   - --external:cloudflare:*   don't bundle CF platform modules
//   - --loader:.json=json   dispatch.mjs imports manifest.json with JSON assertion
//
// cwd is the standalone dir so relative imports in the compiled Next tree
// resolve correctly.
func runEsbuild(ctx context.Context, entry, out, cwd string, log *shared.Logger) error {
	// Aliases redirect Next's public module specifiers to our runtime
	// shims. Path is relative to the standalone dir (esbuild's cwd).
	runtimeRoot := ".nextdeploy-cf/_nextdeploy/runtime"

	args := []string{
		"--yes",
		"esbuild@latest",
		entry,
		"--bundle",
		"--platform=node",
		"--format=esm",
		"--target=esnext",
		"--main-fields=workerd,module,main",
		"--conditions=workerd,worker,node",
		"--external:node:*",
		"--external:cloudflare:*",
		"--loader:.node=copy",
		"--loader:.json=json",
		"--alias:next/cache=" + filepath.Join(runtimeRoot, "next_shims", "cache.mjs"),
		"--alias:next/headers=" + filepath.Join(runtimeRoot, "next_shims", "headers.mjs"),
		"--alias:next/server=" + filepath.Join(runtimeRoot, "next_shims", "server.mjs"),
		"--outfile=" + out,
	}

	log.Info("Bundling Worker via esbuild (entry: %s)...", filepath.Base(entry))
	// NOSONAR: spawning `npx` is intentional and every argument here is
	// either a static flag or a path the compiler just emitted into
	// OutDir. No user-supplied string reaches argv; there is no shell
	// interpolation — exec.CommandContext takes argv directly, not a
	// shell string.
	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
