package nextcore

import (
	"github.com/Golangcodes/nextdeploy/shared/config"
)

type OutputMode string

const (
	OutputModeDefault    OutputMode = "default"
	OutputModeStandalone OutputMode = "standalone"
	OutputModeExport     OutputMode = "export"
)

type NextCorePayload struct {
	AppName           string            `json:"app_name"`
	NextVersion       string            `json:"next_version"`
	NextBuildMetadata NextBuildMetadata `json:"nextbuildmetadata"`
	StaticRoutes      []string          `json:"static_routes"`
	DynamicRoutes     []string          `json:"dynamic_routes"`
	BuildCommand      string            `json:"build_command"`
	StartCommand      string            `json:"start_command"`
	HasImageAssets    bool              `json:"has_image_assets"`
	CDNEnabled        bool              `json:"cdn_enabled"`
	Domain            string            `json:"domain"`
	Middleware        *MiddlewareConfig `json:"middleware"`
	StaticAssets      *StaticAssets     `json:"static_assets"`
	GitCommit         string            `json:"git_commit,omitempty"`
	GitDirty          bool              `json:"git_dirty,omitempty"`
	GeneratedAt       string            `json:"generated_at,omitempty"`
	BuildLockFile     string            `json:"build_lock_file,omitempty"`
	MetadataFilePath  string            `json:"metadata_file_path,omitempty"`
	AssetsOutputDir   string            `json:"assets_output_dir,omitempty"`
	Config            config.SafeConfig `json:"config,omitempty"`
	ImageAssets       ImageAssets       `json:"image_assets"`
	RouteInfo         RouteInfo         `json:"route_info"`
	DetectedFeatures  *DetectedFeatures `json:"detected_features,omitempty"`
	DistDir           string            `json:"dist_dir"`
	ExportDir         string            `json:"export_dir"`
	OutputMode        OutputMode        `json:"output_mode"`
	NextBuild         NextBuild         `json:"next_build"`
	WorkingDir        string            `json:"working_dir"`
	RootDir           string            `json:"root_dir"`
	PackageManager    string            `json:"package_manager"`
	Entrypoint        string            `json:"entrypoint"`
}

type DeployMetadata struct {
	GeneratedAt string            `json:"generated_at"`
	Routes      RoutesManifest    `json:"routes"`
	BuildInfo   BuildManifest     `json:"build_info"`
	Middleware  []string          `json:"middleware"`
	EnvVars     map[string]string `json:"env_vars"`
}

type BuildLock struct {
	GitCommit   string `json:"git_commit"`
	GitDirty    bool   `json:"git_dirty"`
	GeneratedAt string `json:"generated_at"`
	Metadata    string `json:"metadata_file"`
}

type RoutesManifest struct {
	Version       int            `json:"version"`
	Pages         []string       `json:"pages"`
	DynamicRoutes []DynamicRoute `json:"dynamicRoutes"`
}

type DynamicRoute struct {
	Page      string            `json:"page"`
	Regex     string            `json:"regex"`
	RouteKeys map[string]string `json:"routeKeys"`
}

type BuildManifest struct {
	Pages map[string][]string `json:"pages"`
}

type StaticAsset struct {
	Path         string `json:"path"`
	AbsolutePath string `json:"absolute_path"`
	PublicPath   string `json:"public_path"`
	Type         string `json:"type"`
	Extension    string `json:"extension"`
	Size         int64  `json:"size"`
}

type StaticAssets struct {
	PublicDir    []StaticAsset `json:"public_dir"`
	StaticFolder []StaticAsset `json:"static_folder"`
	NextStatic   []StaticAsset `json:"next_static"`
	OtherAssets  []StaticAsset `json:"other_assets"`
}

type MiddlewareConfig struct {
	Path         string            `json:"path"`
	Matchers     []MiddlewareRoute `json:"matchers"`
	Runtime      string            `json:"runtime,omitempty"`
	Regions      []string          `json:"regions,omitempty"`
	UnstableFlag string            `json:"unstable_flag,omitempty"`
}

type MiddlewareRoute struct {
	Pathname string                `json:"pathname,omitempty"`
	Pattern  string                `json:"pattern,omitempty"`
	Has      []MiddlewareCondition `json:"has,omitempty"`
	Missing  []MiddlewareCondition `json:"missing,omitempty"`
	Type     string                `json:"type,omitempty"`
	Key      string                `json:"key,omitempty"`
	Value    string                `json:"value,omitempty"`
}

type MiddlewareCondition struct {
	Type  string `json:"type"`
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

type NextConfig struct {
	BasePath                   string                 `json:"basePath,omitempty"`
	Output                     string                 `json:"output,omitempty"`
	Images                     *ImageConfig           `json:"images,omitempty"`
	ReactStrictMode            bool                   `json:"reactStrictMode,omitempty"`
	PoweredByHeader            bool                   `json:"poweredByHeader,omitempty"`
	TrailingSlash              bool                   `json:"trailingSlash,omitempty"`
	PageExtensions             []string               `json:"pageExtensions,omitempty"`
	AssetPrefix                string                 `json:"assetPrefix,omitempty"`
	DistDir                    string                 `json:"distDir,omitempty"`
	CleanDistDir               bool                   `json:"cleanDistDir,omitempty"`
	GenerateBuildId            interface{}            `json:"generateBuildId,omitempty"`
	OnDemandEntries            map[string]interface{} `json:"onDemandEntries,omitempty"`
	CompileOptions             map[string]interface{} `json:"compileOptions,omitempty"`
	Headers                    []interface{}          `json:"headers,omitempty"`
	Redirects                  []interface{}          `json:"redirects,omitempty"`
	Rewrites                   []interface{}          `json:"rewrites,omitempty"`
	SkipMiddlewareUrlNormalize bool                   `json:"skipMiddlewareUrlNormalize,omitempty"`
	SkipTrailingSlashRedirect  bool                   `json:"skipTrailingSlashRedirect,omitempty"`
	Env                        map[string]string      `json:"env,omitempty"`
	PublicRuntimeConfig        map[string]interface{} `json:"publicRuntimeConfig,omitempty"`
	ServerRuntimeConfig        map[string]interface{} `json:"serverRuntimeConfig,omitempty"`
	Compiler                   *CompilerConfig        `json:"compiler,omitempty"`
	Webpack                    interface{}            `json:"webpack,omitempty"`
	Webpack5                   bool                   `json:"webpack5,omitempty"`
	Experimental               *ExperimentalConfig    `json:"experimental,omitempty"`
	EdgeRegions                []string               `json:"edgeRegions,omitempty"`
	EdgeRuntime                string                 `json:"edgeRuntime,omitempty"`
	I18n                       *I18nConfig            `json:"i18n,omitempty"`
	AnalyticsId                string                 `json:"analyticsId,omitempty"`
	MdxRs                      bool                   `json:"mdxRs,omitempty"`
}

type CompilerConfig struct {
	Emotion               interface{} `json:"emotion,omitempty"`
	ReactRemoveProperties interface{} `json:"reactRemoveProperties,omitempty"`
	RemoveConsole         interface{} `json:"removeConsole,omitempty"`
	StyledComponents      interface{} `json:"styledComponents,omitempty"`
	Relay                 interface{} `json:"relay,omitempty"`
}

type ExperimentalConfig struct {
	AppDir                            bool                   `json:"appDir,omitempty"`
	CaseSensitiveRoutes               bool                   `json:"caseSensitiveRoutes,omitempty"`
	UseDeploymentId                   bool                   `json:"useDeploymentId,omitempty"`
	UseDeploymentIdServerActions      bool                   `json:"useDeploymentIdServerActions,omitempty"`
	DeploymentId                      string                 `json:"deploymentId,omitempty"`
	ServerComponents                  bool                   `json:"serverComponents,omitempty"`
	ServerActions                     bool                   `json:"serverActions,omitempty"`
	ServerActionsBodySizeLimit        int                    `json:"serverActionsBodySizeLimit,omitempty"`
	OptimizeCss                       bool                   `json:"optimizeCss,omitempty"`
	OptimisticClientCache             bool                   `json:"optimisticClientCache,omitempty"`
	ClientRouterFilter                bool                   `json:"clientRouterFilter,omitempty"`
	ClientRouterFilterRedirects       bool                   `json:"clientRouterFilterRedirects,omitempty"`
	ClientRouterFilterAllowedRate     float64                `json:"clientRouterFilterAllowedRate,omitempty"`
	ExternalDir                       string                 `json:"externalDir,omitempty"`
	ExternalMiddlewareRewritesResolve bool                   `json:"externalMiddlewareRewritesResolve,omitempty"`
	FallbackNodePolyfills             bool                   `json:"fallbackNodePolyfills,omitempty"`
	ForceSwcTransforms                bool                   `json:"forceSwcTransforms,omitempty"`
	FullySpecified                    bool                   `json:"fullySpecified,omitempty"`
	SwcFileReading                    bool                   `json:"swcFileReading,omitempty"`
	SwcMinify                         bool                   `json:"swcMinify,omitempty"`
	SwcPlugins                        []interface{}          `json:"swcPlugins,omitempty"`
	SwcTraceProfiling                 bool                   `json:"swcTraceProfiling,omitempty"`
	Turbo                             map[string]interface{} `json:"turbo,omitempty"`
	Turbotrace                        map[string]interface{} `json:"turbotrace,omitempty"`
	ScrollRestoration                 bool                   `json:"scrollRestoration,omitempty"`
	NewNextLinkBehavior               bool                   `json:"newNextLinkBehavior,omitempty"`
	ManualClientBasePath              bool                   `json:"manualClientBasePath,omitempty"`
	LegacyBrowsers                    bool                   `json:"legacyBrowsers,omitempty"`
	DisableOptimizedLoading           bool                   `json:"disableOptimizedLoading,omitempty"`
	GzipSize                          bool                   `json:"gzipSize,omitempty"`
	SharedPool                        bool                   `json:"sharedPool,omitempty"`
	WebVitalsAttribution              []string               `json:"webVitalsAttribution,omitempty"`
	InstrumentationHook               string                 `json:"instrumentationHook,omitempty"`
}

type ImageConfig struct {
	Domains               []string             `json:"domains,omitempty"`
	Formats               []string             `json:"formats,omitempty"`
	DeviceSizes           []int                `json:"deviceSizes,omitempty"`
	ImageSizes            []int                `json:"imageSizes,omitempty"`
	Path                  string               `json:"path,omitempty"`
	Loader                string               `json:"loader,omitempty"`
	LoaderFile            string               `json:"loaderFile,omitempty"`
	MinimumCacheTTL       int                  `json:"minimumCacheTTL,omitempty"`
	Unoptimized           bool                 `json:"unoptimized,omitempty"`
	ContentSecurityPolicy string               `json:"contentSecurityPolicy,omitempty"`
	RemotePatterns        []ImageRemotePattern `json:"remotePatterns,omitempty"`
}

type ImageRemotePattern struct {
	Protocol string `json:"protocol,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Port     string `json:"port,omitempty"`
	Pathname string `json:"pathname,omitempty"`
}

type I18nConfig struct {
	Locales         []string `json:"locales"`
	DefaultLocale   string   `json:"defaultLocale"`
	Domains         []Domain `json:"domains,omitempty"`
	LocaleDetection bool     `json:"localeDetection,omitempty"`
}

type Domain struct {
	Domain        string   `json:"domain"`
	Locales       []string `json:"locales,omitempty"`
	DefaultLocale string   `json:"defaultLocale,omitempty"`
}

type ImageAsset struct {
	Path           string `json:"path"`
	AbsolutePath   string `json:"absolute_path"`
	PublicPath     string `json:"public_path"`
	Format         string `json:"format"`
	IsOptimized    bool   `json:"is_optimized"`
	IsStaticImport bool   `json:"is_static_import"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
}

type ImageAssets struct {
	PublicImages    []ImageAsset `json:"public_images"`
	OptimizedImages []ImageAsset `json:"optimized_images"`
	StaticImports   []ImageAsset `json:"static_imports"`
}

type RouteInfo struct {
	StaticRoutes     []string          `json:"static_routes"`
	DynamicRoutes    []string          `json:"dynamic_routes"`
	SSGRoutes        map[string]string `json:"ssg_routes"`
	SSRRoutes        []string          `json:"ssr_routes"`
	ISRRoutes        map[string]string `json:"isr_routes"`
	APIRoutes        []string          `json:"api_routes"`
	FallbackRoutes   map[string]string `json:"fallback_routes"`
	MiddlewareRoutes []string          `json:"middleware_routes"`
}

type NextBuildMetadata struct {
	BuildID               string      `json:"buildId"`
	BuildManifest         interface{} `json:"buildManifest"`
	AppBuildManifest      interface{} `json:"appBuildManifest"`
	PrerenderManifest     interface{} `json:"prerenderManifest"`
	RoutesManifest        interface{} `json:"routesManifest"`
	ImagesManifest        interface{} `json:"imagesManifest"`
	AppPathRoutesManifest interface{} `json:"appPathRoutesManifest"`
	ReactLoadableManifest interface{} `json:"reactLoadableManifest"`
	Diagnostics           []string    `json:"diagnostics"`
	OutputMode            OutputMode  `json:"outputMode"`
	HasAppRouter          bool        `json:"hasAppRouter"`
}

type NextBuild struct {
	RootFiles      RootFiles     `json:"root_files"`
	Cache          Cache         `json:"cache"`
	Server         Server        `json:"server"`
	Static         Static        `json:"static"`
	HasAppRouter   bool          `json:"has_app_router"`
	HasPagesRouter bool          `json:"has_pages_router"`
	BuildMetadata  BuildMetadata `json:"build_metadata"`
}

type RootFiles struct {
	BuildManifest         string `json:"build_manifest"`
	AppBuildManifest      string `json:"app_build_manifest"`
	ReactLoadableManifest string `json:"react_loadable_manifest"`
	PackageJSON           string `json:"package_json"`
	LastBuildTimestamp    string `json:"last_build_timestamp"`
	TraceFile             string `json:"trace_file,omitempty"`
}

type Cache struct {
	Images  []ImageCacheEntry `json:"images"`
	Webpack WebpackCache      `json:"webpack"`
	SWC     []string          `json:"swc"`
}

type ImageCacheEntry struct {
	Hash      string `json:"hash"`
	Format    string `json:"format"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	CachePath string `json:"cache_path"`
}

type WebpackCache struct {
	ClientDevelopment     []string `json:"client_development"`
	ClientProduction      []string `json:"client_production"`
	ServerDevelopment     []string `json:"server_development"`
	ServerProduction      []string `json:"server_production"`
	EdgeServerDevelopment []string `json:"edge_server_development"`
	EdgeServerProduction  []string `json:"edge_server_production"`
}

type Server struct {
	Manifests    ServerManifests `json:"manifests"`
	AppRoutes    []AppRoute      `json:"app_routes"`
	VendorChunks []string        `json:"vendor_chunks"`
	Middleware   Middleware      `json:"middleware"`
}

type ServerManifests struct {
	AppPaths        string `json:"app_paths"`
	Middleware      string `json:"middleware"`
	Pages           string `json:"pages"`
	Font            string `json:"font"`
	ServerReference string `json:"server_reference"`
}

type AppRoute struct {
	RoutePath       string `json:"route_path"`
	PageJS          string `json:"page_js"`
	ClientReference string `json:"client_reference"`
}

type Middleware struct {
	Path     string   `json:"path"`
	Matchers []string `json:"matchers"`
}

type Static struct {
	Chunks  Chunks             `json:"chunks"`
	CSS     []CSSFile          `json:"css"`
	Media   []MediaFile        `json:"media"`
	Webpack []WebpackHotUpdate `json:"webpack"`
}

type Chunks struct {
	App       []string `json:"app"`
	Pages     []string `json:"pages"`
	Polyfills string   `json:"polyfills"`
	Webpack   string   `json:"webpack"`
	Main      string   `json:"main"`
}

type CSSFile struct {
	Path     string `json:"path"`
	IsGlobal bool   `json:"is_global"`
}

type MediaFile struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Ext  string `json:"ext"`
}

type WebpackHotUpdate struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type BuildMetadata struct {
	NextVersion   string     `json:"next_version"`
	BuildTarget   string     `json:"build_target"`
	BuildID       string     `json:"build_id"`
	HasTypeScript bool       `json:"has_typescript"`
	HasESLint     bool       `json:"has_eslint"`
	OutputMode    OutputMode `json:"output_mode"`
}
