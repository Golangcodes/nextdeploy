package nextcompile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildManifest_MinimalPayload(t *testing.T) {
	p := Payload{
		AppName:      "demo",
		BasePath:     "/app",
		OutputMode:   "standalone",
		HasAppRouter: true,
		Routes: RouteInfo{
			StaticRoutes:  []string{"/", "/about"},
			SSRRoutes:     []string{"/dashboard"},
			APIRoutes:     []string{"/api/users"},
			DynamicRoutes: []string{"/blog/[slug]"},
			SSGRoutes:     map[string]string{"/blog": "/blog.html"},
			ISRRoutes:     map[string]string{"/news": "/news.html"},
			ISRDetail: []ISRRoute{
				{Path: "/news", Tags: []string{"news", "home"}, Revalidate: 60},
			},
		},
	}
	nv := NextVersion{Raw: "14.2.3"}
	rv := ReactVersion{Raw: "18.3.1"}
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	m := BuildManifest(p, nv, rv, nil, now)

	if m.SchemaVersion != manifestSchemaVersion {
		t.Errorf("schema version: got %s", m.SchemaVersion)
	}
	if m.AppName != "demo" {
		t.Errorf("app name lost")
	}
	if m.GeneratedAt != "2026-04-18T12:00:00Z" {
		t.Errorf("generated at: %s", m.GeneratedAt)
	}
	if m.ISR.Intervals["/news"] != 60 {
		t.Errorf("interval missing")
	}
	if got := m.ISR.Tags["news"]; len(got) != 1 || got[0] != "/news" {
		t.Errorf("tag index: got %v", got)
	}
	if got := m.Routes.Static; len(got) != 2 || got[0] != "/" {
		t.Errorf("static routes: %v", got)
	}
}

func TestBuildManifest_Deterministic(t *testing.T) {
	// Two identical payloads with keys inserted in different orders should
	// produce byte-identical JSON — guards the reproducible-build claim.
	base := Payload{
		AppName: "dup",
		Routes: RouteInfo{
			StaticRoutes: []string{"/b", "/a", "/c"}, // unsorted input
			SSGRoutes:    map[string]string{"/z": "z.html", "/a": "a.html"},
		},
	}
	nv := NextVersion{Raw: "14.0.0"}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	a := BuildManifest(base, nv, ReactVersion{}, nil, now)
	b := BuildManifest(base, nv, ReactVersion{}, nil, now)

	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(ab) != string(bb) {
		t.Fatalf("not deterministic:\n%s\n%s", ab, bb)
	}

	// Also verify static routes are sorted in output.
	decoded := Manifest{}
	_ = json.Unmarshal(ab, &decoded)
	if decoded.Routes.Static[0] != "/a" || decoded.Routes.Static[2] != "/c" {
		t.Errorf("static not sorted: %v", decoded.Routes.Static)
	}
}

func TestEmitManifest_WritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	m := BuildManifest(
		Payload{AppName: "t", Routes: RouteInfo{StaticRoutes: []string{"/"}}},
		NextVersion{Raw: "14.2.0"},
		ReactVersion{Raw: "18.3.1"},
		nil,
		time.Now(),
	)

	path, err := EmitManifest(m, dir)
	if err != nil {
		t.Fatalf("EmitManifest: %v", err)
	}
	if exp := filepath.Join(dir, "_nextdeploy", "manifest.json"); path != exp {
		t.Errorf("path: got %s, want %s", path, exp)
	}

	buf, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var back Manifest
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf)
	}
	if back.AppName != "t" {
		t.Errorf("round trip lost appName")
	}
	if back.SchemaVersion != manifestSchemaVersion {
		t.Errorf("schema version lost in round trip")
	}
	// Trailing newline for clean diffs.
	if len(buf) == 0 || buf[len(buf)-1] != '\n' {
		t.Errorf("missing trailing newline")
	}
}
