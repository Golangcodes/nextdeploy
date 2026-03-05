package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/dns"
	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/cli/internal/serverless"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/caddy"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/git"
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

		// --- PRE-DEPLOY VALIDATIONS ---
		if git.IsDirty() {
			log.Warn("⚠️  Git directory is dirty (uncommitted changes).")
			log.Warn("   It is recommended to commit your changes before shipping for better deployment provenance.")
		}

		// Load metadata for both targets to use in branching/previews
		var meta nextcore.NextCorePayload
		metadataBytes, err := os.ReadFile(".nextdeploy/metadata.json")
		if err == nil {
			_ = json.Unmarshal(metadataBytes, &meta)
		}

		// --- RESOLVE EFFECTIVE TARGET ---
		effectiveTarget := cfg.ResolveTargetType(meta.Config.TargetType)

		// --- BRANCH BY TARGET TYPE ---
		if effectiveTarget == "serverless" {
			log.Info("Deployment Target: SERVERLESS (AWS Lambda + S3 + CloudFront)")
			if cfg.Serverless == nil {
				log.Error("Inferred 'serverless' target but 'serverless' config block is missing.")
				os.Exit(1)
			}

			// Validate Serverless specific constraints (e.g., ISR vs CDN check)
			if meta.DetectedFeatures != nil {
				isrRoutes := meta.RouteInfo.ISRRoutes
				if len(isrRoutes) > 0 && !cfg.App.CDNEnabled {
					log.Warn("⚠️  ISR routes detected but CDN (CloudFront) is not explicitly enabled in config.")
					log.Warn("   Revalidation will not work correctly without a CDN layer.")
				}

				// Secret Validation against Feature Detection
				if meta.DetectedFeatures.HasStripe && os.Getenv("STRIPE_SECRET_KEY") == "" {
					log.Warn("⚠️  Stripe detected in build but STRIPE_SECRET_KEY is not set in environment.")
				}
			}

			if err := serverless.Deploy(context.Background(), cfg, &meta); err != nil {
				log.Error("Serverless deployment failed: %v", err)
				os.Exit(1)
			}
			return
		}

		// Default to VPS logic
		log.Info("Deployment Target: VPS (Traditional Server)")

		// Show Caddy Configuration Plan only for VPS
		if meta.AppName != "" {
			domain := meta.Domain
			if domain == "" {
				domain = cfg.App.Domain
			}
			if domain != "" {
				caddyPlan := caddy.GenerateCaddyfile(meta.AppName, domain, string(meta.OutputMode), meta.Config.Port, "/opt/nextdeploy/apps/"+meta.AppName+"/current", meta.DetectedFeatures, meta.DistDir, meta.ExportDir)
				log.Info("  Caddy Configuration Plan Preview:")
				lines := strings.Split(caddyPlan, "\n")
				for _, line := range lines {
					if strings.TrimSpace(line) != "" {
						log.Info("  %s", line)
					}
				}
			}
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
		log.Info("Deployment server: %s", deploymentServer)

		// Generate DNS guide for VPS
		if cfg.App.Domain != "" {
			if err := dns.GenerateVPSGuide(cfg.App.Domain, deploymentServer); err != nil {
				log.Warn("Failed to generate DNS guide: %v", err)
			} else {
				log.Info("  🌐 DNS Guide Generated: dns.md (Point %s to %s)", cfg.App.Domain, deploymentServer)
			}
		}

		tarballName := "app.tar.gz"
		if _, err := os.Stat(tarballName); os.IsNotExist(err) {
			log.Error("Deployment artifact %s not found. Did you run 'nextdeploy build'?", tarballName)
			os.Exit(1)
		}

		remotePath := fmt.Sprintf("/opt/nextdeploy/uploads/nextdeploy_%s_%d.tar.gz", cfg.App.Name, time.Now().Unix())
		log.Info("Uploading %s to %s on %s...", tarballName, remotePath, deploymentServer)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		err = srv.UploadFile(ctx, deploymentServer, tarballName, remotePath)
		if err != nil {
			log.Error("Failed to upload tarball: %v", err)
			os.Exit(1)
		}

		log.Info("Upload complete. Triggering daemon to process deployment...")

		daemonCmd := fmt.Sprintf("sudo /usr/local/bin/nextdeployd ship --tarball=\"%s\" --socket-path=/run/nextdeployd/nextdeployd.sock", remotePath)
		output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, os.Stdout)
		if err != nil {
			log.Error("Failed to trigger daemon (ensure nextdeployd is in PATH): %v\nOutput: %s", err, output)
			os.Exit(1)
		}

		log.Info("Ship successful! Deployment instructions relayed to the daemon.")

		// --- 8. Generate VPS Visual Report ---
		port := meta.Config.Port
		if port == 0 {
			port = cfg.App.Port
		}
		if port == 0 {
			port = 3000 // Default Next.js port
		}

		resMap := server.VPSResourceMap{
			AppName:        cfg.App.Name,
			Environment:    "production",
			ServerIP:       deploymentServer,
			CustomDomain:   cfg.App.Domain,
			Port:           port,
			DeploymentTime: time.Now(),
		}

		reportPath, err := server.GenerateVPSResourceView(&cfg.App, resMap)
		if err == nil {
			log.Info("════════════════════════════════════════════════════════════")
			log.Success("✨  Visual Deployment Report generated: %s", reportPath)
			log.Info("    Open this file in your browser to see your provisioned resources.")
			log.Info("    DNS setup instructions are included in the report and dns.md!")
			log.Info("════════════════════════════════════════════════════════════")
		} else {
			log.Warn("Failed to generate visual report: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(shipCmd)
}
