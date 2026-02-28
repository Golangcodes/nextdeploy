package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared/caddy"
)

type CaddyManager struct {
	configDir string
}

func NewCaddyManager() *CaddyManager {
	dir := "/etc/caddy/nextdeploy.d"
	_ = os.MkdirAll(dir, 0750)
	return &CaddyManager{
		configDir: dir,
	}
}

// GenerateConfig creates a Caddyfile block for a specific Next.js app
func (cm *CaddyManager) GenerateConfig(appName, domain, outputMode string, port int, appDir string) error {
	caddyConfig := caddy.GenerateCaddyfile(appName, domain, outputMode, port, appDir)

	configPath := filepath.Join(cm.configDir, fmt.Sprintf("%s.caddy", appName))
	err := os.WriteFile(configPath, []byte(caddyConfig), 0600)
	if err != nil {
		return fmt.Errorf("failed to write caddy config for %s: %w", appName, err)
	}

	log.Printf("Caddy config generated for %s at %s", appName, configPath)
	return nil
}

// EnsureMainCaddyfile exists and imports our directory
func (cm *CaddyManager) EnsureMainCaddyfile() error {
	mainCaddyfile := "/etc/caddy/Caddyfile"
	importDirective := fmt.Sprintf("import %s/*.caddy\n", cm.configDir)

	content, err := os.ReadFile(mainCaddyfile)
	if err != nil {
		if os.IsNotExist(err) {
			// Create it if it doesn't exist at all
			return os.WriteFile(mainCaddyfile, []byte(importDirective), 0600)
		}
		return fmt.Errorf("failed to read main Caddyfile: %w", err)
	}

	// Simple check if our import is there
	contentStr := string(content)
	if !containsStr(contentStr, importDirective) {
		// Append to the file (safest)
		f, err := os.OpenFile(mainCaddyfile, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("failed to open main Caddyfile for appending: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString("\n" + importDirective); err != nil {
			return fmt.Errorf("failed to append to main Caddyfile: %w", err)
		}
	}

	return nil
}

// Reload reloads the Caddy daemon via systemctl
func (cm *CaddyManager) Reload() error {
	// 1. Try systemctl reload first (the standard way)
	cmd := exec.Command("systemctl", "reload", "caddy")
	output, err := cmd.CombinedOutput()
	if err == nil {
		log.Println("Caddy reloaded successfully via systemctl.")
		return nil
	}

	log.Printf("Warning: systemctl reload caddy failed (%v), falling back to direct caddy reload...", err)

	// 2. Fallback to direct 'caddy reload' if systemctl fails
	// We use the same config path as in the systemd unit
	fallbackCmd := exec.Command("caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	output, err = fallbackCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy reload failed (systemctl and fallback): %v - %s", err, string(output))
	}

	log.Println("Caddy reloaded successfully via direct fallback command.")
	return nil
}

// Validate checks the syntax of the generated Caddy configuration
func (cm *CaddyManager) Validate() error {
	cmd := exec.Command("caddy", "validate", "--config", "/etc/caddy/Caddyfile")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy validation failed: %v - %s", err, string(output))
	}
	return nil
}

// RemoveConfig removes a caddy configuration file when deleting an app
func (cm *CaddyManager) RemoveConfig(appName string) error {
	configPath := filepath.Join(cm.configDir, fmt.Sprintf("%s.caddy", appName))
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove caddy config for %s: %w", appName, err)
	}
	return nil
}

func containsStr(s, substr string) bool {
	// Simple string contain check bypassing strings import to save time
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
