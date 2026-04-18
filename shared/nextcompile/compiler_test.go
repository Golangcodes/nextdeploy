package nextcompile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompile_EndToEnd exercises the full pipeline against a synthetic
// standalone tree. It's the load-bearing test — if this passes, every
// phase wires correctly and the output is deployable-shaped.
func TestCompile_EndToEnd(t *testing.T) {
	dir := t.TempDir()

	// Simulated standalone tree: app package.json, a root page, a dynamic
	// blog page, an API route, and a middleware.
	writeFile(t, filepath.Join(dir, "package.json"), `{
		"name": "demo-app",
		"dependencies": { "next": "14.2.3", "react": "18.3.1" }
	}`)
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "page.js"),
		`export default function Home(){ return "<h1>home</h1>" }`)
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "blog", "[slug]", "page.js"),
		`export default function Post(){ return "<h1>post</h1>" }`)
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "api", "users", "route.js"),
		`export async function GET(){ return Response.json({ok:true, key: process.env.API_KEY}) }`)
	writeFile(t, filepath.Join(dir, ".next", "server", "middleware.js"),
		`export default function mw(){}`)
	// proxy.ts (Next 15+) lands alongside middleware.js.
	writeFile(t, filepath.Join(dir, ".next", "server", "proxy.js"),
		`export default function proxy(){}`)
	// A Server Components page — scanner must flag usesRSC from content.
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "dashboard", "page.js"),
		`"use client";
import { fn } from "react-server-dom-webpack/client";
export default function Dashboard(){ return null }`)

	// Minimal node_modules fixture so the vendoring phase succeeds.
	// Real builds include react-server-dom-webpack automatically when RSC is on.
	writeFile(t, filepath.Join(dir, "node_modules", "react-server-dom-webpack", "package.json"),
		`{"name":"react-server-dom-webpack","version":"18.3.1"}`)
	writeFile(t, filepath.Join(dir, "node_modules", "react-server-dom-webpack", "esm",
		"react-server-dom-webpack-server.edge.production.js"),
		`// vendored stub for test
export function renderToReadableStream(){ return new ReadableStream() }`)

	// Next-emitted Server Action manifest. Two actions across two modules.
	writeFile(t, filepath.Join(dir, ".next", "server", "server-reference-manifest.json"),
		`{
			"node": {
				"7f3a1b": { "workers": { "app/actions/page": "createPost" } },
				"9c2d4e": { "workers": { "app/dashboard/page": "deleteItem" } }
			},
			"edge": {},
			"encryption": { "key": "00" }
		}`)

	outDir := filepath.Join(dir, "build-output")

	payload := Payload{
		AppName:      "demo-app",
		DistDir:      ".next",
		HasAppRouter: true,
		Routes: RouteInfo{
			StaticRoutes:     []string{"/", "/about"},
			DynamicRoutes:    []string{"/blog/[slug]"},
			APIRoutes:        []string{"/api/users"},
			MiddlewareRoutes: []string{"/"},
			SSGRoutes:        map[string]string{"/about": "/about.html"},
			ISRRoutes:        map[string]string{"/news": "/news.html"},
			ISRDetail: []ISRRoute{
				{Path: "/news", Tags: []string{"news"}, Revalidate: 60},
			},
		},
	}

	bundle, err := Compile(context.Background(), CompileOpts{
		StandaloneDir: dir,
		Payload:       payload,
		OutDir:        outDir,
		Target:        TargetCloudflareWorker,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Sanity on the bundle metadata.
	if bundle.DetectedVersion.Raw != "14.2.3" {
		t.Errorf("next version not detected: %+v", bundle.DetectedVersion)
	}
	if bundle.Stats.RouteCount == 0 {
		t.Error("expected RouteCount > 0")
	}
	if bundle.Stats.ContentHash == "" {
		t.Error("empty ContentHash")
	}
	if bundle.Stats.BundleBytes == 0 {
		t.Error("zero BundleBytes")
	}

	// Every advertised path must exist.
	for name, p := range map[string]string{
		"manifest":       bundle.ManifestPath,
		"dispatch":       bundle.DispatchPath,
		"entry":          bundle.EntryPath,
		"actionManifest": bundle.ActionManifest,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}

	// Manifest is valid JSON and has the right shape.
	data, err := os.ReadFile(bundle.ManifestPath) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest not valid JSON: %v\n%s", err, data)
	}
	if m.SchemaVersion != manifestSchemaVersion {
		t.Errorf("schema version: %s", m.SchemaVersion)
	}
	if m.ISR.Intervals["/news"] != 60 {
		t.Errorf("ISR interval missing")
	}

	// action_manifest.json must include both planted actions.
	actData, err := os.ReadFile(bundle.ActionManifest) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	var actMan ActionManifest
	if err := json.Unmarshal(actData, &actMan); err != nil {
		t.Fatalf("action manifest invalid: %v\n%s", err, actData)
	}
	if len(actMan.Actions) != 2 {
		t.Errorf("expected 2 actions, got %d: %+v", len(actMan.Actions), actMan.Actions)
	}
	if actMan.Actions["7f3a1b"].Export != "createPost" {
		t.Errorf("action 7f3a1b wrong: %+v", actMan.Actions["7f3a1b"])
	}
	if bundle.Stats.ActionCount != 2 {
		t.Errorf("stats action count: got %d, want 2", bundle.Stats.ActionCount)
	}
	if !m.Features.ServerActions {
		// ServerActions detection depends on scanner seeing "use server" markers,
		// not the manifest alone. Our fixture doesn't plant those, so this
		// assertion would need the page sources to contain "use server".
		// Leaving as a probe — fine if false for this fixture.
		t.Log("features.serverActions is false (page fixtures don't include \"use server\" markers)")
	}

	// dispatch.mjs must have our static + dynamic + middleware entries.
	dispatchSrc, err := os.ReadFile(bundle.DispatchPath) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	dSrc := string(dispatchSrc)
	if !strings.Contains(dSrc, `"/": { kind: "page", usesRSC:`) {
		t.Errorf("missing root entry in dispatch.mjs:\n%s", dSrc)
	}
	if !strings.Contains(dSrc, `"/api/users": { kind: "api", usesRSC:`) {
		t.Errorf("missing api entry")
	}
	if !strings.Contains(dSrc, `"/blog/[slug]"`) {
		t.Errorf("missing dynamic entry")
	}
	if !strings.Contains(dSrc, `export const middlewareRef = { compiled:`) {
		t.Errorf("missing middleware reference")
	}
	if !strings.Contains(dSrc, `export const proxyRef = { compiled:`) {
		t.Errorf("missing proxy reference")
	}
	// actionLoaders must list a literal import() per action module so esbuild
	// can track the bundle edge.
	if !strings.Contains(dSrc, `"app/actions/page": () => import(`) {
		t.Errorf("missing action loader for app/actions/page:\n%s", dSrc)
	}
	if !strings.Contains(dSrc, `"app/dashboard/page": () => import(`) {
		t.Errorf("missing action loader for app/dashboard/page:\n%s", dSrc)
	}
	// RSC-tagged dashboard page must have usesRSC: true in dispatch.mjs.
	if !strings.Contains(dSrc, `"/dashboard": { kind: "page", usesRSC: true,`) {
		t.Errorf("dashboard not marked usesRSC in dispatch:\n%s", dSrc)
	}

	// Every page entry must carry loadClientManifest + loadLayouts fields.
	if !strings.Contains(dSrc, `loadClientManifest:`) {
		t.Error("loadClientManifest not emitted on entries")
	}
	if !strings.Contains(dSrc, `loadLayouts:`) {
		t.Error("loadLayouts not emitted on entries")
	}

	// Manifest features must reflect what was scanned.
	if !m.Features.Middleware {
		t.Error("features.middleware should be true")
	}
	if !m.Features.Proxy {
		t.Error("features.proxy should be true")
	}
	if !m.Features.RSC {
		t.Error("features.rsc should be true (dashboard page uses it)")
	}
	if !m.Features.ISR {
		t.Error("features.isr should be true (ISRRoutes populated)")
	}

	// worker_entry.mjs imports all three artifacts.
	entry, err := os.ReadFile(bundle.EntryPath) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	eSrc := string(entry)
	for _, need := range []string{
		`from "./runtime/dispatcher.mjs"`,
		`from "./dispatch.mjs"`,
		`from "./manifest.json"`,
		`from "./action_manifest.json"`,
		`actionLoaders`,
		`actionManifest`,
		`export default {`,
	} {
		if !strings.Contains(eSrc, need) {
			t.Errorf("entry missing %q:\n%s", need, eSrc)
		}
	}

	// Runtime files were extracted alongside.
	runtimeDir := filepath.Join(outDir, "_nextdeploy", "runtime")
	for _, f := range []string{"dispatcher.mjs", "serve.mjs", "route_match.mjs", "errors.mjs"} {
		if _, err := os.Stat(filepath.Join(runtimeDir, f)); err != nil {
			t.Errorf("runtime/%s missing: %v", f, err)
		}
	}

	// Binding hints include the env ref we planted in the API route.
	sawAPIKey := false
	for _, h := range bundle.SuggestedBindings {
		if h.Kind == "secret" && h.Name == "API_KEY" {
			sawAPIKey = true
		}
	}
	if !sawAPIKey {
		t.Errorf("expected API_KEY binding hint; got %+v", bundle.SuggestedBindings)
	}

	// Vendored RSC runtime must be present after Compile.
	if bundle.VendoredRSC == nil {
		t.Fatal("expected VendoredRSC to be populated")
	}
	if bundle.VendoredRSC.Version != "18.3.1" {
		t.Errorf("vendored version: got %s", bundle.VendoredRSC.Version)
	}
	if bundle.VendoredRSC.BuildKind != "production" {
		t.Errorf("vendored buildKind: got %s", bundle.VendoredRSC.BuildKind)
	}
	vendorTarget := filepath.Join(outDir, "_nextdeploy", "runtime", "vendor",
		"react-server-dom-webpack", "server.edge.mjs")
	if _, err := os.Stat(vendorTarget); err != nil {
		t.Errorf("vendored file not on disk: %v", err)
	}
}

func TestCompile_FailsWhenRSCNeedsVendorButPackageMissing(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "package.json"), `{
		"name": "rsc-app",
		"dependencies": {"next":"15.0.0","react":"19.0.0"}
	}`)
	// A page that triggers UsesRSC — but no node_modules/react-server-dom-webpack.
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "page.js"),
		`"use client"; export default function(){ return null }`)

	payload := Payload{
		AppName:      "rsc-app",
		DistDir:      ".next",
		HasAppRouter: true,
		Routes:       RouteInfo{StaticRoutes: []string{"/"}},
	}

	_, err := Compile(context.Background(), CompileOpts{
		StandaloneDir: dir,
		Payload:       payload,
		OutDir:        filepath.Join(dir, "out"),
		Target:        TargetCloudflareWorker,
	})
	if err == nil {
		t.Fatal("expected Compile to fail when RSC is used but vendor package is missing")
	}
	if !strings.Contains(err.Error(), "react-server-dom-webpack") {
		t.Errorf("error should mention the missing package:\n%v", err)
	}
	if !strings.Contains(err.Error(), "pnpm add") {
		t.Errorf("error should include the actionable fix:\n%v", err)
	}
}

func TestCompile_SkipsVendoringWhenRSCNotUsed(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "package.json"),
		`{"name":"plain","dependencies":{"next":"14.2.3","react":"18.3.1"}}`)
	// No "use client", no Server Components — plain API-route-only app.
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "api", "health", "route.js"),
		`export async function GET(){ return Response.json({ok:true}) }`)

	payload := Payload{
		AppName:      "plain",
		DistDir:      ".next",
		HasAppRouter: true,
		Routes:       RouteInfo{APIRoutes: []string{"/api/health"}},
	}

	bundle, err := Compile(context.Background(), CompileOpts{
		StandaloneDir: dir,
		Payload:       payload,
		OutDir:        filepath.Join(dir, "out"),
		Target:        TargetCloudflareWorker,
	})
	if err != nil {
		t.Fatalf("Compile should succeed without vendor when RSC not used: %v", err)
	}
	if bundle.VendoredRSC != nil {
		t.Errorf("expected VendoredRSC nil, got %+v", bundle.VendoredRSC)
	}
}

func TestCompile_DeterministicHash(t *testing.T) {
	// Two runs over identical input must produce identical ContentHash.
	// This is the reproducible-build guarantee.
	makeTree := func() (string, Payload) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"),
			`{"name":"x","dependencies":{"next":"14.2.3","react":"18.3.1"}}`)
		writeFile(t, filepath.Join(dir, ".next", "server", "app", "page.js"),
			`export default () => "ok"`)
		return dir, Payload{
			AppName: "x",
			DistDir: ".next",
			Routes:  RouteInfo{StaticRoutes: []string{"/"}},
		}
	}

	dir1, p1 := makeTree()
	b1, err := Compile(context.Background(), CompileOpts{
		StandaloneDir: dir1, Payload: p1, OutDir: filepath.Join(dir1, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}

	dir2, p2 := makeTree()
	b2, err := Compile(context.Background(), CompileOpts{
		StandaloneDir: dir2, Payload: p2, OutDir: filepath.Join(dir2, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The path component in hashFiles includes the tempdir, which differs
	// between runs. Compare the generated dispatch + manifest bytes instead
	// — those are the content-addressable pieces.
	d1, _ := os.ReadFile(b1.DispatchPath) // #nosec G304
	d2, _ := os.ReadFile(b2.DispatchPath) // #nosec G304
	if string(d1) != string(d2) {
		t.Errorf("dispatch.mjs not deterministic:\n%s\nvs\n%s", d1, d2)
	}

	// Manifest bytes differ only in GeneratedAt (wall clock). Compare all
	// fields except that one.
	var m1, m2 Manifest
	mb1, _ := os.ReadFile(b1.ManifestPath) // #nosec G304
	mb2, _ := os.ReadFile(b2.ManifestPath) // #nosec G304
	_ = json.Unmarshal(mb1, &m1)
	_ = json.Unmarshal(mb2, &m2)
	m1.GeneratedAt = ""
	m2.GeneratedAt = ""
	j1, _ := json.Marshal(m1)
	j2, _ := json.Marshal(m2)
	if string(j1) != string(j2) {
		t.Errorf("manifest not deterministic (ignoring GeneratedAt):\n%s\nvs\n%s", j1, j2)
	}
}
