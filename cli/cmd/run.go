package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/spf13/cobra"
)

var (
	runLogger = shared.PackageLogger("runimage", "🚀 RUN")
)

var (
	envFile string
)

var runimageCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the built application locally mimicking the production environment",
	Long: `Loads the build metadata from .nextdeploy/metadata.json and runs the application
natively (no Docker) using the same entrypoint and environment injection as the production daemon.`,
	Run: func(cmd *cobra.Command, args []string) {
		runLocal()
	},
}

func init() {
	runimageCmd.Flags().StringVar(&envFile, "env-file", "", "Path to a custom .env file to load")
	rootCmd.AddCommand(runimageCmd)
}

func runLocal() {
	cfg, err := config.Load()
	if err != nil {
		runLogger.Error("Failed to load configuration: %v", err)
		os.Exit(1)
	}

	metadata, err := nextcore.LoadMetadata()
	if err != nil {
		runLogger.Error("Metadata Error: %v", err)
		runLogger.Info("Did you run 'nextdeploy build' yet?")
		os.Exit(1)
	}

	runLogger.Info("Simulating production for %s (Version: %s, Mode: %s)",
		metadata.AppName, metadata.GitCommit[:7], metadata.OutputMode)

	env := os.Environ()
	if envFile != "" {
		runLogger.Info("Loading secrets from %s...", envFile)
		fileEnv, err := loadEnvFile(envFile)
		if err != nil {
			runLogger.Error("Failed to load env file: %v", err)
		} else {
			env = append(env, fileEnv...)
		}
	} else {
		if _, err := os.Stat(".env.nextdeploy"); err == nil {
			runLogger.Info("Loading secrets from .env.nextdeploy...")
			fileEnv, _ := loadEnvFile(".env.nextdeploy")
			env = append(env, fileEnv...)
		}
	}

	port := fmt.Sprintf("%d", cfg.App.Port)
	if port == "0" {
		port = "3000"
	}
	env = append(env, "PORT="+port)
	env = append(env, "NODE_ENV=production")
	var runCmd *exec.Cmd
	cwd, _ := os.Getwd()
	switch metadata.OutputMode {
	case nextcore.OutputModeStandalone:
		serverJs := filepath.Join(".next", "standalone", "server.js")
		if _, err := os.Stat(serverJs); os.IsNotExist(err) {
			runLogger.Error("Standalone server.js not found at %s", serverJs)
			os.Exit(1)
		}
		runLogger.Info("Starting native standalone server: node %s", serverJs)
		runCmd = exec.Command("node", serverJs)
	case nextcore.OutputModeDefault:
		runLogger.Info("Starting native production server: npm start")
		runCmd = exec.Command("npm", "start")

	case nextcore.OutputModeExport:
		runLogger.Warn("Export mode detected. This app is static and should be served via a web server (like Caddy).")
		runLogger.Info("Try: caddy file-server --listen :%s --root out", port)
		return

	default:
		runLogger.Error("Unknown output mode: %s", metadata.OutputMode)
		os.Exit(1)
	}

	runCmd.Env = env
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	runCmd.Dir = cwd
	runLogger.Info("Application starting on http://localhost:%s", port)
	if err := runCmd.Run(); err != nil {
		runLogger.Error("Application exited with error: %v", err)
		os.Exit(1)
	}
}

func loadEnvFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result = append(result, line)
	}
	return result, nil
}
