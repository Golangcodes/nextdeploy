package cmd

import (
	"nextdeploy/shared"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var generateCICmd = &cobra.Command{
	Use:     "generate-ci",
	Aliases: []string{"ci"},
	Short:   "Generate a GitHub Actions workflow for zero-touch deployment",
	Long: `Creates a .github/workflows/nextdeploy.yml file that automatically 
builds your Next.js project and ships it using NextDeploy on every push to main.`,
	Run: func(cmd *cobra.Command, args []string) {
		log := shared.PackageLogger("generate-ci", "🤖 CI/CD")
		log.Info("Setting up NextDeploy GitHub Actions workflow...")

		workflowContent := `name: Deploy Next.js App

on:
  push:
    branches:
      - main

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout Code
        uses: actions/checkout@v3

      - name: Setup Node.js
        uses: actions/setup-node@v3
        with:
          node-version: 20

      - name: Install Dependencies
        run: npm ci

      - name: Setup Go (For NextDeploy CLI)
        uses: actions/setup-go@v4
        with:
          go-version: 1.22

      - name: Install NextDeploy CLI
        run: |
          go install github.com/aynaash/NextDeploy/cli@latest
          export PATH=$PATH:$(go env GOPATH)/bin

      - name: Setup SSH Key
        run: |
          mkdir -p ~/.ssh
          echo "${{ secrets.SSH_PRIVATE_KEY }}" > ~/.ssh/nextdeploy-key.pem
          chmod 600 ~/.ssh/nextdeploy-key.pem

      - name: Build Next.js Payload
        run: nextdeploy build

      - name: Ship Application
        env:
          DOPPLER_TOKEN: ${{ secrets.DOPPLER_TOKEN }}
        run: nextdeploy deploy
`

		// Create .github/workflows directory
		workflowDir := filepath.Join(".github", "workflows")
		if err := os.MkdirAll(workflowDir, 0750); err != nil {
			log.Error("Failed to create .github/workflows directory: %v", err)
			os.Exit(1)
		}

		workflowPath := filepath.Join(workflowDir, "nextdeploy.yml")

		// Check if file already exists
		if _, err := os.Stat(workflowPath); err == nil {
			log.Error("Workflow file already exists at %s. Aborting to prevent overwrite.", workflowPath)
			os.Exit(1)
		}

		if err := os.WriteFile(workflowPath, []byte(workflowContent), 0600); err != nil {
			log.Error("Failed to write workflow file: %v", err)
			os.Exit(1)
		}

		log.Info("\n✓ Successfully generated GitHub Actions workflow at: %s", workflowPath)
		log.Info("\nNext Steps:")
		log.Info("1. Go to your GitHub Repository Settings > Secrets and variables > Actions.")
		log.Info("2. Add a new repository secret named: SSH_PRIVATE_KEY")
		log.Info("3. (Optional) Add a new repository secret named: DOPPLER_TOKEN")
		log.Info("4. Commit your code and push to `main`.\n")
		log.Info("GitHub Actions will now handle all future deployments automatically! 🚀")
	},
}

func init() {
	rootCmd.AddCommand(generateCICmd)
}
