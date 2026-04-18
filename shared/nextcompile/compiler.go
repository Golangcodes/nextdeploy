package nextcompile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Compile runs the full build-side pipeline against a Next.js standalone
// tree and produces a CompiledBundle ready for the adapter's esbuild step.
//
// This is the single public entry point; callers should not invoke the
// phase functions directly. Phase ordering and error policy are documented
// inline — deviations will break reproducibility guarantees.
//
// Contract:
//   - On error, no guarantee that OutDir has been cleaned. The adapter's
//     build-bundle step should treat OutDir as disposable (blow it away
//     and re-run on retry) rather than expect partial success.
//   - On success, every path in the returned CompiledBundle exists.
//   - CompileStats.ContentHash is deterministic for identical input.
func Compile(ctx context.Context, opts CompileOpts) (*CompiledBundle, error) {
	opts = normalizeOpts(opts)
	log := opts.Log

	start := time.Now()
	log.Info("nextcompile: starting (target=%s, standalone=%s)", opts.Target, opts.StandaloneDir)

	// Phase 1 — detect Next + React versions.
	nextVer, reactVer, err := DetectVersions(opts.StandaloneDir)
	if err != nil {
		return nil, fmt.Errorf("detect versions: %w", err)
	}
	log.Info("nextcompile: detected next=%s react=%s variant=%s",
		nextVer.Raw, reactVer.Raw, nextVer.RuntimeVariant())

	// Phase 2 — scan compiled server tree in parallel.
	refs, err := ScanCompiledServer(ctx, opts.StandaloneDir, opts.Payload)
	if err != nil {
		return nil, fmt.Errorf("scan compiled server: %w", err)
	}
	log.Info("nextcompile: scanned %d compiled modules", len(refs))

	// Phase 3 — server actions. Parse Next's server-reference-manifest.json
	// from the standalone tree. Missing manifest → no actions (fine).
	actionManifest, err := DetectServerActions(opts.StandaloneDir, opts.Payload.DistDir)
	if err != nil && !errors.Is(err, ErrNoActionManifest) {
		return nil, fmt.Errorf("detect server actions: %w", err)
	}
	if actionManifest != nil && len(actionManifest.Actions) > 0 {
		log.Info("nextcompile: detected %d Server Actions", len(actionManifest.Actions))
	}

	// Phase 4 — binding hints (stub until bindings.go lands).
	hints := deriveBindingsStub(refs, opts.Payload)

	// Phase 5 — elide dead routes (stub until dedupe.go lands).
	kept, elided := elideDeadRoutesStub(refs, opts.Payload.Routes)

	// Phase 6 — ensure OutDir exists. Every emit below writes under
	// <OutDir>/_nextdeploy/ so this covers them all.
	if err := ensureOutDir(opts.OutDir); err != nil {
		return nil, fmt.Errorf("prepare out dir: %w", err)
	}

	// Phase 7 — build + emit manifest.json.
	generatedAt := time.Now()
	manifest := BuildManifest(opts.Payload, nextVer, reactVer, kept, generatedAt)
	manifestPath, err := EmitManifest(manifest, opts.OutDir)
	if err != nil {
		return nil, fmt.Errorf("emit manifest: %w", err)
	}

	// Phase 8 — emit dispatch.mjs (build-time route table + action loaders).
	dispatchPath, err := EmitDispatchTable(kept, actionManifest, opts.OutDir)
	if err != nil {
		return nil, fmt.Errorf("emit dispatch: %w", err)
	}

	// Phase 9 — extract embedded JS runtime (dispatcher + serve + route_match + errors + context + rsc).
	runtimeFiles, err := ExtractRuntime(opts.OutDir)
	if err != nil {
		return nil, fmt.Errorf("extract runtime: %w", err)
	}
	log.Debug("nextcompile: extracted %d runtime files", len(runtimeFiles))

	// Phase 10 — vendor RSC runtime when the target requires it.
	vendored, err := maybeVendorRSC(opts, manifest.Features, reactVer, log)
	if err != nil {
		return nil, err
	}

	// Phase 11 — emit action_manifest.json. Always emitted (empty when app
	// has no actions) so the runtime's import is never missing.
	actionManifestPath, err := EmitActionManifest(actionManifest, opts.OutDir)
	if err != nil {
		return nil, fmt.Errorf("emit action manifest: %w", err)
	}

	// Phase 12 — emit worker_entry.mjs (the esbuild entrypoint).
	entryPath, err := EmitWorkerEntry(opts.OutDir)
	if err != nil {
		return nil, fmt.Errorf("emit worker entry: %w", err)
	}

	// Phase 13 — compute content hash over every generated/extracted file
	// in deterministic order. Adapter uses this to skip no-op redeploys.
	hashInputs := append([]string{manifestPath, dispatchPath, actionManifestPath, entryPath}, runtimeFiles...)
	if vendored != nil {
		hashInputs = append(hashInputs, vendored.TargetPath)
	}
	sort.Strings(hashInputs)
	contentHash, totalBytes, err := hashFiles(hashInputs)
	if err != nil {
		return nil, fmt.Errorf("hash bundle: %w", err)
	}

	actionCount := 0
	if actionManifest != nil {
		actionCount = len(actionManifest.Actions)
	}
	stats := CompileStats{
		RouteCount:       len(kept),
		ActionCount:      actionCount,
		DeadRoutesElided: elided,
		BundleBytes:      totalBytes,
		Duration:         time.Since(start),
		ContentHash:      contentHash,
	}

	log.Info("nextcompile: complete in %s (routes=%d actions=%d elided=%d bytes=%d hash=%s)",
		stats.Duration.Round(time.Millisecond),
		stats.RouteCount, stats.ActionCount, stats.DeadRoutesElided,
		stats.BundleBytes, stats.ContentHash[:12])

	return &CompiledBundle{
		BundleDir:         opts.OutDir,
		EntryPath:         entryPath,
		ManifestPath:      manifestPath,
		DispatchPath:      dispatchPath,
		ActionManifest:    actionManifestPath,
		DetectedVersion:   nextVer,
		DetectedReact:     reactVer,
		SuggestedBindings: hints,
		VendoredRSC:       vendored,
		Stats:             stats,
	}, nil
}

// maybeVendorRSC runs VendorRSC only for targets that can't install npm
// packages at runtime (Workers). Other targets ship node_modules and
// resolve at runtime, so vendoring is unnecessary.
//
// Failure policy:
//   - package missing + features.RSC == true  → fatal, clear error
//   - package missing + features.RSC == false → debug log, proceed
//   - package missing, any other error        → bubble up unchanged
func maybeVendorRSC(opts CompileOpts, features ManifestFeatures, react ReactVersion, log Logger) (*VendoredPackage, error) {
	if opts.Target != TargetCloudflareWorker {
		return nil, nil
	}

	vendored, err := VendorRSC(opts.StandaloneDir, opts.OutDir)
	if err == nil {
		log.Info("nextcompile: vendored %s@%s (%s build, %d bytes)",
			vendored.Name, vendored.Version, vendored.BuildKind, vendored.Bytes)
		return vendored, nil
	}

	if errors.Is(err, ErrRSCPackageNotFound) {
		if features.RSC {
			return nil, fmt.Errorf(
				"RSC pages detected but react-server-dom-webpack is not installed.\n"+
					"Detected React: %s.\n"+
					"Fix: at the app level, run `pnpm add react-server-dom-webpack@<matching-react>`\n"+
					"or ensure the standalone build includes it under node_modules.\n"+
					"See _nextdeploy/runtime/vendor/README.md for details",
				react.Raw)
		}
		log.Debug("nextcompile: RSC package not installed; app does not use RSC, skipping vendoring")
		return nil, nil
	}
	return nil, fmt.Errorf("vendor RSC: %w", err)
}

// normalizeOpts fills in defensible defaults so the rest of the pipeline
// can assume a fully-populated CompileOpts.
func normalizeOpts(opts CompileOpts) CompileOpts {
	if opts.Target == "" {
		opts.Target = TargetCloudflareWorker
	}
	if opts.OutDir == "" {
		opts.OutDir = filepath.Join(opts.StandaloneDir, ".nextdeploy-build")
	}
	if opts.Log == nil {
		opts.Log = nopLogger{}
	}
	return opts
}

func ensureOutDir(dir string) error {
	// Restrictive perms — this tree ends up in a worker bundle and
	// may contain compiled secrets-by-reference.
	return os.MkdirAll(dir, 0o750)
}

// hashFiles produces a single SHA-256 over the concatenation of every path's
// contents in the order given. Also returns the total byte count, which is
// cheap to compute alongside and populates CompileStats.BundleBytes.
func hashFiles(paths []string) (string, int64, error) {
	h := sha256.New()
	var total int64
	for _, p := range paths {
		data, err := os.ReadFile(p) // #nosec G304 — hashing the compiler's own output
		if err != nil {
			return "", 0, fmt.Errorf("read %s: %w", p, err)
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(data)
		total += int64(len(data))
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}

// ── Stub phases (until dedicated files land) ─────────────────────────────────

func deriveBindingsStub(refs []ModuleRef, _ Payload) []BindingHint {
	seen := map[string]struct{}{}
	var hints []BindingHint
	for _, r := range refs {
		for _, env := range r.EnvRefs {
			if _, ok := seen[env]; ok {
				continue
			}
			seen[env] = struct{}{}
			hints = append(hints, BindingHint{
				Kind:    "secret",
				Name:    env,
				Reason:  "referenced via process.env in compiled output",
				Sources: []string{r.CompiledPath},
			})
		}
	}
	return hints
}

func elideDeadRoutesStub(refs []ModuleRef, _ RouteInfo) ([]ModuleRef, int) {
	return refs, 0
}
