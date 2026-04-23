package nextcompile

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRoutePathFromCompiled(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"server/app/page.js", "/"},
		{"server/app/dashboard/page.js", "/dashboard"},
		{"server/app/api/users/route.js", "/api/users"},
		{"server/app/blog/[slug]/page.js", "/blog/[slug]"},
		{"server/app/(marketing)/about/page.js", "/about"},
		{"server/app/layout.js", "/_root_layout"},
		{"server/app/dashboard/layout.js", "/dashboard/_layout"},
		{"server/pages/index.js", "/"},
		{"server/pages/blog/[slug].js", "/blog/[slug]"},
		{"server/pages/api/users.js", "/api/users"},
		{"server/middleware.js", "/_middleware"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := routePathFromCompiled(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestKindFromCompiled(t *testing.T) {
	cases := []struct {
		in   string
		want RouteKind
	}{
		{"server/app/page.js", RouteKindPage},
		{"server/app/api/users/route.js", RouteKindAPI},
		{"server/pages/api/users.js", RouteKindAPI},
		{"server/pages/index.js", RouteKindPage},
		{"server/app/layout.js", RouteKindLayout},
		{"server/middleware.js", RouteKindMiddleware},
		{"server/proxy.js", RouteKindProxy},
		{"server/proxy.mjs", RouteKindProxy},
		{"server/chunks/some-chunk.js", RouteKindUnknown},
	}
	for _, tc := range cases {
		got := kindFromCompiled(tc.in)
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestStripRouteGroups(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"(marketing)/about", "about"},
		{"(group1)/(group2)/deep", "deep"},
		{"plain/path", "plain/path"},
		{"blog/[slug]", "blog/[slug]"},
	}
	for _, tc := range cases {
		got := stripRouteGroups(tc.in)
		if got != tc.want {
			t.Errorf("%q → %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAnalyzeModule_EnvAndFetch(t *testing.T) {
	dir := t.TempDir()
	src := `
export async function GET() {
  const key = process.env.ALGOLIA_KEY;
  const other = process.env.DATABASE_URL;
  const dup = process.env.ALGOLIA_KEY;
  const r = await fetch("https://api.example.com/v1/data");
  const r2 = await fetch('https://other.example.com/q');
  return Response.json({key, other});
}
`
	path := filepath.Join(dir, "server", "app", "api", "users", "route.js")
	writeFile(t, path, src)

	ref, err := analyzeModule(dir, dir, path)
	if err != nil {
		t.Fatalf("analyzeModule: %v", err)
	}

	if ref.RoutePath != "/api/users" {
		t.Errorf("RoutePath: got %q, want /api/users", ref.RoutePath)
	}
	if ref.Kind != RouteKindAPI {
		t.Errorf("Kind: got %s, want api", ref.Kind)
	}
	if len(ref.EnvRefs) != 2 {
		t.Errorf("EnvRefs: got %v, want 2 unique entries", ref.EnvRefs)
	}
	if !contains(ref.EnvRefs, "ALGOLIA_KEY") || !contains(ref.EnvRefs, "DATABASE_URL") {
		t.Errorf("EnvRefs missing expected keys: %v", ref.EnvRefs)
	}
	if len(ref.FetchTargets) != 2 {
		t.Errorf("FetchTargets: got %v, want 2", ref.FetchTargets)
	}
}

func TestAnalyzeModule_RSCAndActions(t *testing.T) {
	dir := t.TempDir()
	src := `
"use client";
import { fn } from "react-server-dom-webpack/client";
// __next_internal_action_entry_do_not_use__ [["action123","default"]]
"use server";
export default function Page() { return null }
`
	path := filepath.Join(dir, "server", "app", "page.js")
	writeFile(t, path, src)

	ref, err := analyzeModule(dir, dir, path)
	if err != nil {
		t.Fatalf("analyzeModule: %v", err)
	}
	if !ref.UsesRSC {
		t.Error("expected UsesRSC=true")
	}
	if !ref.HasActions {
		t.Error("expected HasActions=true")
	}
}

func TestScanCompiledServer_Minimal(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, ".next", "server", "app", "page.js"), `
export default function Home() { return null }
`)
	writeFile(t, filepath.Join(dir, ".next", "server", "app", "api", "users", "route.js"), `
export async function GET() { return new Response(process.env.X) }
`)
	writeFile(t, filepath.Join(dir, ".next", "server", "middleware.js"), `
export default function mw() {}
`)
	// A chunk that should be skipped.
	writeFile(t, filepath.Join(dir, ".next", "server", "chunks", "util.js"), `export const x = 1`)

	payload := Payload{
		DistDir: ".next",
		Routes: RouteInfo{
			APIRoutes: []string{"/api/users"},
		},
	}

	refs, err := ScanCompiledServer(context.Background(), dir, payload)
	if err != nil {
		t.Fatalf("ScanCompiledServer: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3: %+v", len(refs), refs)
	}

	byPath := map[string]ModuleRef{}
	for _, r := range refs {
		byPath[r.RoutePath] = r
	}
	if byPath["/"].Kind != RouteKindPage {
		t.Errorf("/ kind: got %s", byPath["/"].Kind)
	}
	if byPath["/api/users"].Kind != RouteKindAPI {
		t.Errorf("/api/users kind: got %s", byPath["/api/users"].Kind)
	}
	if !contains(byPath["/api/users"].EnvRefs, "X") {
		t.Errorf("/api/users env refs: %v", byPath["/api/users"].EnvRefs)
	}
	if byPath["/_middleware"].Kind != RouteKindMiddleware {
		t.Errorf("middleware kind: got %s", byPath["/_middleware"].Kind)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
