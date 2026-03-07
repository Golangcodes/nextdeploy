package cmd

import (
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/updater"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for and install CLI updates",
	Long:  `Automatically check for the latest version of the NextDeploy CLI and upgrade the local binary if a newer version is available.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return updater.SelfUpdate(shared.Version)
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
