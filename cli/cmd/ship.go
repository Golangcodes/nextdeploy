package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/caddy"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"

	"github.com/spf13/cobra"
)

var shipCmd = &cobra.Command{
	Use:     "ship",
	Aliases: []string{"deploy"},
	Short:   "Upload the deployment artifact to the remote server and start it",
	Long:    "Ships the tarball to the target server defined in your configuration and tells the daemon to execute the deployment. CI/CD friendly.",
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("ship", "🚀 SHIP")
		log.Info("Starting NextDeploy ship process...")

		cfg, err := config.Load()
		if err != nil {
			log.Error("Failed to load config: %v", err)
			os.Exit(1)
		}

		var meta nextcore.NextCorePayload
		metadataBytes, err := os.ReadFile(".nextdeploy/metadata.json")
		if err == nil {
			if err := json.Unmarshal(metadataBytes, &meta); err == nil {
				caddyPlan := caddy.GenerateCaddyfile(meta.AppName, meta.Domain, string(meta.OutputMode), meta.Config.Port, "/opt/nextdeploy/apps/"+meta.AppName+"/current")
				log.Info("  Caddy Configuration Plan:")
				lines := strings.Split(caddyPlan, "\n")
				for _, line := range lines {
					if strings.TrimSpace(line) != "" {
						log.Info("  %s", line)
					}
				}
			}
		}

		if cfg.TargetType == "serverless" {
			log.Info("Deployment Target: SERVERLESS (No VPS or Daemon required): coming soon")
			if cfg.Serverless == nil {
				log.Error("TargetType is 'serverless' but 'serverless' config block is missing.")
				os.Exit(1)
			}

			if err := serverless.Deploy(context.Background(), cfg, &meta); err != nil {
				log.Error("Serverless deployment failed: %v", err)
				os.Exit(1)
			}
			return
		}

		log.Info("Deployment Target: VPS (Daemon execution)")

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
		log.Info("Deployment server: %s", deploymentServer)

		tarballName := "app.tar.gz"
		if _, err := os.Stat(tarballName); os.IsNotExist(err) {
			log.Error("Deployment artifact %s not found. Did you run 'nextdeploy build'?", tarballName)
			os.Exit(1)
		}

		remotePath := fmt.Sprintf("/opt/nextdeploy/uploads/nextdeploy_%s_%d.tar.gz", cfg.App.Name, time.Now().Unix())
		log.Info("Remote path for artifact: %s", remotePath)

		log.Info("Uploading %s to %s on %s...", tarballName, remotePath, deploymentServer)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		err = srv.UploadFile(ctx, deploymentServer, tarballName, remotePath)
		if err != nil {
			log.Error("Failed to upload tarball: %v", err)
			os.Exit(1)
		}

		log.Info("Upload complete. Triggering daemon to process deployment...")

		daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd ship --tarball=\"%s\"", remotePath)
		output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, os.Stdout)
		if err != nil {
			log.Error("Failed to trigger daemon (ensure nextdeployd is in PATH): %v\nOutput: %s", err, output)
			os.Exit(1)
		}

		log.Info("Ship successful! Deployment instructions have been successfully relayed to the daemon.")
	},
}

func init() {
	rootCmd.AddCommand(shipCmd)
}
