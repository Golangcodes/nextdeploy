package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Stream application logs natively from the daemon",
	Long:  "Streams systemd journal logs natively with capabilities to filter by specific Next.js routes.",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("logs", "🚀 LOGS")
		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

		appName := cfg.App.Name
		routeFilter, _ := cmd.Flags().GetString("route")
		showAudit, _ := cmd.Flags().GetBool("audit")
		showDaemon, _ := cmd.Flags().GetBool("daemon")

		log.Info("Streaming logs for %s...", appName)

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

		ctx := context.Background()

		if showAudit {
			log.Info("Streaming audit logs for %s...", appName)
			journalCmd := "tail -f -n 50 /var/log/nextdeployd/audit.log"
			_, err = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, os.Stdout)
			if err != nil {
				log.Error("Audit log stream interrupted: %v", err)
			}
			return
		}

		if showDaemon {
			log.Info("Streaming daemon logs for %s...", appName)
			journalCmd := "tail -f -n 50 /var/log/nextdeployd/nextdeployd.log"
			_, err = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, os.Stdout)
			if err != nil {
				log.Error("Daemon log stream interrupted: %v", err)
			}
			return
		}

		daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd logs --appName=%s", appName)
		serviceName, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, nil)
		if err != nil {
			// Extract any "Error: ..." from the output if it exists
			errMsg := "Failed to resolve service name"
			if strings.Contains(serviceName, "Error:") {
				parts := strings.Split(serviceName, "Error:")
				errMsg = strings.TrimSpace(parts[len(parts)-1])
			} else if serviceName != "" {
				errMsg = strings.TrimSpace(serviceName)
			}
			log.Error("%s", errMsg)
			os.Exit(1)
		}
		serviceName = strings.TrimSpace(serviceName)
		if lines := strings.Split(serviceName, "\n"); len(lines) > 1 {
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line != "" && strings.HasSuffix(line, ".service") {
					serviceName = line
					break
				}
			}
		}

		fmt.Printf("NextDeploy Logs: %s (Service: %s)\n", appName, serviceName)
		if routeFilter != "" {
			fmt.Printf("Route Filter: %s\n", routeFilter)
		}
		fmt.Println("──────────────────────────────────────────────────")

		journalCmd := fmt.Sprintf("journalctl -u %s -f -n 50", serviceName)
		if routeFilter != "" {
			journalCmd += fmt.Sprintf(" | grep \"%s\"", routeFilter)
		}

		_, err = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, os.Stdout)
		if err != nil {
			log.Error("Logs stream interrupted: %v", err)
		}
	},
}

func init() {
	logsCmd.Flags().String("route", "", "Filter logs by a specific route (e.g. /api/upload)")
	logsCmd.Flags().Bool("audit", false, "Stream the daemon audit log")
	logsCmd.Flags().Bool("daemon", false, "Stream the daemon process log")
	rootCmd.AddCommand(logsCmd)
}
