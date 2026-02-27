package caddy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CaddyManager provides simplified Caddy server configuration management
type CaddyManager struct {
	adminAPI string       // Caddy admin API endpoint (e.g., "http://localhost:2019")
	client   *http.Client // HTTP client for API requests
}

// New creates a new CaddyManager instance
func New(adminAPI string) *CaddyManager {
	return &CaddyManager{
		adminAPI: strings.TrimSuffix(adminAPI, "/"),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Config represents a Caddy configuration (can be either JSON or Caddyfile format)
type Config struct {
	Content string // The configuration content
	Format  string // "json" or "caddyfile"
}

// GetConfig retrieves the current Caddy configuration
func (cm *CaddyManager) GetConfig(ctx context.Context) (*Config, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", cm.adminAPI+"/config/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return &Config{
		Content: string(body),
		Format:  "json", // Caddy admin API always returns JSON
	}, nil
}

// LoadConfig loads a configuration from a file
func (cm *CaddyManager) LoadConfig(ctx context.Context, filePath string) (*Config, error) {
	// #nosec G304
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	format := "caddyfile"
	if strings.HasSuffix(filePath, ".json") {
		format = "json"
	}

	return &Config{
		Content: string(content),
		Format:  format,
	}, nil
}

// SaveConfig saves the configuration to a file
func (cm *CaddyManager) SaveConfig(ctx context.Context, config *Config, filePath string) error {
	return os.WriteFile(filePath, []byte(config.Content), 0600)
}

// ApplyConfig applies a new configuration to Caddy
func (cm *CaddyManager) ApplyConfig(ctx context.Context, config *Config) error {
	var (
		url  string
		body io.Reader
	)

	switch config.Format {
	case "json":
		url = cm.adminAPI + "/config/"
		body = strings.NewReader(config.Content)
	case "caddyfile":
		url = cm.adminAPI + "/load"
		body = strings.NewReader(config.Content)
	default:
		return fmt.Errorf("unsupported config format: %s", config.Format)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if config.Format == "caddyfile" {
		req.Header.Set("Content-Type", "text/caddyfile")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to apply config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to apply config (status %d): %s", resp.StatusCode, string(errorBody))
	}

	return nil
}

// ValidateConfig validates a configuration without applying it
func (cm *CaddyManager) ValidateConfig(ctx context.Context, config *Config) error {
	if config.Format != "caddyfile" {
		return fmt.Errorf("validation is only supported for caddyfile format")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cm.adminAPI+"/validate", strings.NewReader(config.Content))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/caddyfile")

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("config validation failed (status %d): %s", resp.StatusCode, string(errorBody))
	}

	return nil
}

// GetConfigAsCaddyfile retrieves the current config in Caddyfile format
func (cm *CaddyManager) GetConfigAsCaddyfile(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", cm.adminAPI+"/config/caddyfile", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get caddyfile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// PatchConfig applies a partial configuration update
func (cm *CaddyManager) PatchConfig(ctx context.Context, path string, config interface{}) error {
	jsonData, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PATCH", cm.adminAPI+path, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// #nosec G704
	resp, err := cm.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to patch config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to patch config (status %d): %s", resp.StatusCode, string(errorBody))
	}

	return nil
}

//	func updatecaddyconfig(ctx context.context, servermgr *server.serverstruct, servername, domain, port string, stream io.writer) error {
//		const maxretries = 3
//		const retrydelay = 500 * time.millisecond
//
//		shiplogs.info("starting caddy config update",
//			"domain", domain,
//			"port", port,
//			"server", servername)
//
//		// 1. verify caddy is running
//		shiplogs.debug("checking caddy availability")
//		if _, err := servermgr.executecommand(ctx, servername, "caddy -v", stream); err != nil {
//			shiplogs.error("caddy not available", "error", err)
//			return fmt.errorf("caddy container not found: %w", err)
//		}
//
//		// 2. get current config to modify
//		getconfigcmd := `curl -ss "http://localhost:2019/config/apps/http/servers/srv0"`
//		shiplogs.debug("fetching current caddy config")
//		currentconfig, err := servermgr.executecommand(ctx, servername, getconfigcmd, stream)
//		if err != nil {
//			shiplogs.error("failed to get current config", "error", err)
//			return fmt.errorf("failed to get current config: %w", err)
//		}
//
//		// 3. parse and modify config
//		var config struct {
//			routes []map[string]interface{} `json:"routes"`
//		}
//		if err := json.unmarshal([]byte(currentconfig), &config); err != nil {
//			shiplogs.error("failed to parse config", "error", err)
//			return fmt.errorf("failed to parse config: %w", err)
//		}
//
//		// remove existing route if it exists
//		ShipLogs.Debug("Removing existing route if present")
//		filteredRoutes := make([]map[string]interface{}, 0)
//		for _, route := range config.Routes {
//			if route["match"] != nil {
//				if matches, ok := route["match"].([]interface{}); ok {
//					for _, match := range matches {
//						if hostMatch, ok := match.(map[string]interface{}); ok {
//							if hosts, ok := hostMatch["host"].([]interface{}); ok {
//								for _, h := range hosts {
//									if h == domain {
//										continue // Skip this route
//									}
//								}
//							}
//						}
//					}
//				}
//			}
//			filteredRoutes = append(filteredRoutes, route)
//		}
//
//		// Add new route
//		newRoute := map[string]interface{}{
//			"match": []map[string]interface{}{
//				{
//					"host": []string{domain},
//				},
//			},
//			"handle": []map[string]interface{}{
//				{
//					"handler": "reverse_proxy",
//					"upstreams": []map[string]interface{}{
//						{
//							"dial": fmt.Sprintf("localhost:%s", port),
//						},
//					},
//				},
//			},
//		}
//		config.Routes = append(filteredRoutes, newRoute)
//
//		updatedConfig, err := json.Marshal(config)
//		if err != nil {
//			ShipLogs.Error("Failed to marshal updated config", "error", err)
//			return fmt.Errorf("failed to marshal config: %w", err)
//		}
//
//		// 4. Apply updated config
//		updateCmd := fmt.Sprintf(
//			`curl -sS -X POST "http://localhost:2019/load" \
//	        -H "Content-Type: application/json" \
//	        -d '%s'`,
//			strings.ReplaceAll(string(updatedConfig), "'", "'\\''"))
//
//		ShipLogs.Debug("Applying updated config", "config", string(updatedConfig))
//		if _, err := serverMgr.ExecuteCommand(ctx, serverName, updateCmd, stream); err != nil {
//			ShipLogs.Error("Failed to update config", "error", err)
//			return fmt.Errorf("failed to update config: %w", err)
//		}
//
//		// 5. Verify update
//		verifyCmd := `curl -sS "http://localhost:2019/config/apps/http/servers/srv0"`
//		for i := 0; i < maxRetries; i++ {
//			ShipLogs.Debug("Verifying config update", "attempt", i+1)
//			verifyOutput, err := serverMgr.ExecuteCommand(ctx, serverName, verifyCmd, stream)
//			if err == nil && strings.Contains(verifyOutput, domain) {
//				ShipLogs.Info("Caddy config updated successfully")
//				return nil
//			}
//			time.Sleep(retryDelay)
//		}
//
//		ShipLogs.Error("Failed to verify config update")
//		return fmt.Errorf("failed to verify config update after %d attempts", maxRetries)
//	}
//	func UpdateCaddyConfig(ctx context.Context, serverMgr *server.ServerStruct, serverName, domain, port string, stream io.Writer) error {
// 	const maxRetries = 3
// 	const retryDelay = 500 * time.Millisecond
//
// 	ShipLogs.Info("Starting Caddy config update (simulated)",
// 		"domain", domain,
// 		"port", port,
// 		"server", serverName)
//
// 	// 1. Simulate Caddy availability check
// 	ShipLogs.Debug("Simulating Caddy availability check")
// 	if _, err := simulateCommand("caddy -v"); err != nil {
// 		ShipLogs.Error("Caddy not available (simulated)", "error", err)
// 		return fmt.Errorf("caddy container not found (simulated): %w", err)
// 	}
//
// 	// 2. Simulate getting current config
// 	ShipLogs.Debug("Simulating fetching current Caddy config")
// 	currentConfig := simulateCurrentConfig()
//
// 	// 3. Parse and modify config (simulated)
// 	var config struct {
// 		Routes []map[string]interface{} `json:"routes"`
// 	}
// 	if err := json.Unmarshal([]byte(currentConfig), &config); err != nil {
// 		ShipLogs.Error("Failed to parse config (simulated)", "error", err)
// 		return fmt.Errorf("failed to parse config (simulated): %w", err)
// 	}
//
// 	// Simulate removing existing route
// 	ShipLogs.Debug("Simulating removing existing route if present")
// 	filteredRoutes := simulateRouteRemoval(config.Routes, domain)
//
// 	// Simulate adding new route
// 	newRoute := map[string]interface{}{
// 		"match": []map[string]interface{}{
// 			{
// 				"host": []string{domain},
// 			},
// 		},
// 		"handle": []map[string]interface{}{
// 			{
// 				"handler": "reverse_proxy",
// 				"upstreams": []map[string]interface{}{
// 					{
// 						"dial": fmt.Sprintf("localhost:%s", port),
// 					},
// 				},
// 			},
// 		},
// 	}
// 	config.Routes = append(filteredRoutes, newRoute)
//
// 	updatedConfig, err := json.Marshal(config)
// 	if err != nil {
// 		ShipLogs.Error("Failed to marshal updated config (simulated)", "error", err)
// 		return fmt.Errorf("failed to marshal config (simulated): %w", err)
// 	}
//
// 	// 4. Simulate applying updated config
// 	ShipLogs.Debug("Simulating applying updated config", "config", string(updatedConfig))
// 	if err := simulateConfigUpdate(string(updatedConfig)); err != nil {
// 		ShipLogs.Error("Failed to update config (simulated)", "error", err)
// 		return fmt.Errorf("failed to update config (simulated): %w", err)
// 	}
//
// 	// 5. Simulate verification
// 	for i := 0; i < maxRetries; i++ {
// 		ShipLogs.Debug("Simulating config update verification", "attempt", i+1)
// 		if simulateVerifyConfig(domain) {
// 			ShipLogs.Info("Caddy config updated successfully (simulated)")
// 			return nil
// 		}
// 		time.Sleep(retryDelay)
// 	}
//
// 	ShipLogs.Error("Failed to verify config update (simulated)")
// 	return fmt.Errorf("failed to verify config update after %d attempts (simulated)", maxRetries)
// }
//
//
