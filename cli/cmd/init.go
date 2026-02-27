// NOTE: cross compile safe
package cmd

// FEATURE: GIVE USER  ABILITY TO START FROM ZERO USING OUR OWN NEXTJS TEMPLATES

//TODO: this commands main job should be to give developers a Scaffolded and opionated NextJS web app templates
import (
	"github.com/spf13/cobra"
	"github.com/Golangcodes/nextdeploy/cli/internal/initialcommand"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
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
