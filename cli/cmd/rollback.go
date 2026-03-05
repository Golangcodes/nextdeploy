package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/spf13/cobra"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback to the previous deployment instantly",
	Long:  "Swaps the active symlink to the previous release and restarts the application via the daemon.",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("rollback", "⏪ ROLLBACK")
		log.Info("Starting NextDeploy rollback process...")

		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

		switch cfg.TargetType {
		case "serverless":
			log.Info("Deployment Target: SERVERLESS (No VPS required)")
			if cfg.Serverless == nil {
				log.Error("TargetType is 'serverless' but 'serverless' config block is missing.")
				os.Exit(1)
			}

			if err := serverless.Rollback(context.Background(), cfg); err != nil {
				log.Error("Serverless rollback failed: %v", err)
				os.Exit(1)
			}
			log.Info("✅ Serverless rollback successful!")

		case "vps":
			log.Info("Deployment Target: VPS (Daemon execution)")

			if len(cfg.Servers) == 0 {
				log.Error("TargetType is 'vps' but no servers are defined in config.")
				os.Exit(1)
			}

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

			log.Info("Triggering daemon to rollback %s on %s...", cfg.App.Name, deploymentServer)
			daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd rollback --appName=\"%s\"", cfg.App.Name)
			output, err := srv.ExecuteCommand(context.Background(), deploymentServer, daemonCmd, os.Stdout)
			if err != nil {
				log.Error("Rollback failed: %v\nOutput: %s", err, output)
				os.Exit(1)
			}

			log.Info("✅ Rollback successful!")

		default:
			log.Error("Unknown or unsupported target_type: %s", cfg.TargetType)
			log.Info("Supported types are: vps, serverless")
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
}
