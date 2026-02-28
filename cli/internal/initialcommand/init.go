package initialcommand

import (
	"fmt"
	"strings"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
)

func RunInitCommand(cmd *cobra.Command, args []string) error {
	log := shared.PackageLogger("Initialization", "Initialization")

	log.Info("NextDeploy Initialization")
	log.Info("----------------------------------------")
	log.Info("Analysing your Next.js project...")

	payload, err := nextcore.GenerateMetadata()
	if err != nil {
		log.Error("Failed to analyze Next.js project: %v", err)
		return err
	}

	log.Info("\n  ✓ Next.js %s detected", payload.NextVersion)
	hasAppRouter := payload.NextBuildMetadata.HasAppRouter
	if hasAppRouter {
		log.Info("  ✓ App Router detected")
	} else {
		log.Info("  ✓ Pages Router detected")
	}
	log.Info("  ✓ Output mode: %s", payload.OutputMode)
	log.Info("  ✓ Package manager: %s\n", payload.PackageManager)

	log.Info("Scanning your routes...")

	log.Info("  ✓ %d static routes", len(payload.StaticRoutes))
	if len(payload.DynamicRoutes) > 0 {
		log.Info("  ✓ %d dynamic routes", len(payload.DynamicRoutes))
	}
	if payload.Middleware != nil {
		log.Info("  ✓ Middleware route detected")
	}
	if payload.HasImageAssets {
		log.Info("  ✓ Image optimization enabled")
	}

	log.Info("\nNextDeploy understands your application.\n")
	prompt := &survey.Select{
		Message: "Where would you like to deploy your Next.js application?",
		Options: []string{"VPS (Virtual Private Server - SSH)", "Serverless (AWS CloudFront & Lambda)"},
	}
	var targetChoice string
	if err := survey.AskOne(prompt, &targetChoice); err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}

	cfg := &config.NextDeployConfig{
		Version: "1.0",
		App: config.AppConfig{
			Name:        payload.AppName,
			Port:        3000,
			Environment: "production",
		},
	}

	if strings.Contains(targetChoice, "Serverless") {
		cfg.TargetType = "serverless"
		cfg.Serverless = &config.ServerlessConfig{
			Provider: "aws",
			Region:   "us-east-1",
			S3Bucket: "my-nextjs-assets-bucket",
		}
	} else {
		cfg.TargetType = "vps"
		cfg.Servers = []config.ServerConfig{
			{
				Host:     "your-vps-ip",
				Username: "root",
				KeyPath:  "~/.ssh/id_rsa",
			},
		}
	}

	if err := config.SaveConfig("nextdeploy.yml", cfg); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	log.Info("\n🎉 Setup complete! Next steps:")
	log.Info("- Review your nextdeploy.yml configuration")
	log.Info("- Run 'nextdeploy prepare' to prepare a target server")

	return nil
}
