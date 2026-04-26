package serverless

import (
	"strings"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcompile"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

// publicAssetKeysFromPackage extracts the subset of S3 keys that came from
// the app's public/ directory — anything not prefixed with "_next/" is a
// public file the runtime should serve at the bare URL root. Returns nil
// for a nil package so the caller doesn't need to guard.
func publicAssetKeysFromPackage(pkg *packaging.PackageResult) []string {
	if pkg == nil {
		return nil
	}
	out := make([]string, 0, len(pkg.S3Assets))
	for _, a := range pkg.S3Assets {
		if strings.HasPrefix(a.S3Key, "_next/") {
			continue
		}
		out = append(out, a.S3Key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toCompilePayload translates nextcore.NextCorePayload (+ nextdeploy config)
// into the shape nextcompile.Compile expects. The duplication is deliberate:
// nextcompile lives under shared/ and must not import from cli/ or take a
// hard dependency on the full nextcore shape, so we carry a minimal mirror
// of the types and translate at the adapter boundary.
//
// Fields populated today:
//   - AppName, DistDir, OutputMode, HasAppRouter, BuildID, GitCommit
//   - Routes (1:1 field mapping — nextcore and nextcompile RouteInfo match)
//   - Middleware (lossy: only Path/Matchers[].{Pathname,Pattern}/Runtime;
//     Has/Missing/header/cookie conditions dropped because the runtime
//     can't consume them yet)
//
// Fields not populated yet (tracked in nextcompile todos):
//   - BasePath, I18n, ImageConfig — these live in NextConfig (parsed by
//     nextcore.ParseNextConfigFile) but aren't embedded in NextCorePayload.
//     When the adapter starts calling ParseNextConfigFile directly, we'll
//     extend this converter to forward them.
func toCompilePayload(meta *nextcore.NextCorePayload, _ *config.NextDeployConfig, publicAssets []string) nextcompile.Payload {
	if meta == nil {
		return nextcompile.Payload{PublicAssets: publicAssets}
	}

	p := nextcompile.Payload{
		AppName:      meta.AppName,
		DistDir:      meta.DistDir,
		OutputMode:   string(meta.OutputMode),
		HasAppRouter: meta.NextBuildMetadata.HasAppRouter,
		BuildID:      meta.NextBuildMetadata.BuildID,
		GitCommit:    meta.GitCommit,
		Routes:       convertRoutes(meta.RouteInfo),
		PublicAssets: publicAssets,
	}

	if meta.Middleware != nil {
		p.Middleware = &nextcompile.MiddlewareConfig{
			Path:     meta.Middleware.Path,
			Runtime:  meta.Middleware.Runtime,
			Matchers: convertMiddlewareMatchers(meta.Middleware.Matchers),
		}
	}

	return p
}

func convertRoutes(in nextcore.RouteInfo) nextcompile.RouteInfo {
	return nextcompile.RouteInfo{
		StaticRoutes:     in.StaticRoutes,
		DynamicRoutes:    in.DynamicRoutes,
		SSGRoutes:        in.SSGRoutes,
		SSRRoutes:        in.SSRRoutes,
		ISRRoutes:        in.ISRRoutes,
		ISRDetail:        convertISRDetail(in.ISRDetail),
		APIRoutes:        in.APIRoutes,
		FallbackRoutes:   in.FallbackRoutes,
		MiddlewareRoutes: in.MiddlewareRoutes,
	}
}

func convertISRDetail(in []nextcore.ISRRoute) []nextcompile.ISRRoute {
	if len(in) == 0 {
		return nil
	}
	out := make([]nextcompile.ISRRoute, len(in))
	for i, r := range in {
		out[i] = nextcompile.ISRRoute{
			Path:       r.Path,
			Tags:       r.Tags,
			Revalidate: r.Revalidate,
		}
	}
	return out
}

func convertMiddlewareMatchers(in []nextcore.MiddlewareRoute) []nextcompile.MiddlewareMatcher {
	if len(in) == 0 {
		return nil
	}
	out := make([]nextcompile.MiddlewareMatcher, len(in))
	for i, m := range in {
		out[i] = nextcompile.MiddlewareMatcher{
			Pathname: m.Pathname,
			Pattern:  m.Pattern,
		}
	}
	return out
}
