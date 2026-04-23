package cmd

import (
	"os"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/Golangcodes/nextdeploy/shared/utils"

	"github.com/spf13/cobra"
)

var forceBuild bool

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
	rootCmd.AddCommand(buildCmd)
}
