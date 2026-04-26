package serverless

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Golangcodes/nextdeploy/shared/nextcompile"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// TestAdapterPreEsbuildStateIsValid runs the nextcompile phase of
// BuildWorkerBundle against a synthetic standalone tree and asserts the
// output is exactly what the downstream esbuild invocation expects.
//
// The esbuild step itself is skipped (needs npx in PATH + network to
// install on first run) — but this test proves that everything up to
// esbuild is producing a correct, deployable bundle structure.
func TestAdapterPreEsbuildStateIsValid(t *testing.T) {
	dir := t.TempDir()

	// Synthetic standalone tree.
	writeTestFile(t, filepath.Join(dir, "package.json"), `{
		"name": "fullstack-demo",
		"dependencies": {"next":"15.0.0","react":"19.0.0"}
	}`)
	writeTestFile(t, filepath.Join(dir, ".next", "server", "app", "page.js"),
		`export default function Home(){ return "<h1>home</h1>" }`)
	writeTestFile(t, filepath.Join(dir, ".next", "server", "app", "api", "hello", "route.js"),
		`export async function GET(){ return Response.json({ok:true}) }`)
	writeTestFile(t, filepath.Join(dir, ".next", "server", "proxy.js"),
		`export default function proxy(){}`)
	writeTestFile(t, filepath.Join(dir, ".next", "server", "middleware.js"),
		`export default function mw(){}`)
	// Server Action manifest
	writeTestFile(t, filepath.Join(dir, ".next", "server", "server-reference-manifest.json"),
		`{"node":{"actHash":{"workers":{"app/actions/page":"doThing"}}},"edge":{}}`)
	// Minimal react-server-dom-webpack vendor fixture — the real build
	// Next 15 with React 19 would include this automatically.
	writeTestFile(t, filepath.Join(dir, "node_modules", "react-server-dom-webpack", "package.json"),
		`{"name":"react-server-dom-webpack","version":"19.0.0"}`)
	writeTestFile(t, filepath.Join(dir, "node_modules", "react-server-dom-webpack",
		"esm", "react-server-dom-webpack-server.edge.production.js"),
		`export function renderToReadableStream(){}`)

	meta := &nextcore.NextCorePayload{
		AppName:    "fullstack-demo",
		DistDir:    ".next",
		OutputMode: nextcore.OutputModeStandalone,
		GitCommit:  "deadbeef",
		NextBuildMetadata: nextcore.NextBuildMetadata{
			BuildID:      "build-42",
			HasAppRouter: true,
		},
		RouteInfo: nextcore.RouteInfo{
			APIRoutes:        []string{"/api/hello"},
			MiddlewareRoutes: []string{"/"},
		},
		Middleware: &nextcore.MiddlewareConfig{
			Path:    "middleware.ts",
			Runtime: "edge",
		},
	}

	// Run nextcompile directly (skipping esbuild). This is the same code
	// path BuildWorkerBundle takes, minus the final exec.
	payload := toCompilePayload(meta, nil, nil)
	bundle, err := nextcompile.Compile(context.Background(), nextcompile.CompileOpts{
		StandaloneDir: dir,
		Payload:       payload,
		OutDir:        filepath.Join(dir, ".nextdeploy-cf"),
		Target:        nextcompile.TargetCloudflareWorker,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Every artifact the adapter will feed to esbuild must exist.
	for name, p := range map[string]string{
		"entry":          bundle.EntryPath,
		"manifest":       bundle.ManifestPath,
		"dispatch":       bundle.DispatchPath,
		"actionManifest": bundle.ActionManifest,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}

	// Action manifest contains our planted action.
	actData, _ := os.ReadFile(bundle.ActionManifest) // #nosec G304
	var actMan nextcompile.ActionManifest
	if err := json.Unmarshal(actData, &actMan); err != nil {
		t.Fatalf("action manifest invalid: %v", err)
	}
	if actMan.Actions["actHash"].Export != "doThing" {
		t.Errorf("action not wired: %+v", actMan.Actions)
	}

	// Entry point must reference every dependency esbuild will follow.
	entry, _ := os.ReadFile(bundle.EntryPath) // #nosec G304
	entrySrc := string(entry)
	for _, need := range []string{
		`from "./runtime/dispatcher.mjs"`,
		`from "./dispatch.mjs"`,
		`from "./manifest.json"`,
		`from "./action_manifest.json"`,
		`actionLoaders`,
	} {
		if !strings.Contains(entrySrc, need) {
			t.Errorf("entry missing %q", need)
		}
	}

	// Dispatch table must include the API route, proxy, middleware, and
	// action loader — the full dispatch surface.
	dispatch, _ := os.ReadFile(bundle.DispatchPath) // #nosec G304
	dSrc := string(dispatch)
	for _, need := range []string{
		`"/api/hello":`,
		`export const proxyRef = { compiled:`,
		`export const middlewareRef = { compiled:`,
		`"app/actions/page": () => import(`,
	} {
		if !strings.Contains(dSrc, need) {
			t.Errorf("dispatch missing %q:\n%s", need, dSrc)
		}
	}

	// Runtime files must be extracted alongside.
	runtimeDir := filepath.Join(bundle.BundleDir, "_nextdeploy", "runtime")
	for _, name := range []string{
		"dispatcher.mjs", "serve.mjs", "route_match.mjs", "errors.mjs",
		"context.mjs", "rsc.mjs", "actions.mjs",
	} {
		if _, err := os.Stat(filepath.Join(runtimeDir, name)); err != nil {
			t.Errorf("runtime/%s missing: %v", name, err)
		}
	}

	// Vendored RSC bundle for React 19 must be present (the fixture has it).
	vendor := filepath.Join(runtimeDir, "vendor", "react-server-dom-webpack", "server.edge.mjs")
	if _, err := os.Stat(vendor); err != nil {
		t.Errorf("vendored RSC bundle missing: %v", err)
	}
	if bundle.VendoredRSC == nil || bundle.VendoredRSC.Version != "19.0.0" {
		t.Errorf("VendoredRSC metadata wrong: %+v", bundle.VendoredRSC)
	}

	// Stats must be populated — content hash non-empty, bytes > 0.
	if bundle.Stats.ContentHash == "" {
		t.Error("empty ContentHash")
	}
	if bundle.Stats.BundleBytes == 0 {
		t.Error("zero BundleBytes")
	}
	if bundle.Stats.ActionCount != 1 {
		t.Errorf("ActionCount: got %d, want 1", bundle.Stats.ActionCount)
	}
}

// writeTestFile mirrors the test helper in nextcompile — duplicated here
// because Go test helpers don't cross package boundaries.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
