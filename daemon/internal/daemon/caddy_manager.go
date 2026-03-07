package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

	// Surgically remove the default :80 block if it exists (Welcome to Caddy page)
	if strings.Contains(contentStr, ":80 {") && strings.Contains(contentStr, "root * /usr/share/caddy") {
		// This is a naive but effective way to strip the default block for common Caddy installs
		lines := strings.Split(contentStr, "\n")
		var newLines []string
		inDefaultBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == ":80 {" {
				inDefaultBlock = true
				continue
			}
			if inDefaultBlock {
				if trimmed == "}" {
					inDefaultBlock = false
				}
				continue
			}
			newLines = append(newLines, line)
		}
		contentStr = strings.Join(newLines, "\n")
	}

	newContent := contentStr

	// Ensure global options block is at the top
	if !strings.Contains(newContent, "order coraza_waf") {
		newContent = corazaGlobal + newContent
	}

	// Ensure import directive is present
	if !strings.Contains(newContent, importDirective) {
		if len(newContent) > 0 && newContent[len(newContent)-1] != '\n' {
			newContent += "\n"
		}
		newContent += importDirective
	}

	if newContent != string(content) {
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
