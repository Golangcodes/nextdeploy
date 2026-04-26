package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/Golangcodes/nextdeploy/shared/utils"

	"github.com/spf13/cobra"
)

var (
	forceBuild  bool
	workerBuild bool
)

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the Next.js app and prepare a deployable tarball",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("build", "BUILD")
		log.Info("Starting NextDeploy build process...")

		if forceBuild {
			log.Info("Force build enabled. Bypassing incremental build check...")
		} else if err := nextcore.ValidateBuildState(); err == nil {
			log.Info("Git commit unchanged. Skipping build (Incremental build state matched).")
			os.Exit(0)
		} else {
			log.Info("Build state validation returned: %v. Proceeding with full build.", err)
		}

		payload, err := nextcore.GenerateMetadata()
		if err != nil {
			log.Error("Failed to generate metadata: %v", err)
			os.Exit(1)
		}
		log.Info("Build mode: %s", payload.OutputMode)
		log.Info("Dist directory: %s", payload.DistDir)
		log.Info("Export directory: %s", payload.ExportDir)

		// --- PRE-BUILD VALIDATIONS ---
		// Guide users to the best build mode for their target type
		if payload.Config.TargetType == "serverless" && payload.OutputMode != nextcore.OutputModeStandalone {
			log.Error("✗ Serverless deployments REQUIRE 'output: \"standalone\"' in your next.config configuration.")
			log.Error("  Please add it to your Next.js config and run 'nextdeploy build' again.")
			os.Exit(1)
		} else if payload.Config.TargetType == "vps" && payload.OutputMode == nextcore.OutputModeStandalone {
			log.Warn("! You are targeting 'vps' but your output mode is 'standalone'.")
			log.Warn("  While this works, the default output mode is often recommended for standard VPS deployments.")
		}

		if payload.DetectedFeatures.HasServerActions && payload.OutputMode == nextcore.OutputModeExport {
			log.Error("✗ Server Actions detected, but OutputMode is 'export'. Server Actions require a runtime.")
			log.Error("  Please change your Next.js config or target to a runtime-enabled mode.")
			os.Exit(1)
		}

		releaseDir := ""
		switch payload.OutputMode {
		case nextcore.OutputModeStandalone:
			releaseDir = filepath.Join(payload.DistDir, "standalone")
			log.Info("Copying public/ → %s/public/...", releaseDir)
			if err := utils.CopyDir("public", filepath.Join(releaseDir, "public")); err != nil {
				log.Error("Failed to copy public/: %v", err)
				os.Exit(1)
			}
			log.Info("Copying %s/static/ → %s/%s/static/...", payload.DistDir, releaseDir, payload.DistDir)
			if err := utils.CopyDir(filepath.Join(payload.DistDir, "static"), filepath.Join(releaseDir, payload.DistDir, "static")); err != nil {
				log.Error("Failed to copy %s/static/: %v", payload.DistDir, err)
				os.Exit(1)
			}
			log.Info("Copying deployment metadata...")
			if err := utils.CopyFile(".nextdeploy/metadata.json", filepath.Join(releaseDir, "metadata.json")); err != nil {
				log.Error("Failed to copy metadata.json: %v", err)
				os.Exit(1)
			}
		case nextcore.OutputModeExport:
			releaseDir = payload.ExportDir
			if err := utils.CopyFile(".nextdeploy/metadata.json", filepath.Join(releaseDir, "metadata.json")); err != nil {
				log.Error("Failed to copy metadata.json: %v", err)
				os.Exit(1)
			}
		default:
			releaseDir = "."
			log.Info("Copying deployment metadata for default mode...")
			if err := utils.CopyFile(".nextdeploy/metadata.json", "metadata.json"); err != nil {
				log.Error("Failed to copy metadata.json: %v", err)
				os.Exit(1)
			}
		}

		log.Info("Release directory: %s", releaseDir)

		tarballName := "app.tar.gz"
		log.Info("Creating tarball: %s", tarballName)

		if err := utils.CreateTarball(releaseDir, tarballName, payload.Config.TargetType, &payload, log); err != nil {
			log.Error("Failed to create tarball: %v", err)
			os.Exit(1)
		}

		log.Info("Build complete! Artifact: %s", tarballName)

		// --- OPTIONAL: Cloudflare Worker bundle (--worker) ---
		// Runs nextcompile + esbuild locally. No Cloudflare API calls, so this
		// works without credentials — handy for dogfooding the CF adapter or
		// inspecting worker.mjs before a real deploy.
		if workerBuild {
			cfg, err := config.Load()
			if err != nil {
				log.Error("--worker: failed to load config: %v", err)
				os.Exit(1)
			}
			if cfg.Serverless == nil || cfg.Serverless.Provider != "cloudflare" {
				log.Error("--worker requires serverless.provider: cloudflare in nextdeploy.yml")
				os.Exit(1)
			}
			if payload.OutputMode != nextcore.OutputModeStandalone {
				log.Error("--worker requires Next.js output: \"standalone\" (got %q)", payload.OutputMode)
				os.Exit(1)
			}
			standaloneDir, err := filepath.Abs(filepath.Join(payload.DistDir, "standalone"))
			if err != nil {
				log.Error("--worker: failed to resolve standalone dir: %v", err)
				os.Exit(1)
			}
			bundlePath, err := serverless.BuildWorkerBundle(context.Background(), standaloneDir, &payload, cfg, nil, log)
			if err != nil {
				log.Error("Worker bundle build failed: %v", err)
				os.Exit(1)
			}
			log.Info("Worker bundle: %s", bundlePath)
		}

		// --- POST-BUILD VALIDATIONS ---
		if payload.Config.TargetType == "serverless" {
			info, err := os.Stat(tarballName)
			if err == nil {
				sizeMB := float64(info.Size()) / (1024 * 1024)
				log.Info("Final tarball size: %.2fMB", sizeMB)
			}

			standaloneDir := filepath.Join(releaseDir)
			if payload.OutputMode == nextcore.OutputModeStandalone {
				report, err := packaging.AuditStandaloneSize(standaloneDir)
				if err == nil {
					log.Info("Bundle Audit: %.2fMB total (node_modules: %.2fMB)", report.TotalMB, report.NodeModulesMB)
					if len(report.TopOffenders) > 0 {
						log.Info("   Top offender: %s (%.2fMB)", report.TopOffenders[0].Package, report.TopOffenders[0].SizeMB)
					}
					if report.TotalMB > 250 {
						log.Warn("WARNING: Bundle size exceeds Lambda's 250MB unzipped limit!")
						log.Warn("   Run 'nextdeploy inspect' for a full report.")
					} else if report.TotalMB > 200 {
						log.Warn("WARNING: Bundle size is approaching Lambda's 250MB limit.")
					}
				}
			}
		}
	},
}

func init() {
	buildCmd.Flags().BoolVarP(&forceBuild, "force", "f", false, "Force a full build even if git commit is unchanged")
	buildCmd.Flags().BoolVar(&workerBuild, "worker", false, "Also compile the Cloudflare Workers bundle (requires serverless.provider=cloudflare, no API creds needed)")
	rootCmd.AddCommand(buildCmd)
}
