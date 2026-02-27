package cmd

import (
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/secrets"
	"os"
)
import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	Elogs = shared.PackageLogger("EncryptFiles::", "🔐 Encrypt Files::")
)
var (
	encryptfiles = &cobra.Command{
		Use:   "secrets",
		Short: "Encrypt files for secure storage",
		Long: `
       Encrypt files using AES encryption for secure storage. 
			 This command allows you to specify the files to encrypt and the output location.
		`,
		PreRun: func(cmd *cobra.Command, args []string) {
			// make sure context of the files exists
			filesContextExist() // This function should check if the context for files exists
		},
		Run: func(cmd *cobra.Command, args []string) {
			encryptFiles()
			fmt.Println("Files encrypted successfully!")
		},
	}
)

func encryptFiles() {
	// Initialize the secret manager
	sm, err := secrets.NewSecretManager()
	if err != nil {
		Elogs.Error("Failed to initialize SecretManager: %v", err)
		os.Exit(1)
	}

	// Check if Doppler is enabled
	if sm.IsDopplerEnabled() {
		Elogs.Info("Doppler is enabled, no need for this whole process...")
	}
	// first check if the key exist
	if sm.IsKeyExist() {
		Elogs.Info("Encryption key already exists, using existing key for encryption.")
	} else {
		Elogs.Info("No existing encryption key found, generating a new key.")
		key, err := sm.GeneratePlatformKey()
		if err != nil {
			Elogs.Error("Failed to generate encryption key:%v", err)
			os.Exit(1)
		}
		Elogs.Info("Encryption key generated successfully: %s", key)
	}
	// Get the key now since it exists
	home, _ := os.UserHomeDir()
	appname := sm.GetAppName()
	keyPath := home + "/.nextdeploy/" + appname + "/master.key"
	// #nosec G304
	key, err := os.ReadFile(keyPath)
	if err != nil {
		Elogs.Error("Failed to read encryption key from %s: %v", keyPath, err)
		os.Exit(1)
	}
	// Encrypt the encrypt
	if err := sm.EncryptFile("nextdeploy.yml", key); err != nil {
		Elogs.Error("Failed to encrypt files: %v", err)
		os.Exit(1)
	}
	// Encrypt the .env file
	if err := sm.EncryptFile(".env", key); err != nil {
		Elogs.Error("Failed to encrpt env files:%s", err)
		os.Exit(1)
	}

	// write files to gitignore

	Elogs.Info("Files encrypted successfully.")

}

func filesContextExist() {
	// Placeholder for checking if the files context exists
	// This function should implement the logic to verify the existence of the files context
	Elogs.Info("Checking if files context exists...")
	// For now, we assume it always exists
	// check for nextdeploy.yml file exists
	if _, err := os.Stat("nextdeploy.yml"); os.IsNotExist(err) {
		Elogs.Error("nextdeploy.yml file does not exist. Please run 'nextdeploy init' first.")
		os.Exit(1)
	} else {
		Elogs.Info("Files context exists, proceeding with encryption.")
	}
	// check env files exist
	if _, err := os.Stat(".env"); os.IsNotExist(err) {
		Elogs.Error(".env file does not exist. Please create it before running this command.")
		os.Exit(1)
	} else {
		Elogs.Info(".env file exists, proceeding with encryption.")
	}
}

func init() {
	rootCmd.AddCommand(encryptfiles)
}
