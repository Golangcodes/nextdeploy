package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current status of your deployed application",
	Long:  "Queries the daemon for active PID, uptime, memory, routes, and ISR cache warmth.",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("status", "📊 STATUS")
		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

		appName := cfg.App.Name
		log.Info("Querying status for %s...", appName)

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

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd status --appName=%s", appName)
		output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, nil)
		if err != nil {
			log.Error("Failed to query daemon: %v\nOutput: %s", err, output)
			os.Exit(1)
		}

		// Strip shell noise — only keep lines starting from "Status:" header
		output = strings.TrimSpace(output)
		if idx := strings.Index(output, "Status:"); idx >= 0 {
			output = output[idx:]
		}

		fmt.Printf("\n🚀 NextDeploy Status: %s\n", appName)
		fmt.Println("──────────────────────────────────────────────────")
		fmt.Println(output)
		fmt.Println("──────────────────────────────────────────────────")
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
