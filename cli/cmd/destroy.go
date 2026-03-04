package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/spf13/cobra"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Remove the application files from the remote server",
	Long:  "Deletes the application deployment from the remote VPS by removing the files in /opt/nextdeploy/apps/<app-name>.",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("destroy", "🧹 DESTROY")
		log.Info("Starting NextDeploy destruction process...")

		// 1. Load config
		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

		// 2. Try to load metadata to get app name and targeted VPS
		var meta nextcore.NextCorePayload
		metadataBytes, err := os.ReadFile(".nextdeploy/metadata.json")
		if err != nil {
			log.Warn("No deployment metadata found (did you deploy yet?). destruction may be incomplete.")
		} else {
			_ = json.Unmarshal(metadataBytes, &meta)
		}

		appName := cfg.App.Name
		if meta.AppName != "" {
			appName = meta.AppName
		}

		destroyedAny := false

		if cfg.TargetType == "serverless" || cfg.Serverless != nil {
			log.Info("Targeting SERVERLESS for destruction...")

			if cfg.Serverless == nil {
				log.Error("TargetType is 'serverless' but 'serverless' config block is missing.")
				// Do not exit, continue to check for VPS cleanup
			} else {
				// Initialize provider
				var p serverless.Provider
				switch cfg.Serverless.Provider {
				case "aws":
					p = serverless.NewAWSProvider()
				default:
					log.Error("Unsupported serverless provider: %s", cfg.Serverless.Provider)
					// Do not exit, continue to check for VPS cleanup
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()

				if err := p.Initialize(ctx, cfg); err != nil {
					log.Error("Failed to initialize serverless provider: %v", err)
				} else {
					if err := p.Destroy(ctx, cfg); err != nil {
						log.Error("Serverless destruction failed: %v", err)
					} else {
						log.Info("✅ Serverless resources successfully processed for destruction.")
						destroyedAny = true
					}
				}
			}
		}

		// Always check for VPS cleanup if servers are defined, because user might have switched targets
		if len(cfg.Servers) > 0 {
			log.Info("Targeting VPS for destruction of app: %s", appName)

			// 3. Connect to server
			srv, err := server.New(server.WithConfig(), server.WithSSH())
			if err != nil {
				log.Warn("Failed to initialize server connection (skipping VPS cleanup): %v", err)
			} else {
				defer srv.CloseSSHConnection()

				deploymentServer, err := srv.GetDeploymentServer()
				if err != nil {
					log.Warn("Failed to get deployment server: %v", err)
				} else {
					log.Info("Connecting to %s...", deploymentServer)
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()

					// 4. Remove files
					remoteAppDir := fmt.Sprintf("/opt/nextdeploy/apps/%s", appName)
					log.Info("Removing remote directory: %s", remoteAppDir)

					rmCmd := fmt.Sprintf("sudo systemctl stop %s || true && sudo rm -rf %s", appName, remoteAppDir)
					output, err := srv.ExecuteCommand(ctx, deploymentServer, rmCmd, os.Stdout)
					if err != nil {
						log.Error("Failed to delete remote files: %v\nOutput: %s", err, output)
					} else {
						log.Info("✅ App files successfully removed from remote server.")
						destroyedAny = true
					}
				}
			}
		}

		if !destroyedAny {
			log.Warn("Destruction process finished, but no resources were identified or removed.")
			log.Info("If you already destroyed everything, this is normal.")
		} else {
			log.Info("Note: Manual steps may still be required (DNS updates, Caddyfile entries, etc.) to fully decommission.")
		}
		log.Info("Note: This did not remove the systemd service or Caddy configuration if they were manually created.")
	},
}

func init() {
	rootCmd.AddCommand(destroyCmd)
}
