// TODO: we build simple provisioning logic for devs with terraform
package cmd

import (
	"github.com/Golangcodes/nextdeploy/shared"

	"github.com/spf13/cobra"
)

var (
	provisionLogger = shared.PackageLogger("Provision", "🔧 Provision")
)

var provisionResources = &cobra.Command{
	Use:   "provision",
	Short: "Provision cloud resources for Next.js deployment",
	Long: `
The 'provision' command automates the setup of required cloud infrastructure for deploying
Next.js applications. It handles the creation and configuration of:

Core Components:
- Container Registry repositories for Docker image storage
- Identity and Access Management (IAM) roles with least-privilege policies
- Dedicated deployment users with appropriate permissions
- Infrastructure as Code (IaC) templates for consistent environments

This command ensures your cloud environment is properly configured
before application deployment, following infrastructure best practices.
This command should be built in cloud agnostic way. By providing user with as many cloud options as
possible. User should be able to pass their prefered cloud as flag.
`,
	PreRunE: collectResourcesData,
	RunE:    executeProvisioning,
}

func init() {
	provisionResources.Flags().StringP(
		"cloud-provider",
		"c",
		"aws",
		"Target cloud platform (aws|gcp|azure) for resource provisioning",
	)

	// Register the command with the root command
	rootCmd.AddCommand(provisionResources)
}

func executeProvisioning(cmd *cobra.Command, args []string) error {
	provisionLogger.Info("Initializing infrastructure provisioning...")
	return nil
}

func collectResourcesData(cmd *cobra.Command, args []string) error {
	provisionLogger.Info("Preparing provisioning configuration...")
	return nil
}
