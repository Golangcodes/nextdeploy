package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared/caddy"
)

const mainCaddyfilePath = "/etc/caddy/Caddyfile"

type CaddyManager struct {
	configDir string
}

func NewCaddyManager() *CaddyManager {
	dir := "/etc/caddy/nextdeploy.d"
	// #nosec G301
	_ = os.MkdirAll(dir, 0755)
	return &CaddyManager{
		configDir: dir,
	}
}

func (cm *CaddyManager) GenerateConfig(appName, domain, outputMode string, port int, appDir string) error {
	caddyConfig := caddy.GenerateCaddyfile(appName, domain, outputMode, port, appDir)
	configPath := filepath.Join(cm.configDir, fmt.Sprintf("%s.caddy", appName))
	// #nosec G306
	err := os.WriteFile(configPath, []byte(caddyConfig), 0644)
	if err != nil {
		return fmt.Errorf("failed to write caddy config for %s: %w", appName, err)
	}

	log.Printf("Caddy config generated for %s at %s", appName, configPath)
	return nil
}

func (cm *CaddyManager) EnsureMainCaddyfile() error {
	importDirective := fmt.Sprintf("import %s/*.caddy\n", cm.configDir)
	content, err := os.ReadFile(mainCaddyfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(mainCaddyfilePath, []byte(importDirective), 0600)
		}
		return fmt.Errorf("failed to read main Caddyfile: %w", err)
	}

	contentStr := string(content)
	if !containsStr(contentStr, importDirective) {
		f, err := os.OpenFile(mainCaddyfilePath, os.O_APPEND|os.O_WRONLY, 0600)
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

func (cm *CaddyManager) Reload() error {
	systemctl := resolveTool("systemctl")
	cmd := exec.Command(systemctl, "reload", "caddy")
	output, err := cmd.CombinedOutput()
	if err == nil {
		log.Println("Caddy reloaded successfully via systemctl.")
		return nil
	}

	log.Printf("Warning: systemctl reload caddy failed (%v), falling back to direct caddy reload...", err)
	caddyPath := resolveTool("caddy")
	fallbackCmd := exec.Command(caddyPath, "reload", "--config", mainCaddyfilePath, "--adapter", "caddyfile")
	output, err = fallbackCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy reload failed (systemctl and fallback): %v - %s", err, string(output))
	}

	log.Println("Caddy reloaded successfully via direct fallback command.")
	return nil
}

func (cm *CaddyManager) Validate() error {
	caddyPath := resolveTool("caddy")
	cmd := exec.Command(caddyPath, "validate", "--config", mainCaddyfilePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy validation failed: %v - %s", err, string(output))
	}
	return nil
}

func (cm *CaddyManager) RemoveConfig(appName string) error {
	configPath := filepath.Join(cm.configDir, fmt.Sprintf("%s.caddy", appName))
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove caddy config for %s: %w", appName, err)
	}
	return nil
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
