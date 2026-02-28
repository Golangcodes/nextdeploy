package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage application secrets and environment variables",
	Long:  "Allows you to set, get, list, and unset environment variables for your deployed application. Secrets are securely synced to the daemon.",
}

var secretsSetCmd = &cobra.Command{
	Use:   "set KEY=VALUE...",
	Short: "Set one or more secrets",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runSecretAction("set", args)
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get KEY",
	Short: "Get a secret value",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runSecretAction("get", args)
	},
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all secret names",
	Run: func(cmd *cobra.Command, args []string) {
		runSecretAction("list", args)
	},
}

var secretsUnsetCmd = &cobra.Command{
	Use:   "unset KEY...",
	Short: "Remove one or more secrets",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runSecretAction("unset", args)
	},
}

func runSecretAction(action string, args []string) {
	log := shared.PackageLogger("secrets", "🔐 SECRETS")
	cfg, err := config.Load()
	if err != nil {
		log.Error("Failed to load config: %v", err)
		os.Exit(1)
	}

	appName := cfg.App.Name
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch action {
	case "set":
		for _, arg := range args {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) != 2 {
				log.Warn("Invalid format for '%s', expected KEY=VALUE", arg)
				continue
			}
			key, value := parts[0], parts[1]
			daemonCmd := fmt.Sprintf("/usr/local/bin/nextdeployd secrets --action=set --appName=%s --key=%s --value='%s'", appName, key, value)
			output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, nil)
			if err != nil {
				log.Error("Failed to set secret %s: %v\nOutput: %s", key, err, output)
			} else {
				log.Success("✅ Secret %s set", key)
			}
		}
	case "get":
		key := args[0]
		daemonCmd := fmt.Sprintf("/usr/local/bin/nextdeployd secrets --action=get --appName=%s --key=%s", appName, key)
		output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, nil)
		if err != nil {
			log.Error("Failed to get secret %s: %v\nOutput: %s", key, err, output)
		} else {
			fmt.Printf("%s=%s\n", key, strings.TrimSpace(output))
		}
	case "list":
		daemonCmd := fmt.Sprintf("/usr/local/bin/nextdeployd secrets --action=list --appName=%s", appName)
		output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, nil)
		if err != nil {
			log.Error("Failed to list secrets: %v\nOutput: %s", err, output)
		} else {
			fmt.Printf("Secrets for %s:\n%s\n", appName, output)
		}
	case "unset":
		for _, key := range args {
			daemonCmd := fmt.Sprintf("/usr/local/bin/nextdeployd secrets --action=unset --appName=%s --key=%s", appName, key)
			output, err := srv.ExecuteCommand(ctx, deploymentServer, daemonCmd, nil)
			if err != nil {
				log.Error("Failed to unset secret %s: %v\nOutput: %s", key, err, output)
			} else {
				log.Success("✅ Secret %s removed", key)
			}
		}
	}
}

func init() {
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsUnsetCmd)
	rootCmd.AddCommand(secretsCmd)
}
