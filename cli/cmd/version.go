package cmd

import (
	"fmt"

	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the current version of the NextDeploy CLI",
	Long:  `Display the version number and build metadata of the NextDeploy command-line tool.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("nextdeploy %s\n", shared.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
