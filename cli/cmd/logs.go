package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Golangcodes/nextdeploy/cli/internal/logs"
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
		showAll, _ := cmd.Flags().GetBool("all")

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
		agg := logs.NewAggregator(os.Stdout, appName)

		fmt.Printf("\n\033[1;36mNextDeploy Unified Log Stream: %s\033[0m\n", appName)
		fmt.Println("\033[90m──────────────────────────────────────────────────\033[0m")

		if showAll {
			var wg sync.WaitGroup
			wg.Add(3)

			// 1. App Logs
			go func() {
				defer wg.Done()
				streamAppLogs(ctx, srv, deploymentServer, appName, routeFilter, agg.GetWriter(logs.SourceApp))
			}()

			// 2. Audit Logs
			go func() {
				defer wg.Done()
				journalCmd := "tail -f -n 20 /var/log/nextdeployd/audit.log"
				_, _ = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, agg.GetWriter(logs.SourceAudit))
			}()

			// 3. Daemon Logs
			go func() {
				defer wg.Done()
				journalCmd := "tail -f -n 20 /var/log/nextdeployd/nextdeployd.log"
				_, _ = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, agg.GetWriter(logs.SourceDaemon))
			}()

			wg.Wait()
			return
		}

		if showAudit {
			journalCmd := "tail -f -n 50 /var/log/nextdeployd/audit.log"
			_, err = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, agg.GetWriter(logs.SourceAudit))
			return
		}

		if showDaemon {
			journalCmd := "tail -f -n 50 /var/log/nextdeployd/nextdeployd.log"
			_, err = srv.ExecuteCommand(ctx, deploymentServer, "sudo "+journalCmd, agg.GetWriter(logs.SourceDaemon))
			return
		}

		streamAppLogs(ctx, srv, deploymentServer, appName, routeFilter, agg.GetWriter(logs.SourceApp))
	},
}

func streamAppLogs(ctx context.Context, srv *server.ServerStruct, serverName, appName, routeFilter string, out io.Writer) {
	daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd logs --appName=%s", appName)
	serviceName, err := srv.ExecuteCommand(ctx, serverName, daemonCmd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31mError: %v\033[0m\n", err)
		return
	}
	serviceName = strings.TrimSpace(serviceName)

	journalCmd := fmt.Sprintf("journalctl -u %s -f -n 50", serviceName)
	if routeFilter != "" {
		journalCmd += fmt.Sprintf(" | grep --line-buffered \"%s\"", routeFilter)
	}

	_, _ = srv.ExecuteCommand(ctx, serverName, "sudo "+journalCmd, out)
}

func init() {
	logsCmd.Flags().String("route", "", "Filter logs by a specific route (e.g. /api/upload)")
	logsCmd.Flags().Bool("audit", false, "Stream the daemon audit log")
	logsCmd.Flags().Bool("daemon", false, "Stream the daemon process log")
	logsCmd.Flags().Bool("all", false, "Stream everything (App + Audit + Daemon)")
	rootCmd.AddCommand(logsCmd)
}
