package initialcommand

import (
	"encoding/json"
	"fmt"
	"os"
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

	// Basic analysis without requiring nextdeploy.yml
	appName := "example-app"
	if data, err := os.ReadFile("package.json"); err == nil {
		var pkg struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &pkg); err == nil && pkg.Name != "" {
			appName = pkg.Name
		}
	}

	nextVersion, _ := nextcore.GetNextJsVersion("package.json")
	if nextVersion != "" {
		log.Info("  ✓ Next.js %s detected", nextVersion)
	}

	cwd, _ := os.Getwd()
	pkgManager, _ := nextcore.DetectPackageManager(cwd)
	if pkgManager != nextcore.Unknown {
		log.Info("  ✓ Package manager: %s\n", pkgManager.String())
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

	targetType := "vps"
	if strings.Contains(targetChoice, "Serverless") {
		targetType = "serverless"
	}

	// Use the raw template to preserve comments
	configContent := config.GetSampleConfigTemplate(targetType)
	configContent = strings.ReplaceAll(configContent, "name: example-app", "name: "+appName)

	if err := os.WriteFile("nextdeploy.yml", []byte(configContent), 0600); err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	log.Info("\n🎉 Setup complete! Next steps:")
	log.Info("- Review your nextdeploy.yml configuration")
	log.Info("- Run 'nextdeploy prepare' to prepare a target server")

	return nil
}
