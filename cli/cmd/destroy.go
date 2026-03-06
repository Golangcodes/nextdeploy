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

		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

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

		switch cfg.TargetType {
		case "serverless":
			log.Info("Targeting SERVERLESS for destruction...")

			if cfg.Serverless == nil {
				log.Error("TargetType is 'serverless' but 'serverless' config block is missing.")
				os.Exit(1)
			}

			var p serverless.Provider
			switch cfg.Serverless.Provider {
			case "aws":
				p = serverless.NewAWSProvider()
			default:
				log.Error("Unsupported serverless provider: %s", cfg.Serverless.Provider)
				os.Exit(1)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			if err := p.Initialize(ctx, cfg); err != nil {
				log.Error("Failed to initialize serverless provider: %v", err)
				os.Exit(1)
			}

			if err := p.Destroy(ctx, cfg); err != nil {
				log.Error("Serverless destruction failed: %v", err)
				os.Exit(1)
			}

			log.Info(" Serverless resources successfully destroyed.")

		case "vps":
			if len(cfg.Servers) == 0 {
				log.Error("TargetType is 'vps' but no servers are defined in config.")
				os.Exit(1)
			}

			log.Info("Targeting VPS for destruction of app: %s", appName)

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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// 4. Trigger daemon to destroy app
			log.Info("Triggering daemon to destroy app: %s...", appName)
			destroyCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd destroy --appName=%s --socket-path=/run/nextdeployd/nextdeployd.sock", appName)
			output, err := srv.ExecuteCommand(ctx, deploymentServer, destroyCmd, os.Stdout)
			if err != nil {
				log.Error("Failed to destroy app via daemon: %v\nOutput: %s", err, output)
				os.Exit(1)
			}

			log.Info("App resources successfully destroyed by daemon.")

		default:
			log.Error("Unknown or unsupported target_type: %s", cfg.TargetType)
			log.Info("Supported types are: vps, serverless")
			os.Exit(1)
		}

		log.Info("\nNote: Manual steps may still be required (DNS updates, Caddyfile entries, etc.) to fully decommission.")
		log.Info("Note: This did not remove the systemd service or Caddy configuration if they were manually created.")
	},
}

func init() {
	rootCmd.AddCommand(destroyCmd)
}
