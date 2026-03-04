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

		if cfg.TargetType == "serverless" {
			log.Info("Targeting SERVERLESS for destruction of app: %s", appName)

			if cfg.Serverless == nil {
				log.Error("TargetType is 'serverless' but 'serverless' config block is missing.")
				os.Exit(1)
			}

			// Initialize provider
			var p serverless.Provider
			switch cfg.Serverless.Provider {
			case "aws":
				p = serverless.NewAWSProvider()
			default:
				log.Error("Unsupported serverless provider: %s", cfg.Serverless.Provider)
				os.Exit(1)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			if err := p.Initialize(ctx, cfg); err != nil {
				log.Error("Failed to initialize serverless provider: %v", err)
				os.Exit(1)
			}

			if err := p.Destroy(ctx, cfg); err != nil {
				log.Error("Serverless destruction failed: %v", err)
				os.Exit(1)
			}

			log.Info("✅ Serverless resources successfully processed for destruction.")
			return
		}

		log.Info("Targeting VPS for destruction of app: %s", appName)

		// 3. Connect to server
		srv, err := server.New(server.WithConfig(), server.WithSSH())
		if err != nil {
			log.Error("Failed to initialize server connection: %v", err)
			os.Exit(1)
		}
		defer srv.CloseSSHConnection()

		deploymentServer, err := srv.GetDeploymentServer()
		if err != nil {
			log.Error("Failed to get deployment server: %v", err)
			os.Exit(1)
		}

		log.Info("Connecting to %s...", deploymentServer)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// 4. Remove files
		remoteAppDir := fmt.Sprintf("/opt/nextdeploy/apps/%s", appName)
		log.Info("Removing remote directory: %s", remoteAppDir)

		rmCmd := fmt.Sprintf("sudo rm -rf %s", remoteAppDir)
		output, err := srv.ExecuteCommand(ctx, deploymentServer, rmCmd, os.Stdout)
		if err != nil {
			log.Error("Failed to delete remote files: %v\nOutput: %s", err, output)
			os.Exit(1)
		}

		log.Info("✅ App files successfully removed from remote server.")
		log.Info("Note: This did not remove the systemd service or Caddy configuration if they were manually created.")
	},
}

func init() {
	rootCmd.AddCommand(destroyCmd)
}
