package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Golangcodes/nextdeploy/shared/caddy"
	"github.com/Golangcodes/nextdeploy/shared/nextcore"
)

const mainCaddyfilePath = "/etc/caddy/Caddyfile"

type CaddyManager struct {
	configDir string
}

func NewCaddyManager() *CaddyManager {
	dir := "/etc/caddy/nextdeploy.d"
	// #nosec G:u301
	_ = os.MkdirAll(dir, 0750)
	return &CaddyManager{
		configDir: dir,
	}
}

func (cm *CaddyManager) GenerateConfig(appName, domain, outputMode string, port int, appDir string, features *nextcore.DetectedFeatures, distDir, exportDir string) error {
	caddyConfig := caddy.GenerateCaddyfile(appName, domain, outputMode, port, appDir, features, distDir, exportDir)
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
	corazaGlobal := "{\n\torder coraza_waf first\n}\n\n"

	content, err := os.ReadFile(mainCaddyfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return os.WriteFile(mainCaddyfilePath, []byte(corazaGlobal+importDirective), 0600)
		}
		return fmt.Errorf("failed to read main Caddyfile: %w", err)
	}

	contentStr := string(content)
	newContent := contentStr

	if !containsStr(newContent, "order coraza_waf") {
		newContent = corazaGlobal + newContent
	}

	if !containsStr(newContent, importDirective) {
		if len(newContent) > 0 && newContent[len(newContent)-1] != '\n' {
			newContent += "\n"
		}
		newContent += importDirective
	}

	if newContent != contentStr {
		if err := os.WriteFile(mainCaddyfilePath, []byte(newContent), 0600); err != nil {
			return fmt.Errorf("failed to update main Caddyfile: %w", err)
		}
	}

	return nil
}

func (cm *CaddyManager) Reload() error {
	// Validate config before attempting reload to prevent broken configs
	if err := cm.Validate(); err != nil {
		return fmt.Errorf("config validation failed before reload: %w", err)
	}

	systemctl := resolveTool("systemctl")
	// #nosec G204
	cmd := exec.Command(systemctl, "reload", "caddy")
	output, err := cmd.CombinedOutput()
	if err == nil {
		log.Println("Caddy reloaded successfully via systemctl.")
		return nil
	}

	log.Printf("Warning: systemctl reload caddy failed (%v), falling back to direct caddy reload...", err)
	caddyPath := resolveTool("caddy")
	// #nosec G204
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
	// #nosec G204
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
