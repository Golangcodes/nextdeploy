package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	var caddyConfig string

	commonHeaders := `
	encode zstd gzip
	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
		X-Content-Type-Options "nosniff"
		X-Frame-Options "SAMEORIGIN"
		Referrer-Policy "strict-origin-when-cross-origin"
	}`

	if outputMode == "export" {
		// Static site hosting
		staticDir := filepath.Join(appDir, "out")
		caddyConfig = fmt.Sprintf(`%s {%s
	root * %s
	file_server
}`, domain, commonHeaders, staticDir)
	} else {
		// Reverse proxy for standalone or default mode
		nextStaticDir := filepath.Join(appDir, ".next", "static")

		caddyConfig = fmt.Sprintf(`%s {%s
	
	handle_path /_next/static/* {
		root * %s
		header Cache-Control "public, max-age=31536000, immutable"
		file_server
	}

	handle {
		reverse_proxy localhost:%d
	}
}`, domain, commonHeaders, nextStaticDir, port)
	}

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
	cmd := exec.Command("systemctl", "reload", "caddy")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reload caddy: %v - %s", err, string(output))
	}
	log.Println("Caddy reloaded successfully.")
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
