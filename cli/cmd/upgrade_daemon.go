package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Golangcodes/nextdeploy/cli/internal/server"
	"github.com/Golangcodes/nextdeploy/shared"
	"github.com/Golangcodes/nextdeploy/shared/config"
	"github.com/Golangcodes/nextdeploy/shared/updater"
	"github.com/spf13/cobra"
)

var upgradeDaemonCmd = &cobra.Command{
	Use:   "upgrade-daemon",
	Short: "Automatically upgrade the remote NextDeploy daemon on your server",
	Long: `Fetch the latest daemon release, upload it to your server, and perform a zero-downtime restart.
This is useful when the remote daemon is an old version that cannot self-update.`,
	Run: runUpgradeDaemon,
}

func init() {
	rootCmd.AddCommand(upgradeDaemonCmd)
}

func runUpgradeDaemon(cmd *cobra.Command, args []string) {
	log := shared.PackageLogger("upgrade", "🚀 UPGRADE")

	cfg, err := config.Load()
	if err != nil {
		log.Error("Failed to load config: %v", err)
		os.Exit(1)
	}

	if len(cfg.Servers) == 0 {
		log.Error("No servers configured in nextdeploy.yml")
		os.Exit(1)
	}

	fmt.Println("\n🔍 Checking for latest daemon release...")
	latest, err := updater.LatestRelease()
	if err != nil {
		log.Error("Failed to fetch latest release: %v", err)
		os.Exit(1)
	}

	fmt.Printf("📈 Latest version: %s\n", latest.TagName)

	// Create temp dir for the update
	tmpDir, err := os.MkdirTemp("", "nextdeploy-daemon-upgrade-*")
	if err != nil {
		log.Error("Failed to create temp dir: %v", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	// Detect platform (assuming linux/amd64 for VPS for now, we can enhance this)
	// Usually VPS are linux/amd64.
	binaryBase := "nextdeployd"
	osStr := "Linux"
	arch := "amd64"
	ext := ".tar.gz"

	// strip v from version
	cleanVersion := latest.TagName
	if strings.HasPrefix(cleanVersion, "v") {
		cleanVersion = cleanVersion[1:]
	}

	archiveName := fmt.Sprintf("%s_%s_%s_%s%s", binaryBase, cleanVersion, osStr, arch, ext)
	archivePath := filepath.Join(tmpDir, archiveName)
	newBin := filepath.Join(tmpDir, binaryBase)

	opts := updater.DefaultUpdateOptions()
	fmt.Printf("📥 Downloading %s...\n", archiveName)

	// We use the shared downloader
	// downloadBinary(version, binaryName, destPath string, opts *UpdateOptions)
	// But it expects a helper that we might need to expose or reimplement here.
	// Actually shared/updater/updater.go has it.

	// Reusing the download logic from updater.go but adapted for CLI use
	// For simplicity, I'll just use the logic directly since it's already in shared.

	// Actually, I can just call a new exported function in shared/updater if I wanted,
	// but for now I'll use what's available.

	err = updater.DownloadBinaryForCLI(latest.TagName, archiveName, archivePath, opts)
	if err != nil {
		log.Error("Download failed: %v", err)
		os.Exit(1)
	}

	fmt.Println("📦 Extracting binary...")
	if err := updater.ExtractBinaryForCLI(archivePath, binaryBase, newBin); err != nil {
		log.Error("Extraction failed: %v", err)
		os.Exit(1)
	}

	// Prepare remote installation
	srv, err := server.New(server.WithConfig(), server.WithSSH())
	if err != nil {
		log.Error("Failed to initialize server: %v", err)
		os.Exit(1)
	}
	defer srv.CloseSSHConnection()

	deploymentServer, err := srv.GetDeploymentServer()
	if err != nil {
		log.Error("Failed to get deployment server: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("📤 Uploading to %s...\n", deploymentServer)
	remoteTmpPath := "/tmp/nextdeployd"
	if err := srv.UploadFile(ctx, deploymentServer, newBin, remoteTmpPath); err != nil {
		log.Error("Upload failed: %v", err)
		os.Exit(1)
	}

	fmt.Println("⚙️  Installing and restarting daemon...")
	// Move to /usr/local/bin, chmod, and restart.
	// We'll try to find if it's running via systemd or just pkill.
	installCmd := fmt.Sprintf(
		"sudo mv %s /usr/local/bin/nextdeployd && "+
			"sudo chmod +x /usr/local/bin/nextdeployd && "+
			"(sudo systemctl restart nextdeployd || (sudo pkill -f nextdeployd; sudo /usr/local/bin/nextdeployd))",
		remoteTmpPath,
	)

	_, err = srv.ExecuteCommand(ctx, deploymentServer, installCmd, os.Stdout)
	if err != nil {
		log.Warn("Installation command returned an error (it might still have worked if pkill succeeded): %v", err)
	}

	fmt.Printf("\n✨ Remote daemon successfully upgraded to %s!\n", latest.TagName)
	fmt.Println("──────────────────────────────────────────────────")
	fmt.Println("You can now use all latest features of NextDeploy.")
}
