package nextcore

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CollectBuildMetadata runs the Next.js build and reads the manifests it
// produces. It intentionally does NOT compute OutputMode — the canonical
// source is NextConfig.Output, and the caller threads that into the payload.
func CollectBuildMetadata(buildCmd string) (*NextBuildMetadata, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	NextCoreLogger.Debug("Building Next.js app to generate build metadata")

	if err := os.MkdirAll(".nextdeploy", 0750); err != nil {
		return nil, fmt.Errorf("failed to create .nextdeploy directory: %w", err)
	}

	// #nosec G204
	cmd := exec.Command("sh", "-c", buildCmd)
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("build failed: %w", err)
	}

	nextDir := filepath.Join(projectDir, ".next")
	// #nosec G304
	buildID, err := os.ReadFile(filepath.Join(nextDir, "BUILD_ID"))
	if err != nil {
		return nil, fmt.Errorf("failed to read BUILD_ID: %w", err)
	}

	readJSON := func(filename string) (interface{}, error) {
		// #nosec G304
		data, err := os.ReadFile(filepath.Join(nextDir, filename))
		if err != nil {
			return nil, err
		}
		var result interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		return result, nil
	}

	buildManifest, _ := readJSON("build-manifest.json")
	appBuildManifest, err := readJSON("app-build-manifest.json")
	if err != nil {
		appBuildManifest, _ = readJSON("server/app-build-manifest.json")
	}
	prerenderManifest, _ := readJSON("prerender-manifest.json")
	routesManifest, _ := readJSON("routes-manifest.json")
	imagesManifest, _ := readJSON("images-manifest.json")
	appPathRoutesManifest, _ := readJSON("app-path-routes-manifest.json")
	reactLoadableManifest, _ := readJSON("react-loadable-manifest.json")

	var diagnostics []string
	if files, err := os.ReadDir(filepath.Join(nextDir, "diagnostics")); err == nil {
		for _, file := range files {
			diagnostics = append(diagnostics, file.Name())
		}
	}

	hasAppRouter := appPathRoutesManifest != nil
	if !hasAppRouter {
		for _, rel := range []string{"app", filepath.Join("src", "app")} {
			if _, err := os.Stat(filepath.Join(projectDir, rel)); err == nil {
				hasAppRouter = true
				break
			}
		}
	}

	return &NextBuildMetadata{
		BuildID:               string(buildID),
		BuildManifest:         buildManifest,
		AppBuildManifest:      appBuildManifest,
		PrerenderManifest:     prerenderManifest,
		RoutesManifest:        routesManifest,
		ImagesManifest:        imagesManifest,
		AppPathRoutesManifest: appPathRoutesManifest,
		ReactLoadableManifest: reactLoadableManifest,
		Diagnostics:           diagnostics,
		HasAppRouter:          hasAppRouter,
	}, nil
}

// detectOutputMode resolves the Next.js output mode using the parsed config
// first (the authoritative source) and falling back to filesystem / raw
// config-file scanning when the config couldn't be evaluated.
func detectOutputMode(projectDir string, nextConfig *NextConfig, distDir string) OutputMode {
	if nextConfig != nil {
		switch nextConfig.Output {
		case "export":
			return OutputModeExport
		case "standalone":
			return OutputModeStandalone
		}
	}
	if distDir == "" {
		distDir = ".next"
	}
	if _, err := os.Stat(filepath.Join(projectDir, distDir, "standalone")); err == nil {
		return OutputModeStandalone
	}
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		// #nosec G304
		b, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		s := string(b)
		if strings.Contains(s, "output: 'export'") || strings.Contains(s, `output: "export"`) {
			return OutputModeExport
		}
	}
	return OutputModeDefault
}
