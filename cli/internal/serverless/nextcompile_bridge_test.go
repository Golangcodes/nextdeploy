package serverless

import (
	"testing"

	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

func TestToCompilePayload_MapsAllFields(t *testing.T) {
	meta := &nextcore.NextCorePayload{
		AppName:    "demo",
		DistDir:    ".next",
		OutputMode: nextcore.OutputModeStandalone,
		GitCommit:  "abc123",
		NextBuildMetadata: nextcore.NextBuildMetadata{
			BuildID:      "build-id-42",
			HasAppRouter: true,
		},
		RouteInfo: nextcore.RouteInfo{
			StaticRoutes:     []string{"/", "/about"},
			DynamicRoutes:    []string{"/blog/[slug]"},
			SSGRoutes:        map[string]string{"/about": "/about.html"},
			ISRRoutes:        map[string]string{"/news": "/news.html"},
			ISRDetail:        []nextcore.ISRRoute{{Path: "/news", Tags: []string{"t1"}, Revalidate: 60}},
			APIRoutes:        []string{"/api/x"},
			SSRRoutes:        []string{"/dashboard"},
			MiddlewareRoutes: []string{"/"},
			FallbackRoutes:   map[string]string{},
		},
		Middleware: &nextcore.MiddlewareConfig{
			Path:    "middleware.ts",
			Runtime: "edge",
			Matchers: []nextcore.MiddlewareRoute{
				{Pathname: "/api/:path*", Pattern: "^/api/.*"},
			},
		},
	}

	got := toCompilePayload(meta, nil)

	if got.AppName != "demo" {
		t.Errorf("AppName: got %q", got.AppName)
	}
	if got.DistDir != ".next" {
		t.Errorf("DistDir: got %q", got.DistDir)
	}
	if got.OutputMode != "standalone" {
		t.Errorf("OutputMode: got %q", got.OutputMode)
	}
	if !got.HasAppRouter {
		t.Error("HasAppRouter lost")
	}
	if got.BuildID != "build-id-42" {
		t.Errorf("BuildID: got %q", got.BuildID)
	}
	if got.GitCommit != "abc123" {
		t.Errorf("GitCommit: got %q", got.GitCommit)
	}

	// Routes passed through (same shape).
	if len(got.Routes.StaticRoutes) != 2 {
		t.Errorf("StaticRoutes: got %v", got.Routes.StaticRoutes)
	}
	if got.Routes.SSGRoutes["/about"] != "/about.html" {
		t.Errorf("SSGRoutes lost entry")
	}
	if len(got.Routes.ISRDetail) != 1 || got.Routes.ISRDetail[0].Revalidate != 60 {
		t.Errorf("ISRDetail lost: %+v", got.Routes.ISRDetail)
	}

	// Middleware retained (lossy matcher conversion).
	if got.Middleware == nil {
		t.Fatal("Middleware nil")
	}
	if got.Middleware.Runtime != "edge" {
		t.Errorf("Middleware runtime: got %q", got.Middleware.Runtime)
	}
	if len(got.Middleware.Matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(got.Middleware.Matchers))
	}
	if got.Middleware.Matchers[0].Pathname != "/api/:path*" {
		t.Errorf("matcher pathname: %+v", got.Middleware.Matchers[0])
	}
}

func TestToCompilePayload_NilSafe(t *testing.T) {
	// Converter must never panic on nil inputs — DeployCompute gates on
	// static-export mode but defensive programming covers every call path.
	got := toCompilePayload(nil, nil)
	if got.AppName != "" {
		t.Errorf("expected zero payload, got %+v", got)
	}
	if got.Middleware != nil {
		t.Errorf("expected nil middleware, got %+v", got.Middleware)
	}
}

func TestToCompilePayload_NoMiddleware(t *testing.T) {
	meta := &nextcore.NextCorePayload{
		AppName: "nomw",
		DistDir: ".next",
	}
	got := toCompilePayload(meta, nil)
	if got.Middleware != nil {
		t.Errorf("expected nil middleware when nextcore has none; got %+v", got.Middleware)
	}
}
