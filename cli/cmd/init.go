package cmd

import (
	"github.com/Golangcodes/nextdeploy/cli/internal/initialcommand"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
	"github.com/spf13/cobra"
)

type PackageManager string

func (pm PackageManager) String() string {
	return string(pm)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Next.js deployment configuration",
	Long: `Scaffolds deployment configuration for Next.js applications including:
- Dockerfile for containerization
- nextdeploy.yml configuration
- Optional sample files and gitignore updates`,
	PreRunE: nextcore.ValidateNextJSProject,
	RunE:    initialcommand.RunInitCommand,
}

func init() {
	rootCmd.AddCommand(initCmd)
}
