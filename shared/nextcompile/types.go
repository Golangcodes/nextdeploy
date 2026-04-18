// Package nextcompile transforms a Next.js standalone build into a
// runtime-native Worker/Lambda bundle by analyzing the compiled output
// and emitting a dispatch table + manifest that a minimal JS runtime
// consumes at request time.
//
// This is the build-time half of the nextcore adapter story. The JS
// runtime half lives in runtime_src/ and is embedded as pre-built
// bundles under runtime_assets/.
//
// Pipeline (see compiler.go):
//
//	NextCorePayload (from shared/nextcore) + .next/standalone
//	    │
//	    ▼
//	 DetectVersions ── pick runtime variant (v13 / v14 / v15)
//	    │
//	    ▼
//	 ScanCompiledServer ── walk .next/server/**, build ModuleRef graph
//	    │
//	    ▼
//	 DetectServerActions ── parse server-reference-manifest.json
//	    │
//	    ▼
//	 DeriveBindings ── static analysis → binding hints
//	    │
//	    ▼
//	 ElideDeadRoutes ── drop orphans
//	    │
//	    ▼
//	 Emit{Manifest,DispatchTable,ActionManifest} → <OutDir>/_nextdeploy/
//	    │
//	    ▼
//	 ExtractRuntimeForVersion + AssembleBundle → CompiledBundle
package nextcompile

import "time"

// Target selects which deploy surface the bundle is compiled for.
// The same scan phase feeds every target; emit phases diverge.
type Target string

const (
	TargetCloudflareWorker Target = "cloudflare-worker"
	TargetAWSLambda        Target = "aws-lambda"
	TargetVPS              Target = "vps"
)

// RouteKind categorizes a compiled module. Dispatch order in the runtime
// follows this roughly: Middleware → Static/SSG → ISR → SSR/Page → API/Action.
type RouteKind string

const (
	RouteKindPage       RouteKind = "page"
	RouteKindLayout     RouteKind = "layout"
	RouteKindAPI        RouteKind = "api"
	RouteKindAction     RouteKind = "action"
	RouteKindMiddleware RouteKind = "middleware"
	// RouteKindProxy is Next 15's proxy.ts — a Node-runtime middleware
	// complement. Same dispatch position as middleware but uses the Node
	// execution model (can call crypto, Node streams, etc.). If both
	// proxy and middleware exist, proxy wins (Next's documented order).
	RouteKindProxy   RouteKind = "proxy"
	RouteKindStatic  RouteKind = "static"
	RouteKindUnknown RouteKind = "unknown"
)

// NextVersion is the semver-ish breakdown of the detected Next.js version.
// Raw is preserved verbatim (e.g. "15.0.0-canary.42") for diagnostics.
type NextVersion struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

// ReactVersion mirrors NextVersion. Tracked separately because the RSC
// runtime bundle is keyed on React version, not Next version — two Next
// minors can ship against the same React minor.
type ReactVersion struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

// CompileOpts is the input bag for Compile. StandaloneDir and Payload are
// required; everything else has a defensible zero value.
type CompileOpts struct {
	// StandaloneDir is the path to .next/standalone (or its extracted tarball).
	StandaloneDir string

	// Payload is the nextcore extraction. Routes, middleware, image config,
	// and ISR tag data all flow from here into the emitted manifest.
	Payload Payload

	// OutDir is where _nextdeploy/{manifest.json,dispatch.mjs,...} land.
	// Defaults to <StandaloneDir>/.nextdeploy-build when empty.
	OutDir string

	// Target picks the emit strategy. Defaults to TargetCloudflareWorker.
	Target Target

	// Verbose toggles per-step timing logs.
	Verbose bool

	// Log sinks diagnostics. Compile tolerates a nil logger (no output).
	Log Logger
}

// Payload is the subset of nextcore.NextCorePayload that the compiler
// actually reads. Declared as a minimal interface-ish struct so the
// compiler package can stay free of the nextcore import cycle risk
// while staying strongly typed at the adapter boundary.
//
// Callers populate this by translating from nextcore.NextCorePayload
// in the adapter (cli/internal/serverless/cloudflare_adapter.go).
type Payload struct {
	AppName       string
	DistDir       string
	OutputMode    string
	BasePath      string
	HasAppRouter  bool
	Routes        RouteInfo
	Middleware    *MiddlewareConfig
	ImageConfig   *ImageConfig
	I18n          *I18nConfig
	BuildID       string
	GitCommit     string
}

// RouteInfo mirrors nextcore.RouteInfo. Duplicated here so the compiler
// never imports nextcore directly (see Payload doc).
type RouteInfo struct {
	StaticRoutes     []string
	DynamicRoutes    []string
	SSGRoutes        map[string]string // route -> HTML path
	SSRRoutes        []string
	ISRRoutes        map[string]string
	ISRDetail        []ISRRoute
	APIRoutes        []string
	FallbackRoutes   map[string]string
	MiddlewareRoutes []string
}

// ISRRoute mirrors nextcore.ISRRoute for the same duplication reason.
type ISRRoute struct {
	Path       string
	Tags       []string
	Revalidate int
}

// MiddlewareConfig mirrors the subset of nextcore.MiddlewareConfig the
// compiler forwards into the runtime manifest. The full matcher shape is
// opaque here — it gets emitted as JSON into manifest.json verbatim.
type MiddlewareConfig struct {
	Path     string
	Matchers []MiddlewareMatcher
	Runtime  string
}

type MiddlewareMatcher struct {
	Pathname string
	Pattern  string
}

// ImageConfig mirrors the minimal shape the /_next/image runtime handler
// needs: the remote-pattern whitelist. Everything else (device sizes,
// format preference, etc.) is included raw in the emitted manifest.
type ImageConfig struct {
	RemotePatterns []ImageRemotePattern
	Domains        []string
	Formats        []string
	Unoptimized    bool
}

type ImageRemotePattern struct {
	Protocol string
	Hostname string
	Port     string
	Pathname string
}

// I18nConfig mirrors nextcore.I18nConfig for locale-aware dispatch.
type I18nConfig struct {
	Locales         []string
	DefaultLocale   string
	LocaleDetection bool
}

// ModuleRef is one compiled server module — a page, layout, route handler,
// or middleware — plus the static-analysis facts the compiler derived.
type ModuleRef struct {
	// RoutePath is the user-facing URL (e.g. "/api/users" or "/dashboard/[id]").
	RoutePath string

	// Kind is what the dispatcher should do with this module.
	Kind RouteKind

	// CompiledPath is relative to StandaloneDir (e.g. "server/app/api/users/route.js").
	CompiledPath string

	// HasActions is true when Next's server-reference-manifest lists an
	// action ID rooted in this module.
	HasActions bool

	// UsesRSC is true when the compiled source contains Server Component
	// markers ("use client" boundaries or Flight-payload imports).
	UsesRSC bool

	// ClientManifestPath points at the Next-emitted
	// page_client-reference-manifest.json sibling, when present.
	// Relative to StandaloneDir. Used by rsc.mjs as Flight bundlerConfig.
	ClientManifestPath string

	// LayoutChain is the ordered list of compiled layout.js paths that
	// wrap this page, from root to nearest. Empty for non-page kinds.
	LayoutChain []string

	// EnvRefs are the unique process.env.X identifiers the compiler
	// found via lexical scan. Used by DeriveBindings to suggest secrets.
	EnvRefs []string

	// FetchTargets are literal fetch() URL prefixes extracted from the
	// module. Used to suggest KV/R2/D1/service bindings.
	FetchTargets []string

	// ByteSize is the raw size of the compiled source on disk.
	ByteSize int64

	// PPREnabled is true when Next compiled this page with Partial
	// Prerendering. The runtime dispatcher returns a clear 501 for now
	// since our renderer doesn't implement the static-shell / dynamic-
	// holes protocol yet.
	PPREnabled bool
}

// BindingHint is a compiler-derived suggestion for the deployment config.
// Emitted as warnings rather than errors — the user has final say.
type BindingHint struct {
	Kind      string   // "secret" | "kv" | "r2" | "d1" | "service" | "queue"
	Name      string   // env var name or logical binding name
	Reason    string   // human-readable justification
	Sources   []string // ModuleRef.CompiledPath list where the hint was derived
}

// CompileStats is the post-run summary. Logged in full at info, content-hashed
// for reproducible-build verification.
type CompileStats struct {
	RouteCount       int
	ActionCount      int
	DeadRoutesElided int
	BundleBytes      int64
	Duration         time.Duration
	ContentHash      string
}

// CompiledBundle is the output of Compile. Everything in BundleDir is
// ready for the downstream esbuild step; the adapter doesn't need to
// know the subtree layout beyond EntryPath.
type CompiledBundle struct {
	BundleDir         string
	EntryPath         string
	ManifestPath      string
	DispatchPath      string
	ActionManifest    string
	DetectedVersion   NextVersion
	DetectedReact     ReactVersion
	SuggestedBindings []BindingHint
	// VendoredRSC is populated when the target requires vendoring and the
	// react-server-dom-webpack package was located in node_modules. Nil
	// when the app does not use RSC, or when vendoring was not applicable
	// for the target. Checked by adapter logs and bundle reports.
	VendoredRSC *VendoredPackage
	Stats       CompileStats
}

// Logger is the minimal sink the compiler writes to. Matches the subset
// of shared.Logger the package actually uses; kept as an interface so
// tests can pass a no-op sink without pulling in the full shared package.
type Logger interface {
	Info(format string, args ...any)
	Warn(format string, args ...any)
	Debug(format string, args ...any)
}

// nopLogger is the zero-value sink used when CompileOpts.Log is nil.
// Tests + callers that don't care about output get a coherent Logger
// without having to import shared/. The three methods are intentionally
// empty — this is a discard sink, not a stub.
type nopLogger struct{}

func (nopLogger) Info(string, ...any)  { /* intentional no-op: discard sink */ }
func (nopLogger) Warn(string, ...any)  { /* intentional no-op: discard sink */ }
func (nopLogger) Debug(string, ...any) { /* intentional no-op: discard sink */ }
