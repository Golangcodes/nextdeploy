// TODO: decouple this
package nextcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/git"
)

const (
	BuildLockFileName = ".nextdeploy/build.lock"
	MetadataFileName  = ".nextdeploy/metadata.json"
	AssetsOutputDir   = ".nextdeploy/assets"
	PublicDir         = "public"
)

var (
	NextCoreLogger = shared.PackageLogger("nextcore", "📦 NEXTCORE")
)

func GenerateMetadata() (metadata NextCorePayload, err error) {
	NextCoreLogger.Info("Generating metadata for Next.js application...")
	cfg, err := config.Load()
	if err != nil {
		NextCoreLogger.Error("Failed to load configuration: %v", err)
		return NextCorePayload{}, err
	}

	AppName := cfg.App.Name
	NextJsVersion, err := GetNextJsVersion("package.json")
	if err != nil {
		NextCoreLogger.Error("Failed to get Next.js version: %v", err)
		return NextCorePayload{}, err
	}
	NextCoreLogger.Info("Next.js version: %s", NextJsVersion)
	NextCoreLogger.Info("Collecting build metadata...")
	buildMeta, err := CollectBuildMetadata()
	if err != nil {
		NextCoreLogger.Error("Failed to collect build metadata: %v", err)
		return NextCorePayload{}, err
	}
	routeInfo, err := getRoutesFromManifests(buildMeta)
	if err != nil {
		return NextCorePayload{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		NextCoreLogger.Error("Error getting current working directory")
		return NextCorePayload{}, err
	}
	packageManager, err := DetectPackageManager(cwd)
	if err != nil {
		NextCoreLogger.Error("Failed to detect package manager: %v", err)
		return NextCorePayload{}, err
	}
	buildCommand, err := buildCommand(packageManager.String())
	if err != nil {
		NextCoreLogger.Error("Failed to get build command: %v", err)
		return NextCorePayload{}, err
	}
	startCommand, err := startCommand(packageManager.String())
	if err != nil {
		NextCoreLogger.Error("Failed to get start command: %v", err)
		return NextCorePayload{}, err
	}
	imagesAssets, err := detectImageAssets(buildMeta, cwd)
	var HasImageAssets bool
	if err != nil {
		NextCoreLogger.Error("Failed to detect image assets: %v", err)
		return NextCorePayload{}, err
	}
	if imagesAssets == nil {
		NextCoreLogger.Info("No image assets found in the Next.js build")
	} else {
		HasImageAssets = true
	}

	domainName := cfg.App.Domain
	middlewareConfig, err := ParseMiddleware(cwd)

	if err != nil {
		NextCoreLogger.Error("Failed to parse middleware configuration: %v", err)
		return NextCorePayload{}, err
	}

	StaticAssets, err := ParseStaticAssets(cwd)
	if err != nil {
		NextCoreLogger.Error("Failed to parse static assets: %v", err)
		return NextCorePayload{}, err
	}

	gitCommt, err := git.GetCommitHash()
	if err != nil {
		NextCoreLogger.Error("Failed to get git commit hash: %v", err)
		return NextCorePayload{}, err
	} else {
		NextCoreLogger.Debug("Git commit hash: %s", gitCommt)
	}
	gitDiry := git.IsDirty()

	PayloadPath, err := filepath.Abs(filepath.Join(cwd, MetadataFileName))
	if err != nil {
		NextCoreLogger.Error("Failed to get payload path: %v", err)
		return NextCorePayload{}, err
	}
	buildLockPath, err := filepath.Abs(filepath.Join(cwd, BuildLockFileName))
	if err != nil {
		NextCoreLogger.Error("Failed to get build lock path: %v", err)
		return NextCorePayload{}, err
	}
	AssetsOutputDir, err := filepath.Abs(filepath.Join(cwd, AssetsOutputDir))
	if err != nil {
		NextCoreLogger.Error("Failed to get assets output directory: %v", err)
		return NextCorePayload{}, err
	}
	// 4. Copy static assets
	if err := copyStaticAssets(); err != nil {
		NextCoreLogger.Error("Failed to copy static assets: %v", err)
		return NextCorePayload{}, fmt.Errorf("failed to copy static assets: %w", err)
	}

	// 4. Track git state
	metadata = NextCorePayload{
		AppName:           AppName,
		NextVersion:       NextJsVersion,
		NextBuildMetadata: *buildMeta,
		Config: config.SafeConfig{
			AppName:     cfg.App.Name,
			Domain:      cfg.App.Domain,
			Port:        cfg.App.Port,
			Environment: cfg.App.Environment,
		},
		BuildCommand:     buildCommand,
		StartCommand:     startCommand,
		Entrypoint:       deriveEntrypoint(buildMeta.OutputMode, "."),
		HasImageAssets:   HasImageAssets,
		CDNEnabled:       false,
		Domain:           domainName,
		RouteInfo:        *routeInfo,
		Middleware:       middlewareConfig,
		StaticAssets:     StaticAssets,
		GitCommit:        gitCommt,
		GitDirty:         gitDiry,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		MetadataFilePath: PayloadPath,
		BuildLockFile:    buildLockPath,
		AssetsOutputDir:  AssetsOutputDir,
		PackageManager:   packageManager.String(),
		RootDir:          cwd,
		OutputMode:       buildMeta.OutputMode,
	}

	if err := createBuildLock(&metadata); err != nil {
		NextCoreLogger.Error("Failed to create build lock: %v", err)
		return NextCorePayload{}, fmt.Errorf("failed to create build lock: %w", err)
	}

	return metadata, nil
}

func deriveEntrypoint(outputMode OutputMode, releaseDir string) string {
	switch outputMode {
	case OutputModeStandalone:
		return filepath.Join(releaseDir, "server.js")
	case OutputModeDefault:
		return filepath.Join(releaseDir, "node_modules", ".bin", "next start")
	case OutputModeExport:
		return "" // no process entrypoint - Caddy serves files directly
	default:
		return ""
	}
}

func copyStaticAssets() error {
	srcDir := "public"
	dstDir := filepath.Join(".nextdeploy", "assets")

	// Create destination directory
	if err := os.MkdirAll(dstDir, 0750); err != nil {
		NextCoreLogger.Error("Failed to create destination directory: %v", err)
		return err
	}

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			NextCoreLogger.Error("Error walking path %s: %v", path, err)
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Create relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			NextCoreLogger.Error("Failed to get relative path for %s: %v", path, err)
			return err
		}

		// Create destination path
		dstPath := filepath.Join(dstDir, relPath)

		// Create destination directory structure
		if err := os.MkdirAll(filepath.Dir(dstPath), 0750); err != nil {
			NextCoreLogger.Error("Failed to create directory for %s: %v", dstPath, err)
			return err
		}

		// Copy file
		return copyFile(path, dstPath)
	})
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	// #nosec G304
	source, err := os.Open(src)
	if err != nil {
		NextCoreLogger.Error("Failed to open source file %s: %v", src, err)
		return err
	}
	defer source.Close()

	// #nosec G304
	destination, err := os.Create(dst)
	if err != nil {
		NextCoreLogger.Error("Failed to create destination file %s: %v", dst, err)
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// createBuildLock creates the build.lock file with git state
func createBuildLock(metadata *NextCorePayload) error {
	commitHash, err := git.GetCommitHash()
	if err != nil {
		NextCoreLogger.Error("Failed to get git commit hash: %v", err)
		return fmt.Errorf("failed to get git commit hash: %w", err)
	}

	dirty := git.IsDirty()
	// Write metadata to json file
	fileName := ".nextdeploy/metadata.json"
	marshalledData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		NextCoreLogger.Error("Failed to marshal metadata: %v", err)
		return err
	}
	if err := os.WriteFile(fileName, marshalledData, 0600); err != nil {
		NextCoreLogger.Error("Failed to write metadata json: %v", err)
		return err
	}

	buildLock := BuildLock{
		GitCommit:   commitHash,
		GitDirty:    dirty,
		GeneratedAt: metadata.GeneratedAt,
		Metadata:    fileName,
	}

	data, err := json.MarshalIndent(buildLock, "", "  ")
	if err != nil {
		NextCoreLogger.Error("Failed to marshal build lock: %v", err)
		return err
	}

	return os.WriteFile(filepath.Join(".nextdeploy", "build.lock"), data, 0600)
}

// getPublicEnvVars collects NEXT_PUBLIC_* environment variables
func getPublicEnvVars() map[string]string {
	vars := make(map[string]string)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "NEXT_PUBLIC_") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				vars[parts[0]] = parts[1]
			}
		}
	}
	return vars
}

// ValidateBuildState checks if the current git state matches the build lock
func ValidateBuildState() error {
	lockPath := filepath.Join(".nextdeploy", "build.lock")
	// #nosec G304
	data, err := os.ReadFile(lockPath)
	if err != nil {
		NextCoreLogger.Error("Failed to read build lock file: %v", err)
		return fmt.Errorf("failed to read build lock: %w", err)
	}

	var lock BuildLock
	if err := json.Unmarshal(data, &lock); err != nil {
		NextCoreLogger.Error("Failed to parse build lock file: %v", err)
		return fmt.Errorf("failed to parse build lock: %w", err)
	}

	currentCommit, err := git.GetCommitHash()
	if err != nil {
		NextCoreLogger.Error("Failed to get current git commit: %v", err)
		return fmt.Errorf("failed to get current git commit: %w", err)
	}
	//TODO: use this data to avoid unnecessary builds
	NextCoreLogger.Info("Current git commit: %s", currentCommit)
	NextCoreLogger.Info("Expected git commit: %s", lock.GitCommit)
	if currentCommit != lock.GitCommit {
		NextCoreLogger.Error("Git commit mismatch: expected %s, got %s", lock.GitCommit, currentCommit)
		return fmt.Errorf("git commit mismatch: expected %s, got %s", lock.GitCommit, currentCommit)
	}

	if git.IsDirty() && !lock.GitDirty {
		return errors.New("working directory is dirty but build lock expects clean state")
	}

	return nil
}

var assetExtensions = map[string]string{
	// Images
	".jpg":  "image",
	".jpeg": "image",
	".png":  "image",
	".gif":  "image",
	".webp": "image",
	".avif": "image",
	".svg":  "image",
	".ico":  "image",

	// Fonts
	".woff":  "font",
	".woff2": "font",
	".ttf":   "font",
	".otf":   "font",
	".eot":   "font",

	// Stylesheets
	".css": "stylesheet",

	// Scripts
	".js":  "script",
	".mjs": "script",
	".cjs": "script",
	".jsx": "script",
	".ts":  "script",
	".tsx": "script",

	// Documents
	".html": "document",
	".htm":  "document",
	".pdf":  "document",
	".txt":  "document",
	".md":   "document",
	".json": "document",
	".xml":  "document",
}

// ParseStaticAssets scans the project for static assets
func ParseStaticAssets(projectDir string) (*StaticAssets, error) {
	assets := &StaticAssets{}

	// 1. Scan public directory
	publicDir := filepath.Join(projectDir, "public")
	if _, err := os.Stat(publicDir); err == nil {
		NextCoreLogger.Debug("Scanning public directory: %s", publicDir)
		publicAssets, err := scanDirectory(publicDir, projectDir, "/")
		if err != nil {
			NextCoreLogger.Error("Failed to scan public directory: %v", err)
			return nil, fmt.Errorf("failed to scan public directory: %w", err)
		}
		assets.PublicDir = publicAssets
	}

	// 2. Scan static directory (legacy)
	staticDir := filepath.Join(projectDir, "static")
	if _, err := os.Stat(staticDir); err == nil {
		NextCoreLogger.Debug("Scanning static directory: %s", staticDir)
		staticAssets, err := scanDirectory(staticDir, projectDir, "/static")
		if err != nil {
			NextCoreLogger.Error("Failed to scan static directory: %v", err)
			return nil, fmt.Errorf("failed to scan static directory: %w", err)
		}
		assets.StaticFolder = staticAssets
	}

	// 3. Scan .next/static directory
	nextStaticDir := filepath.Join(projectDir, ".next", "static")
	if _, err := os.Stat(nextStaticDir); err == nil {
		NextCoreLogger.Debug("Scanning .next/static directory: %s", nextStaticDir)
		nextStaticAssets, err := scanDirectory(nextStaticDir, projectDir, "/_next/static")
		if err != nil {
			return nil, fmt.Errorf("failed to scan .next/static directory: %w", err)
		}
		assets.NextStatic = nextStaticAssets
	}

	// 4. Scan for other common static assets in root
	rootAssets, err := scanRootAssets(projectDir)
	if err != nil {
		NextCoreLogger.Error("Failed to scan root assets: %v", err)
		return nil, fmt.Errorf("failed to scan root assets: %w", err)
	}
	assets.OtherAssets = rootAssets

	return assets, nil
}

// scanDirectory recursively scans a directory for static assets
func scanDirectory(dirPath, projectDir, publicPathPrefix string) ([]StaticAsset, error) {
	var assets []StaticAsset

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			NextCoreLogger.Error("Error accessing path %s: %v", path, err)
			return err
		}

		if !info.IsDir() {
			relPath, err := filepath.Rel(dirPath, path)
			if err != nil {
				NextCoreLogger.Error("Failed to get relative path for %s: %v", path, err)
				return err
			}

			ext := strings.ToLower(filepath.Ext(path))
			assetType := "other"
			if t, ok := assetExtensions[ext]; ok {
				assetType = t
			}

			relProjectPath, err := filepath.Rel(projectDir, path)
			if err != nil {
				return err
			}

			publicPath := filepath.Join(publicPathPrefix, relPath)
			// Convert path separators to forward slashes for URLs
			publicPath = filepath.ToSlash(publicPath)

			assets = append(assets, StaticAsset{
				Path:         relProjectPath,
				AbsolutePath: path,
				PublicPath:   publicPath,
				Type:         assetType,
				Extension:    ext,
				Size:         info.Size(),
			})
		}
		return nil
	})

	return assets, err
}

// scanRootAssets scans for common static files in project root
func scanRootAssets(projectDir string) ([]StaticAsset, error) {
	var assets []StaticAsset

	rootFiles := []string{
		"favicon.ico",
		"robots.txt",
		"sitemap.xml",
		"manifest.json",
	}

	for _, file := range rootFiles {
		path := filepath.Join(projectDir, file)
		if _, err := os.Stat(path); err == nil {
			info, err := os.Stat(path)
			if err != nil {
				NextCoreLogger.Debug("Failed to get file info for %s: %v", path, err)
				continue
			}

			ext := strings.ToLower(filepath.Ext(path))
			assetType := "other"
			if t, ok := assetExtensions[ext]; ok {
				assetType = t
			}

			assets = append(assets, StaticAsset{
				Path:         file,
				AbsolutePath: path,
				PublicPath:   "/" + file,
				Type:         assetType,
				Extension:    ext,
				Size:         info.Size(),
			})
		}
	}

	return assets, nil
}

// ParseMiddleware parses Next.js middleware configuration
func ParseMiddleware(projectDir string) (*MiddlewareConfig, error) {
	config := &MiddlewareConfig{
		Path:     filepath.Join(projectDir, "middleware.js"),
		Matchers: []MiddlewareRoute{},
		Runtime:  "nodejs", // Default runtime
	}

	// Check for middleware.ts first, then middleware.js
	middlewarePaths := []string{
		filepath.Join(projectDir, "middleware.ts"),
		filepath.Join(projectDir, "middleware.js"),
	}

	var middlewareFile string
	for _, path := range middlewarePaths {
		if _, err := os.Stat(path); err == nil {
			middlewareFile = path
			config.Path = path
			break
		}
	}

	if middlewareFile == "" {
		return nil, nil // No middleware file found
	}

	// #nosec G304
	// Read middleware file content
	content, err := os.ReadFile(middlewareFile)
	if err != nil {
		NextCoreLogger.Error("Failed to read middleware file: %v", err)
		return nil, fmt.Errorf("failed to read middleware file: %w", err)
	}

	// Parse middleware matchers
	matchers, err := parseMiddlewareMatchers(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse middleware matchers: %w", err)
	}
	config.Matchers = matchers

	// Check for Edge runtime
	if strings.Contains(string(content), "runtime: 'edge'") {
		config.Runtime = "edge"
	}

	// Check for regions configuration
	if regions := parseEdgeRegions(string(content)); len(regions) > 0 {
		config.Regions = regions
	}

	// Check for unstable flags
	if flag := parseUnstableFlag(string(content)); flag != "" {
		config.UnstableFlag = flag
	}

	return config, nil
}

// parseMiddlewareMatchers extracts route matchers from middleware file
func parseMiddlewareMatchers(content string) ([]MiddlewareRoute, error) {
	var matchers []MiddlewareRoute

	// First try to parse config object style
	configObjRegex := regexp.MustCompile(`config\s*=\s*{([^}]*)}`)
	configMatches := configObjRegex.FindStringSubmatch(content)
	if len(configMatches) > 1 {
		// Try to parse as JSON (with some cleaning)
		cleaned := strings.ReplaceAll(configMatches[1], "'", `"`)
		cleaned = strings.ReplaceAll(cleaned, "\n", "")
		cleaned = fmt.Sprintf("{%s}", cleaned)

		var config struct {
			Matcher []struct {
				Pathname string `json:"pathname"`
				Pattern  string `json:"pattern"`
				Has      []struct {
					Type  string `json:"type"`
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"has"`
				Missing []struct {
					Type  string `json:"type"`
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"missing"`
			} `json:"matcher"`
		}

		if err := json.Unmarshal([]byte(cleaned), &config); err == nil {
			for _, m := range config.Matcher {
				route := MiddlewareRoute{
					Pathname: m.Pathname,
					Pattern:  m.Pattern,
				}

				for _, h := range m.Has {
					route.Has = append(route.Has, MiddlewareCondition{
						Type:  h.Type,
						Key:   h.Key,
						Value: h.Value,
					})
				}

				for _, miss := range m.Missing {
					route.Missing = append(route.Missing, MiddlewareCondition{
						Type:  miss.Type,
						Key:   miss.Key,
						Value: miss.Value,
					})
				}

				matchers = append(matchers, route)
			}
			return matchers, nil
		}
	}

	// Fallback to parsing individual matchers
	matcherRegex := regexp.MustCompile(`matcher:\s*(\[[^\]]+\]|{[^}]+})`)
	matcherMatches := matcherRegex.FindStringSubmatch(content)
	if len(matcherMatches) > 1 {
		cleaned := strings.ReplaceAll(matcherMatches[1], "'", `"`)
		cleaned = strings.ReplaceAll(cleaned, "\n", "")

		// Handle array of paths
		if strings.HasPrefix(cleaned, "[") {
			var paths []string
			if err := json.Unmarshal([]byte(cleaned), &paths); err == nil {
				for _, path := range paths {
					matchers = append(matchers, MiddlewareRoute{
						Pathname: path,
					})
				}
				return matchers, nil
			}
		}

		// Handle object matcher
		if strings.HasPrefix(cleaned, "{") {
			var matcher struct {
				Pathname string `json:"pathname"`
				Pattern  string `json:"pattern"`
			}
			if err := json.Unmarshal([]byte(cleaned), &matcher); err == nil {
				matchers = append(matchers, MiddlewareRoute{
					Pathname: matcher.Pathname,
					Pattern:  matcher.Pattern,
				})
				return matchers, nil
			}
		}
	}

	// Fallback to parsing simple path strings
	pathRegex := regexp.MustCompile(`path:\s*['"]([^'"]+)['"]`)
	pathMatches := pathRegex.FindAllStringSubmatch(content, -1)
	for _, match := range pathMatches {
		if len(match) > 1 {
			matchers = append(matchers, MiddlewareRoute{
				Pathname: match[1],
			})
		}
	}

	return matchers, nil
}

// parseUnstableFlag extracts unstable configuration flags
func parseUnstableFlag(content string) string {
	flagRegex := regexp.MustCompile(`unstable_(\w+):\s*true`)
	matches := flagRegex.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

func parseImageConfig(images map[string]interface{}) ImageConfig {
	result := ImageConfig{
		Loader: "default",
	}

	if domains, ok := images["domains"].([]interface{}); ok {
		for _, d := range domains {
			if s, ok := d.(string); ok {
				result.Domains = append(result.Domains, s)
			}
		}
	}

	if formats, ok := images["formats"].([]interface{}); ok {
		for _, f := range formats {
			if s, ok := f.(string); ok {
				result.Formats = append(result.Formats, s)
			}
		}
	}

	if deviceSizes, ok := images["deviceSizes"].([]interface{}); ok {
		for _, s := range deviceSizes {
			if n, ok := s.(float64); ok {
				result.DeviceSizes = append(result.DeviceSizes, int(n))
			}
		}
	}

	if imageSizes, ok := images["imageSizes"].([]interface{}); ok {
		for _, s := range imageSizes {
			if n, ok := s.(float64); ok {
				result.ImageSizes = append(result.ImageSizes, int(n))
			}
		}
	}

	if loader, ok := images["loader"].(string); ok {
		result.Loader = loader
	}

	if path, ok := images["path"].(string); ok {
		result.Path = path
	}

	if ttl, ok := images["minimumCacheTTL"].(float64); ok {
		result.MinimumCacheTTL = int(ttl)
	}

	if unoptimized, ok := images["unoptimized"].(bool); ok {
		result.Unoptimized = unoptimized
	}

	return result
}

// detectImageAssets finds all image assets in the Next.js build
func detectImageAssets(buildMeta *NextBuildMetadata, projectDir string) (*ImageAssets, error) {
	assets := &ImageAssets{}
	var err error

	// 1. Find images in public directory
	publicDir := filepath.Join(projectDir, PublicDir)
	assets.PublicImages, err = findPublicImages(publicDir, projectDir)
	if err != nil {
		NextCoreLogger.Error("Failed to find public images: %v", err)
		return nil, err
	}

	// 2. Find optimized images from Next.js image manifest
	if buildMeta.ImagesManifest != nil {
		if imagesManifest, ok := buildMeta.ImagesManifest.(map[string]interface{}); ok {
			assets.OptimizedImages = parseImagesManifest(imagesManifest, projectDir)
		}
	}

	// 3. Find static image imports from build manifest
	if buildMeta.BuildManifest != nil {
		if buildManifest, ok := buildMeta.BuildManifest.(map[string]interface{}); ok {
			assets.StaticImports = parseStaticImageImports(buildManifest, projectDir)
		}
	}

	return assets, nil
}

// findPublicImages scans the public directory for image assets
func findPublicImages(publicDir, projectDir string) ([]ImageAsset, error) {
	var images []ImageAsset

	// Supported image extensions
	imageExts := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
		".gif":  true,
		".avif": true,
		".svg":  true,
	}

	err := filepath.Walk(publicDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if imageExts[ext] {
				relPath, err := filepath.Rel(publicDir, path)
				if err != nil {
					return err
				}

				images = append(images, ImageAsset{
					Path:         relPath,
					AbsolutePath: path,
					PublicPath:   filepath.Join("/", relPath),
					Format:       strings.TrimPrefix(ext, "."),
					IsOptimized:  false,
				})
			}
		}
		return nil
	})

	return images, err
}

// parseImagesManifest extracts info from Next.js images-manifest.json
func parseImagesManifest(manifest map[string]interface{}, projectDir string) []ImageAsset {
	var images []ImageAsset

	if imagesMap, ok := manifest["images"].(map[string]interface{}); ok {
		for _, img := range imagesMap {
			if imgMap, ok := img.(map[string]interface{}); ok {
				path, _ := imgMap["path"].(string)
				format, _ := imgMap["format"].(string)

				asset := ImageAsset{
					Path:         path,
					AbsolutePath: filepath.Join(projectDir, ".next", "server", path),
					PublicPath:   "/_next/image?url=" + path + "&w=3840&q=75", // Example URL
					Format:       format,
					IsOptimized:  true,
				}

				if width, ok := imgMap["width"].(float64); ok {
					asset.Width = int(width)
				}
				if height, ok := imgMap["height"].(float64); ok {
					asset.Height = int(height)
				}

				images = append(images, asset)
			}
		}
	}

	return images
}

// parseStaticImageImports finds statically imported images from build manifest
func parseStaticImageImports(buildManifest map[string]interface{}, projectDir string) []ImageAsset {
	var images []ImageAsset

	if files, ok := buildManifest["staticImageImports"].(map[string]interface{}); ok {
		for path, data := range files {
			if dataMap, ok := data.(map[string]interface{}); ok {
				format := strings.TrimPrefix(filepath.Ext(path), ".")

				asset := ImageAsset{
					Path:           path,
					AbsolutePath:   filepath.Join(projectDir, path),
					PublicPath:     path, // This will be hashed in the actual build
					Format:         format,
					IsOptimized:    true,
					IsStaticImport: true,
				}

				if width, ok := dataMap["width"].(float64); ok {
					asset.Width = int(width)
				}
				if height, ok := dataMap["height"].(float64); ok {
					asset.Height = int(height)
				}

				images = append(images, asset)
			}
		}
	}

	return images
}
func startCommand(PackageManager string) (string, error) {
	if PackageManager == "" {
		return "", fmt.Errorf("no package manager provided")
	}
	switch PackageManager {
	case "npm":
		return "npm start", nil
	case "yarn":
		return "yarn start", nil
	case "pnpm":
		return "pnpm start", nil
	case "bun":
		return "bun start", nil
	default:
		return "npm start", fmt.Errorf("unsupported package manager: %s", PackageManager)

	}
}
func buildCommand(PackageManager string) (string, error) {

	if PackageManager == "" {
		PackageManager = "npm" // default to npm if not specified
	}

	switch PackageManager {
	case "npm":
		return "npm run build", nil
	case "yarn":
		return "yarn build", nil
	case "pnpm":
		return "pnpm run build", nil
	case "bun":
		return "bun run build", nil
	default:
		return "npm run build", fmt.Errorf("unsupported package manager: %s", PackageManager)
	}

}
