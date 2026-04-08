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

var (
	rollbackSteps    int
	rollbackToCommit string
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback to a previous deployment instantly",
	Long: `Roll back to a previous deployment.

By default, rolls back one step (the deployment immediately before the active one).
Use --steps N to walk further back (up to the retention limit, currently 5).
Use --to <commit> to roll back to a specific git commit (full or short SHA prefix);
the commit must still be within the retention window.`,
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("rollback", "⏪ ROLLBACK")
		log.Info("Starting NextDeploy rollback process...")

		if rollbackSteps < 0 {
			log.Error("--steps must be >= 0")
			os.Exit(1)
		}
		if rollbackToCommit != "" && rollbackSteps > 1 {
			log.Error("--to and --steps are mutually exclusive")
			os.Exit(1)
		}

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

			opts := serverless.RollbackOptions{
				Steps:    rollbackSteps,
				ToCommit: rollbackToCommit,
			}
			if err := serverless.Rollback(context.Background(), cfg, opts); err != nil {
				log.Error("Serverless rollback failed: %v", err)
				os.Exit(1)
			}
			log.Info("Serverless rollback successful!")

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
			daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd rollback --appName=%q", cfg.App.Name)
			if rollbackToCommit != "" {
				daemonCmd += fmt.Sprintf(" --toCommit=%q", rollbackToCommit)
			} else if rollbackSteps > 0 {
				daemonCmd += fmt.Sprintf(" --steps=%d", rollbackSteps)
			}
			output, err := srv.ExecuteCommand(context.Background(), deploymentServer, daemonCmd, os.Stdout)
			if err != nil {
				log.Error("Rollback failed: %v\nOutput: %s", err, output)
				os.Exit(1)
			}

			log.Info("Rollback successful!")

		default:
			log.Error("Unknown or unsupported target_type: %s", cfg.TargetType)
			log.Info("Supported types are: vps, serverless")
			os.Exit(1)
		}
	},
}

func init() {
	rollbackCmd.Flags().IntVar(&rollbackSteps, "steps", 1, "number of deployments to walk back from the active one (max = retention, currently 5)")
	rollbackCmd.Flags().StringVar(&rollbackToCommit, "to", "", "git commit (full or short SHA prefix) to roll back to; must be within the retention window")
	rootCmd.AddCommand(rollbackCmd)
}
