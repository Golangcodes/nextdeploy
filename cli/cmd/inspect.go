package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/internal/packaging"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect the deployment bundle size and dependencies",
	Long:  "Analyzes the .next/standalone directory to identify large dependencies and estimate Lambda deployment size.",
	Run: func(cmd *cobra.Command, args []string) {
		projectDir, err := os.Getwd()
		if err != nil {
			fmt.Printf("✗ Error getting current directory: %v\n", err)
			os.Exit(1)
		}

		payload, err := nextcore.LoadMetadata()
		distDir := ".next"
		if err == nil {
			distDir = payload.DistDir
		}

		standaloneDir := filepath.Join(projectDir, distDir, "standalone")
		if _, err := os.Stat(standaloneDir); os.IsNotExist(err) {
			fmt.Printf("✗ standalone directory not found at %s\n", standaloneDir)
			fmt.Println("  Ensure 'output: \"standalone\"' is set in your next.config.mjs and run 'nextdeploy build' first.")
			os.Exit(1)
		}

		fmt.Printf("🔍 Inspecting bundle in %s...\n\n", standaloneDir)

		report, err := packaging.AuditStandaloneSize(standaloneDir)
		if err != nil {
			fmt.Printf("✗ Error auditing bundle: %v\n", err)
			os.Exit(1)
		}

		totalMB := report.TotalMB
		lambdaLimit := 250.0
		usagePercent := (totalMB / lambdaLimit) * 100

		fmt.Printf("Lambda Package Analysis:\n")
		fmt.Printf("   Total Size:      %.2f MB / %.0f MB (%.1f%% usage)\n", totalMB, lambdaLimit, usagePercent)
		fmt.Printf("   node_modules:    %.2f MB\n", report.NodeModulesMB)
		fmt.Printf("   Server Code:     %.2f MB\n", report.ServerCodeMB)
		fmt.Println()

		if totalMB > lambdaLimit {
			fmt.Printf(" WARNING: Your bundle exceeds the AWS Lambda 250MB unzipped limit!\n")
		} else if totalMB > 200 {
			fmt.Printf("WARNING: Your bundle is approaching the 250MB limit.\n")
		}

		fmt.Printf("Top Dependencies by Size:\n")
		for i, offender := range report.TopOffenders {
			fmt.Printf("   %2d. %-30s %.2f MB\n", i+1, offender.Package, offender.SizeMB)
		}

		fmt.Println("\nTips to reduce size:")
		fmt.Println("   - Ensure specific large binaries (sharp, prisma) are only installed if needed.")
		fmt.Println("   - Use 'next-bundle-analyzer' to find large client-side chunks.")
		fmt.Println("   - Check for large data files mistakenly included in your source tree.")
	},
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}
