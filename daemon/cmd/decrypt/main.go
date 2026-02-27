package main

import (
	"fmt"
	"nextdeploy/shared/secrets"
	"os"
)

func main() {
	sm, err := secrets.NewSecretManager()
	if err != nil {
		fmt.Println("Failed to initialize SecretManager:", err)
		return
	}

	// Check if Doppler is enabled
	if sm.IsDopplerEnabled() {
		fmt.Println("Doppler is enabled, no need for this whole process...")
		return
	}
	// Check if the key exists
	master, err := os.ReadFile("~/app/.nextdeploykeys/master.key")
	if err != nil {
		fmt.Println("Failed to read encryption key:", err)
		return
	}
	// Decrypt the nextdeploy.yml file
	nextdeployyml, err := sm.DecryptFile("nextdeploy.yml.enc", master)
	if err != nil {
		fmt.Println("Failed to decrypt nextdeploy.yml:", err)
		return
	}
	fmt.Println("Decrypted nextdeploy.yml content:\n", string(nextdeployyml))
	// Decrypt the .env file
	envfile, err := sm.DecryptFile(".env.enc", master)
	if err != nil {
		fmt.Println("Failed to decrypt .env file:", err)
		return
	}
	fmt.Println("Decrypted .env content:\n", string(envfile))
	// save the decrypted files
	err = os.WriteFile("nextdeploy.yml", []byte(nextdeployyml), 0600)
	if err != nil {
		fmt.Println("Failed to write decrypted nextdeploy.yml:", err)
		return
	}
	err = os.WriteFile(".env", []byte(envfile), 0600)
	if err != nil {
		fmt.Println("Failed to write decrypted .env file:", err)
		return
	}
	// Print success message
	fmt.Println("Files decrypted successfully!")
	fmt.Println("Using encryption key:", string(master))
}
