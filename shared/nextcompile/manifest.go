package nextcompile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// manifestSchemaVersion bumps when the shape changes in a way the runtime
// needs to branch on. Runtime asserts compatibility on load.
const manifestSchemaVersion = "1"

// Manifest is the wire format emitted into _nextdeploy/manifest.json.
// Every field is shaped for direct consumption by the JS runtime with no
// client-side transformation — what you see here is what the dispatcher
// reads at request time.
//
// Field order is stable (json:"-" via explicit struct layout) so identical
// input produces identical bytes, which is the load-bearing property for
// content-addressable bundle hashing.
type Manifest struct {
	SchemaVersion string          `json:"schemaVersion"`
	GeneratedAt   string          `json:"generatedAt"`
	AppName       string          `json:"appName"`
	BasePath      string          `json:"basePath,omitempty"`
	NextVersion   string          `json:"nextVersion"`
	ReactVersion  string          `json:"reactVersion,omitempty"`
	BuildID       string          `json:"buildId,omitempty"`
	GitCommit     string          `json:"gitCommit,omitempty"`
	Routes        ManifestRoutes  `json:"routes"`
	ISR           ManifestISR     `json:"isr"`
	Middleware    *ManifestMiddle `json:"middleware,omitempty"`
	Images        *ManifestImages `json:"images,omitempty"`
	I18n          *ManifestI18n   `json:"i18n,omitempty"`
	HasAppRouter  bool            `json:"hasAppRouter"`
	OutputMode    string          `json:"outputMode,omitempty"`
	// Features is the app's detected capability surface. Runtime consults
	// this to decide which handlers to wire; operators can eyeball it to
	// confirm the deployed bundle actually supports what they expect.
	Features ManifestFeatures `json:"features"`
}

// ManifestFeatures is the detected capability summary. True = the app
// uses the feature. The runtime bundle serves what it can; features
// whose runtime is not yet wired return a clear 501 rather than fail
// silently.
type ManifestFeatures struct {
	RSC           bool `json:"rsc"`               // any page or layout uses Server Components
	ServerActions bool `json:"serverActions"`     // any module carries action markers
	Middleware    bool `json:"middleware"`        // Edge-runtime middleware.ts present
	Proxy         bool `json:"proxy"`             // Node-runtime proxy.ts present (Next 15+)
	ISR           bool `json:"isr"`               // any route has ISR revalidation
	ImageOptimize bool `json:"imageOptimization"` // /_next/image is expected to work
	I18n          bool `json:"i18n"`              // locales declared
	PPR           bool `json:"ppr"`               // any page opts into Partial Prerendering
	After         bool `json:"after"`             // any module uses the after() API
}

// ManifestRoutes is the dispatch classification the runtime consults in
// priority order: middleware → static → ssg → isr → dynamic (ssr/page) → api.
type ManifestRoutes struct {
	Static     []string          `json:"static"`
	SSG        map[string]string `json:"ssg"` // route -> HTML path
	SSR        []string          `json:"ssr"`
	ISR        map[string]string `json:"isr"`
	API        []string          `json:"api"`
	Dynamic    []string          `json:"dynamic"`
	Fallback   map[string]string `json:"fallback"`
	Middleware []string          `json:"middleware"`
}

type ManifestISR struct {
	Tags      map[string][]string `json:"tags"`
	Intervals map[string]int      `json:"intervals"`
}

type ManifestMiddle struct {
	Path     string              `json:"path"`
	Matchers []ManifestMiddleMat `json:"matchers"`
	Runtime  string              `json:"runtime,omitempty"`
}

type ManifestMiddleMat struct {
	Pathname string `json:"pathname,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
}

type ManifestImages struct {
	RemotePatterns []ManifestImagePat `json:"remotePatterns"`
	Domains        []string           `json:"domains,omitempty"`
	Formats        []string           `json:"formats,omitempty"`
	Unoptimized    bool               `json:"unoptimized,omitempty"`
}

type ManifestImagePat struct {
	Protocol string `json:"protocol,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Port     string `json:"port,omitempty"`
	Pathname string `json:"pathname,omitempty"`
}

type ManifestI18n struct {
	Locales         []string `json:"locales"`
	DefaultLocale   string   `json:"defaultLocale"`
	LocaleDetection bool     `json:"localeDetection"`
}

// BuildManifest constructs the Manifest from Payload + version info + the
// scanned refs (for feature detection). Pure — no I/O — so tests can call
// it without a filesystem.
func BuildManifest(p Payload, next NextVersion, react ReactVersion, refs []ModuleRef, generatedAt time.Time) Manifest {
	m := Manifest{
		SchemaVersion: manifestSchemaVersion,
		GeneratedAt:   generatedAt.UTC().Format(time.RFC3339),
		AppName:       p.AppName,
		BasePath:      p.BasePath,
		NextVersion:   next.Raw,
		ReactVersion:  react.Raw,
		BuildID:       p.BuildID,
		GitCommit:     p.GitCommit,
		HasAppRouter:  p.HasAppRouter,
		OutputMode:    p.OutputMode,
		Routes:        buildManifestRoutes(p.Routes),
		ISR:           buildManifestISR(p.Routes.ISRDetail),
		Features:      buildFeatures(p, refs),
	}

	if p.Middleware != nil {
		m.Middleware = &ManifestMiddle{
			Path:     p.Middleware.Path,
			Runtime:  p.Middleware.Runtime,
			Matchers: convertMatchers(p.Middleware.Matchers),
		}
	}

	if p.ImageConfig != nil {
		m.Images = &ManifestImages{
			RemotePatterns: convertRemotePatterns(p.ImageConfig.RemotePatterns),
			Domains:        sortedCopy(p.ImageConfig.Domains),
			Formats:        sortedCopy(p.ImageConfig.Formats),
			Unoptimized:    p.ImageConfig.Unoptimized,
		}
	}

	if p.I18n != nil {
		m.I18n = &ManifestI18n{
			Locales:         sortedCopy(p.I18n.Locales),
			DefaultLocale:   p.I18n.DefaultLocale,
			LocaleDetection: p.I18n.LocaleDetection,
		}
	}

	return m
}

func buildManifestRoutes(r RouteInfo) ManifestRoutes {
	return ManifestRoutes{
		Static:     sortedCopy(r.StaticRoutes),
		SSG:        copyMap(r.SSGRoutes),
		SSR:        sortedCopy(r.SSRRoutes),
		ISR:        copyMap(r.ISRRoutes),
		API:        sortedCopy(r.APIRoutes),
		Dynamic:    sortedCopy(r.DynamicRoutes),
		Fallback:   copyMap(r.FallbackRoutes),
		Middleware: sortedCopy(r.MiddlewareRoutes),
	}
}

// buildFeatures derives the capability summary from the Payload + scanned
// refs. Intentionally conservative: a feature is true only when there's
// concrete evidence the app uses it. "True but runtime not wired" is the
// operator's signal to check the runtime version or wait for the next
// nextcompile release.
func buildFeatures(p Payload, refs []ModuleRef) ManifestFeatures {
	f := ManifestFeatures{
		ISR:           len(p.Routes.ISRRoutes) > 0 || len(p.Routes.ISRDetail) > 0,
		ImageOptimize: p.ImageConfig != nil && !p.ImageConfig.Unoptimized,
		I18n:          p.I18n != nil && len(p.I18n.Locales) > 0,
	}
	for _, r := range refs {
		switch r.Kind {
		case RouteKindMiddleware:
			f.Middleware = true
		case RouteKindProxy:
			f.Proxy = true
		}
		if r.UsesRSC {
			f.RSC = true
		}
		if r.HasActions {
			f.ServerActions = true
		}
		if r.PPREnabled {
			f.PPR = true
		}
	}
	return f
}

// buildManifestISR reshapes ISRDetail into the tag-indexed form the runtime
// uses for revalidateTag fan-out. Tag lists are deterministically sorted so
// the manifest stays reproducible.
func buildManifestISR(details []ISRRoute) ManifestISR {
	out := ManifestISR{
		Tags:      map[string][]string{},
		Intervals: map[string]int{},
	}
	for _, d := range details {
		if d.Revalidate > 0 {
			out.Intervals[d.Path] = d.Revalidate
		}
		for _, tag := range d.Tags {
			out.Tags[tag] = append(out.Tags[tag], d.Path)
		}
	}
	for tag := range out.Tags {
		sort.Strings(out.Tags[tag])
	}
	return out
}

func convertMatchers(in []MiddlewareMatcher) []ManifestMiddleMat {
	if len(in) == 0 {
		return nil
	}
	out := make([]ManifestMiddleMat, len(in))
	for i, m := range in {
		out[i] = ManifestMiddleMat{Pathname: m.Pathname, Pattern: m.Pattern}
	}
	return out
}

func convertRemotePatterns(in []ImageRemotePattern) []ManifestImagePat {
	if len(in) == 0 {
		return nil
	}
	out := make([]ManifestImagePat, len(in))
	for i, p := range in {
		out[i] = ManifestImagePat{
			Protocol: p.Protocol,
			Hostname: p.Hostname,
			Port:     p.Port,
			Pathname: p.Pathname,
		}
	}
	return out
}

// sortedCopy returns a defensively-copied + sorted slice. Sorting is load-
// bearing for byte-identical output across runs.
func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}

func copyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// EmitManifest writes the manifest JSON to <outDir>/_nextdeploy/manifest.json.
// Returns the final path. Uses 2-space indent + trailing newline for human-
// friendly diffs; encoding/json's sorted map key emission is what gives us
// byte-identical output across runs.
func EmitManifest(m Manifest, outDir string) (string, error) {
	dir := filepath.Join(outDir, "_nextdeploy")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir _nextdeploy: %w", err)
	}
	path := filepath.Join(dir, "manifest.json")

	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	buf = append(buf, '\n')

	if err := os.WriteFile(path, buf, 0o640); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return path, nil
}
