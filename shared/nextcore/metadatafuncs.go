package nextcore

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func CollectBuildMetadata() (*NextBuildMetadata, error) {
	projectDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	NextCoreLogger.Debug("Build the next to generate build metadata")
	PackageManager, err := DetectPackageManager(projectDir)
	if err != nil {
		PackageManager = "npm"
	}
	buildCommand, err := buildCommand(string(PackageManager))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(".nextdeploy", 0750); err != nil {
		return nil, fmt.Errorf("failed to create .nextdeploy directory: %w", err)
	}
	cmd := exec.Command("sh", "-c", buildCommand)
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("build failed:%w", err)
	}

	nextDir := filepath.Join(projectDir, ".next")
	buildID, err := os.ReadFile(filepath.Join(nextDir, "BUILD_ID"))
	if err != nil {
		return nil, fmt.Errorf("failed to read BUILD_ID: %w", err)
	}
	readJSON := func(filename string) (interface{}, error) {
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
	appBuildManifest, _ := readJSON("app-build-manifest.json")
	prerenderManifest, _ := readJSON("prerender-manifest.json")
	routesManifest, _ := readJSON("routes-manifest.json")
	imagesManifest, _ := readJSON("images-manifest.json")
	appPathRoutesManifest, _ := readJSON("app-path-routes-manifest.json")
	reactLoadableManifest, _ := readJSON("react-loadable-manifest.json")
	var diagnostics []string
	diagnosticsDir := filepath.Join(nextDir, "diagnostics")
	if files, err := os.ReadDir(diagnosticsDir); err == nil {
		for _, file := range files {
			diagnostics = append(diagnostics, file.Name())
		}
	}

	outputMode := OutputModeDefault
	if _, err := os.Stat(filepath.Join(nextDir, "standalone")); err == nil {
		outputMode = OutputModeStandalone
	} else if b, err := os.ReadFile(filepath.Join(projectDir, "next.config.js")); err == nil {
		content := string(b)
		if strings.Contains(content, "output: 'export'") || strings.Contains(content, "output: \"export\"") {
			outputMode = OutputModeExport
		}
	} else if b, err := os.ReadFile(filepath.Join(projectDir, "next.config.mjs")); err == nil {
		content := string(b)
		if strings.Contains(content, "output: 'export'") || strings.Contains(content, "output: \"export\"") {
			outputMode = OutputModeExport
		}
	}

	hasAppRouter := appPathRoutesManifest != nil
	if _, err := os.Stat(filepath.Join(projectDir, "app")); err == nil {
		hasAppRouter = true
	} else if _, err := os.Stat(filepath.Join(projectDir, "src", "app")); err == nil {
		hasAppRouter = true
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
		OutputMode:            outputMode,
		HasAppRouter:          hasAppRouter,
	}, nil

}
